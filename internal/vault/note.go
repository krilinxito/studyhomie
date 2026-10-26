// Package vault stores notes as Markdown files with YAML frontmatter and
// [[wikilinks]], so the folder opens directly in Obsidian.
package vault

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Resource is an external study resource. It is always a link, never copied content.
type Resource struct {
	Type  string `yaml:"type" json:"type"` // video | web | book | paper | interactive
	Title string `yaml:"title" json:"title"`
	URL   string `yaml:"url,omitempty" json:"url,omitempty"`
	Why   string `yaml:"why,omitempty" json:"why,omitempty"`
}

// Meta is the frontmatter of a topic note.
type Meta struct {
	Title     string     `yaml:"title" json:"title"`
	Status    string     `yaml:"status,omitempty" json:"status,omitempty"` // new | prepared | tested | explained
	Source    string     `yaml:"source,omitempty" json:"source,omitempty"` // roadmap | material | daily | adhoc
	Mastery   int        `yaml:"mastery,omitempty" json:"mastery,omitempty"`
	Summary   string     `yaml:"summary,omitempty" json:"summary,omitempty"`   // what the learner should understand (roadmap)
	Field     string     `yaml:"field,omitempty" json:"field,omitempty"`       // knowledge field (daily)
	Roadmap   string     `yaml:"roadmap,omitempty" json:"roadmap,omitempty"`   // slug of the roadmap this topic belongs to
	Order     int        `yaml:"order,omitempty" json:"order,omitempty"`       // position within the roadmap
	Requires  []string   `yaml:"requires,omitempty" json:"requires,omitempty"` // slugs of prerequisite topics
	Material  string     `yaml:"material,omitempty" json:"material,omitempty"` // slug of the source material, if any
	Created   string     `yaml:"created,omitempty" json:"created,omitempty"`
	Updated   string     `yaml:"updated,omitempty" json:"updated,omitempty"`
	Resources []Resource `yaml:"resources,omitempty" json:"resources,omitempty"`
}

type Note struct {
	Slug string
	Meta Meta
	Body string
}

var wikilinkRe = regexp.MustCompile(`\[\[([^\]\|#]+)(?:[#|][^\]]*)?\]\]`)

// Links returns the slugs this note links to, without duplicates, in order.
func (n Note) Links() []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range wikilinkRe.FindAllStringSubmatch(n.Body, -1) {
		s := Slugify(m[1])
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Slugify turns a title into a file-safe lowercase slug.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// Marshal renders the note as frontmatter + body.
func (n Note) Marshal() ([]byte, error) {
	fm, err := yaml.Marshal(n.Meta)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimLeft(n.Body, "\n"))
	if !strings.HasSuffix(n.Body, "\n") {
		b.WriteByte('\n')
	}
	return b.Bytes(), nil
}

// ParseNote reads frontmatter + body. A file without frontmatter is all body.
func ParseNote(slug string, data []byte) (Note, error) {
	n := Note{Slug: slug}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.HasPrefix(text, "---\n") {
		rest := text[4:]
		if end := strings.Index(rest, "\n---\n"); end >= 0 {
			if err := yaml.Unmarshal([]byte(rest[:end]), &n.Meta); err != nil {
				return n, fmt.Errorf("frontmatter of %s: %w", slug, err)
			}
			n.Body = strings.TrimLeft(rest[end+5:], "\n")
			return n, nil
		}
	}
	n.Body = text
	return n, nil
}
