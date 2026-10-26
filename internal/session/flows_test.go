package session

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func jsonReply(s string) provider.Response { return reply("```json\n"+s+"\n```", 10) }

func newReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

func noteFor(slug, title, status string, mastery int) vault.Note {
	return vault.Note{Slug: slug, Meta: vault.Meta{Title: title, Status: status, Mastery: mastery}}
}

const scopeReply = `{"questions":["¿Para qué lo quieres?","¿Qué nivel tienes?"]}`

const planReply = `{"subject":"Probabilidad","nodes":[
 {"title":"Bayes","summary":"Actualizar creencias con evidencia","requires":["Probabilidad condicional"],"related":["Inferencia"]},
 {"title":"Probabilidad condicional","summary":"Entender P(A|B)","requires":[]},
 {"title":"Inferencia","summary":"Sacar conclusiones de datos","requires":["Bayes"]}
]}`

func TestRoadmapBuildsConnectedVault(t *testing.T) {
	f := &fake{replies: []provider.Response{jsonReply(scopeReply), jsonReply(planReply)}}
	s, out := newSession(t, f, "Para ML\nPrincipiante\n")
	rslug, err := s.Roadmap(context.Background(), "Probabilidad")
	if err != nil {
		t.Fatal(err)
	}
	if rslug != "probabilidad" {
		t.Fatalf("roadmap slug = %q", rslug)
	}
	// the plan request carries the learner's answers
	if p := f.reqs[1].Prompt; !strings.Contains(p, "Para ML") || !strings.Contains(p, "Principiante") {
		t.Errorf("plan prompt lacks the scope answers:\n%s", p)
	}

	bayes, err := s.V.Load("bayes")
	if err != nil {
		t.Fatal(err)
	}
	// topics are stored in study order: prerequisites first
	cond, _ := s.V.Load("probabilidad-condicional")
	if cond.Meta.Order != 1 || bayes.Meta.Order != 2 {
		t.Errorf("order: condicional=%d bayes=%d", cond.Meta.Order, bayes.Meta.Order)
	}
	if bayes.Meta.Status != "new" || bayes.Meta.Roadmap != "probabilidad" || len(bayes.Meta.Requires) != 1 || bayes.Meta.Requires[0] != "probabilidad-condicional" {
		t.Errorf("bayes meta = %+v", bayes.Meta)
	}
	for _, want := range []string{"## Conexiones", "[[probabilidad-condicional|Probabilidad condicional]]", "[[inferencia|Inferencia]]"} {
		if !strings.Contains(bayes.Body, want) {
			t.Errorf("bayes body missing %q:\n%s", want, bayes.Body)
		}
	}
	_, edges, _ := s.V.Graph()
	has := func(from, to string) bool {
		for _, e := range edges {
			if e.From == from && e.To == to {
				return true
			}
		}
		return false
	}
	if !has("bayes", "probabilidad-condicional") || !has("bayes", "inferencia") || !has("inferencia", "bayes") {
		t.Errorf("graph edges missing: %v", edges)
	}
	rn, err := s.V.LoadFrom("roadmaps", "probabilidad")
	if err != nil || !strings.Contains(rn.Body, "Para ML") || !strings.Contains(rn.Body, "1. [[probabilidad-condicional|") {
		t.Errorf("roadmap note wrong: %v\n%s", err, rn.Body)
	}
	if !strings.Contains(out.String(), "Roadmap «Probabilidad» (3 temas)") {
		t.Errorf("output: %s", out.String())
	}
}

func TestRoadmapRejectsCycleAndWritesNothing(t *testing.T) {
	cyc := `{"subject":"X","nodes":[{"title":"A","summary":"a","requires":["B"]},{"title":"B","summary":"b","requires":["A"]}]}`
	f := &fake{replies: []provider.Response{jsonReply(scopeReply), jsonReply(cyc)}}
	s, _ := newSession(t, f, "x\ny\n")
	if _, err := s.Roadmap(context.Background(), "X"); err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("expected a circular prerequisites error, got %v", err)
	}
	if notes, _ := s.V.List(); len(notes) != 0 {
		t.Errorf("a rejected plan must not leave notes behind: %d", len(notes))
	}
}

