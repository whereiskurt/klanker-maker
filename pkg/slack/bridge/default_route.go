package bridge

import (
	"context"
	"errors"
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
	allow   map[string]struct{}
}

func NewDefaultRoute(alias, profile string, emails []string) *DefaultRoute {
	allow := make(map[string]struct{}, len(emails))
	for _, e := range emails {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			allow[e] = struct{}{}
		}
	}
	if len(allow) == 0 || alias == "" || profile == "" {
		return nil
	}
	return &DefaultRoute{Alias: alias, Profile: profile, allow: allow}
}

// Allows reports whether email is on the list. Empty email is always denied.
func (r *DefaultRoute) Allows(email string) bool {
	if r == nil {
		return false
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	_, ok := r.allow[email]
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
