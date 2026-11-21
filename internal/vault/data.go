package vault

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"
)

// Data categories: what the learner can see, weigh and delete from Ajustes → Datos.
const (
	CatGuides      = GuidesDir
	CatConcepts    = ConceptsDir
	CatRoadmaps    = "roadmaps"
	CatApuntes     = ApuntesDir
	CatAttachments = "adjuntos"
	CatDecks       = FlashcardsDir
	CatMaterial    = MaterialDir
	CatInteractive = "interactive"
	CatDaily       = "daily"
	CatSessions    = "sessions"
	CatUploads     = "uploads"
	CatHandoffs    = "handoffs"
	CatSources     = "sources"
	CatPractice    = "practice"
	CatState       = "state"
)

// Categories in the order the settings page lists them.
var Categories = []string{
	CatGuides, CatConcepts, CatRoadmaps, CatFuentes, CatApuntes, CatAttachments, CatDecks, CatMaterial,
	CatInteractive, CatDaily, CatSessions, CatPractice, CatUploads, CatHandoffs, CatSources, CatState,
}

// catSpec says where a category lives: files with ext in dir, or its subfolders (dirs).
type catSpec struct {
	dir  string
	ext  string
	dirs bool
}

var catSpecs = map[string]catSpec{
	CatGuides:      {dir: GuidesDir, ext: ".md"},
	CatConcepts:    {dir: ConceptsDir, ext: ".md"},
	CatRoadmaps:    {dir: "roadmaps", ext: ".md"},
	CatApuntes:     {dir: ApuntesDir, ext: ".md"},
	CatAttachments: {dir: AttachDir},
	CatDecks:       {dir: FlashcardsDir, ext: ".md"},
	CatMaterial:    {dir: MaterialDir, dirs: true},
	CatInteractive: {dir: "interactive", ext: ".html"},
	CatDaily:       {dir: "daily", ext: ".md"},
	CatSessions:    {dir: ".studyhomie/sessions", ext: ".json"},
	CatUploads:     {dir: ".studyhomie/uploads", dirs: true},
	CatHandoffs:    {dir: ".studyhomie/handoff", ext: ".md"},
	CatSources:     {dir: ".studyhomie/material", ext: ".md"},
	CatPractice:    {dir: PracticeDir, ext: ".json"},
	CatFuentes:     {dir: SourcesDir, ext: ".md"},
}

// PracticeDir holds the quiz and Feynman sessions that can be resumed, one JSON file each.
const PracticeDir = ".studyhomie/practice"

// State files: one item each, reset rather than deleted.
var stateFiles = map[string]string{
	"stats": ".studyhomie/stats.json",
	"cards": ".studyhomie/cards.json",
}

// DataItem is one deletable thing of a category.
type DataItem struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Bytes    int64     `json:"bytes"`
	Modified time.Time `json:"modified"`
	Rel      string    `json:"rel"` // vault-relative path
}

// CategorySize is how many items a category has and how much they weigh.
type CategorySize struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
	Bytes int64  `json:"bytes"`
}

// IsCategory reports whether name is a known data category.
func IsCategory(name string) bool {
	_, ok := catSpecs[name]
	return ok || name == CatState
}

// Inventory weighs every category.
func (v *Vault) Inventory() []CategorySize {
	out := make([]CategorySize, 0, len(Categories))
	for _, c := range Categories {
		items, _ := v.Items(c)
		cs := CategorySize{ID: c, Count: len(items)}
		for _, it := range items {
			cs.Bytes += it.Bytes
		}
		out = append(out, cs)
	}
	return out
}

