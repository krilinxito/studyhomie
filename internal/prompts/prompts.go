// Package prompts holds the embedded system prompts, one per phase.
package prompts

import (
	"embed"
	"strings"
)

//go:embed *.md
var files embed.FS

// voiced are the prompts whose output the learner reads, so they share the writing rules in style.md.
// The others produce HTML, transcriptions or summaries for the program.
var voiced = map[string]bool{"prepare": true, "feynman": true, "quiz": true, "daily": true, "scope": true, "roadmap": true, "extend": true, "ask": true}

// Get returns the named prompt (without extension) with {{language}} filled in.
func Get(name, language string) string {
	data, err := files.ReadFile(name + ".md")
	if err != nil {
		panic("missing prompt " + name) // programming error: prompts are embedded at build time
	}
	text := string(data)
	if voiced[name] {
		style, err := files.ReadFile("style.md")
		if err != nil {
			panic("missing prompt style") // programming error: prompts are embedded at build time
		}
		text = strings.TrimRight(text, "\n") + "\n\n" + string(style)
	}
	return strings.ReplaceAll(text, "{{language}}", language)
}
