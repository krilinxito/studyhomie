package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Vault is a folder of Markdown notes.
type Vault struct {
	Root string

	mu sync.Mutex // serializes Update
}

var subdirs = []string{"topics", "roadmaps", "daily", "interactive"}

// Open creates the vault layout if needed.
func Open(root string) (*Vault, error) {
	for _, d := range subdirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return nil, err
		}
	}
	return &Vault{Root: root}, nil
}

func (v *Vault) topicPath(slug string) string {
	return filepath.Join(v.Root, "topics", slug+".md")
}

// Load reads a topic note by slug.
func (v *Vault) Load(slug string) (Note, error) {
	data, err := os.ReadFile(v.topicPath(slug))
	if err != nil {
		return Note{}, err
	}
	return ParseNote(slug, data)
}

// Exists reports whether a topic note is present.
// Update loads a note, lets fn change it and saves it, all under one lock so that two
// updates arriving together (a doubt answered while base concepts are being written)
// do not overwrite each other.
func (v *Vault) Update(slug string, fn func(*Note) error) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	n, err := v.Load(slug)
	if err != nil {
		return err
	}
	if err := fn(&n); err != nil {
		return err
	}
	return v.Save(n)
}

func (v *Vault) Exists(slug string) bool {
	_, err := os.Stat(v.topicPath(slug))
	return err == nil
}

// Save writes a topic note, stamping created/updated dates.
func (v *Vault) Save(n Note) error { return v.SaveIn("topics", n) }

// SaveIn writes a note into one of the vault folders (topics, roadmaps, daily).
func (v *Vault) SaveIn(dir string, n Note) error {
	if n.Slug == "" {
		n.Slug = Slugify(n.Meta.Title)
	}
	if n.Slug == "" {
		return errors.New("note needs a title or slug")
	}
	now := time.Now().Format("2006-01-02")
	if n.Meta.Created == "" {
		n.Meta.Created = now
	}
	n.Meta.Updated = now
	data, err := n.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(v.Root, dir, n.Slug+".md"), data, 0o644)
}

// LoadFrom reads a note from one of the vault folders.
func (v *Vault) LoadFrom(dir, slug string) (Note, error) {
	data, err := os.ReadFile(filepath.Join(v.Root, dir, slug+".md"))
	if err != nil {
		return Note{}, err
	}
	return ParseNote(slug, data)
}

// SaveInteractive writes a generated simulation to interactive/<slug>.html.
func (v *Vault) SaveInteractive(slug, html string) error {
	return os.WriteFile(filepath.Join(v.Root, "interactive", slug+".html"), []byte(html), 0o644)
}

// LoadInteractive reads a generated simulation.
func (v *Vault) LoadInteractive(slug string) (string, error) {
	data, err := os.ReadFile(filepath.Join(v.Root, "interactive", slug+".html"))
	return string(data), err
}

func (v *Vault) materialPath(slug string) string {
	return filepath.Join(v.Root, ".studyhomie", "material", slug+".md")
}

// SaveMaterial stores the extracted text of a source document (outside Obsidian's view).
func (v *Vault) SaveMaterial(slug, text string) error {
	p := v.materialPath(slug)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(text), 0o644)
}

// LoadMaterial returns the stored text of a source document, or "" if absent.
func (v *Vault) LoadMaterial(slug string) string {
	data, err := os.ReadFile(v.materialPath(slug))
	if err != nil {
		return ""
	}
	return string(data)
}

// AppendSection adds a "## heading" section to an existing note, replacing a
// previous section with the same heading so re-running a phase does not pile up.
func (v *Vault) AppendSection(slug, heading, content string) error {
	n, err := v.Load(slug)
	if err != nil {
		return err
	}
	n.Body = ReplaceSection(n.Body, heading, content)
	return v.Save(n)
}

// demoteHeadings turns "#" and "##" headings into "###" outside code fences, so
// model-written content can never open a section of its own inside a note.
func demoteHeadings(content string) string {
	lines := strings.Split(content, "\n")
	inFence := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(l, "# ") || strings.HasPrefix(l, "## ") {
			lines[i] = "### " + strings.TrimLeft(l, "# ")
		}
	}
	return strings.Join(lines, "\n")
}

