package cmd

import (
	"strings"
	"testing"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// TestDoctorNetworkNATIdle exercises checkNATIdle (Phase 125 — NAT gateway
// idle doctor check).
//
//   - NAT enabled, zero running private sandboxes  -> WARN with cost + safe-to-disable.
//   - NAT enabled, >=1 running private sandbox     -> OK.
//   - NAT not enabled                               -> SKIPPED (silent on a Phase 124 install).
//   - Private-but-not-running / empty placement rows are excluded from the count.
func TestDoctorNetworkNATIdle(t *testing.T) {
	tests := []struct {
		name       string
		natEnabled bool
		metas      []kmaws.SandboxMetadata
		wantStatus CheckStatus
		wantMsgSub string
		wantRemSub string
	}{
		{
			name:       "Test 1: NAT enabled, zero running private sandboxes -> WARN",
			natEnabled: true,
			metas:      nil,
			wantStatus: CheckWarn,
			wantMsgSub: "$132",
			wantRemSub: "network.nat_gateway",
		},
		{
			name:       "Test 2: NAT enabled, at least one running private sandbox -> OK",
			natEnabled: true,
			metas: []kmaws.SandboxMetadata{
				{SandboxID: "sb-1", NetworkPlacement: "private", Status: "running"},
			},
			wantStatus: CheckOK,
		},
		{
			name:       "Test 3: NAT not enabled -> SKIPPED",
			natEnabled: false,
			metas: []kmaws.SandboxMetadata{
				{SandboxID: "sb-1", NetworkPlacement: "private", Status: "running"},
			},
			wantStatus: CheckSkipped,
		},
		{
			// Phase 125 live-UAT correction: a stopped private sandbox still DEPENDS
			// on NAT — it needs an egress path again the moment it resumes. Tearing
			// down NAT under it would break the resume. This case previously
			// asserted the opposite and codified the bug.
			name:       "Test 7a: NAT enabled, private-but-stopped sandbox still depends -> OK",
			natEnabled: true,
			metas: []kmaws.SandboxMetadata{
				{SandboxID: "sb-1", NetworkPlacement: "private", Status: "stopped"},
			},
			wantStatus: CheckOK,
		},
		{
			name:       "Test 7b: NAT enabled, running-but-empty-placement sandbox excluded -> WARN",
			natEnabled: true,
			metas: []kmaws.SandboxMetadata{
				{SandboxID: "sb-1", NetworkPlacement: "", Status: "running"},
			},
			wantStatus: CheckWarn,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkNATIdle(tc.natEnabled, tc.metas)
			if got.Status != tc.wantStatus {
				t.Errorf("status: got %v, want %v (message: %q)", got.Status, tc.wantStatus, got.Message)
			}
			if tc.wantMsgSub != "" && !strings.Contains(got.Message, tc.wantMsgSub) {
				t.Errorf("message: got %q, want substring %q", got.Message, tc.wantMsgSub)
			}
			if tc.wantRemSub != "" && !strings.Contains(got.Remediation, tc.wantRemSub) {
				t.Errorf("remediation: got %q, want substring %q", got.Remediation, tc.wantRemSub)
			}
			if !strings.Contains(got.Remediation, "km init") && tc.wantStatus == CheckWarn && tc.wantRemSub == "network.nat_gateway" {
				t.Errorf("remediation: got %q, want it to also mention `km init`", got.Remediation)
			}
		})
	}
}

// TestDoctorNetworkPrivateWithoutNAT exercises checkPrivateWithoutNAT
// (Phase 125 — private sandboxes without NAT doctor check).
//
//   - NAT not enabled, >=1 running private sandbox -> WARN naming the sandbox ids.
//   - NAT not enabled, zero running private sandboxes -> SKIPPED.
//   - NAT enabled -> SKIPPED or OK, never WARN (the dangerous condition cannot occur).
//   - Private-but-not-running / empty placement rows are excluded from the count.
func TestDoctorNetworkPrivateWithoutNAT(t *testing.T) {
	tests := []struct {
		name       string
		natEnabled bool
		metas      []kmaws.SandboxMetadata
		wantStatus CheckStatus
		wantMsgSub string
	}{
		{
			name:       "Test 4: NAT not enabled, running private sandboxes -> WARN naming ids",
			natEnabled: false,
			metas: []kmaws.SandboxMetadata{
				{SandboxID: "sb-priv-1", NetworkPlacement: "private", Status: "running"},
				{SandboxID: "sb-priv-2", NetworkPlacement: "private", Status: "running"},
			},
			wantStatus: CheckWarn,
			wantMsgSub: "sb-priv-1",
		},
		{
			name:       "Test 5: NAT not enabled, no private sandboxes -> SKIPPED",
			natEnabled: false,
			metas:      nil,
			wantStatus: CheckSkipped,
		},
		{
			name:       "Test 6: NAT enabled -> never WARN (SKIPPED here)",
			natEnabled: true,
			metas: []kmaws.SandboxMetadata{
				{SandboxID: "sb-priv-1", NetworkPlacement: "private", Status: "running"},
			},
			wantStatus: CheckSkipped,
		},
		{
			// Phase 125 live-UAT correction: mirror of 7a. A stopped private sandbox
			// with NAT off IS a problem — it has no egress path to resume into.
			name:       "Test 7c: NAT not enabled, private-but-stopped is still broken -> WARN",
			natEnabled: false,
			metas: []kmaws.SandboxMetadata{
				{SandboxID: "sb-1", NetworkPlacement: "private", Status: "stopped"},
			},
			wantStatus: CheckWarn,
		},
		{
			name:       "Test 7d: NAT not enabled, running-but-empty-placement excluded -> SKIPPED",
			natEnabled: false,
			metas: []kmaws.SandboxMetadata{
				{SandboxID: "sb-1", NetworkPlacement: "", Status: "running"},
			},
			wantStatus: CheckSkipped,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkPrivateWithoutNAT(tc.natEnabled, tc.metas)
			if got.Status != tc.wantStatus {
				t.Errorf("status: got %v, want %v (message: %q)", got.Status, tc.wantStatus, got.Message)
			}
			if got.Status == CheckWarn && tc.wantStatus == CheckWarn {
				// The dangerous condition can never coexist with natEnabled==true.
				if tc.natEnabled {
					t.Errorf("checkPrivateWithoutNAT WARN with natEnabled=true — should never happen")
				}
			}
			if tc.wantMsgSub != "" && !strings.Contains(got.Message, tc.wantMsgSub) {
				t.Errorf("message: got %q, want substring %q", got.Message, tc.wantMsgSub)
			}
		})
	}
}
