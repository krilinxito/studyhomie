// Package roadmap models a learning plan as a graph of topics.
package roadmap

import (
	"fmt"
	"sort"

	"github.com/krilinxito/studyhomie/internal/vault"
)

// Node is one topic of the plan. Requires and Related hold titles or slugs.
type Node struct {
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Requires []string `json:"requires"`
	Related  []string `json:"related"`
}

// Plan is the model's roadmap for a subject.
type Plan struct {
	Subject string `json:"subject"`
	Nodes   []Node `json:"nodes"`
}

// Slug returns the vault slug of a node.
func (n Node) Slug() string { return vault.Slugify(n.Title) }

// Normalize cleans a model-written plan and checks it is a valid graph:
// titles must be unique, references are resolved to known nodes (unknown
// references are dropped and reported), and prerequisites must not form a cycle.
// Nodes come back in study order: prerequisites first, otherwise model order.
func (p Plan) Normalize() (Plan, []string, error) {
	var warnings []string
	if len(p.Nodes) == 0 {
		return p, nil, fmt.Errorf("the roadmap has no topics")
	}
	known := map[string]bool{}
	for i, n := range p.Nodes {
		s := n.Slug()
		if s == "" {
			return p, nil, fmt.Errorf("topic %d has no usable title", i+1)
		}
		if known[s] {
			return p, nil, fmt.Errorf("duplicate topic %q", n.Title)
		}
		known[s] = true
	}

	clean := func(owner Node, refs []string, kind string) []string {
		var out []string
		seen := map[string]bool{owner.Slug(): true}
		for _, r := range refs {
			s := vault.Slugify(r)
			switch {
			case seen[s]:
			case !known[s]:
				warnings = append(warnings, fmt.Sprintf("%q: %s %q is not in the roadmap, ignored", owner.Title, kind, r))
			default:
				seen[s] = true
				out = append(out, s)
			}
		}
		return out
	}
	nodes := make([]Node, len(p.Nodes))
	for i, n := range p.Nodes {
		n.Requires = clean(n, n.Requires, "prerequisite")
		n.Related = clean(n, n.Related, "related topic")
		nodes[i] = n
	}

	order, err := topoOrder(nodes)
	if err != nil {
		return p, warnings, err
	}
	p.Nodes = order
	return p, warnings, nil
}

// topoOrder is Kahn's algorithm, picking the earliest model-ordered ready node each round.
func topoOrder(nodes []Node) ([]Node, error) {
	pos := map[string]int{}
	for i, n := range nodes {
		pos[n.Slug()] = i
	}
	missing := make([]int, len(nodes))
	dependents := map[string][]int{}
	for i, n := range nodes {
		missing[i] = len(n.Requires)
		for _, r := range n.Requires {
			dependents[r] = append(dependents[r], i)
		}
	}
	var ready []int
	for i, m := range missing {
		if m == 0 {
			ready = append(ready, i)
		}
	}
	var out []Node
	for len(ready) > 0 {
		sort.Ints(ready)
		i := ready[0]
		ready = ready[1:]
		out = append(out, nodes[i])
		for _, d := range dependents[nodes[i].Slug()] {
			if missing[d]--; missing[d] == 0 {
				ready = append(ready, d)
			}
		}
	}
	if len(out) != len(nodes) {
		var stuck []string
		for i, m := range missing {
			if m > 0 {
				stuck = append(stuck, nodes[i].Title)
			}
		}
		return nil, fmt.Errorf("circular prerequisites among: %v", stuck)
	}
	return out, nil
}

// Next returns the first topic, in study order, that is not done and whose
// prerequisites are all done. ok is false when everything is done or blocked.
func Next(notes []vault.Note) (vault.Note, bool) {
	done := map[string]bool{}
	for _, n := range notes {
		if n.Meta.Status == "explained" {
			done[n.Slug] = true
		}
	}
	sorted := append([]vault.Note(nil), notes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Meta.Order < sorted[j].Meta.Order })
	for _, n := range sorted {
		if done[n.Slug] {
			continue
		}
		ready := true
		for _, r := range n.Meta.Requires {
			if !done[r] {
				ready = false
				break
			}
		}
		if ready {
			return n, true
		}
	}
	return vault.Note{}, false
}
