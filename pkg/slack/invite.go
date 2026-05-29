package slack

// invite.go — unified invite orchestrator.
//
// EnsureMemberByEmail composes users.lookupByEmail + conversations.invite +
// conversations.inviteShared into a single typed-result function used by all
// three Phase 72 call sites:
//
//   - internal/app/cmd/slack.go::RunSlackInit (replaces direct InviteShared call)
//   - internal/app/cmd/slack_invite.go::RunSlackInvite (NEW Phase 72 cmd)
//   - internal/app/cmd/create_slack.go (NEW Phase 72 profile-driven email loop)
//
// The orchestrator is pure: it depends on the narrow InviteAPI interface and
// (optionally) a Prompter for the Connect-fallback UX. No HTTP, no SSM, no
// cobra. The cmd layer wires *slack.Client → InviteAPI and a stdin-prompter
// → Prompter.

import (
	"context"
	"errors"
	"fmt"
)

// Prompter collects yes/no input for the Connect-fallback flow. The cmd layer
// supplies a stdin-backed implementation; tests inject a recording fake.
type Prompter interface {
	// ConfirmConnect returns true if the operator confirms sending a Slack
	// Connect invite to email. The implementation is responsible for showing
	// the email address and the Pro-tier requirement in the prompt text and
	// defaulting to N (no).
	ConfirmConnect(email string) (bool, error)
}

// InviteAPI is the narrow Slack client surface needed by EnsureMemberByEmail.
// *slack.Client satisfies it. Tests inject a recording fake.
type InviteAPI interface {
	LookupUserByEmail(ctx context.Context, email string) (userID string, found bool, err error)
	// InviteUserToChannelStrict returns ErrAlreadyInChannel (use errors.Is)
	// when the user is already a member; nil on fresh invite; *SlackAPIError
	// for any other Slack error. The orchestrator distinguishes these to
	// return InvitedDirect vs AlreadyMember.
	InviteUserToChannelStrict(ctx context.Context, channelID, userID string) error
	InviteShared(ctx context.Context, channelID, email string) error
}

// EnsureMemberOpts controls EnsureMemberByEmail behavior.
type EnsureMemberOpts struct {
	// ForceExternal: skip lookup, go straight to InviteShared. Maps to the
	// `km slack invite --external` flag. Highest precedence.
	ForceExternal bool
	// Interactive: when true and lookup misses, call Prompter.ConfirmConnect
	// before falling back to Connect. When false (km create, scheduled,
	// piped), the AutoConnect field decides the miss path. Interactive takes
	// precedence over AutoConnect.
	Interactive bool
	// AutoConnect: on a non-interactive lookup miss (Interactive=false), when
	// true, fall back to Slack Connect (InviteShared) with NO prompt instead of
	// returning SkippedExternal. Set by the km create loop from
	// spec.cli.useSlackConnect (default true), and hard-true for the primary
	// operator invite. Ignored when ForceExternal=true or Interactive=true.
	AutoConnect bool
	// DryRun: classify only. Do the read-only lookup, return the result that
	// represents the action that WOULD be taken, and perform NO Slack write
	// (no conversations.invite, no inviteShared, no prompt). Used by
	// `km slack invite --dry-run` so an operator can probe native-vs-Connect
	// classification against a real workspace with zero side effects.
	// Note: AlreadyMember cannot be detected in DryRun (membership requires the
	// write) — a workspace member returns InvitedDirect ("would invite").
	DryRun bool
	// Prompter is required when Interactive=true; ignored otherwise.
	Prompter Prompter
}

// EnsureMemberResult is a typed enum.
type EnsureMemberResult int

const (
	// InvitedDirect: lookup hit, conversations.invite added the user.
	InvitedDirect EnsureMemberResult = iota + 1
	// InvitedConnect: Connect invite (conversations.inviteShared) sent.
	InvitedConnect
	// AlreadyMember: lookup hit but user was already in the channel.
	AlreadyMember
	// SkippedExternal: non-interactive lookup miss, OR interactive miss with
	// operator decline. No invite sent. Caller logs+continues.
	SkippedExternal
	// Failed: any error in lookup, invite, or Connect. Caller decides
	// warn-vs-fail; the underlying error is also returned.
	Failed
)

func (r EnsureMemberResult) String() string {
	switch r {
	case InvitedDirect:
		return "InvitedDirect"
	case InvitedConnect:
		return "InvitedConnect"
	case AlreadyMember:
		return "AlreadyMember"
	case SkippedExternal:
		return "SkippedExternal"
	case Failed:
		return "Failed"
	default:
		return fmt.Sprintf("Unknown(%d)", int(r))
	}
}

