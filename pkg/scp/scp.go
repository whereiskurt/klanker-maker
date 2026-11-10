// Package scp renders the {prefix}-sandbox-containment Service Control Policy.
//
// It is the ONE definition of that policy. `km bootstrap --scp` previews its
// output, `km bootstrap` hands its output to infra/modules/scp/v3.0.0 through
// KM_SCP_POLICY_JSON (the module deploys the string verbatim), and `km doctor`
// compares the live policy against it. Before v3.0.0 the policy was written
// twice — once as an aws_iam_policy_document in HCL, once as a Go preview — and
// the two had drifted (the preview denied three actions the deployed policy did
// not), so a reviewer reading the preview was reading a policy nobody ran.
//
// Inputs come from km-config.yaml: resource_prefix, accounts.application,
// region, and the optional scp: block (extra trusted roles per statement,
// extra regions). Pure: no AWS calls.
package scp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// MaxBytes is the safety threshold the module's precondition also enforces.
// AWS's hard limit for an SCP document is 5,120 characters.
const MaxBytes = 5000

// Input is everything the policy depends on.
type Input struct {
	// ResourcePrefix names the policy ({prefix}-sandbox-containment) and is used
	// to build the sample sandbox role names the trust-pattern guard checks.
	ResourcePrefix string
	// ApplicationAccountID scopes the operator SSO trust entry.
	ApplicationAccountID string
	// Region is the install's primary region; always allowed.
	Region string
	// RegionLabel is the short form (use1) the sandbox role name ends with.
	RegionLabel string

	// ExtraRegions are allowed in addition to Region (scp.extra_regions).
	ExtraRegions []string
	// TrustedRoles are exempt from every statement the control plane is exempt
	// from: infra/storage, instance mutation, IAM escalation, region lock
	// (scp.trusted_roles). NOT the SSM-pivot statement.
	TrustedRoles []string
	// TrustedIAMRoles are exempt from DenyIAMEscalation only (scp.trusted_iam_roles).
	TrustedIAMRoles []string
	// TrustedSSMRoles are exempt from DenySSMPivot only (scp.trusted_ssm_roles).
	TrustedSSMRoles []string
}

// Statement is one SCP statement. Condition is operator → key → values.
type Statement struct {
	Sid       string                         `json:"Sid"`
	Effect    string                         `json:"Effect"`
	Action    []string                       `json:"Action,omitempty"`
	NotAction []string                       `json:"NotAction,omitempty"`
	Resource  string                         `json:"Resource"`
	Condition map[string]map[string][]string `json:"Condition,omitempty"`
}

// Policy is the full document.
type Policy struct {
	Version   string      `json:"Version"`
	Statement []Statement `json:"Statement"`
}

// PolicyName returns {prefix}-sandbox-containment.
func PolicyName(prefix string) string {
	if prefix == "" {
		prefix = "km"
	}
	return prefix + "-sandbox-containment"
}

// Built-in trust patterns. The account is wildcarded and the prefix is a
// leading `*` so several installs sharing one application account do not deny
// each other's control plane (each install attaches its own SCP and SCPs
// intersect). An SCP only ever evaluates principals of the account it is
// attached to, so the account wildcard widens nothing.
//
// Every pattern here must END in a fixed role-name suffix or carry a hyphenated
// infix a sandbox id cannot contain: the sandbox role is
// {prefix}-ec2spot-ssm-{metadata.prefix}-{8hex}-{regionlabel}, and
// metadata.prefix is any ^[a-z][a-z0-9]{0,11}$ — so a pattern like
// "*-lifecycle-*" was matched by a profile with `prefix: lifecycle`.
// ValidateTrustPatterns enforces this for the built-ins and the config extras.
const (
	ttlHandler     = "arn:aws:iam::*:role/*-ttl-handler"
	createHandler  = "arn:aws:iam::*:role/*-create-handler"
	budgetEnforcer = "arn:aws:iam::*:role/*-budget-enforcer-*"
)

