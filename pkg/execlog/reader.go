package execlog

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
)

// ReadDir reads the live store in dir and its one rotated generation, oldest
// record first.
//
// It never fails on bad data. A missing directory is the normal state of a box
// that has executed nothing yet; a corrupt line is skipped. The trace is
// forensic evidence read after the fact, so partial data is strictly better
// than an error that hides the rest of it.
//
// A genuine open failure that is NOT "missing" — above all EACCES, since the
// store is 0700 root-only (see the package doc) — is returned rather than
// swallowed. Reading nothing and reporting nothing look identical to a caller
// that only checks len(recs)==0, and this store has exactly one producer:
// unlike flowlog's multi-writer directory, there is no "other producer" left
// to answer for the trace once this file can't be opened. Silently returning
// an empty slice here is the read-side twin of the Phase 131 flow-store
// EACCES defect that flowlog.Writer's warnOnce exists to prevent.
func ReadDir(dir string) ([]Record, error) {
	var out []Record
	for _, p := range []string{Path(dir), Path(dir) + ".1"} {
		recs, err := readFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out, nil
}

// readFile parses one JSONL file, skipping anything unparseable. A missing
// file yields no records (the normal, expected case), but any other open
// error — permissions above all — is returned rather than discarded.
func readFile(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Record
	sc := bufio.NewScanner(f)
	// A record with 20 args of 128 bytes is ~2.6 KB, but a corrupt file can
	// present one enormous "line"; cap it rather than allocating without bound.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		if r.Kind != KindExec && r.Kind != KindExit {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}
