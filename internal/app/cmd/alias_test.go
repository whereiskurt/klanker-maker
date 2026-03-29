package cmd_test

import (
	"strings"
	"testing"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// TestListCmd_AliasColumn verifies that km list shows an ALIAS column
// and displays the alias for each sandbox that has one.
func TestListCmd_AliasColumn(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{
				SandboxID: "sb-aa11bb22",
				Profile:   "claude-dev",
				Substrate: "ec2",
				Region:    "us-east-1",
				Status:    "running",
				Alias:     "orc",
			},
			{
				SandboxID: "sb-cc33dd44",
				Profile:   "claude-dev",
				Substrate: "ec2",
				Region:    "us-east-1",
				Status:    "running",
				Alias:     "wrkr-1",
			},
			{
				SandboxID: "sb-ee55ff66",
				Profile:   "open-dev",
				Substrate: "ecs",
				Region:    "us-west-2",
				Status:    "running",
				Alias:     "", // no alias
			},
		},
	}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	// Header must contain ALIAS column
	if !strings.Contains(out, "ALIAS") {
		t.Errorf("output missing 'ALIAS' header column:\n%s", out)
	}

	// Alias values must appear in output
	if !strings.Contains(out, "orc") {
		t.Errorf("output missing alias 'orc':\n%s", out)
	}
	if !strings.Contains(out, "wrkr-1") {
		t.Errorf("output missing alias 'wrkr-1':\n%s", out)
	}
}

// TestListCmd_AliasEmpty shows dash or empty for sandbox without alias.
func TestListCmd_AliasEmpty(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{
				SandboxID: "sb-noalias1",
				Profile:   "open-dev",
				Substrate: "ec2",
				Region:    "us-east-1",
				Status:    "running",
				Alias:     "",
			},
		},
	}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	// Must still have ALIAS column header
	if !strings.Contains(out, "ALIAS") {
		t.Errorf("output missing 'ALIAS' header column:\n%s", out)
	}
}
