package vault

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// SourcesDir holds one note per external source (a video, a page, a book, a paper), so a source
// that several guides use is a single note, and Obsidian's backlinks list every guide citing it.
const SourcesDir = "fuentes"

// CatFuentes is the data category of the source notes (not CatSources, the text extracted from material).
const CatFuentes = SourcesDir

// SourceMeta is the frontmatter of a source note.
type SourceMeta struct {
	Type        string `yaml:"type" json:"type"` // video | web | book | paper
	Title       string `yaml:"title" json:"title"`
	URL         string `yaml:"url,omitempty" json:"url,omitempty"`
	Author      string `yaml:"author,omitempty" json:"author,omitempty"` // author, or the channel of a video
	Year        int    `yaml:"year,omitempty" json:"year,omitempty"`
	Duration    string `yaml:"duration,omitempty" json:"duration,omitempty"` // of a video, as 12:34 or 1:02:03
	Lang        string `yaml:"lang,omitempty" json:"lang,omitempty"`
	Level       string `yaml:"level,omitempty" json:"level,omitempty"`             // introductorio | intermedio | avanzado
	Reliability string `yaml:"reliability,omitempty" json:"reliability,omitempty"` // alta | media | baja
	DOI         string `yaml:"doi,omitempty" json:"doi,omitempty"`
	ISBN        string `yaml:"isbn,omitempty" json:"isbn,omitempty"`
	Citations   int    `yaml:"citations,omitempty" json:"citations,omitempty"`
	Summary     string `yaml:"summary,omitempty" json:"summary,omitempty"`
	Area        string `yaml:"area,omitempty" json:"area,omitempty"`
	Seen        bool   `yaml:"seen,omitempty" json:"seen,omitempty"` // watched or read
	Key         string `yaml:"key" json:"key"`                       // what makes two sources the same one
	Created     string `yaml:"created,omitempty" json:"created,omitempty"`
	Updated     string `yaml:"updated,omitempty" json:"updated,omitempty"`
}

// SourceNote is one note of fuentes/.
type SourceNote struct {
	Slug string
	Meta SourceMeta
	Body string
}

// SourceRef is a source a guide or concept cites, with why it helps there.
type SourceRef struct {
	ID  string `yaml:"id" json:"id"`
	Why string `yaml:"why,omitempty" json:"why,omitempty"`
}

var (
	ytIDRe  = regexp.MustCompile(`(?i)(?:youtube\.com/(?:watch\?(?:[^#]*&)?v=|embed/|shorts/|live/)|youtu\.be/)([\w-]{11})`)
	doiRe   = regexp.MustCompile(`(?i)\b(10\.\d{4,9}/[^\s"<>?#]+)`)
	isbnClr = strings.NewReplacer("-", "", " ", "")
)

