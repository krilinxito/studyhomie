package vault

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// DeleteGuide removes a study guide note, its companion handoff/interactive/apunte files,
// and cleans up stale references to it in concept notes and other guides.
func (v *Vault) DeleteGuide(slug string) error {
	slug = Slugify(slug)
	if slug == "" {
		return os.ErrNotExist
	}
	removed := false
	for _, dir := range []string{GuidesDir, legacyDir} {
		p := filepath.Join(v.Root, dir, slug+".md")
		if err := os.Remove(p); err == nil {
			removed = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if !removed {
		return os.ErrNotExist
	}

	// Best-effort cleanup of companion files belonging exclusively to this guide.
	_ = os.Remove(filepath.Join(v.Root, ".studyhomie", "handoffs", slug+".md"))
	_ = os.Remove(filepath.Join(v.Root, "interactive", slug+".html"))
	_ = os.Remove(filepath.Join(v.Root, ApuntesDir, slug+".md"))

	// Remove stale guide references from concept notes so shared counts stay accurate.
	if concepts, err := v.ListConcepts(); err == nil {
		for _, c := range concepts {
			if slices.Contains(c.Meta.Guides, slug) {
				c.Meta.Guides = removeStr(c.Meta.Guides, slug)
				_ = v.SaveConcept(c)
			}
		}
	}
	// Remove stale prerequisite references from remaining guides.
	if notes, err := v.List(); err == nil {
		for _, n := range notes {
			if slices.Contains(n.Meta.Requires, slug) {
				n.Meta.Requires = removeStr(n.Meta.Requires, slug)
				_ = v.Save(n)
			}
		}
	}
	return nil
}

// DeleteConcept removes a concept note and cleans up references to it from guides and other concepts.
func (v *Vault) DeleteConcept(id string) error {
	id = ConceptID(id)
	if id == "" {
		return os.ErrNotExist
	}
	if err := os.Remove(v.conceptPath(id)); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(v.Root, ApuntesDir, id+".md"))

	if notes, err := v.List(); err == nil {
		for _, n := range notes {
			changed := false
			if slices.Contains(n.Meta.Concepts, id) {
				n.Meta.Concepts = removeStr(n.Meta.Concepts, id)
				changed = true
			}
			if slices.Contains(n.Meta.Base, id) {
				n.Meta.Base = removeStr(n.Meta.Base, id)
				changed = true
			}
			if changed {
				_ = v.Save(n)
			}
		}
	}
	if concepts, err := v.ListConcepts(); err == nil {
		for _, c := range concepts {
			if slices.Contains(c.Meta.Requires, id) {
				c.Meta.Requires = removeStr(c.Meta.Requires, id)
				_ = v.SaveConcept(c)
			}
		}
	}
	return nil
}

func removeStr(list []string, target string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}
