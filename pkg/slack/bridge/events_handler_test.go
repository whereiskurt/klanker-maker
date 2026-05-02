package bridge

import "testing"

// TestEventsHandler_ValidMessage covers REQ-SLACK-IN-EVENTS / REQ-SLACK-IN-DELIVERY.
// Implemented by Plan 67-03 (handler skeleton) and Plan 67-05 (SQS adapter wired).
func TestEventsHandler_ValidMessage(t *testing.T)           { t.Skip("Wave 0 stub — Plan 67-03/05") }
func TestEventsHandler_BadSigningSecret(t *testing.T)       { t.Skip("Wave 0 stub — Plan 67-03") }
func TestEventsHandler_StaleTimestamp(t *testing.T)         { t.Skip("Wave 0 stub — Plan 67-03") }
func TestEventsHandler_URLVerification(t *testing.T)        { t.Skip("Wave 0 stub — Plan 67-03") }
func TestEventsHandler_BotSelfMessageFiltered(t *testing.T) { t.Skip("Wave 0 stub — Plan 67-03") }
func TestEventsHandler_ReplayedEventID(t *testing.T)        { t.Skip("Wave 0 stub — Plan 67-03") }
func TestEventsHandler_UnknownChannel(t *testing.T)         { t.Skip("Wave 0 stub — Plan 67-05") }