// YouTubeID is the id of a YouTube video URL, or "".
func YouTubeID(u string) string {
	if m := ytIDRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

// DOIOf finds a DOI in a URL or text, lower-cased; "" when there is none.
func DOIOf(s string) string {
	if m := doiRe.FindStringSubmatch(s); m != nil {
		return strings.ToLower(strings.TrimRight(m[1], ".,;)"))
	}
	return ""
}

// tracking parameters dropped when comparing URLs
var trackingParams = []string{"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "fbclid", "gclid", "ref", "si", "feature"}

// SourceKey is what makes two sources the same one: the YouTube id, the DOI, the ISBN, or the URL
// without scheme, www, tracking parameters, fragment and trailing slash. A book without a URL is
// known by its title.
func SourceKey(r Resource) string {
	if id := YouTubeID(r.URL); id != "" {
		return "youtube:" + id
	}
	if d := DOIOf(r.DOI + " " + r.URL); d != "" {
		return "doi:" + d
	}
	if isbn := isbnClr.Replace(r.ISBN); isbn != "" {
		return "isbn:" + strings.ToLower(isbn)
	}
	if r.URL != "" {
		if u, err := url.Parse(strings.TrimSpace(r.URL)); err == nil && u.Host != "" {
			q := u.Query()
			for _, p := range trackingParams {
				q.Del(p)
			}
			host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
			host = strings.TrimPrefix(host, "m.")
			k := "url:" + host + strings.TrimRight(u.EscapedPath(), "/")
			if enc := q.Encode(); enc != "" {
				k += "?" + enc
			}
			return k
		}
	}
	if t := Slugify(foldAccents(r.Title)); t != "" {
		return "title:" + r.Type + ":" + t
	}
	return ""
}

// External reports whether a resource is something outside the vault (not a local simulation).
func External(r Resource) bool {
	if r.Type == "interactive" {
		return false
	}
	return r.URL != "" || r.Title != ""
}

func (v *Vault) sourcePath(slug string) string { return filepath.Join(v.Root, SourcesDir, slug+".md") }

// LoadSource reads a source note.
func (v *Vault) LoadSource(slug string) (SourceNote, error) {
	if !validName(slug) {
		return SourceNote{}, os.ErrNotExist
	}
	data, err := os.ReadFile(v.sourcePath(slug))
	if err != nil {
		return SourceNote{}, err
	}
	s := SourceNote{Slug: slug}
	s.Body, err = parseFront(slug, data, &s.Meta)
	return s, err
}

// SaveSource writes a source note, stamping its dates.
func (v *Vault) SaveSource(s SourceNote) error {
	if err := os.MkdirAll(filepath.Join(v.Root, SourcesDir), 0o755); err != nil {
		return err
	}
	now := time.Now().Format("2006-01-02")
	if s.Meta.Created == "" {
		s.Meta.Created = now
	}
	s.Meta.Updated = now
	data, err := marshalFront(s.Meta, s.Body)
	if err != nil {
		return err
	}
	return os.WriteFile(v.sourcePath(s.Slug), data, 0o644)
}

// ListSources reads every source note, by file name.
func (v *Vault) ListSources() ([]SourceNote, error) {
	entries, err := os.ReadDir(filepath.Join(v.Root, SourcesDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []SourceNote
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		if s, err := v.LoadSource(strings.TrimSuffix(e.Name(), ".md")); err == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

// UpdateSource loads a source note, lets fn change it and saves it under the vault lock.
func (v *Vault) UpdateSource(slug string, fn func(*SourceNote) error) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, err := v.LoadSource(slug)
	if err != nil {
		return err
	}
	if err := fn(&s); err != nil {
		return err
	}
	return v.SaveSource(s)
}

// sourceSlug picks a free file name for a new source: its title, then its title and a number.
func (v *Vault) sourceSlug(title, key string) string {
	base := Slugify(foldAccents(title))
	if len(base) > 60 {
		base = strings.TrimRight(base[:60], "-")
	}
	if base == "" {
		base = Slugify(strings.NewReplacer(":", "-", "/", "-", ".", "-").Replace(key))
	}
	if base == "" {
		base = "fuente"
	}
	slug := base
	for i := 2; ; i++ {
		if _, err := os.Stat(v.sourcePath(slug)); errors.Is(err, os.ErrNotExist) {
			return slug
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
}

// sourceBody is a new source note: what it is, why it helps, the learner's notes and, for a video, the video.
func sourceBody(r Resource) string {
	var b strings.Builder
	if r.Summary != "" {
		fmt.Fprintf(&b, "## Resumen\n\n%s\n\n", r.Summary)
	}
	if r.Why != "" {
		fmt.Fprintf(&b, "## Por qué sirve\n\n%s\n\n", r.Why)
	}
	if YouTubeID(r.URL) != "" {
		fmt.Fprintf(&b, "![](%s)\n\n", r.URL) // Obsidian embeds the video
	} else if r.URL != "" {
		fmt.Fprintf(&b, "<%s>\n\n", r.URL)
	}
	b.WriteString("## Mis notas\n\n")
	return b.String()
}

// sourceIndex maps the key of every source note to its slug.
func (v *Vault) sourceIndex() map[string]string {
	idx := map[string]string{}
	list, _ := v.ListSources()
	for _, s := range list {
		k := s.Meta.Key
		if k == "" {
			k = SourceKey(s.Resource())
		}
		if k != "" {
			idx[k] = s.Slug
		}
	}
	return idx
}

// PutSource files a resource as a source note and returns its slug: the existing note when the
// vault already has that source (its empty fields are filled), else a new one.
func (v *Vault) PutSource(r Resource) (string, error) {
	return v.putSource(r, v.sourceIndex())
}

func (v *Vault) putSource(r Resource, idx map[string]string) (string, error) {
	key := SourceKey(r)
	if key == "" {
		return "", errors.New("source without url or title")
	}
	if slug, ok := idx[key]; ok {
		err := v.UpdateSource(slug, func(s *SourceNote) error {
			m := &s.Meta
			fill := func(dst *string, src string) {
				if *dst == "" {
					*dst = src
				}
			}
			fill(&m.Author, r.Author)
			fill(&m.Duration, r.Duration)
			fill(&m.Lang, r.Lang)
			fill(&m.Level, r.Level)
			fill(&m.Reliability, r.Reliability)
			fill(&m.DOI, r.DOI)
			fill(&m.Summary, r.Summary)
			if m.Year == 0 {
				m.Year = r.Year
			}
			m.Citations = max(m.Citations, r.Citations)
			return nil
		})
		return slug, err
	}
	typ := r.Type
	if typ == "" {
		typ = "web"
	}
	s := SourceNote{Slug: v.sourceSlug(r.Title, key), Body: sourceBody(r), Meta: SourceMeta{
		Type: typ, Title: strings.TrimSpace(r.Title), URL: r.URL, Author: r.Author, Year: r.Year, Duration: r.Duration,
		Lang: r.Lang, Level: r.Level, Reliability: r.Reliability, DOI: r.DOI, ISBN: r.ISBN, Citations: r.Citations,
		Summary: r.Summary, Area: r.Area, Key: key,
	}}
	if s.Meta.Title == "" {
		s.Meta.Title = r.URL
	}
	if err := v.SaveSource(s); err != nil {
		return "", err
	}
	idx[key] = s.Slug
	return s.Slug, nil
}

// Resource is a source note as the reader draws it, with the why of where it is cited.
func (s SourceNote) Resource() Resource {
	m := s.Meta
	return Resource{
		ID: s.Slug, Type: m.Type, Title: m.Title, URL: m.URL, Author: m.Author, Year: m.Year, Duration: m.Duration,
		Lang: m.Lang, Level: m.Level, Reliability: m.Reliability, DOI: m.DOI, ISBN: m.ISBN, Citations: m.Citations,
		Summary: m.Summary, Area: m.Area, Seen: m.Seen,
	}
}

// FileSources files every external resource as a source note and returns the references, in order
// and without repeats; the why of each stays with the reference.
func (v *Vault) FileSources(rs []Resource) ([]SourceRef, error) {
	idx := v.sourceIndex()
	var refs []SourceRef
	for _, r := range rs {
		if !External(r) {
			continue
		}
		slug, err := v.putSource(r, idx)
		if err != nil {
			return refs, err
		}
		if !slices.ContainsFunc(refs, func(x SourceRef) bool { return x.ID == slug }) {
			refs = append(refs, SourceRef{ID: slug, Why: strings.TrimSpace(r.Why)})
		}
	}
	return refs, nil
}

// MergeRefs adds refs to have, keeping the earlier why of a source already cited.
func MergeRefs(have, add []SourceRef) []SourceRef {
	out := append([]SourceRef(nil), have...)
	for _, r := range add {
		if !slices.ContainsFunc(out, func(x SourceRef) bool { return x.ID == r.ID }) {
			out = append(out, r)
		}
	}
	return out
}

// Expand turns references into resources the reader draws: each source note with the why of this
// citation. A reference whose note is gone is skipped.
func (v *Vault) Expand(refs []SourceRef) []Resource {
	var out []Resource
	for _, r := range refs {
		s, err := v.LoadSource(r.ID)
		if err != nil {
			continue
		}
		res := s.Resource()
		res.Why = r.Why
		out = append(out, res)
	}
	return out
}

// GuideResources is everything a guide's reader lists: its sources, then its local simulations.
func (v *Vault) GuideResources(n Note) []Resource {
	return append(v.Expand(n.Meta.Sources), n.Meta.Resources...)
}

// RecursosLines renders the "## Recursos" section: a wikilink to each source note with why it helps,
// then the local simulations as before. Vault-relative links are written relative to guias/.
func (v *Vault) RecursosLines(refs []SourceRef, local []Resource) string {
	var b strings.Builder
	for _, r := range refs {
		title := r.ID
		if s, err := v.LoadSource(r.ID); err == nil && s.Meta.Title != "" {
			title = s.Meta.Title
		}
		line := fmt.Sprintf("- [[%s/%s|%s]]", SourcesDir, r.ID, strings.ReplaceAll(title, "|", "-"))
		if r.Why != "" {
			line += " — " + r.Why
		}
		b.WriteString(line + "\n")
	}
	for _, r := range local {
		line := fmt.Sprintf("- **%s** (%s)", r.Title, r.Type)
		if r.URL != "" {
			u := r.URL
			if !strings.HasPrefix(u, "http") {
				u = "../" + u
			}
			line = fmt.Sprintf("- [%s](%s) (%s)", r.Title, u, r.Type)
		}
		if r.Why != "" {
			line += " — " + r.Why
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// SetRecursos rewrites the "## Recursos" section of body (in whatever language it already has; heading
// names a new one), or removes it when there is nothing to list.
func (v *Vault) SetRecursos(body, heading string, refs []SourceRef, local []Resource) string {
	lines := v.RecursosLines(refs, local)
	if h := existingHeading(body, "Recursos"); h != "" {
		heading = h
	}
	if heading == "" {
		heading = "Recursos"
	}
	if lines == "" {
		return RemoveSection(body, "Recursos")
	}
	return ReplaceSection(body, heading, lines)
}

// existingHeading is the heading text of a section as the note has it, or "".
func existingHeading(body, heading string) string {
	ls := strings.Split(body, "\n")
	if start, _ := findSection(ls, heading); start >= 0 {
		return strings.TrimSpace(strings.TrimSpace(ls[start])[3:])
	}
	return ""
}

// RemoveSection drops a "## heading" section from body.
func RemoveSection(body, heading string) string {
	ls := strings.Split(body, "\n")
	start, end := findSection(ls, heading)
	if start < 0 {
		return body
	}
	out := strings.TrimRight(strings.Join(ls[:start], "\n"), "\n")
	if tail := strings.Join(ls[end:], "\n"); strings.TrimSpace(tail) != "" {
		out += "\n\n" + strings.TrimLeft(tail, "\n")
	}
	return out + "\n"
}

// CitedBy lists the guides and concepts that cite a source.
func (v *Vault) CitedBy(slug string) []Ref {
	var out []Ref
	if notes, err := v.List(); err == nil {
		for _, n := range notes {
			for _, r := range n.Meta.Sources {
				if r.ID == slug {
					out = append(out, Ref{Dir: GuidesDir, Slug: n.Slug, Field: "sources", Value: slug, Extra: r.Why})
				}
			}
		}
	}
	if cs, err := v.ListConcepts(); err == nil {
		for _, c := range cs {
			for _, r := range c.Meta.Sources {
				if r.ID == slug {
					out = append(out, Ref{Dir: ConceptsDir, Slug: c.Slug, Field: "sources", Value: slug, Extra: r.Why})
				}
			}
		}
	}
	return out
}

// DeleteSource moves a source note to the trash and takes it out of the guides and concepts that
// cite it; restoring it puts the citations back.
func (v *Vault) DeleteSource(slug string) (TrashItem, error) {
	s, err := v.LoadSource(slug)
	if err != nil {
		return TrashItem{}, os.ErrNotExist
	}
	refs := v.CitedBy(slug)
	item, err := v.Trash(TrashItem{Kind: CatFuentes, Slug: slug, Title: s.Meta.Title, Files: []string{SourcesDir + "/" + slug + ".md"}, Refs: refs}, nil)
	if err != nil {
		return TrashItem{}, err
	}
	v.unlink(refs)
	return item, nil
}

// SetSourcesOf replaces the sources of a guide or concept and rewrites its "## Recursos".
func (v *Vault) SetSourcesOf(dir, slug string, fn func([]SourceRef) []SourceRef, heading string) error {
	switch dir {
	case GuidesDir:
		return v.Update(slug, func(n *Note) error {
			n.Meta.Sources = fn(n.Meta.Sources)
			n.Body = v.SetRecursos(n.Body, heading, n.Meta.Sources, n.Meta.Resources)
			return nil
		})
	case ConceptsDir:
		return v.UpdateConcept(slug, func(c *Concept) error {
			c.Meta.Sources = fn(c.Meta.Sources)
			c.Body = v.SetRecursos(c.Body, heading, c.Meta.Sources, nil)
			return nil
		})
	}
	return fmt.Errorf("unknown dir %q", dir)
}

// SourceStats is how a source is used: which notes cite it.
type SourceStats struct {
	SourceNote
	CitedBy []Ref
}

// Library lists every source with the notes citing it, newest first.
func (v *Vault) Library() []SourceStats {
	list, _ := v.ListSources()
	cites := map[string][]Ref{}
	if notes, err := v.List(); err == nil {
		for _, n := range notes {
			for _, r := range n.Meta.Sources {
				cites[r.ID] = append(cites[r.ID], Ref{Dir: GuidesDir, Slug: n.Slug, Value: n.Meta.Title, Extra: r.Why})
			}
		}
	}
	if cs, err := v.ListConcepts(); err == nil {
		for _, c := range cs {
			for _, r := range c.Meta.Sources {
				cites[r.ID] = append(cites[r.ID], Ref{Dir: ConceptsDir, Slug: c.Slug, Value: c.Meta.Title, Extra: r.Why})
			}
		}
	}
	out := make([]SourceStats, 0, len(list))
	for _, s := range list {
		out = append(out, SourceStats{SourceNote: s, CitedBy: cites[s.Slug]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Meta.Created > out[j].Meta.Created })
	return out
}

// MigrateSources moves the external resources a guide keeps in its frontmatter (before fuentes/
// existed) into source notes, and rewrites its "## Recursos". Guides already migrated are left
// alone, so it can run at every start. It returns how many guides changed.
func (v *Vault) MigrateSources() (int, error) {
	notes, err := v.List()
	if err != nil {
		return 0, err
	}
	idx := v.sourceIndex()
	changed := 0
	for _, n := range notes {
		var ext, local []Resource
		for _, r := range n.Meta.Resources {
			if External(r) {
				ext = append(ext, r)
			} else {
				local = append(local, r)
			}
		}
		if len(ext) == 0 {
			continue
		}
		var refs []SourceRef
		for _, r := range ext {
			if r.Area == "" {
				r.Area = n.Meta.Area
			}
			slug, err := v.putSource(r, idx)
			if err != nil {
				continue
			}
			refs = MergeRefs(refs, []SourceRef{{ID: slug, Why: strings.TrimSpace(r.Why)}})
		}
		err := v.Update(n.Slug, func(m *Note) error {
			m.Meta.Sources = MergeRefs(m.Meta.Sources, refs)
			m.Meta.Resources = local
			m.Body = v.SetRecursos(m.Body, "", m.Meta.Sources, local)
			return nil
		})
		if err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}
