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
func ReadDir(dir string) ([]Record, error) {
	var out []Record
	for _, p := range []string{Path(dir), Path(dir) + ".1"} {
		out = append(out, readFile(p)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out, nil
}

// readFile parses one JSONL file, skipping anything unparseable. An unreadable
// file yields no records rather than an error.
func readFile(path string) []Record {
	f, err := os.Open(path)
	if err != nil {
		return nil
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
	return out
}