// findSection returns the line range [start, end) of the "## heading" section, or start = -1.
func findSection(lines []string, heading string) (start, end int) {
	marker := "## " + heading
	start = -1
	for i, l := range lines {
		if strings.TrimSpace(l) == marker {
			start = i
			break
		}
	}
	if start < 0 {
		return -1, -1
	}
	end = len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return start, end
}

// Section returns what is under the "## heading" section of body, without the heading line.
// It is empty when the note has no such section.
func Section(body, heading string) string {
	lines := strings.Split(body, "\n")
	start, end := findSection(lines, heading)
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[start+1:end], "\n"))
}

// PlaceSection is ReplaceSection, except a section that does not exist yet goes right after
// the section named after, so the note reads in order. Without that section it is appended.
func PlaceSection(body, heading, content, after string) string {
	lines := strings.Split(body, "\n")
	if start, _ := findSection(lines, heading); start >= 0 {
		return ReplaceSection(body, heading, content)
	}
	_, end := findSection(lines, after)
	if end < 0 {
		return ReplaceSection(body, heading, content)
	}
	section := "## " + heading + "\n\n" + strings.TrimSpace(demoteHeadings(content)) + "\n"
	head := strings.TrimRight(strings.Join(lines[:end], "\n"), "\n")
	tail := strings.Join(lines[end:], "\n")
	res := head + "\n\n" + section
	if strings.TrimSpace(tail) != "" {
		res += "\n" + tail
	}
	return res
}

// ReplaceSection swaps the "## heading" section of body for content, or appends it.
// Headings inside content are demoted so the section stays self-contained.
func ReplaceSection(body, heading, content string) string {
	section := "## " + heading + "\n\n" + strings.TrimSpace(demoteHeadings(content)) + "\n"
	lines := strings.Split(body, "\n")
	start, end := findSection(lines, heading)
	if start < 0 {
		return strings.TrimRight(body, "\n") + "\n\n" + section
	}
	out := strings.Join(lines[:start], "\n")
	tail := strings.Join(lines[end:], "\n")
	res := strings.TrimRight(out, "\n")
	if res != "" {
		res += "\n\n"
	}
	res += section
	if strings.TrimSpace(tail) != "" {
		res += "\n" + tail
	}
	return res
}

// handoffPath lives in a dot-folder so Obsidian ignores it.
func (v *Vault) handoffPath(slug string) string {
	return filepath.Join(v.Root, ".studyhomie", "handoff", slug+".md")
}

// SaveHandoff stores the compact summary the next phase loads instead of the chat.
func (v *Vault) SaveHandoff(slug, text string) error {
	p := v.handoffPath(slug)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.TrimSpace(text)+"\n"), 0o644)
}

// LoadHandoff returns the stored summary, or "" if there is none.
func (v *Vault) LoadHandoff(slug string) string {
	data, err := os.ReadFile(v.handoffPath(slug))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// List returns every topic note, sorted by slug.
func (v *Vault) List() ([]Note, error) { return v.ListIn("topics") }

// ListIn returns every note of a vault folder, sorted by slug.
func (v *Vault) ListIn(sub string) ([]Note, error) {
	var notes []Note
	dir := filepath.Join(v.Root, sub)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".md" {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		slug := strings.TrimSuffix(filepath.Base(p), ".md")
		n, err := ParseNote(slug, data)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		notes = append(notes, n)
		return nil
	})
	sort.Slice(notes, func(i, j int) bool { return notes[i].Slug < notes[j].Slug })
	return notes, err
}

// Edge is a directed link between two topics.
type Edge struct{ From, To string }

// Graph builds nodes and edges from wikilinks. Links to notes that do not exist
// yet are kept as edges so planned topics show up in the roadmap.
func (v *Vault) Graph() (nodes []Note, edges []Edge, err error) {
	nodes, err = v.List()
	if err != nil {
		return nil, nil, err
	}
	for _, n := range nodes {
		for _, to := range n.Links() {
			edges = append(edges, Edge{From: n.Slug, To: to})
		}
	}
	return nodes, edges, nil
}

// Backlinks returns slugs of notes that link to the given slug.
func (v *Vault) Backlinks(slug string) ([]string, error) {
	_, edges, err := v.Graph()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range edges {
		if e.To == slug {
			out = append(out, e.From)
		}
	}
	return out, nil
}
