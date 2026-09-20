package cmd

// list_enrich.go — everything km list learns from AWS after the DynamoDB read,
// shaped so the cost is flat in the number of sandboxes.
//
// Before (2026-09-19): a serial `for i := range records` loop made, per box,
// DescribeInstances for status, DescribeInstances AGAIN for hibernation, then
// computeIdleRemaining reloaded the AWS config (SSO resolution), ran
// DescribeInstances a THIRD time, GetLogEvents, and a FOURTH DescribeInstances
// inside hasActiveSSMSession before DescribeSessions; --wide added a DynamoDB
// Query. Six boxes ≈ 30 round trips ≈ 6–12 s, and `ls` is the most-typed verb.
//
// Now: ONE DescribeInstances per launch account for the whole list (the tag
// filter takes up to 200 values), answering status, hibernation, the launch/
// resume-time floor for idle, AND the instance id SSM needs; then the
// remaining per-row lookups (CloudWatch, SSM sessions, thread counts) fan out
// on a bounded pool — the same shape --auth already used. Wall time ≈ the
// slowest single call.

import (
	"context"
	"sync"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// describeFilterValueMax is EC2's cap on values per DescribeInstances filter.
const describeFilterValueMax = 200

// listFanOut bounds the concurrent per-row lookups (matches the --auth pool).
const listFanOut = 8

// ssmDescribeSessionsAPI is the slice of SSM used to detect a live km shell.
type ssmDescribeSessionsAPI interface {
	DescribeSessions(ctx context.Context, in *ssm.DescribeSessionsInput, optFns ...func(*ssm.Options)) (*ssm.DescribeSessionsOutput, error)
}

// listEnrichDeps are the AWS slices enrichRecords needs. Any nil member simply
// disables the lookups that depend on it (tests inject what they exercise).
type listEnrichDeps struct {
	ec2 ec2DescribeInstancesAPI // home account
	// linkEC2 resolves a launch-account link name to a client scoped to that
	// account, or nil when the link cannot be resolved (then the home client is
	// used, which finds nothing — the pre-existing fallback).
	linkEC2 func(ctx context.Context, linkName string) ec2DescribeInstancesAPI
	cw      cwGetLogEventsAPI
	ssm     ssmDescribeSessionsAPI
	ddb     DDBQueryClient // thread counts for --wide; nil ⇒ skipped
}

// sandboxIDTag returns an instance's km:sandbox-id tag value ("" if untagged).
func sandboxIDTag(inst ec2types.Instance) string {
	for _, tg := range inst.Tags {
		if awssdk.ToString(tg.Key) == "km:sandbox-id" {
			return awssdk.ToString(tg.Value)
		}
	}
	return ""
}

// describeSandboxInstances returns every instance (any state) tagged with one
// of ids, keyed by that tag, in ceil(len(ids)/200) calls. Every id asked for is
// present in the result — with an empty slice when nothing matched — so a
// caller can tell "described, nothing there" from a failed call (error).
func describeSandboxInstances(ctx context.Context, client ec2DescribeInstancesAPI, ids []string) (map[string][]ec2types.Instance, error) {
	out := make(map[string][]ec2types.Instance, len(ids))
	for _, id := range ids {
		out[id] = nil
	}
	for start := 0; start < len(ids); start += describeFilterValueMax {
		end := start + describeFilterValueMax
		if end > len(ids) {
			end = len(ids)
		}
		resp, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			Filters: []ec2types.Filter{
				{Name: awssdk.String("tag:km:sandbox-id"), Values: ids[start:end]},
			},
		})
		if err != nil {
			return nil, err
		}
		for _, res := range resp.Reservations {
			for _, inst := range res.Instances {
				if id := sandboxIDTag(inst); id != "" {
					if _, asked := out[id]; asked {
						out[id] = append(out[id], inst)
					}
				}
			}
		}
	}
	return out, nil
}

