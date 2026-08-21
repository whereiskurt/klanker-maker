package netpolicy

import (
	"os"
	"sync"
	"time"
)

// DefaultReloadInterval is how long a Store trusts its cached view before
// re-checking the file. The proxies consult the store on every egress decision,
// so this bounds the syscall rate; it is also the worst-case delay between a
// sandbox appending a deny and that deny taking effect.
const DefaultReloadInterval = time.Second

// DefaultPath is where the runtime deny list lives on a sandbox.
//
// It is under /var/lib rather than /run deliberately: /run is a tmpfs that is
// cleared on boot, and a reboot that silently dropped the sandbox's accumulated
// denies would widen its policy — exactly the thing the design forbids.
const DefaultPath = "/var/lib/km/netpolicy/deny.list"

// Store is a lazily-reloading view of a deny-list file.
//
// It never reports an error to its callers. A missing, unreadable, or entirely
// malformed file reads as "no runtime denies", which is the correct default for
// the overwhelmingly common case of a sandbox that has never narrowed itself.
// Malformed lines surface through Dropped for diagnostics only.
//
// Safe for concurrent use.
type Store struct {
	path     string
	interval time.Duration

	mu        sync.RWMutex
	entries   []string
	dropped   int
	lastMod   time.Time
	lastSize  int64
	lastCheck time.Time
	loaded    bool
}

// NewStore returns a Store reading path, re-checking the file at most once per
// interval. An interval of 0 checks on every call, which is what tests want and
// what a low-traffic caller can afford.
func NewStore(path string, interval time.Duration) *Store {
	return &Store{path: path, interval: interval}
}

// Path returns the file this store reads.
func (s *Store) Path() string { return s.path }

// Entries returns the current deny patterns, reloading first if the cache has
// expired and the file has changed.
func (s *Store) Entries() []string {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	fresh := s.loaded && s.interval > 0 && time.Since(s.lastCheck) < s.interval
	if fresh {
		out := s.entries
		s.mu.RUnlock()
		return out
	}
	s.mu.RUnlock()

	return s.Reload()
}

// Reload re-reads the file unconditionally and returns the resulting entries.
func (s *Store) Reload() []string {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastCheck = time.Now()

	fi, err := os.Stat(s.path)
	if err != nil {
		// Absent file is the normal "never narrowed" state, not a failure.
		if os.IsNotExist(err) {
			s.entries, s.dropped, s.loaded = nil, 0, true
			s.lastMod, s.lastSize = time.Time{}, 0
			return nil
		}
		s.loaded = true
		return s.entries
	}

	// Unchanged file: keep the parsed view rather than re-reading it.
	if s.loaded && fi.ModTime().Equal(s.lastMod) && fi.Size() == s.lastSize {
		return s.entries
	}

	body, err := os.ReadFile(s.path)
	if err != nil {
		s.loaded = true
		return s.entries
	}

	entries, dropped := ParseLines(string(body))
	s.entries, s.dropped = entries, dropped
	s.lastMod, s.lastSize, s.loaded = fi.ModTime(), fi.Size(), true
	return s.entries
}

// Dropped reports how many malformed lines the last load skipped.
func (s *Store) Dropped() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dropped
}

// Denier answers the single question every egress decision needs: is this host
// denied? It unions a static list (baked in from the profile at create time)
// with an optional runtime Store.
//
// Union is the only combining rule. Adding a source, or adding an entry to a
// source, can only ever deny more.
type Denier struct {
	static []string
	store  *Store
}

// NewDenier combines a static deny list with an optional runtime store. Either
// may be empty or nil.
func NewDenier(static []string, store *Store) *Denier {
	return &Denier{static: static, store: store}
}

// IsDenied reports whether host is denied by either source.
func (d *Denier) IsDenied(host string) bool {
	if d == nil {
		return false
	}
	if Match(host, d.static) {
		return true
	}
	return Match(host, d.store.Entries())
}
