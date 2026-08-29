package flowlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// ReadDir reads every producer file in dir (live files and their one rotated
// generation) and returns all records sorted oldest-first.
//
// It never fails on bad data. A missing directory is the normal state of a box
// that has not yet made an egress decision; a corrupt line is skipped. The only
// error returned is a genuine directory-listing failure, because that means the
// caller is being told about a store it cannot see at all.
func ReadDir(dir string) ([]Record, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "flows.*.jsonl"))
	if err != nil {
		return nil, err
	}
	rotated, err := filepath.Glob(filepath.Join(dir, "flows.*.jsonl.1"))
	if err != nil {
		return nil, err
	}
	matches = append(matches, rotated...)

	var out []Record
	for _, path := range matches {
		out = append(out, readFile(path)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out, nil
}

// readFile parses one JSONL file, skipping anything unparseable. An unreadable
// file yields no records rather than an error: one dead producer must not
// blind the operator to the other two.
func readFile(path string) []Record {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []Record
	sc := bufio.NewScanner(f)
	// Records are small, but a corrupt file can present one enormous "line";
	// cap it rather than letting the scanner allocate without bound.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		if r.Src == "" || r.Verdict == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}
