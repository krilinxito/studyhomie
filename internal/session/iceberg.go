package session

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// known is a concept the vault already has: a written note, or one mapped under some guide and
// left to explain on demand.
type known struct {
	ID      string
	Title   string
	Written bool
}

// lookup finds the concept a planner's id or title stands for, if the vault already has it.
type lookup func(ref string) (known, bool)

// conceptIndex is every concept of the vault by id and by ConceptKey, so a plan reuses a note
// even when the planner spells it a little differently ("Probabilidad condicionada" for
// "Probabilidad condicional"). Written notes win over pending ones.
type conceptIndex struct {
	byID  map[string]known
	byKey map[string]known
	notes []vault.Concept
}

func (s *Session) conceptIndex() *conceptIndex {
	x := &conceptIndex{byID: map[string]known{}, byKey: map[string]known{}}
	put := func(k known, names ...string) {
		if old, ok := x.byID[k.ID]; !ok || (k.Written && !old.Written) {
			x.byID[k.ID] = k
		}
		for _, n := range names {
			key := vault.ConceptKey(n)
			if key == "" {
				continue
			}
			if old, ok := x.byKey[key]; !ok || (k.Written && !old.Written) {
				x.byKey[key] = k
			}
		}
	}
	x.notes, _ = s.V.ListConcepts()
	for _, c := range x.notes {
		title := c.Meta.Title
		if title == "" {
			title = c.Slug
		}
		put(known{ID: c.Slug, Title: title, Written: true}, append([]string{c.Slug, title}, c.Meta.Aliases...)...)
	}
	if notes, err := s.V.List(); err == nil {
		for _, n := range notes {
			for _, p := range n.Meta.Pending {
				put(known{ID: p.ID, Title: p.Title}, p.ID, p.Title)
			}
		}
	}
	return x
}

// find looks a concept up by id, then by key. A written note wins over a pending concept with the
// exact id, so a spelling that is only mapped somewhere still finds the note.
func (x *conceptIndex) find(ref string) (known, bool) {
	k, ok := x.byID[vault.ConceptID(ref)]
	if ok && k.Written {
		return k, true
	}
	if key := vault.ConceptKey(ref); key != "" {
		if kk, found := x.byKey[key]; found && (kk.Written || !ok) {
			return kk, true
		}
	}
	return k, ok
}

// maxExisting bounds the vault concepts a planner sees, so a large vault does not flood its prompt.
const maxExisting = 150

// relevant keeps the n concepts closest to what is being planned: those sharing the most words
// with it (title, aliases, category, summary), sorted by id for a stable prompt.
func relevant(list []vault.Concept, about string, n int) []vault.Concept {
	if len(list) <= n {
		return list
	}
	want := map[string]bool{}
	for _, w := range strings.Fields(vault.ConceptKey(about)) {
		want[w] = true
	}
	score := func(c vault.Concept) int {
		text := strings.Join(append([]string{c.Slug, c.Meta.Title, c.Meta.Category, c.Meta.Summary}, c.Meta.Aliases...), " ")
		got := 0
		seen := map[string]bool{}
		for _, w := range strings.Fields(vault.ConceptKey(text)) {
			if want[w] && !seen[w] {
				seen[w] = true
				got++
			}
		}
		return got
	}
	ranked := append([]vault.Concept(nil), list...)
	scores := make(map[string]int, len(ranked))
	for _, c := range ranked {
		scores[c.Slug] = score(c)
	}
	sort.SliceStable(ranked, func(i, j int) bool { return scores[ranked[i].Slug] > scores[ranked[j].Slug] })
	ranked = ranked[:n]
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].Slug < ranked[j].Slug })
	return ranked
}

// writeExisting lists the vault concepts worth reusing, as `id: title (aliases)`.
func writeExisting(b *strings.Builder, list []vault.Concept, about string) {
	list = relevant(list, about, maxExisting)
	if len(list) == 0 {
		return
	}
	b.WriteString("\nEXISTING CONCEPTS:\n")
	for _, c := range list {
		fmt.Fprintf(b, "%s: %s", c.Slug, c.Meta.Title)
		if len(c.Meta.Aliases) > 0 {
			fmt.Fprintf(b, " (%s)", strings.Join(c.Meta.Aliases, ", "))
		}
		b.WriteString("\n")
	}
}

