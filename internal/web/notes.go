package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/krilinxito/studyhomie/internal/session"
	"github.com/krilinxito/studyhomie/internal/stats"
	"github.com/krilinxito/studyhomie/internal/vault"
)

type gNode struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Kind            string `json:"kind"` // guide | concept
	Status          string `json:"status"`
	Mastery         int    `json:"mastery"`
	ReadProgress    int    `json:"readProgress,omitempty"`
	ActivitiesDone  int    `json:"activitiesDone,omitempty"`
	ActivitiesTotal int    `json:"activitiesTotal,omitempty"`
	Roadmap         string `json:"roadmap,omitempty"`
	Group           string `json:"group"` // the line the node is drawn on: a roadmap, or a guide with its concepts
	Order           int    `json:"order,omitempty"`
	Source          string `json:"source,omitempty"`
	Field           string `json:"field,omitempty"`
	Category        string `json:"category,omitempty"`
	Summary         string `json:"summary,omitempty"`
	Shared          int    `json:"shared,omitempty"`  // a concept several guides stand on: how many
	Foreign         bool   `json:"foreign,omitempty"` // in a chat's map: a concept another conversation wrote
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
	notes, err := s.vault().List()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	concepts, err := s.vault().ListConcepts()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	exists := map[string]bool{}
	group := map[string]string{}
	usedBy := map[string]map[string]bool{} // concept -> guides that stand on it
	use := func(id, guide string) {
		if usedBy[id] == nil {
			usedBy[id] = map[string]bool{}
		}
		usedBy[id][guide] = true
	}
	for _, n := range notes {
		exists[n.Slug] = true
		group[n.Slug] = guideGroup(n)
		for _, id := range n.Meta.Concepts {
			use(id, n.Slug)
		}
		for _, p := range n.Meta.Pending {
			use(p.ID, n.Slug)
		}
	}
	for _, c := range concepts {
		for _, g := range c.Meta.Guides {
			if group[g] != "" {
				use(c.Slug, g)
			}
		}
	}
	shared := func(id string) int {
		if n := len(usedBy[id]); n > 1 {
			return n
		}
		return 0
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
		acts := vault.ParseActivities(n.Body)
		actDone := 0
		for _, a := range acts {
			if a.Done {
				actDone++
			}
		}
		nodes = append(nodes, gNode{ID: n.Slug, Label: title, Kind: "guide", Status: st, Mastery: n.Meta.Mastery,
			ReadProgress: n.Meta.ReadProgress, ActivitiesDone: actDone, ActivitiesTotal: len(acts),
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
		nodes = append(nodes, gNode{ID: c.Slug, Label: title, Kind: "concept", Status: "concept", ReadProgress: c.Meta.ReadProgress, Group: g, Category: c.Meta.Category, Summary: c.Meta.Summary, Shared: shared(c.Slug)})
	}
	// the iceberg under each guide: mapped concepts with no note yet, once even when several
	// guides map the same one, with what each of them says it stands on
	var pendings []string
	pendReqs := map[string][]string{}
	for _, n := range notes {
		for _, p := range n.Meta.Pending {
			if _, ok := pendReqs[p.ID]; ok {
				pendReqs[p.ID] = append(pendReqs[p.ID], p.Requires...)
				continue
			}
			if exists[p.ID] {
				continue
			}
			exists[p.ID] = true
			group[p.ID] = group[n.Slug]
			pendings = append(pendings, p.ID)
			pendReqs[p.ID] = append([]string{}, p.Requires...)
			nodes = append(nodes, gNode{ID: p.ID, Label: p.Title, Kind: "pending", Status: "pending", Group: group[n.Slug], Category: p.Category, Summary: p.Summary, Shared: shared(p.ID)})
		}
	}
	for _, id := range pendings {
		for _, req := range pendReqs[id] {
			if exists[req] {
				add(req, id, "requires")
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
	mine := map[string]bool{} // what this chat wrote or asked for itself
	for slug := range keep {
		mine[slug] = true
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
	foreign := map[string]bool{}
	for _, c := range concepts {
		for _, g := range c.Meta.Guides {
			if guides[g] {
				keep[c.Slug] = true
			}
		}
		// written for a guide of another conversation, and only linked here
		if keep[c.Slug] && !mine[c.Slug] && len(c.Meta.Guides) > 0 && !guides[c.Meta.Guides[0]] {
			foreign[c.Slug] = true
		}
	}
	outN := []gNode{}
	for _, n := range nodes {
		if keep[n.ID] {
			n.Foreign = n.Kind == "concept" && foreign[n.ID]
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
	Guides   int      `json:"guides,omitempty"` // how many guides stand on it
}

// titleOf names a guide or a concept.
func (s *Server) titleOf(slug string) string {
	if o, err := s.vault().Load(slug); err == nil && o.Meta.Title != "" {
		return o.Meta.Title
	}
	if c, err := s.vault().LoadConcept(slug); err == nil && c.Meta.Title != "" {
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
	back, _ := s.vault().Backlinks(slug)
	if n, err := s.vault().Load(slug); err == nil {
		notes := []conceptRef{}
		for _, id := range n.Meta.Concepts {
			ref := conceptRef{Slug: id, Title: id}
			if c, err := s.vault().LoadConcept(id); err == nil {
				ref = conceptRef{Slug: id, Title: c.Meta.Title, Summary: c.Meta.Summary, Category: c.Meta.Category, Requires: c.Meta.Requires, Exists: true, Guides: len(c.Meta.Guides)}
			}
			notes = append(notes, ref)
		}
		_, simErr := s.vault().LoadInteractive(slug)
		if vault.Section(n.Body, "Guía") != "" {
			area := n.Meta.Area
			if area == "" && n.Meta.Field != "" {
				area = vault.NormalizeArea(n.Meta.Field)
			}
			_, _ = stats.Record(s.vault().Root, stats.Event{
				At:    s.Now(),
				Kind:  stats.KindRead,
				Topic: slug,
				Title: n.Meta.Title,
				Area:  area,
			})
		}
		res := s.vault().GuideResources(n)
		if res == nil {
			res = []vault.Resource{}
		}
		writeJSON(w, 200, map[string]any{
			"slug": slug, "kind": "guide", "meta": n.Meta, "body": n.Body, "resources": res,
			"backlinks": s.refs(back), "requires": s.refs(n.Meta.Requires), "concepts": notes, "activities": vault.ParseActivities(n.Body), "hasSimulation": simErr == nil, "apunte": s.vault().HasApunte(slug),
		})
		return
	}
	id := slug
	if !s.vault().ConceptExists(id) {
		id = vault.ConceptID(slug) // a link written with accents
	}
	c, err := s.vault().LoadConcept(id)
	if err != nil {
		fail(w, 404, "no existe esa nota")
		return
	}
	cres := s.vault().Expand(c.Meta.Sources)
	if cres == nil {
		cres = []vault.Resource{}
	}
	writeJSON(w, 200, map[string]any{
		"slug": id, "kind": "concept", "meta": c.Meta, "body": c.Body, "resources": cres,
		"backlinks": s.refs(back), "requires": s.refs(c.Meta.Requires), "guides": s.refs(c.Meta.Guides), "apunte": s.vault().HasApunte(id),
	})
}

// deleteNote removes a guide or concept note from the vault and cleans up stale references.
func (s *Server) deleteNote(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if vault.Slugify(slug) != slug || slug == "" {
		fail(w, 400, "nota inválida")
		return
	}
	v := s.vault()
	if v.Exists(slug) {
		if s.inUse(vault.CatGuides, slug) {
			fail(w, http.StatusConflict, errItemInUse.Error())
			return
		}
		var with []string
		if q := r.URL.Query().Get("with"); q != "" {
			with = strings.Split(q, ",")
		}
		item, err := s.removeItem(vault.CatGuides, slug, with)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "kind": "guide", "slug": slug, "trash": item.ID})
		return
	}
	id := slug
	if !v.ConceptExists(id) {
		id = vault.ConceptID(slug)
	}
	if v.ConceptExists(id) {
		item, err := v.DeleteConcept(id)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "kind": "concept", "slug": id, "trash": item.ID})
		return
	}
	fail(w, 404, "no existe esa nota")
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
		fail(w, shortStatus(err), s.errText(err))
		return
	}
	writeJSON(w, 200, map[string]string{"slug": id})
}

// dig maps one more level under a guide's iceberg and answers with the concepts it added.
func (s *Server) dig(w http.ResponseWriter, r *http.Request) {
	slug, ok := noteSlug(w, r)
	if !ok {
		return
	}
	drain(r)
	ctx, cancel := context.WithTimeout(r.Context(), answerTimeout)
	defer cancel()
	added, err := s.shortSession().Dig(ctx, slug)
	if err != nil {
		fail(w, shortStatus(err), s.errText(err))
		return
	}
	writeJSON(w, 200, map[string]any{"added": added})
}

// vizTimeout bounds building a page for an html widget: a whole document, so longer than a short answer.
const vizTimeout = 6 * time.Minute

// vizPage builds, regenerates, or repairs the page an html widget describes and answers with its path.
func (s *Server) vizPage(w http.ResponseWriter, r *http.Request) {
	slug, ok := noteSlug(w, r)
	if !ok {
		return
	}
	var body struct {
		Brief    string `json:"brief"`
		Force    bool   `json:"force"`
		FixError string `json:"fixError"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	drain(r)
	ctx, cancel := context.WithTimeout(r.Context(), vizTimeout)
	defer cancel()
	src, err := s.shortSession().VizPageWithOpts(ctx, slug, body.Brief, session.VizPageOpts{
		Force:    body.Force,
		FixError: body.FixError,
	})
	if err != nil {
		fail(w, shortStatus(err), s.errText(err))
		return
	}
	writeJSON(w, 200, map[string]string{"src": src})
}

// saveReadProgress stores the furthest scroll reading progress (0-100) reached on a guide or concept.
func (s *Server) saveReadProgress(w http.ResponseWriter, r *http.Request) {
	slug, ok := noteSlug(w, r)
	if !ok {
		return
	}
	var body struct {
		Progress int    `json:"progress"`
		Kind     string `json:"kind"` // guide | concept (optional)
	}
	if !readJSON(w, r, &body) {
		return
	}
	pct := max(0, min(100, body.Progress))
	v := s.vault()
	isConcept := body.Kind == "concept" || (body.Kind != "guide" && !v.Exists(slug))
	if isConcept {
		id := slug
		if !v.ConceptExists(id) {
			id = vault.ConceptID(slug)
		}
		var saved int
		err := v.UpdateConcept(id, func(c *vault.Concept) error {
			if pct > c.Meta.ReadProgress {
				c.Meta.ReadProgress = pct
			}
			saved = c.Meta.ReadProgress
			return nil
		})
		if err != nil {
			fail(w, 404, "no existe esa nota")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "readProgress": saved})
		return
	}
	var saved int
	err := v.Update(slug, func(n *vault.Note) error {
		if pct > n.Meta.ReadProgress {
			n.Meta.ReadProgress = pct
		}
		saved = n.Meta.ReadProgress
		return nil
	})
	if err != nil {
		fail(w, 404, "no existe esa nota")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "readProgress": saved})
}
