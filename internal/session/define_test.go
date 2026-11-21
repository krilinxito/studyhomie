package session

import (
	"context"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestDefineExplainsASentenceInContextAndSavesIt(t *testing.T) {
	f := &fake{replies: []provider.Response{
		reply("Dice que la evidencia **actualiza** lo que creías.\n\n```json\n{\"title\": \"Actualizar creencias con evidencia\", \"summary\": \"La evidencia cambia la probabilidad previa.\", \"kind\": \"passage\"}\n```", 10),
		reply("Más a fondo: ...", 10),
	}}
	s, _ := newSession(t, f, "")
	g := vault.Note{Slug: "bayes", Meta: vault.Meta{Title: "Teorema de Bayes", Status: "prepared", Area: "matematicas",
		Pending: []vault.PendingConcept{{ID: "actualizar-creencias-con-evidencia", Title: "Actualizar creencias", Level: 1}}}, Body: "## Guía\n\nTexto."}
	if err := s.V.Save(g); err != nil {
		t.Fatal(err)
	}
	sentence := "la posterior es proporcional a la verosimilitud por la prior"
	d, err := s.Define(context.Background(), DefineReq{Text: sentence, Context: "En Bayes, " + sentence + ".", Guide: "bayes"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != "passage" || d.Title != "Actualizar creencias con evidencia" || strings.Contains(d.Text, "```") {
		t.Errorf("definition: %+v", d)
	}
	p := f.reqs[0].Prompt
	if !strings.Contains(p, "SELECTED:\n"+sentence) || !strings.Contains(p, "PARAGRAPH IT CAME FROM") || !strings.Contains(p, "Teorema de Bayes") {
		t.Errorf("prompt lacks the context: %s", p)
	}

	// deeper builds on what was said
	if _, err := s.Define(context.Background(), DefineReq{Text: sentence, Deeper: true, Previous: d.Text}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.reqs[1].Prompt, "DEEPER") || !strings.Contains(f.reqs[1].Prompt, "EXPLANATION ALREADY GIVEN") {
		t.Errorf("deeper prompt: %s", f.reqs[1].Prompt)
	}

	id, created, err := s.SaveDefinition(d.Title, d.Summary, d.Text, "bayes")
	if err != nil || !created || id != "actualizar-creencias-con-evidencia" {
		t.Fatalf("save: %q %v %v", id, created, err)
	}
	c, _ := s.V.LoadConcept(id)
	if !strings.Contains(c.Body, "actualiza") || len(c.Meta.Guides) != 1 || c.Meta.Summary == "" {
		t.Errorf("concept: %+v\n%s", c.Meta, c.Body)
	}
	n, _ := s.V.Load("bayes")
	if len(n.Meta.Concepts) != 1 || len(n.Meta.Pending) != 0 {
		t.Errorf("the guide's iceberg: concepts %v pending %v", n.Meta.Concepts, n.Meta.Pending)
	}
	// a short term the vault has is found without the AI; a passage never is
	if got, ok := s.FindConcept("Actualizar creencias con evidencia"); !ok || got != id {
		t.Errorf("find: %q %v", got, ok)
	}
	if _, ok := s.FindConcept(sentence); ok {
		t.Error("a sentence must not match a concept note")
	}
	// saving it again links, it does not overwrite
	if _, created, _ := s.SaveDefinition(d.Title, "", "otro texto", ""); created {
		t.Error("an existing concept was written again")
	}
	if len(f.reqs) != 2 {
		t.Errorf("saving must not call the AI: %d calls", len(f.reqs))
	}
}

func TestDefineRejectsEmptyAndHugeSelections(t *testing.T) {
	s, _ := newSession(t, &fake{}, "")
	if _, err := s.Define(context.Background(), DefineReq{Text: "  "}); !IsInputError(err) {
		t.Errorf("empty: %v", err)
	}
	if _, err := s.Define(context.Background(), DefineReq{Text: strings.Repeat("palabra ", 200)}); !IsInputError(err) {
		t.Errorf("huge: %v", err)
	}
}
