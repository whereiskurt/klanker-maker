package cmd

import "embed"

//go:embed help/*.txt
var helpFS embed.FS

// helpText reads an embedded help file and returns its content.
// Panics if the file is missing — all help files are compiled in.
func helpText(name string) string {
	b, err := helpFS.ReadFile("help/" + name + ".txt")
	if err != nil {
		panic("missing embedded help file: " + name + ".txt")
	}
	return string(b)
}
