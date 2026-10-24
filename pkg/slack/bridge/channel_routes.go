package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
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
// event time becomes (RouteFromPattern). Channels are the current channels the
// glob matched at km init: they ride on the pattern as {id, name} pairs rather
// than as full RouteEntry rows, so a glob over many channels stays compact, and
// removing the glob removes them with it. BuildRouteTable expands them.
type PatternEntry struct {
	Glob         string           `json:"glob"`
	Alias        string           `json:"alias"`
	Profile      string           `json:"profile"`
	Allow        []string         `json:"allow"`
	ProfileAllow []string         `json:"profile_allow,omitempty"`
	MentionOnly  bool             `json:"mention_only"`
	History      *RouteHistory    `json:"history,omitempty"`
	Channels     []PatternChannel `json:"channels,omitempty"`
}

// PatternChannel is one current channel a glob matched at km init.
type PatternChannel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RoutesDocument is the on-the-wire table.
type RoutesDocument struct {
	V        int            `json:"v"`
	Routes   []RouteEntry   `json:"routes"`
	Patterns []PatternEntry `json:"patterns,omitempty"`
}

// MatchChannelGlob matches a Slack channel name against a glob, case-insensitively.
// Only '*' (any run, including empty) and '?' (exactly one character) are
// special; every other character, including '[' and '\', is a literal.
func MatchChannelGlob(glob, name string) bool {
	if name == "" {
		return false
	}
	g, n := []rune(strings.ToLower(glob)), []rune(strings.ToLower(name))
	gi, ni, star, mark := 0, 0, -1, 0
	for ni < len(n) {
		switch {
		case gi < len(g) && g[gi] == '*':
			star, mark = gi, ni
			gi++
		case gi < len(g) && (g[gi] == '?' || g[gi] == n[ni]):
			gi++
			ni++
		case star >= 0:
			mark++
			ni, gi = mark, star+1
		default:
			return false
		}
	}
	for gi < len(g) && g[gi] == '*' {
		gi++
	}
	return gi == len(g)
}

// reChannelGlob is the config glob grammar without the leading '#'.
var reChannelGlob = regexp.MustCompile(`^[a-z0-9*?][a-z0-9._*?-]{0,79}$`)

// RoutesParamPath is the SSM parameter the table lives at.
func RoutesParamPath(prefix string) string { return "/" + prefix + "/slack/routes" }

// CanonicalRoutesJSON is CanonicalRoutesDocument with no patterns (callers
// predating globs; byte-identical output for them).
func CanonicalRoutesJSON(routes []RouteEntry) ([]byte, error) {
	return CanonicalRoutesDocument(routes, nil)
}

// CanonicalRoutesDocument encodes the table so equal tables are equal bytes:
// routes sorted by channel id, patterns in document (config) order because the
// first matching glob wins at event time, each pattern's channels sorted by id,
// every allow and profile_allow list sorted, no HTML escaping, no trailing newline. km doctor
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
			if len(pats[i].Channels) > 0 {
				chs := append([]PatternChannel(nil), pats[i].Channels...)
				sort.SliceStable(chs, func(a, b int) bool { return chs[a].ID < chs[b].ID })
				pats[i].Channels = chs
			}
		}
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
		case !reChannelGlob.MatchString(p.Glob) || !strings.ContainsAny(p.Glob, "*?"):
			return nil, fmt.Errorf("slack routes: pattern %d has an invalid glob %q", i, p.Glob)
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
		for j, ch := range p.Channels {
			if ch.ID == "" {
				return nil, fmt.Errorf("slack routes: pattern %d (%s) channel %d has no id", i, p.Glob, j)
			}
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

// BuildRouteTable compiles a document into channel id → route: the static
// routes first, then every pattern's matched channels (RouteFromPattern), so a
// static entry for a channel beats any pattern and the first pattern listing a
// channel beats a later one. An entry whose allow or profile_allow list fails
// to compile, or a second static entry for a channel, is skipped with one warn
// line and never widens or replaces a route (same disposition as
// NewDefaultRoute returning nil on an invalid list).
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
		if r := compileRouteEntry(e, warn); r != nil {
			table[e.Channel] = r
		}
	}
	staticIDs := map[string]bool{}
	for id := range table {
		staticIDs[id] = true
	}
	for _, p := range doc.Patterns {
		pr := PatternRoute{Glob: p.Glob, Entry: p}
		for _, ch := range p.Channels {
			if _, dup := table[ch.ID]; dup {
				if !staticIDs[ch.ID] {
					warn("km-slack-bridge: channel matched by two patterns; keeping the first", "channel", ch.ID, "glob", p.Glob)
				}
				continue // exact beats glob; first glob wins
			}
			if r := compileRouteEntry(RouteFromPattern(pr, ch.ID, ch.Name), warn); r != nil {
				table[ch.ID] = r
			}
		}
	}
	return table
}

// compileRouteEntry is one entry's DefaultRoute, nil (after a warn line) when
// either allowlist fails to compile or the entry is incomplete.
func compileRouteEntry(e RouteEntry, warn func(msg string, kv ...any)) *DefaultRoute {
	list, err := CompileEmailAllowlist(e.Allow)
	if err != nil || list.Len() == 0 {
		warn("km-slack-bridge: channel route has an invalid allow list; skipping", "channel", e.Channel, "alias", e.Alias, "err", err)
		return nil
	}
	if len(e.ProfileAllow) > 0 {
		// NewDefaultRoute would drop a bad entry and keep the rest; a
		// narrowed create gate must not silently lose entries, so skip.
		if plist, perr := CompileEmailAllowlist(e.ProfileAllow); perr != nil || plist.Len() == 0 {
			warn("km-slack-bridge: channel route has an invalid profile_allow list; skipping", "channel", e.Channel, "alias", e.Alias, "err", perr)
			return nil
		}
	}
	r := NewDefaultRoute(e.Alias, e.Profile, e.Allow, e.ProfileAllow)
	if r == nil {
		warn("km-slack-bridge: channel route incomplete; skipping", "channel", e.Channel, "alias", e.Alias)
		return nil
	}
	r.MentionOnly = e.MentionOnly
	r.History, r.Pattern = e.History, e.Pattern
	return r
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