func TestNextFollowsPrerequisites(t *testing.T) {
	f := &fake{replies: []provider.Response{jsonReply(scopeReply), jsonReply(planReply)}}
	s, _ := newSession(t, f, "a\nb\n")
	if _, err := s.Roadmap(context.Background(), "Probabilidad"); err != nil {
		t.Fatal(err)
	}
	// "n" at the confirmation: only the suggestion is printed
	s.In = newReader("n\n")
	var out strings.Builder
	s.Out = &out
	if err := s.Next(context.Background(), "probabilidad"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Siguiente tema: Probabilidad condicional (1/3)") || !strings.Contains(out.String(), "Entender P(A|B)") {
		t.Errorf("output: %s", out.String())
	}
	if err := s.Next(context.Background(), "no-existe"); err == nil {
		t.Error("unknown roadmap must be an error")
	}
}

func TestMaterialBuildsRoadmapAndGroundsPreparation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Apuntes de Estadística.md")
	doc := "# Probabilidad condicional\n\nLa probabilidad condicional P(A|B) es la probabilidad de A sabiendo que B ocurrió. Es la base de todo.\n\n" +
		"# Cadenas de Markov\n\nLas cadenas de Markov modelan transiciones entre estados con matrices estocásticas.\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := `{"subject":"Apuntes","nodes":[
	  {"title":"Probabilidad condicional","summary":"Entender P(A|B)"},
	  {"title":"Cadenas de Markov","summary":"Modelar transiciones","requires":["Probabilidad condicional"]}]}`
	f := &fake{replies: []provider.Response{jsonReply(plan), reply(prepReply, 10)}}
	s, _ := newSession(t, f, "\n") // empty focus
	rslug, err := s.Material(context.Background(), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if rslug != "apuntes-de-estadística" {
		t.Fatalf("slug = %q", rslug)
	}
	if p := f.reqs[0].Prompt; !strings.Contains(p, "SOURCE MATERIAL OUTLINE") || !strings.Contains(p, "Cadenas de Markov") || !strings.Contains(p, "Cover the whole material") {
		t.Errorf("planning prompt wrong:\n%s", p)
	}
	n, _ := s.V.Load("cadenas-de-markov")
	if n.Meta.Material != "apuntes-de-estadística" || n.Meta.Source != "material" {
		t.Errorf("meta = %+v", n.Meta)
	}

	if _, err := s.Prepare(context.Background(), "Cadenas de Markov", "ignored"); err != nil {
		t.Fatal(err)
	}
	prep := f.reqs[1].Prompt
	if !strings.Contains(prep, "SOURCE MATERIAL") || !strings.Contains(prep, "matrices estocásticas") {
		t.Errorf("preparation must include the matching excerpt:\n%s", prep)
	}
	if !strings.Contains(prep, "Modelar transiciones") {
		t.Errorf("preparation must include the roadmap objective:\n%s", prep)
	}
	after, _ := s.V.Load("cadenas-de-markov")
	if after.Meta.Source != "material" || after.Meta.Roadmap != rslug || !strings.Contains(after.Body, "## Conexiones") || !strings.Contains(after.Body, "## Guía") {
		t.Errorf("preparing must keep the roadmap data and add the guide:\n%+v\n%s", after.Meta, after.Body)
	}
}

func TestMaterialRejectsUnsupportedAndEmpty(t *testing.T) {
	s, _ := newSession(t, &fake{}, "")
	if _, err := s.Material(context.Background(), []string{"virus.exe"}); err == nil {
		t.Error("unsupported type must be rejected")
	}
	empty := filepath.Join(t.TempDir(), "vacio.txt")
	os.WriteFile(empty, []byte("  \n"), 0o644)
	if _, err := s.Material(context.Background(), []string{empty}); err == nil {
		t.Error("empty document must be rejected")
	}
}

func TestDailyRerollAcceptAndResume(t *testing.T) {
	first := `{"field":"Historia","topic":"La Biblioteca de Alejandría","hook":"¿Qué se perdió realmente?"}`
	second := `{"field":"Física","topic":"Por qué el cielo es azul","hook":"No es el reflejo del mar."}`
	f := &fake{replies: []provider.Response{jsonReply(first), jsonReply(second), reply(prepReply, 10)}}
	// n = reject first proposal, s = accept second, then EOF at "¿Pasamos a la prueba?"
	s, out := newSession(t, f, "n\ns\n")
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }
	s.Cfg.Interests = []string{"Historia", "Física"}
	picks := []int{0, 1}
	s.Pick = func(n int) int { p := picks[0]; picks = picks[1:]; return p }

	if err := s.Daily(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.reqs[0].Prompt, "Field: Historia") || !strings.Contains(f.reqs[1].Prompt, "La Biblioteca de Alejandría") {
		t.Errorf("rerolls must avoid rejected topics: %q / %q", f.reqs[0].Prompt, f.reqs[1].Prompt)
	}
	day, err := s.V.LoadFrom("daily", "2026-10-27")
	if err != nil || day.Meta.Title != "Por qué el cielo es azul" || day.Meta.Field != "Física" {
		t.Fatalf("daily note: %+v %v", day, err)
	}
	topic, err := s.V.Load("por-qué-el-cielo-es-azul")
	if err != nil || topic.Meta.Source != "daily" || topic.Meta.Field != "Física" || topic.Meta.Status != "prepared" {
		t.Fatalf("topic note: %+v %v", topic.Meta, err)
	}

	// same day again: resumes, no new proposal and no model call
	calls := len(f.reqs)
	s.In = newReader("")
	out.Reset()
	if err := s.Daily(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != calls || !strings.Contains(out.String(), "Tu reto de hoy: Por qué el cielo es azul") {
		t.Errorf("resume must not draw a new topic (calls %d→%d): %s", calls, len(f.reqs), out.String())
	}

	// next day: a new draw is made
	s.Now = func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC) }
	f.replies = nil
	for i := 0; i < maxRerolls; i++ {
		f.replies = append(f.replies, jsonReply(first))
	}
	s.In = newReader(strings.Repeat("n\n", maxRerolls))
	picks = make([]int, maxRerolls)
	if err := s.Daily(context.Background(), "Historia"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.V.LoadFrom("daily", "2026-10-28"); err == nil {
		t.Error("rejecting every proposal must not create a daily note")
	}
}

func TestDailyAllFieldsAndCustomField(t *testing.T) {
	proposal := jsonReply(`{"field":"X","topic":"Tema","hook":"Gancho"}`)
	rejectAll := strings.Repeat("n\n", maxRerolls)
	replies := func() []provider.Response {
		var r []provider.Response
		for i := 0; i < maxRerolls; i++ {
			r = append(r, proposal)
		}
		return r
	}

	// AllFields draws from the built-in fields plus configured extras, without duplicates
	s, _ := newSession(t, &fake{replies: replies()}, rejectAll)
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }
	s.Cfg.Interests = []string{"Historia", "Astronomía", "historia del ARTE"}
	var got []int
	s.Pick = func(n int) int { got = append(got, n); return n - 1 }
	if err := s.Daily(context.Background(), AllFields); err != nil {
		t.Fatal(err)
	}
	want := len(config.DefaultInterests()) + 1 // Astronomía is the only extra
	if len(got) == 0 || got[0] != want {
		t.Errorf("all-fields pool = %v, want %d", got, want)
	}

	// any text is accepted as a one-off field
	f := &fake{replies: replies()}
	s, _ = newSession(t, f, rejectAll)
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }
	s.Pick = func(n int) int { return 0 }
	if err := s.Daily(context.Background(), "  Arquitectura "); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.reqs[0].Prompt, "Field: Arquitectura\n") {
		t.Errorf("custom field must reach the prompt trimmed: %q", f.reqs[0].Prompt)
	}

	if !slices.Contains(config.DefaultInterests(), "Historia del arte") {
		t.Error("Historia del arte must be a default field")
	}
}

func TestDailyCompletedTopicIsNotRepeated(t *testing.T) {
	s, out := newSession(t, &fake{}, "")
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }
	topic := noteFor("tema-hecho", "Tema hecho", "explained", 90)
	s.V.Save(topic)
	s.V.SaveIn("daily", noteFor("2026-10-27", "Tema hecho", "", 0))
	if err := s.Daily(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Ya lo completaste hoy (dominio 90/100)") {
		t.Errorf("output: %s", out.String())
	}
}
