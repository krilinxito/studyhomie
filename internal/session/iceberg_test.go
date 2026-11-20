package session

import (
	"context"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func saveConcept(t *testing.T, s *Session, id, title string, guides ...string) {
	t.Helper()
	if err := s.V.SaveConcept(vault.Concept{Slug: id, Meta: vault.ConceptMeta{Title: title, Guides: guides}, Body: "# " + title + "\n"}); err != nil {
		t.Fatal(err)
	}
}

func TestDigMapsOneLevelUnderTheDeepestConcepts(t *testing.T) {
	f := &fake{replies: []provider.Response{reply("```json\n"+`{"concepts":[
	 {"id":"funcion","title":"Función","summary":"una regla que asigna","category":"Fundamentos","for":["integral","seno"]},
	 {"id":"limite","title":"Límite","for":["integral"]},
	 {"id":"probabilidad-condicionada","title":"Probabilidad condicionada","for":["seno"]},
	 {"id":"integral","title":"Integral","for":["seno"]},
	 {"id":"huerfano","title":"Huérfano","for":["nada"]}
	]}`+"\n```", 10)}}
	s, _ := newSession(t, f, "")
	saveConcept(t, s, "integral", "Integral", "fourier")
	saveConcept(t, s, "probabilidad-condicional", "Probabilidad condicional", "otra-guia")
	g := vault.Note{Slug: "fourier", Meta: vault.Meta{Title: "Fourier", Base: []string{"integral", "seno"}, Concepts: []string{"integral"},
		Pending: []vault.PendingConcept{{ID: "seno", Title: "Seno", Summary: "oscila", Level: 1}}}, Body: "## Guía\n\nTexto.\n"}
	if err := s.V.Save(g); err != nil {
		t.Fatal(err)
	}

	added, err := s.Dig(context.Background(), "fourier")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]PlannedConcept{}
	for _, c := range added {
		got[c.ID] = c
	}
	if len(added) != 3 || got["funcion"].Level != 2 || got["limite"].Level != 2 || !got["probabilidad-condicional"].Exists {
		t.Fatalf("added = %+v", added)
	}
	p := f.reqs[0].Prompt
	if !strings.Contains(p, "DIG UNDER") || !strings.Contains(p, "seno: Seno — oscila") || !strings.Contains(p, "probabilidad-condicional: Probabilidad condicional") {
		t.Errorf("dig prompt:\n%s", p)
	}

	n, _ := s.V.Load("fourier")
	pend := map[string]vault.PendingConcept{}
	for _, x := range n.Meta.Pending {
		pend[x.ID] = x
	}
	if pend["funcion"].Level != 2 || pend["funcion"].Category != "fundamentos" || pend["limite"].Level != 2 {
		t.Errorf("pending after dig: %+v", n.Meta.Pending)
	}
	if _, ok := pend["probabilidad-condicional"]; ok {
		t.Error("a concept the vault has is linked, not left pending")
	}
	if strings.Join(n.Meta.Concepts, ",") != "integral,probabilidad-condicional" {
		t.Errorf("concepts = %v", n.Meta.Concepts)
	}
	// the pending parent and the written one both point at what they stand on
	if strings.Join(pend["seno"].Requires, ",") != "funcion,probabilidad-condicional" {
		t.Errorf("seno requires = %v", pend["seno"].Requires)
	}
	if c, _ := s.V.LoadConcept("integral"); strings.Join(c.Meta.Requires, ",") != "funcion,limite" {
		t.Errorf("integral requires = %v", c.Meta.Requires)
	}
	if c, _ := s.V.LoadConcept("probabilidad-condicional"); strings.Join(c.Meta.Guides, ",") != "otra-guia,fourier" {
		t.Errorf("reused concept guides = %v", c.Meta.Guides)
	}

	// the next dig goes under the new bottom
	f.replies = []provider.Response{reply("```json\n{\"concepts\":[]}\n```", 10)}
	if added, err := s.Dig(context.Background(), "fourier"); err != nil || len(added) != 0 {
		t.Fatalf("dig at the bottom: %+v %v", added, err)
	}
	p = f.reqs[1].Prompt
	under := p[strings.Index(p, "DIG UNDER"):]
	if !strings.Contains(under, "funcion: Función") || strings.Contains(under, "seno: Seno") {
		t.Errorf("second dig should go under level 2:\n%s", p)
	}
}

func TestDigUnderAGuideWithNothingMapped(t *testing.T) {
	f := &fake{replies: []provider.Response{reply("```json\n"+`{"concepts":[{"id":"integral","title":"Integral","for":[]},{"id":"fourier","title":"Fourier"}]}`+"\n```", 10)}}
	s, _ := newSession(t, f, "")
	if err := s.V.Save(vault.Note{Slug: "fourier", Meta: vault.Meta{Title: "Fourier"}, Body: "## Guía\n\nTexto.\n"}); err != nil {
		t.Fatal(err)
	}
	added, err := s.Dig(context.Background(), "fourier")
	if err != nil || len(added) != 1 || added[0].ID != "integral" || added[0].Level != 1 {
		t.Fatalf("added = %+v %v", added, err)
	}
	if !strings.Contains(f.reqs[0].Prompt, "the topic itself") {
		t.Errorf("prompt:\n%s", f.reqs[0].Prompt)
	}
	n, _ := s.V.Load("fourier")
	if strings.Join(n.Meta.Base, ",") != "integral" || len(n.Meta.Pending) != 1 {
		t.Errorf("guide after dig: %+v", n.Meta)
	}
	if _, err := s.Dig(context.Background(), "no-existe"); err == nil {
		t.Error("digging under a missing guide must fail")
	}
}

func TestExplainLinksAConceptTheVaultHasUnderAnotherSpelling(t *testing.T) {
	f := &fake{}
	s, _ := newSession(t, f, "")
	saveConcept(t, s, "probabilidad-condicional", "Probabilidad condicional", "otra")
	g := vault.Note{Slug: "bayes", Meta: vault.Meta{Title: "Bayes",
		Pending: []vault.PendingConcept{{ID: "probabilidad-condicionada", Title: "Probabilidad condicionada", Level: 1}}}, Body: "## Guía\n\nTexto.\n"}
	if err := s.V.Save(g); err != nil {
		t.Fatal(err)
	}
	id, err := s.Explain(context.Background(), "bayes", "probabilidad-condicionada", "")
	if err != nil || id != "probabilidad-condicional" || len(f.reqs) != 0 {
		t.Fatalf("explain = %q %v (%d calls)", id, err, len(f.reqs))
	}
	n, _ := s.V.Load("bayes")
	if len(n.Meta.Pending) != 0 || strings.Join(n.Meta.Concepts, ",") != "probabilidad-condicional" {
		t.Errorf("guide after explain: %+v", n.Meta)
	}
}

func TestRelevantKeepsTheClosestConcepts(t *testing.T) {
	var list []vault.Concept
	for _, title := range []string{"Árbol binario", "Pila", "Cola", "Probabilidad condicional", "Integral", "Bayes ingenuo"} {
		list = append(list, vault.Concept{Slug: vault.ConceptID(title), Meta: vault.ConceptMeta{Title: title}})
	}
	got := relevant(list, "Teorema de Bayes y probabilidad", 2)
	if len(got) != 2 || got[0].Slug != "bayes-ingenuo" || got[1].Slug != "probabilidad-condicional" {
		t.Errorf("relevant = %+v", got)
	}
	if len(relevant(list, "x", 10)) != len(list) {
		t.Error("a short list goes whole")
	}
}
