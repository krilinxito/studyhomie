package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Folders of the vault. Guides are the study guides of a topic (with their quiz and Feynman
// history); concepts are the short building-block notes guides stand on.
const (
	GuidesDir   = "guias"
	ConceptsDir = "conceptos"
	legacyDir   = "topics"
)

// ConceptMeta is the frontmatter of a concept note. The keys follow the convention of a
// hand-made Obsidian vault (titulo, aliases, tags, categoria), plus requiere and guias so a
// prerequisite can be told apart from a "see also" link.
type ConceptMeta struct {
	Title    string   `yaml:"titulo" json:"title"`
	Aliases  []string `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	Tags     []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Category string   `yaml:"categoria,omitempty" json:"category,omitempty"`
	Summary  string   `yaml:"resumen,omitempty" json:"summary,omitempty"`
	Requires []string `yaml:"requiere,omitempty" json:"requires,omitempty"` // ids of the concepts it builds on
	Guides   []string `yaml:"guias,omitempty" json:"guides,omitempty"`      // slugs of the guides that use it
	Created  string   `yaml:"creado,omitempty" json:"created,omitempty"`
	Updated  string   `yaml:"actualizado,omitempty" json:"updated,omitempty"`
}

// Concept is one concept note.
type Concept struct {
	Slug string
	Meta ConceptMeta
	Body string
}

// Links returns the slugs this concept links to.
func (c Concept) Links() []string { return Note{Body: c.Body}.Links() }

// ConceptID is the id (and file name) of a concept: kebab-case, without accents.
func ConceptID(title string) string { return Slugify(foldAccents(title)) }

var accents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "Á", "A", "À", "A", "Â", "A", "Ä", "A", "Ã", "A",
	"é", "e", "è", "e", "ê", "e", "ë", "e", "É", "E", "È", "E", "Ê", "E", "Ë", "E",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "Í", "I", "Ì", "I", "Î", "I", "Ï", "I",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "Ó", "O", "Ò", "O", "Ô", "O", "Ö", "O", "Õ", "O",
	"ú", "u", "ù", "u", "û", "u", "ü", "u", "Ú", "U", "Ù", "U", "Û", "U", "Ü", "U",
	"ñ", "n", "Ñ", "N", "ç", "c", "Ç", "C",
)

func foldAccents(s string) string { return accents.Replace(s) }

func (v *Vault) conceptPath(id string) string { return filepath.Join(v.Root, ConceptsDir, id+".md") }

// ConceptExists reports whether a concept note is present.
func (v *Vault) ConceptExists(id string) bool {
	_, err := os.Stat(v.conceptPath(id))
	return err == nil
}

// LoadConcept reads a concept note by id.
func (v *Vault) LoadConcept(id string) (Concept, error) {
	data, err := os.ReadFile(v.conceptPath(id))
	if err != nil {
		return Concept{}, err
	}
	c := Concept{Slug: id}
	c.Body, err = parseFront(id, data, &c.Meta)
	return c, err
}

// SaveConcept writes a concept note, stamping its dates.
func (v *Vault) SaveConcept(c Concept) error {
	if c.Slug == "" {
		c.Slug = ConceptID(c.Meta.Title)
	}
	if c.Slug == "" {
		return errors.New("concept needs a title or id")
	}
	now := time.Now().Format("2006-01-02")
	if c.Meta.Created == "" {
		c.Meta.Created = now
	}
	c.Meta.Updated = now
	data, err := marshalFront(c.Meta, c.Body)
	if err != nil {
		return err
	}
	return os.WriteFile(v.conceptPath(c.Slug), data, 0o644)
}

// UpdateConcept loads a concept, lets fn change it and saves it under the vault lock.
func (v *Vault) UpdateConcept(id string, fn func(*Concept) error) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	c, err := v.LoadConcept(id)
	if err != nil {
		return err
	}
	if err := fn(&c); err != nil {
		return err
	}
	return v.SaveConcept(c)
}

// ListConcepts returns every concept note, sorted by id.
func (v *Vault) ListConcepts() ([]Concept, error) {
	dir := filepath.Join(v.Root, ConceptsDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Concept
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		c, err := v.LoadConcept(strings.TrimSuffix(e.Name(), ".md"))
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil // ReadDir sorts by file name
}

// migrate moves the notes of the old "topics" folder into "guias", once. A file that would
// overwrite a guide stays where it is.
func migrate(root string) error {
	old := filepath.Join(root, legacyDir)
	entries, err := os.ReadDir(old)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dst := filepath.Join(root, GuidesDir)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	left := 0
	for _, e := range entries {
		to := filepath.Join(dst, e.Name())
		if _, err := os.Stat(to); err == nil {
			left++
			continue
		}
		if err := os.Rename(filepath.Join(old, e.Name()), to); err != nil {
			return err
		}
	}
	if left == 0 {
		return os.Remove(old)
	}
	return nil
}
