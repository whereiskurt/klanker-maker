package webhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// NonceStore is the atomic check-and-store primitive backing both the replay
// nonce and the cooldown gate. Satisfied by the shared km nonces DynamoDB table
// (a conditional PutItem with a TTL attribute).
type NonceStore interface {
	// CheckAndStore returns (true, nil) when the key was already present
	// (replay/suppressed), (false, nil) on first insertion, (false, err) on
	// storage failure.
	CheckAndStore(ctx context.Context, key string, ttlSeconds int) (alreadySeen bool, err error)
}

// RateCounter is the atomic counter backing the install-wide rate ceiling.
// Increment returns the post-increment value for the bucket.
type RateCounter interface {
	Increment(ctx context.Context, key string, ttlSeconds int) (int64, error)
}

// ReplayKey is the dedup key for an inbound delivery.
func ReplayKey(source string, env *Envelope) string {
	return fmt.Sprintf("wh:%s:%s", source, env.DeliveryKey)
}

// CooldownKey is the suppression key for a (source, rule, group) triple.
//
// When groupBy expands to a template that still contains an unresolved variable,
// the raw expansion is hashed together with the delivery key so distinct
// payloads keep distinct keys. Collapsing them would suppress unrelated alerts
// for the whole window — the opposite of what a cooldown is for.
func CooldownKey(source string, ruleIdx int, groupBy string, env *Envelope) string {
	if groupBy == "" {
		return fmt.Sprintf("wh-cd:%s:%d:%s", source, ruleIdx, env.DeliveryKey)
	}
	expanded := ExpandTemplate(groupBy, env)
	if expanded == groupBy {
		// Nothing resolved — fall back to per-delivery uniqueness.
		sum := sha256.Sum256([]byte(groupBy + "\x00" + env.DeliveryKey))
		expanded = "unresolved:" + hex.EncodeToString(sum[:8])
	}
	return fmt.Sprintf("wh-cd:%s:%d:%s", source, ruleIdx, expanded)
}

// RateKey buckets a timestamp into a fixed window.
func RateKey(nowUnix int64, windowSeconds int) string {
	if windowSeconds <= 0 {
		windowSeconds = 1
	}
	return fmt.Sprintf("wh-rate:%d", nowUnix/int64(windowSeconds))
}

// CheckRate reports whether a dispatch is within the install-wide ceiling.
//
// The ceiling is install-wide across ALL sources on purpose: one fleet of
// sandboxes and one AI budget, so every source draws down the same allowance.
// Callers MUST invoke this only after the replay and cooldown gates, so
// suppressed duplicates never consume budget.
//
// Fails OPEN on counter errors — matching the cooldown gate and the existing
// bridges. A transient DynamoDB error must never strand a real alert.
func CheckRate(ctx context.Context, rc RateCounter, limit *config.WebhookRateLimit, nowUnix int64) bool {
	if limit == nil || limit.MaxDispatches <= 0 {
		return true
	}
	key := RateKey(nowUnix, limit.WindowSeconds)
	n, err := rc.Increment(ctx, key, limit.WindowSeconds*2)
	if err != nil {
		slog.Warn("webhook_rate_counter_error", "key", key, "error", err.Error())
		return true
	}
	if n > int64(limit.MaxDispatches) {
		slog.Warn("webhook_rate_ceiling_tripped",
			"key", key, "count", n, "max", limit.MaxDispatches)
		return false
	}
	return true
}
