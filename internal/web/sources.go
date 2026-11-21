package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/krilinxito/studyhomie/internal/session"
	"github.com/krilinxito/studyhomie/internal/vault"
)

type sourceCite struct {
	Dir   string `json:"dir"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Why   string `json:"why,omitempty"`
}

type sourceEntry struct {
	Slug    string           `json:"slug"`
	Meta    vault.SourceMeta `json:"meta"`
	CitedBy []sourceCite     `json:"citedBy"`
}

// listSources returns the library of source notes in fuentes/, with the guides and concepts citing each one.
func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	lib := s.vault().Library()
	out := make([]sourceEntry, 0, len(lib))
	for _, st := range lib {
		cites := make([]sourceCite, 0, len(st.CitedBy))
		for _, c := range st.CitedBy {
			title := c.Value
			if title == "" {
				title = c.Slug
			}
			cites = append(cites, sourceCite{Dir: c.Dir, Slug: c.Slug, Title: title, Why: c.Extra})
		}
		out = append(out, sourceEntry{Slug: st.Slug, Meta: st.Meta, CitedBy: cites})
	}
	writeJSON(w, 200, map[string]any{"sources": out})
}

// getSource returns one source note with its user notes section and citations.
func (s *Server) getSource(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" || vault.Slugify(slug) != slug {
		fail(w, 400, "fuente inválida")
		return
	}
	v := s.vault()
	sn, err := v.LoadSource(slug)
	if err != nil {
		fail(w, 404, "no existe esa fuente")
		return
	}
	refs := v.CitedBy(slug)
	cites := make([]sourceCite, 0, len(refs))
	for _, c := range refs {
		cites = append(cites, sourceCite{Dir: c.Dir, Slug: c.Slug, Title: s.titleOf(c.Slug), Why: c.Extra})
	}
	writeJSON(w, 200, map[string]any{
		"slug":    sn.Slug,
		"meta":    sn.Meta,
		"body":    sn.Body,
		"notes":   strings.TrimSpace(vault.Section(sn.Body, "Mis notas")),
		"citedBy": cites,
	})
}

// updateSource toggles seen/read status and/or saves the learner's notes on a source.
func (s *Server) updateSource(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" || vault.Slugify(slug) != slug {
		fail(w, 400, "fuente inválida")
		return
	}
	var body struct {
		Seen  *bool   `json:"seen"`
		Notes *string `json:"notes"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	var updated vault.SourceNote
	err := s.vault().UpdateSource(slug, func(sn *vault.SourceNote) error {
		if body.Seen != nil {
			sn.Meta.Seen = *body.Seen
		}
		if body.Notes != nil {
			sn.Body = vault.ReplaceSection(sn.Body, "Mis notas", strings.TrimSpace(*body.Notes))
		}
		updated = *sn
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		fail(w, 404, "no existe esa fuente")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":    true,
		"slug":  updated.Slug,
		"meta":  updated.Meta,
		"notes": strings.TrimSpace(vault.Section(updated.Body, "Mis notas")),
	})
}

// deleteSource moves a source note to the trash and unlinks it from the notes citing it.
func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" || vault.Slugify(slug) != slug {
		fail(w, 400, "fuente inválida")
		return
	}
	item, err := s.vault().DeleteSource(slug)
	if errors.Is(err, os.ErrNotExist) {
		fail(w, 404, "no existe esa fuente")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "trash": item})
}

// findSources looks for more external material for a guide or concept using public catalogues + AI.
func (s *Server) findSources(w http.ResponseWriter, r *http.Request) {
	slug, ok := noteSlug(w, r)
	if !ok {
		return
	}
	var body struct {
		Kind  string   `json:"kind"` // guide | concept (optional: inferred from vault)
		Kinds []string `json:"kinds"`
		Easy  bool     `json:"easy"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	drain(r)
	v := s.vault()
	dir := ""
	switch body.Kind {
	case "guide":
		dir = vault.GuidesDir
	case "concept":
		dir = vault.ConceptsDir
	default:
		if v.Exists(slug) {
			dir = vault.GuidesDir
		} else {
			if !v.ConceptExists(slug) {
				slug = vault.ConceptID(slug)
			}
			dir = vault.ConceptsDir
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), answerTimeout)
	defer cancel()
	added, err := s.shortSession().FindSources(ctx, dir, slug, session.SourceFilter{Kinds: body.Kinds, Easy: body.Easy})
	if err != nil {
		fail(w, shortStatus(err), s.errText(err))
		return
	}
	var all []vault.Resource
	if dir == vault.GuidesDir {
		if n, err := v.Load(slug); err == nil {
			all = v.GuideResources(n)
		}
	} else {
		if c, err := v.LoadConcept(slug); err == nil {
			all = v.Expand(c.Meta.Sources)
		}
	}
	if added == nil {
		added = []vault.Resource{}
	}
	if all == nil {
		all = []vault.Resource{}
	}
	writeJSON(w, 200, map[string]any{"added": added, "resources": all})
}
