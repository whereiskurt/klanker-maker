package main

import (
	"fmt"

	"github.com/whereiskurt/klanker-maker/pkg/allowlistgen"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// runProfileGen writes a SandboxProfile to stdout describing what this sandbox
// actually reached.
//
// It reuses allowlistgen.Recorder rather than rendering YAML here so the output
// is the same annotated shape `km shell --learn` produces. An operator who has
// read one learned.*.yaml can read this without learning a second format.
//
// Only the allowed census feeds the recorder. A denied destination was never
// reachable, so emitting it into an allowlist would hand back a profile that
// grants more than the box ever had.
func runProfileGen(o opts) int {
	recs, err := flowlog.ReadDir(o.flowDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot read flow store %s: %v\n", prog, o.flowDir, err)
		return 1
	}
	c := flowlog.Summarize(recs)

	rec := allowlistgen.NewRecorder()
	for _, d := range c.Allowed {
		if d.Host == "" {
			continue
		}
		rec.RecordDNSQuery(d.Host)
		rec.RecordHost(d.Host)
	}

	yamlBytes, err := rec.GenerateAnnotatedYAML("")
	if err != nil {
		fmt.Fprintf(o.stderr, "%s profile: generate failed: %v\n", prog, err)
		return 1
	}
	fmt.Fprint(o.stdout, string(yamlBytes))
	return 0
}
