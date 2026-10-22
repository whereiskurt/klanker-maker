package bridge

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrSenderIneligible marks a sender who can never be on the allowlist:
// deleted, a bot, or a profile with no email.
var ErrSenderIneligible = errors.New("slack-bridge: sender is not an eligible human user")

// DefaultRoute is the resolved slack.default_route block. A nil *DefaultRoute
// means the route is OFF; NewDefaultRoute returns nil when both allowlists are
// empty so the deny-by-default contract is structural, not a runtime check.
//
// Two lists, two jobs. list is the INSTALL list (slack.default_route.allow_emails):
// it gates everything the route reaches that is not the route's own box — DM
// targets and replies to a non-alias sandbox without its own allowEmails.
// profileList is the route profile's allowEmails: it gates a cold-create and the
// alias's current box, so narrowing the route profile never narrows the rest of
// the install. Either may be empty, not both.
type DefaultRoute struct {
	Alias       string
	Profile     string
	list        *EmailAllowlist
	profileList *EmailAllowlist
	// MentionOnly: a top-level post in a channel needs an @-mention of the bot
	// to start a turn. Always true for the default route; a channel route may
	// set mention_only: false. DMs never need one (requiresMention).
	MentionOnly bool
}

// NewDefaultRoute compiles both allowlists. An invalid entry is logged and
// dropped (km init already refuses them; this is belt-and-braces) so one typo
// can never widen a list or take the whole route down.
func NewDefaultRoute(alias, profile string, installEmails, profileEmails []string) *DefaultRoute {
	install := compileRouteList("allow_emails", installEmails)
	prof := compileRouteList("route profile allowEmails", profileEmails)
	if alias == "" || profile == "" || (install.Len() == 0 && prof.Len() == 0) {
		return nil
	}
	return &DefaultRoute{Alias: alias, Profile: profile, list: install, profileList: prof, MentionOnly: true}
}

func compileRouteList(name string, emails []string) *EmailAllowlist {
	valid := make([]string, 0, len(emails))
	for _, e := range emails {
		if strings.TrimSpace(e) == "" {
			continue
		}
		if _, _, err := ValidateEmailEntry(e); err != nil {
			log.Printf("slack-bridge: default_route: %s: dropping %v", name, err)
			continue
		}
		valid = append(valid, e)
	}
	list, err := CompileEmailAllowlist(valid)
	if err != nil {
		return nil
	}
	return list
}

// Allows reports whether email is on the INSTALL list. Empty email is always
// denied; an empty install list denies everyone.
func (r *DefaultRoute) Allows(email string) bool {
	if r == nil {
		return false
	}
	return r.list.Allows(email)
}

// AllowsCreate is the gate for the route's own box (a cold-create, or the
// alias's current box without its own allowEmails): the route profile's list
// when it has one, else the install list.
func (r *DefaultRoute) AllowsCreate(email string) bool {
	if r == nil {
		return false
	}
	if r.profileList.Len() > 0 {
		return r.profileList.Allows(email)
	}
	return r.list.Allows(email)
}

// HasProfileList reports whether the route profile declared its own list, i.e.
// whether AllowsCreate can differ from Allows.
func (r *DefaultRoute) HasProfileList() bool {
	return r != nil && r.profileList.Len() > 0
}

// reDomainEntry / reExactEntry are duplicated in internal/app/config
// (validateRouteEmailEntry), which cannot import this package. The two copies
// share a test table (emailEntryCases here, TestValidateRouteEmailEntry_MirrorsBridge
// there): change both together.
var reDomainEntry = regexp.MustCompile(`^\*@(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)
var reExactEntry = regexp.MustCompile(`^[^@\s*,]+@(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

// ValidateEmailEntry normalises one allowlist entry: lowercased, trimmed; either
// an exact address or exactly `*@<domain>`. Any other use of `*` is an error so
// a typo cannot widen an allowlist.
func ValidateEmailEntry(s string) (string, bool, error) {
	n := strings.ToLower(strings.TrimSpace(s))
	switch {
	case reDomainEntry.MatchString(n):
		return n, true, nil
	case reExactEntry.MatchString(n):
		return n, false, nil
	default:
		return "", false, fmt.Errorf("allowlist entry %q: must be an email address or *@<domain>", s)
	}
}

// EmailAllowlist is the one matcher for every email gate: the default route's
// list, a sandbox's own allowEmails, and km-slack dm targets.
type EmailAllowlist struct {
	exact   map[string]struct{}
	domains map[string]struct{}
}

func CompileEmailAllowlist(entries []string) (*EmailAllowlist, error) {
	al := &EmailAllowlist{exact: map[string]struct{}{}, domains: map[string]struct{}{}}
	for _, e := range entries {
		if strings.TrimSpace(e) == "" {
			continue
		}
		n, isDomain, err := ValidateEmailEntry(e)
		if err != nil {
			return nil, err
		}
		if isDomain {
			al.domains[strings.TrimPrefix(n, "*@")] = struct{}{}
		} else {
			al.exact[n] = struct{}{}
		}
	}
	return al, nil
}

func (al *EmailAllowlist) Len() int {
	if al == nil {
		return 0
	}
	return len(al.exact) + len(al.domains)
}

// Allows: exact first, then the domain after the '@'. Subdomains do not
// match (`a@sub.example.com` is not `*@example.com`). Empty ⇒ false; an input
// that is not exactly one '@' (`a@b@example.com`) ⇒ false, so a malformed
// address can never reach a domain entry through its trailing label.
func (al *EmailAllowlist) Allows(email string) bool {
	if al == nil {
		return false
	}
	e := strings.ToLower(strings.TrimSpace(email))
	if e == "" || strings.HasPrefix(e, "*@") || strings.Count(e, "@") != 1 {
		return false
	}
	if _, ok := al.exact[e]; ok {
		return true
	}
	at := strings.LastIndex(e, "@")
	if at < 1 {
		return false
	}
	_, ok := al.domains[e[at+1:]]
	return ok
}

// requiresMention: a DM to the bot is addressed by construction; every other
// conversation kind needs an explicit <@bot> token.
func requiresMention(channelType string) bool { return channelType != "im" }

// CachedSenderEmailFetcher memoises successful lookups for TTL. Failures and
// ErrSenderIneligible are never cached: a transient Slack error costs one
// event, and a re-activated user is seen on their next message.
type CachedSenderEmailFetcher struct {
	Inner SenderEmailFetcher
	TTL   time.Duration
	Now   func() time.Time

	mu    sync.Mutex
	cache map[string]cachedEmail
}

type cachedEmail struct {
	email   string
	expires time.Time
}

func (c *CachedSenderEmailFetcher) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *CachedSenderEmailFetcher) SenderEmail(ctx context.Context, userID string) (string, error) {
	c.mu.Lock()
	if hit, ok := c.cache[userID]; ok && c.now().Before(hit.expires) {
		c.mu.Unlock()
		return hit.email, nil
	}
	c.mu.Unlock()

	email, err := c.Inner.SenderEmail(ctx, userID)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if c.cache == nil {
		c.cache = map[string]cachedEmail{}
	}
	c.cache[userID] = cachedEmail{email: email, expires: c.now().Add(c.TTL)}
	c.mu.Unlock()
	return email, nil
}
