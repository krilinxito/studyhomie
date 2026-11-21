package vault

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// TrashKeep is how long deleted items stay in the trash before they are purged for good.
const TrashKeep = 30 * 24 * time.Hour

// ErrRestoreConflict means something new already lives where a trashed item wants to go back.
var ErrRestoreConflict = errors.New("restore conflict")

// TrashItem is one deletion: the files it moved away and the links it cut, so it can be undone.
type TrashItem struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"` // the data category it came from (guias, conceptos, sessions…)
	Slug    string    `json:"slug,omitempty"`
	Title   string    `json:"title"`
	Deleted time.Time `json:"deleted"`
	Files   []string  `json:"files"` // vault-relative, slash-separated paths as they were
	Bytes   int64     `json:"bytes"`
	Refs    []Ref     `json:"refs,omitempty"`
	// Replace lets a restore overwrite what is there now (state files rebuilt after a reset).
	Replace bool `json:"replace,omitempty"`
}

// Ref is a link from another note that a deletion removed: Value was in Field of Dir/Slug.
type Ref struct {
	Dir   string `json:"dir"` // GuidesDir or ConceptsDir
	Slug  string `json:"slug"`
	Field string `json:"field"` // guides | requires | concepts | base
	Value string `json:"value"`
}

func (v *Vault) trashRoot() string { return filepath.Join(v.Root, ".studyhomie", "trash") }

func (v *Vault) trashDir(id string) (string, error) {
	if !validName(id) {
		return "", os.ErrNotExist
	}
	return filepath.Join(v.trashRoot(), id), nil
}