func ssoPattern(account string) string {
	return "arn:aws:iam::" + account + ":role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*"
}

// Action lists, exported so tests and docs can name them.
var (
	InfraAndStorageActions = []string{
		// Security-group mutation
		"ec2:CreateSecurityGroup", "ec2:DeleteSecurityGroup",
		"ec2:AuthorizeSecurityGroup*", "ec2:RevokeSecurityGroup*",
		"ec2:ModifySecurityGroupRules",
		// Network escape
		"ec2:CreateVpc", "ec2:CreateSubnet", "ec2:CreateRouteTable", "ec2:CreateRoute",
		"ec2:*InternetGateway", "ec2:CreateNatGateway", "ec2:*VpcPeeringConnection",
		"ec2:CreateTransitGateway*",
		// Storage exfiltration and destruction. ec2:CreateTags is deliberately
		// NOT here: RunInstances/CreateVolume with TagSpecifications is
		// authorised as ec2:CreateTags, so denying it would break every
		// non-trusted role that launches or tags anything.
		"ec2:CreateSnapshot", "ec2:CopySnapshot", "ec2:DeleteSnapshot",
		"ec2:CreateImage", "ec2:CopyImage", "ec2:ExportImage", "ec2:DeregisterImage",
	}
	InstanceMutationActions = []string{
		"ec2:RunInstances", "ec2:ModifyInstanceAttribute", "ec2:ModifyInstanceMetadataOptions",
	}
	// IAMEscalationActions keeps "iam:AssumeRole", which is not a real IAM
	// action and so denies nothing. Do NOT "fix" it to sts:AssumeRole: the IMDS
	// fence's self-assume and cross-account launches both need sts:AssumeRole.
	IAMEscalationActions = []string{
		"iam:CreateRole", "iam:AttachRolePolicy", "iam:DetachRolePolicy",
		"iam:PassRole", "iam:AssumeRole",
	}
	SSMPivotActions     = []string{"ssm:SendCommand", "ssm:StartSession"}
	OrgDiscoveryActions = []string{"organizations:List*", "organizations:Describe*"}
	// RegionExemptActions are global (or routed) services the region lock never
	// applies to. bedrock-mantle and aws-marketplace match the ec2spot instance
	// role's own region Deny — without them here that role's exemption was
	// inert, because the SCP still denied the call.
	RegionExemptActions = []string{
		"iam:*", "sts:*", "organizations:*", "support:*", "health:*", "trustedadvisor:*",
		"cloudfront:*", "waf:*", "shield:*", "route53:*", "route53domains:*", "budgets:*",
		"ce:*", "cur:*", "globalaccelerator:*", "networkmanager:*", "pricing:*",
		"bedrock:*", "bedrock-mantle:*", "aws-marketplace:*",
		"s3:GetAccountPublicAccessBlock", "s3:ListAllMyBuckets", "s3:PutAccountPublicAccessBlock",
	}
)

var (
	roleARNPattern = regexp.MustCompile(`^arn:aws:iam::(\d{12}|\*):role/[A-Za-z0-9+=,.@_/*?-]+$`)
	regionPattern  = regexp.MustCompile(`^[a-z]{2}(-gov|-iso[a-z]*)?-[a-z]+-\d$`)
)