// EnsureMemberByEmail is the unified invite primitive. See package godoc for
// the full picture. On success returns one of (InvitedDirect, InvitedConnect,
// AlreadyMember, SkippedExternal) with err == nil. On failure returns Failed
// and the underlying error (wrapped with email + channelID context).
//
// Behavior matrix:
//
//	ForceExternal=true                                  → InviteShared → InvitedConnect | Failed
//	Lookup hit + invite OK                              → InvitedDirect
//	Lookup hit + ErrAlreadyInChannel                    → AlreadyMember
//	Lookup hit + other invite error                     → Failed
//	Lookup miss + Interactive=false + AutoConnect=false → SkippedExternal
//	Lookup miss + Interactive=false + AutoConnect=true  → InviteShared → InvitedConnect | Failed
//	Lookup miss + Interactive=true  + decline           → SkippedExternal
//	Lookup miss + Interactive=true  + approve           → InviteShared → InvitedConnect | Failed
//	Lookup miss + Interactive=true  + Prompter=nil      → Failed
//
// Precedence on a lookup miss: Interactive=true (prompt) is evaluated before
// AutoConnect; AutoConnect only governs the non-interactive miss path.
//
// DryRun overrides all write behavior: ForceExternal→InvitedConnect (no send);
// lookup hit→InvitedDirect (no send, AlreadyMember not detectable); lookup
// miss→SkippedExternal (no send/prompt). The result is the *classification* of
// the planned action; the caller renders "would ..." text.
//
// Free-tier workspace: when InviteShared returns not_allowed_token_type or
// org_login_required, the wrapped error mentions "Slack Connect requires
// Pro tier" so callers don't need to duplicate that hint.
func EnsureMemberByEmail(ctx context.Context, api InviteAPI, channelID, email string, opts EnsureMemberOpts) (EnsureMemberResult, error) {
	if opts.ForceExternal {
		if opts.DryRun {
			return InvitedConnect, nil // would Connect; nothing sent
		}
		if err := api.InviteShared(ctx, channelID, email); err != nil {
			return Failed, wrapConnectError(email, err)
		}
		return InvitedConnect, nil
	}

	userID, found, err := api.LookupUserByEmail(ctx, email)
	if err != nil {
		return Failed, fmt.Errorf("lookup %s: %w", email, err)
	}
	if found {
		if opts.DryRun {
			return InvitedDirect, nil // would invite natively; nothing sent
		}
		err := api.InviteUserToChannelStrict(ctx, channelID, userID)
		if err == nil {
			return InvitedDirect, nil
		}
		if errors.Is(err, ErrAlreadyInChannel) {
			return AlreadyMember, nil
		}
		return Failed, fmt.Errorf("invite %s (%s) to %s: %w", email, userID, channelID, err)
	}

	// Lookup miss → external path.
	if opts.DryRun {
		return SkippedExternal, nil // not a member; would require Connect; nothing sent/prompted
	}
	if !opts.Interactive {
		// Non-interactive: AutoConnect decides. True ⇒ Connect with no prompt
		// (km create useSlackConnect path + primary operator invite). False ⇒
		// skip and let the caller warn.
		if opts.AutoConnect {
			if err := api.InviteShared(ctx, channelID, email); err != nil {
				return Failed, wrapConnectError(email, err)
			}
			return InvitedConnect, nil
		}
		return SkippedExternal, nil
	}
	if opts.Prompter == nil {
		return Failed, errors.New("Interactive=true requires a Prompter")
	}
	confirmed, err := opts.Prompter.ConfirmConnect(email)
	if err != nil {
		return Failed, fmt.Errorf("prompt for %s: %w", email, err)
	}
	if !confirmed {
		return SkippedExternal, nil
	}
	if err := api.InviteShared(ctx, channelID, email); err != nil {
		return Failed, wrapConnectError(email, err)
	}
	return InvitedConnect, nil
}

// wrapConnectError augments Connect errors with a Pro-tier hint when applicable.
func wrapConnectError(email string, err error) error {
	var apierr *SlackAPIError
	if errors.As(err, &apierr) {
		if apierr.Code == "not_allowed_token_type" || apierr.Code == "org_login_required" {
			return fmt.Errorf("Slack Connect to %s failed: %w (this requires a Pro Slack workspace; free-tier workspaces cannot send Connect invites)", email, err)
		}
	}
	return fmt.Errorf("Slack Connect to %s: %w", email, err)
}

// Compile-time assertion: *Client satisfies InviteAPI. If Client method
// signatures change, this line fails the build immediately.
var _ InviteAPI = (*Client)(nil)
