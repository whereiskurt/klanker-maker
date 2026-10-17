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
// means the route is OFF; NewDefaultRoute returns nil for an empty allowlist so
// the deny-by-default contract is structural, not a runtime check.
type DefaultRoute struct {
	Alias   string
	Profile string
	list    *EmailAllowlist
}

// NewDefaultRoute compiles emails into the route's allowlist. An invalid entry
// is logged and dropped (km init already refuses them; this is belt-and-braces)
// so one typo can never widen the list or take the whole route down.
func NewDefaultRoute(alias, profile string, emails []string) *DefaultRoute {
	valid := make([]string, 0, len(emails))
	for _, e := range emails {
		if strings.TrimSpace(e) == "" {
			continue
		}
		if _, _, err := ValidateEmailEntry(e); err != nil {
			log.Printf("slack-bridge: default_route: dropping %v", err)
			continue
		}
		valid = append(valid, e)
	}
	list, err := CompileEmailAllowlist(valid)
	if err != nil || list.Len() == 0 || alias == "" || profile == "" {
		return nil
	}
	return &DefaultRoute{Alias: alias, Profile: profile, list: list}
}

// Allows reports whether email is on the list. Empty email is always denied.
func (r *DefaultRoute) Allows(email string) bool {
	if r == nil {
		return false
	}
	return r.list.Allows(email)
}

// reDomainEntry / reExactEntry are duplicated in internal/app/config
// (validateRouteEmailEntry), which cannot import this package. The two copies
// share a test table (emailEntryCases here, TestValidateRouteEmailEntry_MirrorsBridge
// there): change both together.
var reDomainEntry = regexp.MustCompile(`^\*@(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)
var reExactEntry = regexp.MustCompile(`^[^@\s*]+@(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

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
