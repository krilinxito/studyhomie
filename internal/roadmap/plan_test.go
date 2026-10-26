package roadmap

import (
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/vault"
)

func titles(p Plan) []string {
	var t []string
	for _, n := range p.Nodes {
		t = append(t, n.Title)
	}
	return t
}

func TestNormalizeOrdersByPrerequisites(t *testing.T) {
	p := Plan{Nodes: []Node{
		{Title: "Bayes", Requires: []string{"Probabilidad condicional"}},
		{Title: "Probabilidad condicional", Requires: []string{"Probabilidad"}},
		{Title: "Probabilidad"},
		{Title: "Inferencia", Requires: []string{"bayes"}, Related: []string{"Probabilidad"}},
	}}
	got, warns, err := p.Normalize()
	if err != nil || len(warns) != 0 {
		t.Fatalf("err=%v warns=%v", err, warns)
	}
	if want := "Probabilidad,Probabilidad condicional,Bayes,Inferencia"; strings.Join(titles(got), ",") != want {
		t.Errorf("order = %v, want %s", titles(got), want)
	}
	if got.Nodes[3].Requires[0] != "bayes" || got.Nodes[3].Related[0] != "probabilidad" {
		t.Errorf("refs must be resolved to slugs: %+v", got.Nodes[3])
	}
}

func TestNormalizeDropsUnknownAndSelfRefs(t *testing.T) {
	p := Plan{Nodes: []Node{
		{Title: "A", Requires: []string{"A", "Fantasma"}, Related: []string{"B", "B"}},
		{Title: "B"},
	}}
	got, warns, err := p.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes[0].Requires) != 0 || len(got.Nodes[0].Related) != 1 {
		t.Errorf("refs not cleaned: %+v", got.Nodes[0])
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "Fantasma") {
		t.Errorf("warnings = %v", warns)
	}
}

func TestNormalizeRejectsBadPlans(t *testing.T) {
	cases := map[string]Plan{
		"empty":     {},
		"duplicate": {Nodes: []Node{{Title: "A"}, {Title: "a"}}},
		"no title":  {Nodes: []Node{{Title: "!!!"}}},
		"cycle":     {Nodes: []Node{{Title: "A", Requires: []string{"B"}}, {Title: "B", Requires: []string{"A"}}}},
	}
	for name, p := range cases {
		if _, _, err := p.Normalize(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func note(slug, status string, order int, requires ...string) vault.Note {
	return vault.Note{Slug: slug, Meta: vault.Meta{Status: status, Order: order, Requires: requires}}
}

func TestNext(t *testing.T) {
	notes := []vault.Note{
		note("c", "new", 3, "b"),
		note("a", "explained", 1),
		note("b", "prepared", 2, "a"),
	}
	n, ok := Next(notes)
	if !ok || n.Slug != "b" {
		t.Fatalf("next = %v %v, want b", n.Slug, ok)
	}
	notes[2].Meta.Status = "explained"
	if n, ok = Next(notes); !ok || n.Slug != "c" {
		t.Errorf("next = %v %v, want c", n.Slug, ok)
	}
	notes[0].Meta.Status = "explained"
	if _, ok = Next(notes); ok {
		t.Error("everything done: expected no next topic")
	}
}