// Render validates the input and builds the policy.
func Render(in Input) (Policy, error) {
	if in.ApplicationAccountID == "" {
		return Policy{}, fmt.Errorf("accounts.application is required to render the SCP")
	}
	if in.Region == "" {
		return Policy{}, fmt.Errorf("region is required to render the SCP (it is the region lock)")
	}
	for _, r := range in.ExtraRegions {
		if !regionPattern.MatchString(r) {
			return Policy{}, fmt.Errorf("scp.extra_regions: %q is not an AWS region name", r)
		}
	}
	for key, list := range map[string][]string{
		"scp.trusted_roles":     in.TrustedRoles,
		"scp.trusted_iam_roles": in.TrustedIAMRoles,
		"scp.trusted_ssm_roles": in.TrustedSSMRoles,
	} {
		for _, a := range list {
			if !roleARNPattern.MatchString(a) {
				return Policy{}, fmt.Errorf("%s: %q is not an IAM role ARN pattern (want arn:aws:iam::<account|*>:role/<name>)", key, a)
			}
		}
	}

	base := dedup(append([]string{
		ssoPattern(in.ApplicationAccountID), ttlHandler, createHandler,
	}, in.TrustedRoles...))
	iam := dedup(append(append(append([]string{}, base...), budgetEnforcer), in.TrustedIAMRoles...))
	ssm := dedup(append([]string{
		ttlHandler, createHandler, ssoPattern(in.ApplicationAccountID),
	}, in.TrustedSSMRoles...))

	if err := ValidateTrustPatterns(in, dedup(append(append(append([]string{}, base...), iam...), ssm...))); err != nil {
		return Policy{}, err
	}

	notLike := func(arns []string) map[string]map[string][]string {
		return map[string]map[string][]string{"ArnNotLike": {"aws:PrincipalARN": arns}}
	}
	regionCond := notLike(base)
	regionCond["StringNotEquals"] = map[string][]string{
		"aws:RequestedRegion": dedup(append([]string{in.Region}, in.ExtraRegions...)),
	}

	p := Policy{
		Version: "2012-10-17",
		Statement: []Statement{
			{Sid: "DenyInfraAndStorage", Effect: "Deny", Action: InfraAndStorageActions, Resource: "*", Condition: notLike(base)},
			{Sid: "DenyInstanceMutation", Effect: "Deny", Action: InstanceMutationActions, Resource: "*", Condition: notLike(base)},
			{Sid: "DenyIAMEscalation", Effect: "Deny", Action: IAMEscalationActions, Resource: "*", Condition: notLike(iam)},
			{Sid: "DenySSMPivot", Effect: "Deny", Action: SSMPivotActions, Resource: "*", Condition: notLike(ssm)},
			// Unconditional: applies to every principal in the application
			// account. The management account is exempt from SCPs by AWS design,
			// which is why `km doctor` reads the SCP through {prefix}-org-admin.
			{Sid: "DenyOrgDiscovery", Effect: "Deny", Action: OrgDiscoveryActions, Resource: "*"},
			{Sid: "DenyOutsideRegion", Effect: "Deny", NotAction: RegionExemptActions, Resource: "*", Condition: regionCond},
		},
	}
	if n := len(p.JSON()); n > MaxBytes {
		return Policy{}, fmt.Errorf("rendered SCP is %d bytes, over the %d-byte safety threshold (AWS limit 5,120) — trim the scp: trusted role lists", n, MaxBytes)
	}
	return p, nil
}

// JSON is the compact document — what is deployed and what counts toward the
// size limit.
func (p Policy) JSON() []byte {
	b, _ := json.Marshal(p)
	return b
}

// Indented is the document for display.
func (p Policy) Indented() string {
	b, _ := json.MarshalIndent(p, "", "  ")
	return string(b)
}

// ValidateTrustPatterns rejects any pattern that a sandbox instance role could
// match. The role name is {prefix}-ec2spot-ssm-{metadata.prefix}-{8hex}-{label};
// the candidate metadata.prefix values are "sb" plus every lowercase
// alphanumeric run in the pattern itself (the way `*-lifecycle-*` was beaten
// by `prefix: lifecycle`).
func ValidateTrustPatterns(in Input, patterns []string) error {
	prefixes := []string{in.ResourcePrefix, "km"}
	label := in.RegionLabel
	if label == "" {
		label = "use1"
	}
	tok := regexp.MustCompile(`[a-z][a-z0-9]*`)
	for _, pat := range patterns {
		name := pat[strings.Index(pat, ":role/")+len(":role/"):]
		cands := []string{"sb"}
		for _, t := range tok.FindAllString(name, -1) {
			if len(t) <= 12 {
				cands = append(cands, t)
			}
		}
		for _, rp := range prefixes {
			if rp == "" {
				continue
			}
			for _, c := range cands {
				role := fmt.Sprintf("arn:aws:iam::%s:role/%s-ec2spot-ssm-%s-0123abcd-%s",
					accountOr(in.ApplicationAccountID), rp, c, label)
				if ArnLike(pat, role) {
					return fmt.Errorf("SCP trust pattern %q matches the sandbox instance role %q — a profile with metadata.prefix %q would be exempt from the SCP; narrow the pattern", pat, role, c)
				}
			}
		}
	}
	return nil
}

