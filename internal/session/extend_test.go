package session

import (
	"context"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/provider"
)

func order(body string, headings ...string) bool {
	last := -1
	for _, h := range headings {
		i := strings.Index(body, "\n## "+h+"\n")
		if i < 0 {
			i = strings.Index(body, "## "+h+"\n")
		}
		if i < 0 || i <= last {
			return false
		}
		last = i
	}
	return true
}

func TestExtendAddsBaseConceptsAfterTheGuide(t *testing.T) {
	base := "### [[Probabilidad]]\n\nImagina tirar un dado.\n\n## No abras otra sección\nsigue"
	f := &fake{replies: []provider.Response{reply(prepReply, 10), reply(base, 10)}}
	s, _ := newSession(t, f, "")
	slug, err := s.Prepare(context.Background(), "Bayes", "adhoc")
	if err != nil {
		t.Fatal(err)
	}
	text, err := s.Extend(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Imagina tirar un dado") {
		t.Errorf("the text written must be returned: %q", text)
	}

	n, _ := s.V.Load(slug)
	if !order(n.Body, "Guía", "Conceptos base", "Recursos") {
		t.Errorf("Conceptos base must sit between the guide and the resources:\n%s", n.Body)
	}
	if strings.Count(n.Body, "\n## ") != 2 || !strings.Contains(n.Body, "### No abras otra sección") {
		t.Errorf("headings from the model must not open sections:\n%s", n.Body)
	}
	if n.Meta.Status != "prepared" {
		t.Errorf("extending must not change progress: %q", n.Meta.Status)
	}

	// it is a short answer: only the summary of the guide goes along, and the model gets no tools
	req := f.reqs[1]
	if !strings.Contains(req.Prompt, "resumen corto") || strings.Contains(req.Prompt, "La intuición es X") || len(req.Tools) != 0 {
		t.Errorf("extend must send the guide summary and no tools: %+v", req)
	}
	if !strings.Contains(req.System, "background") {
		t.Errorf("system prompt is not the extend prompt: %q", req.System)
	}

	// running it again replaces the section instead of piling up
	f.replies = append(f.replies, reply("### Otra cosa\n\ntexto", 10))
	if _, err := s.Extend(context.Background(), slug); err != nil {
		t.Fatal(err)
	}
	n, _ = s.V.Load(slug)
	if strings.Count(n.Body, "## Conceptos base") != 1 || strings.Contains(n.Body, "dado") || !strings.Contains(n.Body, "Otra cosa") {
		t.Errorf("section was not replaced:\n%s", n.Body)
	}
}

func TestShortAnswersUseTheQuickModel(t *testing.T) {
	f := &fake{replies: []provider.Response{reply(prepReply, 10), reply("### A\n\nb", 10)}}
	s, _ := newSession(t, f, "")
	s.Cfg = config.Default()
	s.Cfg.Model, s.Cfg.QuickModel = "grande", "rapido"
	slug, _ := s.Prepare(context.Background(), "Bayes", "adhoc")
	if _, err := s.Extend(context.Background(), slug); err != nil {
		t.Fatal(err)
	}
	if f.reqs[0].Model != "grande" || f.reqs[1].Model != "rapido" {
		t.Errorf("the guide keeps the main model, short answers the quick one: %q, %q", f.reqs[0].Model, f.reqs[1].Model)
	}
	s.Cfg.QuickModel = ""
	f.replies = append(f.replies, reply("### A\n\nb", 10))
	s.Extend(context.Background(), slug)
	if f.reqs[2].Model != "grande" {
		t.Errorf("without a quick model the main one is used: %q", f.reqs[2].Model)
	}
}

