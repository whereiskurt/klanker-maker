package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
// list, else the route profile's list), already normalised. ProfileAllow is
// the route profile's own notification.slack.inbound.allowEmails, normalised,
// nil when it has none: as for the default route, it gates a cold-create and
// the alias's own box (DefaultRoute.AllowsCreate), whichever list became Allow.
type RouteEntry struct {
	Channel      string   `json:"channel"`
	Name         string   `json:"name,omitempty"`
	Alias        string   `json:"alias"`
	Profile      string   `json:"profile"`
	Allow        []string `json:"allow"`
	ProfileAllow []string `json:"profile_allow,omitempty"`
	MentionOnly  bool     `json:"mention_only"`
}

// RoutesDocument is the on-the-wire table.
type RoutesDocument struct {
	V      int          `json:"v"`
	Routes []RouteEntry `json:"routes"`
}

// RoutesParamPath is the SSM parameter the table lives at.
func RoutesParamPath(prefix string) string { return "/" + prefix + "/slack/routes" }

// CanonicalRoutesJSON encodes the table so equal tables are equal bytes:
// routes sorted by channel id, each allow and profile_allow list sorted, no HTML escaping, no
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
		if len(out[i].ProfileAllow) > 0 {
			pa := append([]string(nil), out[i].ProfileAllow...)
			sort.Strings(pa)
			out[i].ProfileAllow = pa
		} else {
			out[i].ProfileAllow = nil
		}
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
		table[e.Channel] = r
	}
	return table
}