// Items lists a category, newest first.
func (v *Vault) Items(cat string) ([]DataItem, error) {
	if cat == CatState {
		var out []DataItem
		for _, id := range []string{"stats", "cards"} {
			rel := stateFiles[id]
			if info, err := os.Stat(v.abs(rel)); err == nil {
				out = append(out, DataItem{ID: id, Title: id, Bytes: info.Size(), Modified: info.ModTime(), Rel: rel})
			}
		}
		return out, nil
	}
	spec, ok := catSpecs[cat]
	if !ok {
		return nil, os.ErrNotExist
	}
	entries, err := os.ReadDir(v.abs(spec.dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []DataItem
	for _, e := range entries {
		name := e.Name()
		if spec.dirs != e.IsDir() || strings.HasSuffix(name, ".tmp") {
			continue
		}
		if !spec.dirs && spec.ext != "" && !strings.HasSuffix(name, spec.ext) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		rel := spec.dir + "/" + name
		it := DataItem{ID: strings.TrimSuffix(name, spec.ext), Title: name, Bytes: info.Size(), Modified: info.ModTime(), Rel: rel}
		if spec.ext != "" {
			it.Title = it.ID
		}
		if spec.dirs {
			it.Bytes = pathSize(v.abs(rel))
			it.Title, it.Modified = dirSummary(v.abs(rel), name, info.ModTime())
		}
		switch {
		case spec.ext == ".md":
			if data, err := os.ReadFile(v.abs(rel)); err == nil {
				if t := titleOf(data); t != "" {
					it.Title = t
				}
			}
		case cat == CatSessions:
			if data, err := os.ReadFile(v.abs(rel)); err == nil {
				var head struct {
					Title string `json:"title"`
				}
				if json.Unmarshal(data, &head) == nil && head.Title != "" {
					it.Title = head.Title
				}
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// dirSummary names a folder by the files inside it (an upload folder holds one file) and takes
// the newest modification time.
func dirSummary(dir, name string, mod time.Time) (string, time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return name, mod
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		if info, err := e.Info(); err == nil && info.ModTime().After(mod) {
			mod = info.ModTime()
		}
	}
	if isHex(name) {
		return strings.Join(names, ", "), mod
	}
	return name, mod
}

func isHex(s string) bool {
	if len(s) != 16 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// ItemPath is the vault-relative path of one item, or os.ErrNotExist.
func (v *Vault) ItemPath(cat, id string) (string, error) {
	if cat == CatState {
		rel, ok := stateFiles[id]
		if !ok {
			return "", os.ErrNotExist
		}
		return rel, nil
	}
	spec, ok := catSpecs[cat]
	if !ok || !validName(id) {
		return "", os.ErrNotExist
	}
	rel := spec.dir + "/" + id + spec.ext
	if _, err := os.Lstat(v.abs(rel)); err != nil {
		return "", os.ErrNotExist
	}
	return rel, nil
}

// RemoveItem moves one item to the trash. Guides and concepts take their links with them (see
// DeleteGuide); the rest are plain files. extra is kept with the item (see Trash).
func (v *Vault) RemoveItem(cat, id string, extra map[string][]byte) (TrashItem, error) {
	switch cat {
	case CatGuides:
		return v.DeleteGuide(id, nil, extra)
	case CatConcepts:
		return v.DeleteConcept(id)
	case CatFuentes:
		return v.DeleteSource(id)
	}
	rel, err := v.ItemPath(cat, id)
	if err != nil {
		return TrashItem{}, err
	}
	title := id
	if items, _ := v.Items(cat); items != nil {
		for _, it := range items {
			if it.ID == id {
				title = it.Title
			}
		}
	}
	return v.Trash(TrashItem{Kind: cat, Slug: id, Title: title, Files: []string{rel}, Replace: cat == CatState}, extra)
}

// Orphan is a leftover file whose owner no longer exists.
type Orphan struct {
	Cat   string `json:"cat"`
	ID    string `json:"id"`
	Title string `json:"title"`
	Rel   string `json:"rel"`
	Bytes int64  `json:"bytes"`
}

// UploadKeep is how long an upload nobody uses is kept before it counts as an orphan.
const UploadKeep = 24 * time.Hour

// Orphans finds files left behind by notes that are gone: handoffs, extracted source text,
// simulations and material folders without their guide or roadmap, and old uploads.
func (v *Vault) Orphans(now time.Time) []Orphan {
	owned := func(slug string) bool {
		if v.Exists(slug) {
			return true
		}
		_, err := os.Stat(v.abs("roadmaps/" + slug + ".md"))
		return err == nil
	}
	var out []Orphan
	for _, cat := range []string{CatHandoffs, CatSources, CatInteractive, CatMaterial} {
		items, _ := v.Items(cat)
		for _, it := range items {
			if !owned(it.ID) {
				out = append(out, Orphan{Cat: cat, ID: it.ID, Title: it.Title, Rel: it.Rel, Bytes: it.Bytes})
			}
		}
	}
	items, _ := v.Items(CatUploads)
	for _, it := range items {
		if now.Sub(it.Modified) > UploadKeep {
			out = append(out, Orphan{Cat: CatUploads, ID: it.ID, Title: it.Title, Rel: it.Rel, Bytes: it.Bytes})
		}
	}
	return out
}