func TestExtendNeedsAGuide(t *testing.T) {
	f := &fake{}
	s, _ := newSession(t, f, "")
	if err := s.V.Save(noteFor("bayes", "Bayes", "new", 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Extend(context.Background(), "bayes"); err == nil || !strings.Contains(err.Error(), "guía") {
		t.Errorf("expected a missing-guide error, got %v", err)
	}
	if _, err := s.Doubt(context.Background(), "bayes", "¿por qué?"); err == nil || !strings.Contains(err.Error(), "guía") {
		t.Errorf("a doubt needs a guide too, got %v", err)
	}
	if len(f.reqs) != 0 {
		t.Error("must not call the model without a guide")
	}
}

const doubtReply = "Se multiplican porque piden que pasen **las dos cosas**, ver [[Probabilidad condicional]].\n\nPiensa en tirar dos monedas.\n\n```json\n" +
	`{"concepts":[{"title":"Probabilidad condicional","summary":"Cómo cambia una probabilidad al saber algo más."},{"title":"Bayes","summary":"el propio tema"},{"title":"  ","summary":"vacío"},{"title":"Probabilidad condicional","summary":"repetido"}]}` + "\n```"

func TestDoubtIsAnsweredAndSavedUnderDudas(t *testing.T) {
	f := &fake{replies: []provider.Response{reply(prepReply, 10), reply(doubtReply, 10), reply("Otra respuesta.\n\n```json\n{\"concepts\":[]}\n```", 10)}}
	s, _ := newSession(t, f, "")
	slug, _ := s.Prepare(context.Background(), "Bayes", "adhoc")

	got, err := s.Doubt(context.Background(), slug, "Entiendo qué es una probabilidad,\n pero no por qué se multiplican")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Entry, "### Entiendo qué es una probabilidad, pero no por qué se multiplican\n\n") || strings.Contains(got.Entry, "```json") {
		t.Errorf("entry wrong: %q", got.Entry)
	}
	if len(got.Concepts) != 1 || got.Concepts[0].Slug != "probabilidad-condicional" || got.Concepts[0].Exists {
		t.Errorf("only the new concept should be offered, once: %+v", got.Concepts)
	}

	n, _ := s.V.Load(slug)
	if !order(n.Body, "Guía", "Dudas", "Recursos") || !strings.Contains(n.Body, "Piensa en tirar dos monedas") {
		t.Errorf("the doubt must be saved between the guide and the resources:\n%s", n.Body)
	}
	if n.Meta.Status != "prepared" {
		t.Errorf("a doubt must not change progress: %q", n.Meta.Status)
	}

	// the model sees the summary and the question, with no tools and no guide text
	req := f.reqs[1]
	if !strings.Contains(req.Prompt, "NEW QUESTION:\nEntiendo qué es") || !strings.Contains(req.Prompt, "resumen corto") || len(req.Tools) != 0 {
		t.Errorf("doubt request wrong: %+v", req)
	}
	if strings.Contains(req.Prompt, "DOUBTS ALREADY ANSWERED") {
		t.Error("the first doubt has no earlier ones")
	}

	// a second doubt is added after the first, and the model is told what was already answered
	if _, err := s.Doubt(context.Background(), slug, "¿y si no son independientes?"); err != nil {
		t.Fatal(err)
	}
	n, _ = s.V.Load(slug)
	if strings.Count(n.Body, "## Dudas") != 1 || strings.Index(n.Body, "monedas") > strings.Index(n.Body, "Otra respuesta") {
		t.Errorf("doubts must accumulate in order:\n%s", n.Body)
	}
	if p := f.reqs[2].Prompt; !strings.Contains(p, "DOUBTS ALREADY ANSWERED") || !strings.Contains(p, "monedas") {
		t.Errorf("a follow-up must carry the earlier doubts: %q", p)
	}
}

func TestDoubtSitsAfterBaseConceptsAndKeepsLongQuestionWhole(t *testing.T) {
	f := &fake{replies: []provider.Response{reply(prepReply, 10), reply("### Base\n\nx", 10), reply("Respuesta.", 10)}}
	s, _ := newSession(t, f, "")
	slug, _ := s.Prepare(context.Background(), "Bayes", "adhoc")
	s.Extend(context.Background(), slug)

	long := strings.Repeat("palabra ", 30)
	got, err := s.Doubt(context.Background(), slug, long)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Entry, "…\n\n> [!question] Tu duda\n> "+strings.TrimSpace(long)) {
		t.Errorf("a long question stays whole in a callout: %q", got.Entry)
	}
	n, _ := s.V.Load(slug)
	if !order(n.Body, "Guía", "Conceptos base", "Dudas", "Recursos") {
		t.Errorf("doubts go after the base concepts:\n%s", n.Body)
	}
	if len(got.Concepts) != 0 {
		t.Errorf("an answer without a JSON block offers nothing: %+v", got.Concepts)
	}

	for _, bad := range []string{"  ", strings.Repeat("x", maxQuestion+1)} {
		if _, err := s.Doubt(context.Background(), slug, bad); err == nil {
			t.Errorf("question %q... should be refused", bad[:2])
		}
	}
}

func TestAddConceptCreatesAnEmptyNoteLinkedBack(t *testing.T) {
	f := &fake{replies: []provider.Response{reply(prepReply, 10)}}
	s, _ := newSession(t, f, "")
	slug, _ := s.Prepare(context.Background(), "Bayes", "adhoc")

	got, created, err := s.AddConcept(slug, "Probabilidad condicional", "Cómo cambia una probabilidad al saber algo más.")
	if err != nil || !created || got != "probabilidad-condicional" {
		t.Fatalf("AddConcept = %q, %v, %v", got, created, err)
	}
	n, err := s.V.Load(got)
	if err != nil {
		t.Fatal(err)
	}
	if n.Meta.Status != "new" || n.Meta.Title != "Probabilidad condicional" || !strings.Contains(n.Meta.Summary, "saber algo más") {
		t.Errorf("meta wrong: %+v", n.Meta)
	}
	if links := n.Links(); len(links) != 1 || links[0] != "bayes" {
		t.Errorf("the new note must link back to where the doubt came from: %v\n%s", links, n.Body)
	}

	// asking again does not overwrite what is there
	n.Meta.Status = "explained"
	s.V.Save(n)
	again, created, err := s.AddConcept(slug, "probabilidad condicional", "otra")
	if err != nil || created || again != got {
		t.Errorf("second AddConcept = %q, %v, %v", again, created, err)
	}
	if n2, _ := s.V.Load(got); n2.Meta.Status != "explained" {
		t.Error("an existing note must be left alone")
	}
	if _, _, err := s.AddConcept(slug, "  ", ""); err == nil {
		t.Error("a concept needs a name")
	}
	if _, _, err := s.AddConcept("no-existe", "Algo", ""); err == nil {
		t.Error("the parent must exist")
	}
}
