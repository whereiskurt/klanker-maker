package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// RoutesDocumentVersion is the "v" of the channel route table km init writes to
// SSM /{prefix}/slack/routes and the bridge reads at cold start.
const RoutesDocumentVersion = 1

// RouteEntry is one resolved channel route. Channel is the Slack channel id;
// Name is the channel's name at resolution time (logs and km doctor only);
// Profile is the pre-stage slug (slack-profiles/{Profile}/.km-profile.yaml);
// Allow is the EFFECTIVE allow list km init computed (route list, else install
// list, else the route profile's list), already normalised. ProfileAllow is
// the route profile's own notification.slack.inbound.allowEmails, normalised,
// nil when it has none: as for the default route, it gates a cold-create and
// the alias's own box (DefaultRoute.AllowsCreate), whichever list became Allow.
type RouteEntry struct {
	Channel      string        `json:"channel"`
	Name         string        `json:"name,omitempty"`
	Pattern      string        `json:"pattern,omitempty"` // the glob this entry was expanded from, if any
	Alias        string        `json:"alias"`
	Profile      string        `json:"profile"`
	Allow        []string      `json:"allow"`
	ProfileAllow []string      `json:"profile_allow,omitempty"`
	MentionOnly  bool          `json:"mention_only"`
	History      *RouteHistory `json:"history,omitempty"`
}

// RouteHistory is a route's resolved history policy on the wire (seconds, not
// strings, so the bridge never parses "7d"). All omitempty: a route without a
// history block serialises exactly as before 2026-10-24.
type RouteHistory struct {
	Query            bool `json:"query,omitempty"`
	Mirror           bool `json:"mirror,omitempty"`
	BackfillSeconds  int  `json:"backfill_s,omitempty"`
	RetentionSeconds int  `json:"retention_s,omitempty"`
	MaxMessages      int  `json:"max_messages,omitempty"`
	Announce         bool `json:"announce,omitempty"`
}

// PatternEntry is a glob route template: what a channel that matches Glob at
// event time becomes (RouteFromPattern). Routes expanded at km init from the
// same glob carry Pattern = Glob so km doctor can tell them apart.
type PatternEntry struct {
	Glob         string        `json:"glob"`
	Alias        string        `json:"alias"`
	Profile      string        `json:"profile"`
	Allow        []string      `json:"allow"`
	ProfileAllow []string      `json:"profile_allow,omitempty"`
	MentionOnly  bool          `json:"mention_only"`
	History      *RouteHistory `json:"history,omitempty"`
}

// RoutesDocument is the on-the-wire table.
type RoutesDocument struct {
	V        int            `json:"v"`
	Routes   []RouteEntry   `json:"routes"`
	Patterns []PatternEntry `json:"patterns,omitempty"`
}

// MatchChannelGlob matches a lowercase Slack channel name against a glob with
// '*' and '?' (path.Match semantics; names never contain '/').
func MatchChannelGlob(glob, name string) bool {
	if name == "" {
		return false
	}
	ok, err := path.Match(strings.ToLower(glob), strings.ToLower(name))
	return err == nil && ok
}

// RoutesParamPath is the SSM parameter the table lives at.
func RoutesParamPath(prefix string) string { return "/" + prefix + "/slack/routes" }

// CanonicalRoutesJSON is CanonicalRoutesDocument with no patterns (callers
// predating globs; byte-identical output for them).
func CanonicalRoutesJSON(routes []RouteEntry) ([]byte, error) {
	return CanonicalRoutesDocument(routes, nil)
}

