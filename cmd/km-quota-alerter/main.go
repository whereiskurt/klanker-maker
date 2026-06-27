// Package main — km-quota-alerter Lambda handler.
// Triggered by DynamoDB Streams on the {prefix}-action-quota table.
// On the MODIFY where a window first breaches (breached_at newly set,
// alert_sent absent): sends the operator an SES email, optionally posts to
// a Slack control channel, then sets alert_sent conditionally (exactly one
// alert per sandbox/action/window).
//
// Implementation: Wave 3, plan 09 (Phase 121).
// This file is a compile-target skeleton so Wave 0 stubs build.
package main

func main() {
	// TODO(Wave 3 plan 09): wire AWS Lambda DDB-Streams handler.
}
