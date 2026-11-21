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
	Summary   string     `yaml:"summary,omitempty" json:"summary,omitempty"`     // what the learner should understand (roadmap)
	Field     string     `yaml:"field,omitempty" json:"field,omitempty"`         // knowledge field (daily)
	Area      string     `yaml:"area,omitempty" json:"area,omitempty"`           // canonical area (matematicas, fisica, musica…)
	Roadmap   string     `yaml:"roadmap,omitempty" json:"roadmap,omitempty"`     // slug of the roadmap this topic belongs to
	Order     int        `yaml:"order,omitempty" json:"order,omitempty"`         // position within the roadmap
	Block     string     `yaml:"block,omitempty" json:"block,omitempty"`         // field of the roadmap it belongs to ("Linux", "Redes"…)
	Requires  []string   `yaml:"requires,omitempty" json:"requires,omitempty"`   // slugs of prerequisite topics
	Material  string     `yaml:"material,omitempty" json:"material,omitempty"`   // slug of the source material, if any
	Materials []string   `yaml:"materials,omitempty" json:"materials,omitempty"` // vault-relative paths under material/<topic>/
	Created   string     `yaml:"created,omitempty" json:"created,omitempty"`
	Updated   string     `yaml:"updated,omitempty" json:"updated,omitempty"`
	Resources []Resource `yaml:"resources,omitempty" json:"resources,omitempty"`

	// A guide generated with concept notes: which ones it stands on, how deep the plan went and its focus.
	Concepts []string `yaml:"concepts,omitempty" json:"concepts,omitempty"` // every concept note of the guide
	Base     []string `yaml:"base,omitempty" json:"base,omitempty"`         // the ones the topic needs directly
	Depth    int      `yaml:"depth,omitempty" json:"depth,omitempty"`
	Focus    string   `yaml:"focus,omitempty" json:"focus,omitempty"`
	// A roadmap note: how much of the field it covers (roadmap.Reach*) and how many levels of concept
	// notes each of its guides gets. nil means the configured depth.
	Reach      int  `yaml:"reach,omitempty" json:"reach,omitempty"`
	GuideDepth *int `yaml:"guide_depth,omitempty" json:"guideDepth,omitempty"`
	// The part of the iceberg under water: concepts the guide stands on that have no note yet.
	Pending []PendingConcept `yaml:"pending,omitempty" json:"pending,omitempty"`
}

// PendingConcept is a concept mapped under a guide but not written yet: one click writes it.
type PendingConcept struct {
	ID       string   `yaml:"id" json:"id"`
	Title    string   `yaml:"title" json:"title"`
	Summary  string   `yaml:"summary,omitempty" json:"summary,omitempty"`
	Category string   `yaml:"category,omitempty" json:"category,omitempty"`
	Requires []string `yaml:"requires,omitempty" json:"requires,omitempty"`
	Level    int      `yaml:"level,omitempty" json:"level,omitempty"`
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
func (n Note) Marshal() ([]byte, error) { return marshalFront(n.Meta, n.Body) }

// ParseNote reads frontmatter + body. A file without frontmatter is all body.
func ParseNote(slug string, data []byte) (Note, error) {
	n := Note{Slug: slug}
	body, err := parseFront(slug, data, &n.Meta)
	n.Body = body
	return n, err
}

func marshalFront(meta any, body string) ([]byte, error) {
	fm, err := yaml.Marshal(meta)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimLeft(body, "\n"))
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	return b.Bytes(), nil
}

// parseFront fills meta from the frontmatter and returns the body. A file without frontmatter is all body.
func parseFront(slug string, data []byte, meta any) (string, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.HasPrefix(text, "---\n") {
		rest := text[4:]
		if end := strings.Index(rest, "\n---\n"); end >= 0 {
			if err := yaml.Unmarshal([]byte(rest[:end]), meta); err != nil {
				return text, fmt.Errorf("frontmatter of %s: %w", slug, err)
			}
			return strings.TrimLeft(rest[end+5:], "\n"), nil
		}
	}
	return text, nil
}
