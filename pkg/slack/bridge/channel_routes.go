package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// RoutesDocumentVersion is the "v" of the channel route table km init writes to
// SSM /{prefix}/slack/routes and the bridge reads at cold start.
const RoutesDocumentVersion = 1

// RouteEntry is one resolved channel route. Channel is the Slack channel id;
// Name is the channel's name at resolution time (logs and km doctor only);
// Profile is the pre-stage slug (slack-profiles/{Profile}/.km-profile.yaml);
// Allow is the EFFECTIVE allow list km init computed (route list, else install
// list, else the route profile's list), already normalised.
type RouteEntry struct {
	Channel     string   `json:"channel"`
	Name        string   `json:"name,omitempty"`
	Alias       string   `json:"alias"`
	Profile     string   `json:"profile"`
	Allow       []string `json:"allow"`
	MentionOnly bool     `json:"mention_only"`
}

// RoutesDocument is the on-the-wire table.
type RoutesDocument struct {
	V      int          `json:"v"`
	Routes []RouteEntry `json:"routes"`
}

// RoutesParamPath is the SSM parameter the table lives at.
func RoutesParamPath(prefix string) string { return "/" + prefix + "/slack/routes" }

// CanonicalRoutesJSON encodes the table so equal tables are equal bytes:
// routes sorted by channel id, each allow list sorted, no HTML escaping, no
// trailing newline. km doctor recomputes it to detect drift, so the form must
// never depend on map order.
func CanonicalRoutesJSON(routes []RouteEntry) ([]byte, error) {
	out := make([]RouteEntry, len(routes))
	copy(out, routes)
	for i := range out {
		allow := append([]string(nil), out[i].Allow...)
		sort.Strings(allow)
		if allow == nil {
			allow = []string{}
		}
		out[i].Allow = allow
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	if out == nil {
		out = []RouteEntry{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(RoutesDocument{V: RoutesDocumentVersion, Routes: out}); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
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
	return &doc, nil
}

// ErrNoRoutes is returned by callers that treat an absent parameter as an
// empty table.
var ErrNoRoutes = errors.New("slack routes: no table")

// BuildRouteTable compiles a document into channel id → route. An entry whose
// allow list fails to compile, or a second entry for a channel, is skipped
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
		r := NewDefaultRoute(e.Alias, e.Profile, e.Allow, nil)
		if r == nil {
			warn("km-slack-bridge: channel route incomplete; skipping", "channel", e.Channel, "alias", e.Alias)
			continue
		}
		r.MentionOnly = e.MentionOnly
		table[e.Channel] = r
	}
	return table
}