// CanonicalRoutesDocument encodes the table so equal tables are equal bytes:
// routes sorted by channel id, patterns sorted by glob, every allow and
// profile_allow list sorted, no HTML escaping, no trailing newline. km doctor
// recomputes it to detect drift, so the form must never depend on map order.
func CanonicalRoutesDocument(routes []RouteEntry, patterns []PatternEntry) ([]byte, error) {
	out := make([]RouteEntry, len(routes))
	copy(out, routes)
	for i := range out {
		out[i].Allow, out[i].ProfileAllow = sortedAllow(out[i].Allow), sortedProfileAllow(out[i].ProfileAllow)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	if out == nil {
		out = []RouteEntry{}
	}
	var pats []PatternEntry
	if len(patterns) > 0 {
		pats = make([]PatternEntry, len(patterns))
		copy(pats, patterns)
		for i := range pats {
			pats[i].Allow, pats[i].ProfileAllow = sortedAllow(pats[i].Allow), sortedProfileAllow(pats[i].ProfileAllow)
		}
		sort.SliceStable(pats, func(i, j int) bool { return pats[i].Glob < pats[j].Glob })
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(RoutesDocument{V: RoutesDocumentVersion, Routes: out, Patterns: pats}); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func sortedAllow(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

func sortedProfileAllow(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// RoutesSHA256 is the hex SHA-256 of the parameter bytes (KM_SLACK_ROUTES_SHA256).
func RoutesSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ParseRoutesDocument decodes and shape-checks a table. It rejects a version it
// does not know and any entry missing channel, alias, profile or allow: a
// route that admits nobody must never load as a route that admits everybody.
func ParseRoutesDocument(b []byte) (*RoutesDocument, error) {
	var doc RoutesDocument
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("slack routes: decode: %w", err)
	}
	if doc.V != RoutesDocumentVersion {
		return nil, fmt.Errorf("slack routes: unsupported version %d (want %d)", doc.V, RoutesDocumentVersion)
	}
	for i, r := range doc.Routes {
		switch {
		case r.Channel == "":
			return nil, fmt.Errorf("slack routes: entry %d has no channel", i)
		case r.Alias == "":
			return nil, fmt.Errorf("slack routes: entry %d (%s) has no alias", i, r.Channel)
		case r.Profile == "":
			return nil, fmt.Errorf("slack routes: entry %d (%s) has no profile", i, r.Channel)
		case len(r.Allow) == 0:
			return nil, fmt.Errorf("slack routes: entry %d (%s) has an empty allow list", i, r.Channel)
		}
	}
	for i, p := range doc.Patterns {
		switch {
		case p.Glob == "":
			return nil, fmt.Errorf("slack routes: pattern %d has no glob", i)
		case p.Alias == "":
			return nil, fmt.Errorf("slack routes: pattern %d (%s) has no alias", i, p.Glob)
		case p.Profile == "":
			return nil, fmt.Errorf("slack routes: pattern %d (%s) has no profile", i, p.Glob)
		case len(p.Allow) == 0:
			return nil, fmt.Errorf("slack routes: pattern %d (%s) has an empty allow list", i, p.Glob)
		}
		if err := validateRouteHistory(p.History); err != nil {
			return nil, fmt.Errorf("slack routes: pattern %d (%s): %w", i, p.Glob, err)
		}
	}
	for i, r := range doc.Routes {
		if err := validateRouteHistory(r.History); err != nil {
			return nil, fmt.Errorf("slack routes: entry %d (%s): %w", i, r.Channel, err)
		}
	}
	return &doc, nil
}

// validateRouteHistory mirrors config.SlackRouteHistory.Validate on the wire
// form; the bridge never trusts a hand-edited parameter.
func validateRouteHistory(h *RouteHistory) error {
	if h == nil {
		return nil
	}
	if !h.Query && !h.Mirror {
		return fmt.Errorf("history: neither query nor mirror")
	}
	if h.Mirror && !h.Query {
		return fmt.Errorf("history: mirror without query")
	}
	if h.MaxMessages < 0 || h.MaxMessages > 1000 || h.BackfillSeconds < 0 || h.RetentionSeconds < 0 ||
		h.BackfillSeconds > 30*86400 || h.RetentionSeconds > 30*86400 {
		return fmt.Errorf("history: value out of range")
	}
	return nil
}

// BuildRouteTable compiles a document into channel id → route. An entry whose
// allow or profile_allow list fails to compile, or a second entry for a
// channel, is skipped
// with one warn line and never widens or replaces a route (same disposition
// as NewDefaultRoute returning nil on an invalid list).
func BuildRouteTable(doc *RoutesDocument, warn func(msg string, kv ...any)) map[string]*DefaultRoute {
	table := map[string]*DefaultRoute{}
	if doc == nil {
		return table
	}
	for _, e := range doc.Routes {
		if _, dup := table[e.Channel]; dup {
			warn("km-slack-bridge: channel route listed twice; keeping the first", "channel", e.Channel, "alias", e.Alias)
			continue
		}
		list, err := CompileEmailAllowlist(e.Allow)
		if err != nil || list.Len() == 0 {
			warn("km-slack-bridge: channel route has an invalid allow list; skipping", "channel", e.Channel, "alias", e.Alias, "err", err)
			continue
		}
		if len(e.ProfileAllow) > 0 {
			// NewDefaultRoute would drop a bad entry and keep the rest; a
			// narrowed create gate must not silently lose entries, so skip.
			if plist, perr := CompileEmailAllowlist(e.ProfileAllow); perr != nil || plist.Len() == 0 {
				warn("km-slack-bridge: channel route has an invalid profile_allow list; skipping", "channel", e.Channel, "alias", e.Alias, "err", perr)
				continue
			}
		}
		r := NewDefaultRoute(e.Alias, e.Profile, e.Allow, e.ProfileAllow)
		if r == nil {
			warn("km-slack-bridge: channel route incomplete; skipping", "channel", e.Channel, "alias", e.Alias)
			continue
		}
		r.MentionOnly = e.MentionOnly
		r.History, r.Pattern = e.History, e.Pattern
		table[e.Channel] = r
	}
	return table
}

// PatternRoute is one glob with its template and the DefaultRoute a matched
// channel is routed by.
type PatternRoute struct {
	Glob  string
	Entry PatternEntry
	Route *DefaultRoute
}

// BuildPatternTable compiles doc.Patterns in document order (config order is
// preserved by km init: first match wins at event time).
func BuildPatternTable(doc *RoutesDocument, warn func(msg string, kv ...any)) []PatternRoute {
	var out []PatternRoute
	if doc == nil {
		return out
	}
	for _, p := range doc.Patterns {
		list, err := CompileEmailAllowlist(p.Allow)
		if err != nil || list.Len() == 0 {
			warn("km-slack-bridge: channel pattern has an invalid allow list; skipping", "glob", p.Glob, "alias", p.Alias, "err", err)
			continue
		}
		r := NewDefaultRoute(p.Alias, p.Profile, p.Allow, p.ProfileAllow)
		if r == nil {
			warn("km-slack-bridge: channel pattern incomplete; skipping", "glob", p.Glob, "alias", p.Alias)
			continue
		}
		r.MentionOnly, r.History, r.Pattern = p.MentionOnly, p.History, p.Glob
		out = append(out, PatternRoute{Glob: p.Glob, Entry: p, Route: r})
	}
	return out
}

// RouteFromPattern is the RouteEntry a channel that matched p becomes.
func RouteFromPattern(p PatternRoute, channelID, name string) RouteEntry {
	return RouteEntry{Channel: channelID, Name: name, Pattern: p.Glob, Alias: p.Entry.Alias, Profile: p.Entry.Profile,
		Allow: p.Entry.Allow, ProfileAllow: p.Entry.ProfileAllow, MentionOnly: p.Entry.MentionOnly, History: p.Entry.History}
}
