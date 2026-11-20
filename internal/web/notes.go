package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/krilinxito/studyhomie/internal/vault"
)

type gNode struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"` // guide | concept
	Status   string `json:"status"`
	Mastery  int    `json:"mastery"`
	Roadmap  string `json:"roadmap,omitempty"`
	Group    string `json:"group"` // the line the node is drawn on: a roadmap, or a guide with its concepts
	Order    int    `json:"order,omitempty"`
	Source   string `json:"source,omitempty"`
	Field    string `json:"field,omitempty"`
	Category string `json:"category,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

type gEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // requires: from must be understood before to | related
}

// guideGroup is the line a guide is drawn on: its roadmap, or a line of its own.
func guideGroup(n vault.Note) string {
	if n.Meta.Roadmap != "" {
		return n.Meta.Roadmap
	}
	return "g:" + n.Slug
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	notes, err := s.V.List()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	concepts, err := s.V.ListConcepts()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	exists := map[string]bool{}
	group := map[string]string{}
	for _, n := range notes {
		exists[n.Slug] = true
		group[n.Slug] = guideGroup(n)
	}
	nodes := []gNode{}
	edges := []gEdge{}
	seen := map[string]bool{}
	add := func(from, to, kind string) {
		if from == to || seen[from+">"+to] || (kind == "related" && seen[to+">"+from]) {
			return
		}
		seen[from+">"+to] = true
		edges = append(edges, gEdge{From: from, To: to, Kind: kind})
	}
	for _, n := range notes {
		st := n.Meta.Status
		if st == "" {
			st = "new"
		}
		title := n.Meta.Title
		if title == "" {
			title = n.Slug
		}
		nodes = append(nodes, gNode{ID: n.Slug, Label: title, Kind: "guide", Status: st, Mastery: n.Meta.Mastery,
			Roadmap: n.Meta.Roadmap, Group: group[n.Slug], Order: n.Meta.Order, Source: n.Meta.Source, Field: n.Meta.Field, Summary: n.Meta.Summary})
	}
	var shown []vault.Concept
	for _, c := range concepts {
		if exists[c.Slug] {
			continue // a guide with the same name wins: links resolve to it
		}
		exists[c.Slug] = true
		shown = append(shown, c)
		g := ""
		for _, gs := range c.Meta.Guides {
			if group[gs] != "" {
				g = group[gs]
				break
			}
		}
		if g == "" {
			g = "c:" + c.Meta.Category
		}
		group[c.Slug] = g
		title := c.Meta.Title
		if title == "" {
			title = c.Slug
		}
		nodes = append(nodes, gNode{ID: c.Slug, Label: title, Kind: "concept", Status: "concept", Group: g, Category: c.Meta.Category, Summary: c.Meta.Summary})
	}
	// the iceberg under each guide: mapped concepts with no note yet
	type pend struct {
		id       string
		requires []string
	}
	var pendings []pend
	for _, n := range notes {
		for _, p := range n.Meta.Pending {
			if exists[p.ID] {
				continue
			}
			exists[p.ID] = true
			group[p.ID] = group[n.Slug]
			pendings = append(pendings, pend{p.ID, p.Requires})
			nodes = append(nodes, gNode{ID: p.ID, Label: p.Title, Kind: "pending", Status: "pending", Group: group[n.Slug], Category: p.Category, Summary: p.Summary})
		}
	}
	for _, p := range pendings {
		for _, req := range p.requires {
			if exists[req] {
				add(req, p.id, "requires")
			}
		}
	}
	for _, n := range notes {
		for _, req := range n.Meta.Requires {
			if exists[req] {
				add(req, n.Slug, "requires")
			}
		}
		for _, b := range n.Meta.Base {
			if exists[b] {
				add(b, n.Slug, "requires")
			}
		}
	}
	for _, c := range shown {
		for _, req := range c.Meta.Requires {
			if exists[req] {
				add(req, c.Slug, "requires")
			}
		}
	}
	for _, n := range notes {
		for _, to := range n.Links() {
			if exists[to] && !seen[to+">"+n.Slug] {
				add(n.Slug, to, "related")
			}
		}
	}
	for _, c := range shown {
		for _, to := range c.Links() {
			if exists[to] && !seen[to+">"+c.Slug] {
				add(c.Slug, to, "related")
			}
		}
	}
	if id, err := strconv.Atoi(r.URL.Query().Get("chat")); err == nil {
		nodes, edges = s.chatGraph(id, notes, concepts, nodes, edges)
	}
	writeJSON(w, 200, map[string]any{"nodes": nodes, "edges": edges})
}

// chatGraph keeps the part of the map a chat touched: the guides and concepts it wrote or
// opened, each guide with its concepts and what lies under it, and the links between them.
func (s *Server) chatGraph(id int, notes []vault.Note, concepts []vault.Concept, nodes []gNode, edges []gEdge) ([]gNode, []gEdge) {
	s.mu.Lock()
	run := s.runs[id]
	s.mu.Unlock()
	keep := map[string]bool{}
	if run != nil {
		for _, slug := range run.topics() {
			keep[slug] = true
		}
	}
	guides := map[string]bool{}
	for _, n := range notes {
		if keep[n.Slug] {
			guides[n.Slug] = true
			for _, c := range n.Meta.Concepts {
				keep[c] = true
			}
			for _, b := range n.Meta.Base {
				keep[b] = true
			}
			for _, p := range n.Meta.Pending {
				keep[p.ID] = true
			}
		}
	}
	for _, c := range concepts {
		for _, g := range c.Meta.Guides {
			if guides[g] {
				keep[c.Slug] = true
			}
		}
	}
	outN := []gNode{}
	for _, n := range nodes {
		if keep[n.ID] {
			outN = append(outN, n)
		}
	}
	outE := []gEdge{}
	for _, e := range edges {
		if keep[e.From] && keep[e.To] {
			outE = append(outE, e)
		}
	}
	return outN, outE
}

type linkRef struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// conceptRef is a concept note of a guide, for its Conceptos tab.
type conceptRef struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary,omitempty"`
	Category string   `json:"category,omitempty"`
	Requires []string `json:"requires,omitempty"`
	Exists   bool     `json:"exists"`
}

