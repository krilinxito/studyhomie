// Package prompts holds the embedded system prompts, one per phase.
package prompts

import (
	"embed"
	"strings"
)

//go:embed *.md
var files embed.FS

// Get returns the named prompt (without extension) with {{language}} filled in.
func Get(name, language string) string {
	data, err := files.ReadFile(name + ".md")
	if err != nil {
		panic("missing prompt " + name) // programming error: prompts are embedded at build time
	}
	return strings.ReplaceAll(string(data), "{{language}}", language)
}
