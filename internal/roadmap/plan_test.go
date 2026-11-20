package roadmap

import (
	"fmt"
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

func note(slug, status string, order, mastery int) vault.Note {
	return vault.Note{Slug: slug, Meta: vault.Meta{Status: status, Order: order, Mastery: mastery}}
}

func TestStepsAreLinear(t *testing.T) {
	notes := []vault.Note{
		note("c", "new", 3, 0),
		note("a", "tested", 1, 80),
		note("b", "explained", 2, 60), // explained but under the passing score
	}
	steps := Steps(notes, 70)
	var got []string
	for _, st := range steps {
		got = append(got, fmt.Sprintf("%s:%v:%v", st.Note.Slug, st.Passed, st.Open))
	}
	if want := "a:true:true,b:false:true,c:false:false"; strings.Join(got, ",") != want {
		t.Errorf("steps = %v, want %s", got, want)
	}
	if n, ok := Next(notes, 70); !ok || n.Slug != "b" {
		t.Errorf("next = %v %v, want b", n.Slug, ok)
	}
	if b, locked := Blocker(notes, "c", 70); !locked || b.Slug != "b" {
		t.Errorf("c must be locked by b, got %v %v", b.Slug, locked)
	}
	if _, locked := Blocker(notes, "b", 70); locked {
		t.Error("b is open")
	}
	// a lower passing score opens the rest
	if _, locked := Blocker(notes, "c", 60); locked {
		t.Error("with 60 to pass, c is open")
	}
	notes[2].Meta.Status = "prepared" // a guide alone, never tested, is not passed
	notes[2].Meta.Mastery = 100
	if n, ok := Next(notes, 0); !ok || n.Slug != "b" {
		t.Errorf("an untested topic is not passed even with 0 to pass: %v", n.Slug)
	}
	notes[2].Meta.Status = "tested"
	notes[0].Meta.Status, notes[0].Meta.Mastery = "explained", 90
	if _, ok := Next(notes, 70); ok {
		t.Error("everything passed: expected no next topic")
	}
}