func accountOr(a string) string {
	if a == "" {
		return "123456789012"
	}
	return a
}

// ArnLike reports whether value matches an IAM ArnLike pattern (`*` any run,
// `?` one character, case-sensitive).
func ArnLike(pattern, value string) bool {
	p, v := 0, 0
	star, mark := -1, 0
	for v < len(value) {
		switch {
		case p < len(pattern) && (pattern[p] == '?' || pattern[p] == value[v]):
			p++
			v++
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, v
			p++
		case star >= 0:
			p = star + 1
			mark++
			v = mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// StatementDiff names one statement that differs between two documents.
type StatementDiff struct {
	Sid string
	// Missing are values the want document has and the got document lacks,
	// formatted "Field: value" (e.g. "ArnNotLike aws:principalarn: arn:…").
	Missing []string
	// Extra are values in got that want does not have.
	Extra []string
}

// Compare reports how got (e.g. the live policy) differs from want (the
// rendered one), statement by statement, treating every list as a set and
// condition keys case-insensitively — the same equivalence AWS applies. An
// empty result means equivalent.
func Compare(want, got []byte) ([]StatementDiff, error) {
	w, err := canonical(want)
	if err != nil {
		return nil, fmt.Errorf("parse rendered policy: %w", err)
	}
	g, err := canonical(got)
	if err != nil {
		return nil, fmt.Errorf("parse live policy: %w", err)
	}
	sids := map[string]bool{}
	for s := range w {
		sids[s] = true
	}
	for s := range g {
		sids[s] = true
	}
	keys := make([]string, 0, len(sids))
	for s := range sids {
		keys = append(keys, s)
	}
	sort.Strings(keys)
	var out []StatementDiff
	for _, sid := range keys {
		d := StatementDiff{Sid: sid, Missing: minus(w[sid], g[sid]), Extra: minus(g[sid], w[sid])}
		if len(d.Missing) > 0 || len(d.Extra) > 0 {
			out = append(out, d)
		}
	}
	return out, nil
}

// canonical flattens a policy into Sid → set of "Field: value" strings.
func canonical(doc []byte) (map[string]map[string]bool, error) {
	var raw struct {
		Statement []map[string]json.RawMessage `json:"Statement"`
	}
	dec := json.NewDecoder(bytes.NewReader(doc))
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for i, st := range raw.Statement {
		sid := fmt.Sprintf("#%d", i)
		if s, ok := st["Sid"]; ok {
			_ = json.Unmarshal(s, &sid)
		}
		set := map[string]bool{}
		for field, val := range st {
			switch field {
			case "Sid":
			case "Condition":
				var cond map[string]map[string]json.RawMessage
				if err := json.Unmarshal(val, &cond); err != nil {
					return nil, fmt.Errorf("statement %s condition: %w", sid, err)
				}
				for op, kv := range cond {
					for k, v := range kv {
						for _, s := range stringOrList(v) {
							set[fmt.Sprintf("%s %s: %s", op, strings.ToLower(k), s)] = true
						}
					}
				}
			default:
				for _, s := range stringOrList(val) {
					set[field+": "+s] = true
				}
			}
		}
		out[sid] = set
	}
	return out, nil
}

func stringOrList(v json.RawMessage) []string {
	var one string
	if json.Unmarshal(v, &one) == nil {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(v, &many) == nil {
		return many
	}
	return []string{string(v)}
}

func minus(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
