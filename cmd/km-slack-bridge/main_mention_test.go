package main

import "testing"

// TestWireEventsHandler_BotUserIDPrime stubs the Wave 0 contract for POL-09.
// Plan 91-03 will implement the real test once wireEventsHandler reads
// KM_SLACK_MENTION_ONLY and primes CachedBotUserIDFetcher from KM_SLACK_BOT_USER_ID —
// assert MentionOnly field set, PrimeCache called when env var non-empty.
func TestWireEventsHandler_BotUserIDPrime(t *testing.T) {
	t.Skip("TODO Plan 91-03: implement once wireEventsHandler reads KM_SLACK_MENTION_ONLY and primes CachedBotUserIDFetcher from KM_SLACK_BOT_USER_ID — assert MentionOnly field set, PrimeCache called when env var non-empty")
}
