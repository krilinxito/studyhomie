package vault

import (
	"os"
	"slices"
)

// DeleteGuide moves a study guide to the trash with the files that belong only to it (handoff,
// extracted source text, simulation, apunte, material folder), plus the extra vault-relative
// paths given (its deck, its sessions…), and removes the links other notes had to it. The
// returned item restores all of that. data is kept with the item (see Trash).
func (v *Vault) DeleteGuide(slug string, extra []string, data map[string][]byte) (TrashItem, error) {
	slug = Slugify(slug)
	if slug == "" {
		return TrashItem{}, os.ErrNotExist
	}
	title := slug
	if n, err := v.Load(slug); err == nil && n.Meta.Title != "" {
		title = n.Meta.Title
	}
	files := []string{GuidesDir + "/" + slug + ".md", legacyDir + "/" + slug + ".md"}
	if _, err := os.Stat(v.abs(files[0])); err != nil {
		if _, err := os.Stat(v.abs(files[1])); err != nil {
			return TrashItem{}, os.ErrNotExist
		}
	}
	files = append(files,
		".studyhomie/handoff/"+slug+".md",
		".studyhomie/material/"+slug+".md",
		"interactive/"+slug+".html",
		ApuntesDir+"/"+slug+".md",
		MaterialDir+"/"+slug,
	)
	files = append(files, extra...)

	var refs []Ref
	if concepts, err := v.ListConcepts(); err == nil {
		for _, c := range concepts {
			if slices.Contains(c.Meta.Guides, slug) {
				refs = append(refs, Ref{Dir: ConceptsDir, Slug: c.Slug, Field: "guides", Value: slug})
			}
		}
	}
	if notes, err := v.List(); err == nil {
		for _, n := range notes {
			if n.Slug != slug && slices.Contains(n.Meta.Requires, slug) {
				refs = append(refs, Ref{Dir: GuidesDir, Slug: n.Slug, Field: "requires", Value: slug})
			}
		}
	}
	item, err := v.Trash(TrashItem{Kind: GuidesDir, Slug: slug, Title: title, Files: files, Refs: refs}, data)
	if err != nil {
		return TrashItem{}, err
	}
	v.unlink(refs)
	return item, nil
}

// DeleteConcept moves a concept note (and its apunte) to the trash and removes the links to it.
func (v *Vault) DeleteConcept(id string) (TrashItem, error) {
	id = ConceptID(id)
	if id == "" || !v.ConceptExists(id) {
		return TrashItem{}, os.ErrNotExist
	}
	title := id
	if c, err := v.LoadConcept(id); err == nil && c.Meta.Title != "" {
		title = c.Meta.Title
	}
	var refs []Ref
	if notes, err := v.List(); err == nil {
		for _, n := range notes {
			if slices.Contains(n.Meta.Concepts, id) {
				refs = append(refs, Ref{Dir: GuidesDir, Slug: n.Slug, Field: "concepts", Value: id})
			}
			if slices.Contains(n.Meta.Base, id) {
				refs = append(refs, Ref{Dir: GuidesDir, Slug: n.Slug, Field: "base", Value: id})
			}
		}
	}
	if concepts, err := v.ListConcepts(); err == nil {
		for _, c := range concepts {
			if c.Slug != id && slices.Contains(c.Meta.Requires, id) {
				refs = append(refs, Ref{Dir: ConceptsDir, Slug: c.Slug, Field: "requires", Value: id})
			}
		}
	}
	files := []string{ConceptsDir + "/" + id + ".md", ApuntesDir + "/" + id + ".md"}
	item, err := v.Trash(TrashItem{Kind: ConceptsDir, Slug: id, Title: title, Files: files, Refs: refs}, nil)
	if err != nil {
		return TrashItem{}, err
	}
	v.unlink(refs)
	return item, nil
}

// unlink removes each reference from the note that holds it (the reverse of relink).
func (v *Vault) unlink(refs []Ref) {
	for _, r := range refs {
		if r.Field == "sources" {
			_ = v.SetSourcesOf(r.Dir, r.Slug, func(have []SourceRef) []SourceRef {
				return slices.DeleteFunc(have, func(x SourceRef) bool { return x.ID == r.Value })
			}, "")
			continue
		}
		switch r.Dir {
		case GuidesDir:
			_ = v.Update(r.Slug, func(n *Note) error {
				switch r.Field {
				case "requires":
					n.Meta.Requires = removeStr(n.Meta.Requires, r.Value)
				case "concepts":
					n.Meta.Concepts = removeStr(n.Meta.Concepts, r.Value)
				case "base":
					n.Meta.Base = removeStr(n.Meta.Base, r.Value)
				}
				return nil
			})
		case ConceptsDir:
			_ = v.UpdateConcept(r.Slug, func(c *Concept) error {
				switch r.Field {
				case "guides":
					c.Meta.Guides = removeStr(c.Meta.Guides, r.Value)
				case "requires":
					c.Meta.Requires = removeStr(c.Meta.Requires, r.Value)
				}
				return nil
			})
		}
	}
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
