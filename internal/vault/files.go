package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Folders, in the order the vault page shows them: the ones a person browses and edits.
var Browsable = []string{GuidesDir, ConceptsDir, SourcesDir, ApuntesDir, FlashcardsDir, "roadmaps", "daily"}

// File is one note of the vault, as the vault page lists it.
type File struct {
	Dir     string    `json:"dir"`
	Slug    string    `json:"slug"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
	Size    int64     `json:"size"`
}

// Stats sums up the vault for its page.
type Stats struct {
	Notes map[string]int `json:"notes"` // per folder
	Files int            `json:"files"` // every file, attachments and app state included
	Bytes int64          `json:"bytes"`
}

func browsable(dir string) bool {
	for _, d := range Browsable {
		if d == dir {
			return true
		}
	}
	return false
}

// titleOf reads a note's title from its frontmatter ("title" or "titulo"), falling back to its first heading.
func titleOf(data []byte) string {
	var meta struct {
		Title  string `yaml:"title"`
		Titulo string `yaml:"titulo"`
	}
	body, _ := parseFront("", data, &meta)
	if meta.Title != "" {
		return meta.Title
	}
	if meta.Titulo != "" {
		return meta.Titulo
	}
	for _, line := range strings.SplitN(body, "\n", 20) {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}

// Files lists the notes of the browsable folders, newest first within each folder.
func (v *Vault) Files() ([]File, error) {
	var out []File
	for _, dir := range Browsable {
		entries, err := os.ReadDir(filepath.Join(v.Root, dir))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var files []File
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			slug := strings.TrimSuffix(e.Name(), ".md")
			data, _ := os.ReadFile(filepath.Join(v.Root, dir, e.Name()))
			title := titleOf(data)
			if title == "" {
				title = slug
			}
			files = append(files, File{Dir: dir, Slug: slug, Title: title, Updated: info.ModTime(), Size: info.Size()})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Updated.After(files[j].Updated) })
		out = append(out, files...)
	}
	return out, nil
}

// Stat counts the notes per folder and the size of the whole vault.
func (v *Vault) Stat() Stats {
	st := Stats{Notes: map[string]int{}}
	filepath.WalkDir(v.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		st.Files++
		if info, err := d.Info(); err == nil {
			st.Bytes += info.Size()
		}
		if rel, err := filepath.Rel(v.Root, path); err == nil && strings.HasSuffix(rel, ".md") {
			if dir := filepath.ToSlash(filepath.Dir(rel)); browsable(dir) {
				st.Notes[dir]++
			}
		}
		return nil
	})
	return st
}

// Hit is a note that matches a search, with the text around the first match.
type Hit struct {
	File
	Snippet string `json:"snippet"`
	Count   int    `json:"count"`
}

// Search finds the notes whose title or text contains q, ignoring case and accents.
// Title matches come first; at most limit hits.
func (v *Vault) Search(q string, limit int) ([]Hit, error) {
	needle := strings.ToLower(foldAccents(strings.TrimSpace(q)))
	if needle == "" {
		return nil, nil
	}
	files, err := v.Files()
	if err != nil {
		return nil, err
	}
	var hits []Hit
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(v.Root, f.Dir, f.Slug+".md"))
		if err != nil {
			continue
		}
		var meta map[string]any
		body, _ := parseFront(f.Slug, data, &meta)
		body = plainText(body)
		hay := strings.ToLower(foldAccents(body))
		count := strings.Count(hay, needle)
		inTitle := strings.Contains(strings.ToLower(foldAccents(f.Title)), needle)
		if count == 0 && !inTitle {
			continue
		}
		h := Hit{File: f, Count: count}
		if i := strings.Index(hay, needle); i >= 0 {
			h.Snippet = snippet(body, hay, i, len(needle))
		}
		if inTitle {
			h.Count += 1000 // ranks title matches first
		}
		hits = append(hits, h)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Count > hits[j].Count })
	for i := range hits {
		if hits[i].Count >= 1000 {
			hits[i].Count -= 1000
		}
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

var (
	fenceRe   = regexp.MustCompile("(?s)```[^\n]*\n.*?```")
	aliasRe   = regexp.MustCompile(`!?\[\[[^\]|]+\|([^\]]+)\]\]`)
	wikiRe    = regexp.MustCompile(`!?\[\[([^\]]+)\]\]`)
	mdLinkRe  = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	calloutRe = regexp.MustCompile(`(?m)^>\s*\[![a-zA-Z]+\][-+]?\s*`)
	markRe    = regexp.MustCompile("(?m)^#{1,6}\\s+|^>\\s?|\\*\\*|__|`")
)

// plainText is a note as words to search and quote: no code blocks (the visuals are JSON),
// links read as their text, no heading or emphasis marks.
func plainText(body string) string {
	body = fenceRe.ReplaceAllString(body, " ")
	body = aliasRe.ReplaceAllString(body, "$1")
	body = wikiRe.ReplaceAllString(body, "$1")
	body = mdLinkRe.ReplaceAllString(body, "$1")
	body = calloutRe.ReplaceAllString(body, "")
	return markRe.ReplaceAllString(body, "")
}

// snippet cuts about 140 characters of body around a match found at byte i of its folded copy.
// Folding keeps byte offsets only for ASCII, so the cut is made on the original by rune count.
func snippet(body, folded string, i, n int) string {
	runeAt := utf8.RuneCountInString(folded[:i])
	rs := []rune(body)
	from := max(0, runeAt-60)
	to := min(len(rs), runeAt+utf8.RuneCountInString(folded[i:i+n])+80)
	s := strings.Join(strings.Fields(string(rs[from:to])), " ")
	if from > 0 {
		s = "…" + s
	}
	if to < len(rs) {
		s += "…"
	}
	return s
}

func (v *Vault) rawPath(dir, slug string) (string, error) {
	if !browsable(dir) || slug == "" || Slugify(slug) != slug && ConceptID(slug) != slug {
		return "", fmt.Errorf("nota inválida %s/%s", dir, slug)
	}
	return filepath.Join(v.Root, dir, slug+".md"), nil
}

// ReadRaw returns a note's file as it is on disk, frontmatter included.
func (v *Vault) ReadRaw(dir, slug string) (string, time.Time, error) {
	p, err := v.rawPath(dir, slug)
	if err != nil {
		return "", time.Time{}, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", time.Time{}, err
	}
	data, err := os.ReadFile(p)
	return string(data), info.ModTime(), err
}

// WriteRaw replaces a note's file with text edited by hand. The frontmatter must still be valid
// YAML, or the app could no longer read the note; the file must already exist.
func (v *Vault) WriteRaw(dir, slug, text string) error {
	p, err := v.rawPath(dir, slug)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err != nil {
		return err
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.HasPrefix(text, "---\n") {
		if !strings.Contains(text[4:], "\n---\n") && !strings.HasSuffix(text, "\n---") {
			return errors.New("el bloque de propiedades (---) no está cerrado")
		}
		var meta map[string]any
		if _, err := parseFront(slug, []byte(text), &meta); err != nil {
			return fmt.Errorf("las propiedades (entre ---) no son YAML válido: %v", err)
		}
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return os.WriteFile(p, []byte(text), 0o644)
}