// hibernationFromInstances reports whether any instance has hibernation
// configured — the pure core of the old checkEC2Hibernation.
func hibernationFromInstances(instances []ec2types.Instance) bool {
	for _, inst := range instances {
		if inst.HibernationOptions != nil && inst.HibernationOptions.Configured != nil {
			return *inst.HibernationOptions.Configured
		}
	}
	return false
}

// runningInstances filters to state=running (what the idle signals look at).
func runningInstances(instances []ec2types.Instance) []ec2types.Instance {
	var out []ec2types.Instance
	for _, inst := range instances {
		if inst.State != nil && inst.State.Name == ec2types.InstanceStateNameRunning {
			out = append(out, inst)
		}
	}
	return out
}

// enrichRecords reconciles status/hibernation from one batched describe per
// launch account, then fans out the per-row lookups (idle countdown for
// running boxes; thread counts for --wide). It mutates records in place and
// never fails: every lookup degrades to "unknown" exactly as the serial
// version did — a transient AWS error keeps the stored status, an empty
// describe result still downgrades a stale "running" claim.
func enrichRecords(ctx context.Context, deps listEnrichDeps, records []kmaws.SandboxRecord, resourcePrefix, threadsTable string, wide bool) {
	// 1. One DescribeInstances per launch account.
	byAccount := map[string][]int{} // link name ("" = home) → record indexes
	for i := range records {
		if isEC2Substrate(records[i].Substrate) {
			byAccount[records[i].LaunchAccount] = append(byAccount[records[i].LaunchAccount], i)
		}
	}
	described := map[string][]ec2types.Instance{} // only ids whose describe SUCCEEDED
	if deps.ec2 != nil {
		for link, idxs := range byAccount {
			client := deps.ec2
			if link != "" && deps.linkEC2 != nil {
				if lc := deps.linkEC2(ctx, link); lc != nil {
					client = lc
				}
			}
			ids := make([]string, 0, len(idxs))
			for _, i := range idxs {
				ids = append(ids, records[i].SandboxID)
			}
			got, err := describeSandboxInstances(ctx, client, ids)
			if err != nil {
				continue // stored status stands for this whole group
			}
			for id, insts := range got {
				described[id] = insts
			}
		}
	}
	for i := range records {
		insts, ok := described[records[i].SandboxID]
		if !ok {
			continue
		}
		records[i].Status = reconcileStatusFromInstances(records[i].Status, insts)
		records[i].Hibernation = hibernationFromInstances(insts)
	}

	// 2. Per-row lookups, concurrently. Each goroutine writes only its own
	//    records[i], so no lock is needed on the slice.
	hasInbound := false
	for i := range records {
		if records[i].SlackInboundQueueURL != "" {
			hasInbound = true
			break
		}
	}
	countThreads := wide && hasInbound && deps.ddb != nil

	var wg sync.WaitGroup
	sem := make(chan struct{}, listFanOut)
	for i := range records {
		rec := &records[i]
		wantIdle := rec.Status == "running" && rec.IdleTimeout != ""
		wantThreads := countThreads && rec.SlackChannelID != ""
		if !wantIdle && !wantThreads {
			continue
		}
		insts, known := described[rec.SandboxID]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if wantIdle {
				remaining := computeIdleRemainingWith(ctx, idleDeps{ec2: deps.ec2, cw: deps.cw, ssm: deps.ssm}, idleInput{
					SandboxID: rec.SandboxID, IdleTimeout: rec.IdleTimeout, CreatedAt: rec.CreatedAt,
					ResourcePrefix: resourcePrefix,
					Running:        runningInstances(insts), RunningKnown: known,
				})
				if remaining >= 0 {
					rec.IdleRemaining = formatIdleLabel(remaining, false)
				}
			}
			if wantThreads {
				if n, err := countActiveThreads(ctx, deps.ddb, threadsTable, rec.SlackChannelID); err == nil {
					rec.ActiveThreads = n
				}
			}
		}()
	}
	wg.Wait()
}

// isEC2Substrate mirrors the substrate prefix test km list has always used.
func isEC2Substrate(s string) bool {
	return len(s) >= 3 && s[:3] == "ec2"
}