// validName accepts a single path element: no separators, no "." or "..".
func validName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\:`) && filepath.Base(s) == s
}

// Trash moves the item's files (vault-relative paths; missing ones are skipped) into the trash.
// extra holds data that is not a file of the vault (e.g. the review state of a deck), kept with
// the item and handed back by Restore. It fails with os.ErrNotExist when there is nothing to keep.
func (v *Vault) Trash(item TrashItem, extra map[string][]byte) (TrashItem, error) {
	var present []string
	for _, rel := range item.Files {
		rel = filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
		if rel == "." || strings.HasPrefix(rel, "../") || filepath.IsAbs(filepath.FromSlash(rel)) || slices.Contains(present, rel) {
			continue
		}
		if _, err := os.Lstat(v.abs(rel)); err == nil {
			present = append(present, rel)
		}
	}
	if len(present) == 0 && len(item.Refs) == 0 && len(extra) == 0 {
		return TrashItem{}, os.ErrNotExist
	}
	item.ID = newTrashID(time.Now())
	item.Deleted = time.Now()
	item.Files = present
	item.Bytes = 0
	dir := filepath.Join(v.trashRoot(), item.ID)
	for _, rel := range present {
		item.Bytes += pathSize(v.abs(rel))
		dst := filepath.Join(dir, "files", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return TrashItem{}, err
		}
		if err := os.Rename(v.abs(rel), dst); err != nil {
			return TrashItem{}, err
		}
	}
	for name, data := range extra {
		if !validName(name) {
			continue
		}
		p := filepath.Join(dir, "extra", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return TrashItem{}, err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return TrashItem{}, err
		}
		item.Bytes += int64(len(data))
	}
	if err := writeManifest(dir, item); err != nil {
		return TrashItem{}, err
	}
	return item, nil
}

func (v *Vault) abs(rel string) string { return filepath.Join(v.Root, filepath.FromSlash(rel)) }

func writeManifest(dir string, item TrashItem) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "item.json"), data, 0o644)
}

func newTrashID(now time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return now.Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// TrashList returns what is in the trash, newest first.
func (v *Vault) TrashList() ([]TrashItem, error) {
	entries, err := os.ReadDir(v.trashRoot())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []TrashItem
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		item, err := v.trashItem(e.Name())
		if err != nil {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Deleted.After(out[j].Deleted) })
	return out, nil
}

func (v *Vault) trashItem(id string) (TrashItem, error) {
	dir, err := v.trashDir(id)
	if err != nil {
		return TrashItem{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "item.json"))
	if err != nil {
		return TrashItem{}, err
	}
	var item TrashItem
	if err := json.Unmarshal(data, &item); err != nil {
		return TrashItem{}, err
	}
	item.ID = id
	return item, nil
}

// Restore puts a trashed item back where it was, links included, and returns it with its extra
// data. It fails with ErrRestoreConflict when one of its files exists again (unless Replace).
func (v *Vault) Restore(id string) (TrashItem, map[string][]byte, error) {
	item, err := v.trashItem(id)
	if err != nil {
		return TrashItem{}, nil, os.ErrNotExist
	}
	dir := filepath.Join(v.trashRoot(), id)
	if !item.Replace {
		for _, rel := range item.Files {
			if _, err := os.Lstat(v.abs(rel)); err == nil {
				return item, nil, ErrRestoreConflict
			}
		}
	}
	for _, rel := range item.Files {
		src := filepath.Join(dir, "files", filepath.FromSlash(rel))
		dst := v.abs(rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return item, nil, err
		}
		if item.Replace {
			_ = os.RemoveAll(dst)
		}
		if err := os.Rename(src, dst); err != nil {
			return item, nil, err
		}
	}
	extra := map[string][]byte{}
	if entries, err := os.ReadDir(filepath.Join(dir, "extra")); err == nil {
		for _, e := range entries {
			if data, err := os.ReadFile(filepath.Join(dir, "extra", e.Name())); err == nil {
				extra[e.Name()] = data
			}
		}
	}
	v.relink(item.Refs)
	return item, extra, os.RemoveAll(dir)
}

// relink puts back the links a deletion removed, on the notes that still exist.
func (v *Vault) relink(refs []Ref) {
	add := func(list []string, val string) []string {
		if slices.Contains(list, val) {
			return list
		}
		return append(list, val)
	}
	for _, r := range refs {
		switch r.Dir {
		case GuidesDir:
			if !v.Exists(r.Slug) {
				continue
			}
			_ = v.Update(r.Slug, func(n *Note) error {
				switch r.Field {
				case "requires":
					n.Meta.Requires = add(n.Meta.Requires, r.Value)
				case "concepts":
					n.Meta.Concepts = add(n.Meta.Concepts, r.Value)
				case "base":
					n.Meta.Base = add(n.Meta.Base, r.Value)
				}
				return nil
			})
		case ConceptsDir:
			if !v.ConceptExists(r.Slug) {
				continue
			}
			_ = v.UpdateConcept(r.Slug, func(c *Concept) error {
				switch r.Field {
				case "guides":
					c.Meta.Guides = add(c.Meta.Guides, r.Value)
				case "requires":
					c.Meta.Requires = add(c.Meta.Requires, r.Value)
				}
				return nil
			})
		}
	}
}

// TrashPurge deletes one item for good.
func (v *Vault) TrashPurge(id string) error {
	dir, err := v.trashDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "item.json")); err != nil {
		return os.ErrNotExist
	}
	return os.RemoveAll(dir)
}

// TrashEmpty deletes everything in the trash for good.
func (v *Vault) TrashEmpty() error { return os.RemoveAll(v.trashRoot()) }

// TrashPurgeOlder deletes the items trashed more than keep ago, and returns how many.
func (v *Vault) TrashPurgeOlder(keep time.Duration, now time.Time) int {
	items, _ := v.TrashList()
	n := 0
	for _, it := range items {
		if now.Sub(it.Deleted) > keep && v.TrashPurge(it.ID) == nil {
			n++
		}
	}
	return n
}

// TrashSize is how many items the trash holds and how much they weigh.
func (v *Vault) TrashSize() (int, int64) {
	items, _ := v.TrashList()
	var b int64
	for _, it := range items {
		b += it.Bytes
	}
	return len(items), b
}

// pathSize is the size of a file, or of everything under a folder.
func pathSize(p string) int64 {
	var total int64
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