// titleOf names a guide or a concept.
func (s *Server) titleOf(slug string) string {
	if o, err := s.V.Load(slug); err == nil && o.Meta.Title != "" {
		return o.Meta.Title
	}
	if c, err := s.V.LoadConcept(slug); err == nil && c.Meta.Title != "" {
		return c.Meta.Title
	}
	return slug
}

func (s *Server) refs(slugs []string) []linkRef {
	out := []linkRef{}
	for _, sl := range slugs {
		out = append(out, linkRef{sl, s.titleOf(sl)})
	}
	return out
}

func (s *Server) note(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if vault.Slugify(slug) != slug || slug == "" {
		fail(w, 400, "nota inválida")
		return
	}
	back, _ := s.V.Backlinks(slug)
	if n, err := s.V.Load(slug); err == nil {
		notes := []conceptRef{}
		for _, id := range n.Meta.Concepts {
			ref := conceptRef{Slug: id, Title: id}
			if c, err := s.V.LoadConcept(id); err == nil {
				ref = conceptRef{Slug: id, Title: c.Meta.Title, Summary: c.Meta.Summary, Category: c.Meta.Category, Requires: c.Meta.Requires, Exists: true}
			}
			notes = append(notes, ref)
		}
		_, simErr := s.V.LoadInteractive(slug)
		writeJSON(w, 200, map[string]any{
			"slug": slug, "kind": "guide", "meta": n.Meta, "body": n.Body,
			"backlinks": s.refs(back), "requires": s.refs(n.Meta.Requires), "concepts": notes, "hasSimulation": simErr == nil, "apunte": s.V.HasApunte(slug),
		})
		return
	}
	id := slug
	if !s.V.ConceptExists(id) {
		id = vault.ConceptID(slug) // a link written with accents
	}
	c, err := s.V.LoadConcept(id)
	if err != nil {
		fail(w, 404, "no existe esa nota")
		return
	}
	writeJSON(w, 200, map[string]any{
		"slug": id, "kind": "concept", "meta": c.Meta, "body": c.Body,
		"backlinks": s.refs(back), "requires": s.refs(c.Meta.Requires), "guides": s.refs(c.Meta.Guides), "apunte": s.V.HasApunte(id),
	})
}

// explain writes the note of a concept a guide leaves under water (or any term it links) and answers
// with the concept's id.
func (s *Server) explain(w http.ResponseWriter, r *http.Request) {
	slug, ok := noteSlug(w, r)
	if !ok {
		return
	}
	var body struct {
		Ref     string `json:"ref"`
		Summary string `json:"summary"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	drain(r)
	ctx, cancel := context.WithTimeout(r.Context(), answerTimeout)
	defer cancel()
	id, err := s.shortSession().Explain(ctx, slug, body.Ref, body.Summary)
	if err != nil {
		fail(w, shortStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"slug": id})
}

// vizTimeout bounds building a page for an html widget: a whole document, so longer than a short answer.
const vizTimeout = 6 * time.Minute

// vizPage builds the page an html widget describes and answers with its path.
func (s *Server) vizPage(w http.ResponseWriter, r *http.Request) {
	slug, ok := noteSlug(w, r)
	if !ok {
		return
	}
	var body struct {
		Brief string `json:"brief"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	drain(r)
	ctx, cancel := context.WithTimeout(r.Context(), vizTimeout)
	defer cancel()
	src, err := s.shortSession().VizPage(ctx, slug, body.Brief)
	if err != nil {
		fail(w, shortStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"src": src})
}