// mapDepth is how many levels the planner maps: the depth written now, or one level to explain on
// demand when nothing is written but the iceberg is on.
func mapDepth(depth int, iceberg bool) int {
	if depth == 0 && iceberg {
		return 1
	}
	return depth
}

// maxIceberg bounds how deep digging can go under one guide.
const maxIceberg = 10

// maxDig bounds the concepts one dig adds.
const maxDig = 12

// iceNode is one concept of a guide's iceberg, written or not.
type iceNode struct {
	Title    string
	Summary  string
	Requires []string
	Level    int
	Pending  bool
}

// iceberg is the map under a guide as it stands: its written concepts and the pending ones, each
// on its level (the shortest path from the topic).
func (s *Session) iceberg(g vault.Note) map[string]*iceNode {
	m := map[string]*iceNode{}
	for _, id := range g.Meta.Concepts {
		n := &iceNode{Title: id}
		if c, err := s.V.LoadConcept(id); err == nil {
			n.Title, n.Summary, n.Requires = c.Meta.Title, c.Meta.Summary, c.Meta.Requires
		}
		m[id] = n
	}
	for _, p := range g.Meta.Pending {
		if _, ok := m[p.ID]; !ok {
			m[p.ID] = &iceNode{Title: p.Title, Summary: p.Summary, Requires: p.Requires, Level: p.Level, Pending: true}
		}
	}
	base := g.Meta.Base
	if len(base) == 0 {
		for id, n := range m {
			if !n.Pending {
				base = append(base, id)
			}
		}
	}
	level := map[string]int{}
	queue := []string{}
	for _, id := range base {
		if _, ok := m[id]; ok && level[id] == 0 {
			level[id] = 1
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, r := range m[id].Requires {
			if _, ok := m[r]; ok && level[r] == 0 {
				level[r] = level[id] + 1
				queue = append(queue, r)
			}
		}
	}
	for id, n := range m {
		switch {
		case level[id] > 0:
			n.Level = level[id]
		case n.Level == 0:
			n.Level = 1
		}
	}
	return m
}

// Dig maps one more level under a guide's iceberg: what its deepest concepts stand on. The new
// concepts stay pending (explained on demand), except those the vault already has, which are
// linked. It returns what it added: nothing when the bottom is already the basics.
func (s *Session) Dig(ctx context.Context, guide string) ([]PlannedConcept, error) {
	g, err := s.V.Load(guide)
	if err != nil {
		return nil, inputErrorf("no existe la guía «%s»", guide)
	}
	ice := s.iceberg(g)
	deepest := 0
	for _, n := range ice {
		deepest = max(deepest, n.Level)
	}
	if deepest >= maxIceberg {
		return nil, inputErrorf("este iceberg ya llega a %d niveles; no bajo más", deepest)
	}
	var frontier []string
	for id, n := range ice {
		if n.Level == deepest {
			frontier = append(frontier, id)
		}
	}
	sort.Strings(frontier)

	idx := s.conceptIndex()
	var b strings.Builder
	fmt.Fprintf(&b, "TOPIC: %s\n", g.Meta.Title)
	if g.Meta.Focus != "" {
		fmt.Fprintf(&b, "FOCUS: %s\n", g.Meta.Focus)
	}
	if deepest == 0 {
		b.WriteString("\nMAPPED SO FAR: nothing yet.\n\nDIG UNDER: the topic itself (its level 1).\n")
	} else {
		b.WriteString("\nMAPPED SO FAR (id: title, level):\n")
		ids := make([]string, 0, len(ice))
		for id := range ice {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			return ice[ids[i]].Level < ice[ids[j]].Level || ice[ids[i]].Level == ice[ids[j]].Level && ids[i] < ids[j]
		})
		for _, id := range ids {
			fmt.Fprintf(&b, "%s: %s, %d\n", id, ice[id].Title, ice[id].Level)
		}
		b.WriteString("\nDIG UNDER (the deepest level):\n")
		for _, id := range frontier {
			fmt.Fprintf(&b, "%s: %s", id, ice[id].Title)
			if ice[id].Summary != "" {
				fmt.Fprintf(&b, " — %s", ice[id].Summary)
			}
			b.WriteString("\n")
		}
	}
	writeExisting(&b, idx.notes, g.Meta.Title+" "+g.Meta.Focus)

	s.status("Mapeando lo que hay más abajo de «%s»", g.Meta.Title)
	resp, err := s.quickConvo(prompts.Get("dig", s.aiLang())).Say(ctx, b.String(), nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Concepts []struct {
			ID       string   `json:"id"`
			Title    string   `json:"title"`
			Summary  string   `json:"summary"`
			Category string   `json:"category"`
			For      []string `json:"for"`
		} `json:"concepts"`
	}
	if err := decodeJSON(resp.Text, &raw); err != nil {
		return nil, err
	}

	isFrontier := map[string]bool{}
	for _, id := range frontier {
		isFrontier[id] = true
	}
	topicKey := vault.ConceptKey(g.Meta.Title)
	added := []PlannedConcept{}
	links := map[string][]string{} // parent ("" for the topic) -> new prerequisites
	link := func(parent, child string) {
		for _, x := range links[parent] {
			if x == child {
				return
			}
		}
		links[parent] = append(links[parent], child)
	}
	seen := map[string]bool{}
	for _, c := range raw.Concepts {
		if len(added) == maxDig {
			break
		}
		title := strings.TrimSpace(c.Title)
		id := vault.ConceptID(c.ID)
		if id == "" {
			id = vault.ConceptID(title)
		}
		if id == "" || title == "" || vault.ConceptKey(title) == topicKey {
			continue
		}
		pc := PlannedConcept{ID: id, Title: title, Summary: strings.TrimSpace(c.Summary), Category: vault.ConceptID(c.Category), Level: deepest + 1}
		k, ok := idx.find(id)
		if !ok {
			k, ok = idx.find(title)
		}
		if ok {
			pc.ID, pc.Exists = k.ID, k.Written
			if k.Title != "" {
				pc.Title = k.Title
			}
		}
		var parents []string
		if deepest == 0 {
			parents = []string{""}
		} else {
			for _, f := range c.For {
				if f = vault.ConceptID(f); isFrontier[f] && f != pc.ID {
					parents = append(parents, f)
				}
			}
			if len(parents) == 0 && len(frontier) == 1 && frontier[0] != pc.ID {
				parents = frontier
			}
		}
		if len(parents) == 0 || seen[pc.ID] {
			continue
		}
		if _, mapped := ice[pc.ID]; mapped {
			continue // already in this iceberg, higher up: an edge back up would make a loop
		}
		seen[pc.ID] = true
		for _, p := range parents {
			link(p, pc.ID)
		}
		added = append(added, pc)
	}
	if len(added) == 0 {
		return added, nil
	}

	// written parents keep their prerequisites in their own note
	for parent, kids := range links {
		if parent == "" || ice[parent].Pending {
			continue
		}
		err := s.V.UpdateConcept(parent, func(c *vault.Concept) error {
			c.Meta.Requires = appendNew(c.Meta.Requires, kids...)
			return nil
		})
		if err != nil {
			s.termf("Aviso: no pude enlazar «%s»: %v\n", ice[parent].Title, err)
		}
	}
	for _, c := range added {
		if c.Exists {
			if err := s.linkConcept(c.ID, guide, g.Meta.Title); err != nil {
				s.termf("Aviso: no pude enlazar «%s»: %v\n", c.Title, err)
			}
		}
	}
	err = s.V.Update(guide, func(n *vault.Note) error {
		n.Meta.Base = appendNew(n.Meta.Base, links[""]...)
		for i, p := range n.Meta.Pending {
			n.Meta.Pending[i].Requires = appendNew(p.Requires, links[p.ID]...)
		}
		for _, c := range added {
			if c.Exists {
				n.Meta.Concepts = appendNew(n.Meta.Concepts, c.ID)
				continue
			}
			n.Meta.Pending = append(n.Meta.Pending, vault.PendingConcept{ID: c.ID, Title: c.Title, Summary: c.Summary, Category: c.Category, Level: c.Level})
		}
		return nil
	})
	return added, err
}

// appendNew appends the items list does not have yet.
func appendNew(list []string, items ...string) []string {
	have := map[string]bool{}
	for _, x := range list {
		have[x] = true
	}
	for _, x := range items {
		if !have[x] {
			have[x] = true
			list = append(list, x)
		}
	}
	return list
}
