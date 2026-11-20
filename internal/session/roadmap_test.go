package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
)

const scopeReply = `{"questions":["¿Para qué lo quieres?","¿Qué nivel tienes?"]}`

const planReply = `{"subject":"Probabilidad","nodes":[
 {"title":"Bayes","summary":"Actualizar creencias con evidencia","requires":["Probabilidad condicional"],"related":["Inferencia"]},
 {"title":"Probabilidad condicional","summary":"Entender P(A|B)","requires":[]},
 {"title":"Inferencia","summary":"Sacar conclusiones de datos","requires":["Bayes"]}
]}`

func TestRoadmapBuildsConnectedVault(t *testing.T) {
	f := &fake{replies: []provider.Response{jsonReply(scopeReply), jsonReply(planReply), reply(prepReply, 10)}}
	s, out := newSession(t, f, "Para ML\nPrincipiante\n3\n")
	rslug, err := s.Roadmap(context.Background(), "Probabilidad", RoadmapOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if rslug != "probabilidad" {
		t.Fatalf("roadmap slug = %q", rslug)
	}
	// the plan request carries the learner's answers and the reach they picked
	if p := f.reqs[1].Prompt; !strings.Contains(p, "Para ML") || !strings.Contains(p, "Principiante") || !strings.Contains(p, "ROADMAP DEPTH: 3") {
		t.Errorf("plan prompt lacks the scope answers or the reach:\n%s", p)
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
	// only the first topic gets its guide now
	if !hasGuide(cond) || cond.Meta.Status != "prepared" || hasGuide(bayes) {
		t.Errorf("only topic 1 must have a guide: cond=%v bayes=%v", hasGuide(cond), hasGuide(bayes))
	}
	if len(f.reqs) != 3 || !strings.Contains(f.reqs[2].Prompt, "Topic: Probabilidad condicional") {
		t.Errorf("expected scope, plan and the first guide, got %d requests", len(f.reqs))
	}
	for _, want := range []string{"## Conexiones", "[[probabilidad-condicional|Probabilidad condicional]]", "[[inferencia|Inferencia]]", "- Antes: ", "- Después: "} {
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
	if err != nil || !strings.Contains(rn.Body, "Para ML") || !strings.Contains(rn.Body, "1. [[probabilidad-condicional|") || !strings.Contains(rn.Body, "70/100") {
		t.Errorf("roadmap note wrong: %v\n%s", err, rn.Body)
	}
	if rn.Meta.Reach != 3 || rn.Meta.GuideDepth == nil || *rn.Meta.GuideDepth != 0 {
		t.Errorf("roadmap meta = %+v", rn.Meta)
	}
	if !strings.Contains(out.String(), "Roadmap «Probabilidad» (3 temas)") || !strings.Contains(out.String(), "1 · Enfocado") {
		t.Errorf("output: %s", out.String())
	}
}

func TestRoadmapRejectsCycleAndWritesNothing(t *testing.T) {
	cyc := `{"subject":"X","nodes":[{"title":"A","summary":"a","requires":["B"]},{"title":"B","summary":"b","requires":["A"]}]}`
	f := &fake{replies: []provider.Response{jsonReply(scopeReply), jsonReply(cyc)}}
	s, _ := newSession(t, f, "x\ny\n")
	if _, err := s.Roadmap(context.Background(), "X", RoadmapOpts{Reach: 1}); err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("expected a circular prerequisites error, got %v", err)
	}
	if notes, _ := s.V.List(); len(notes) != 0 {
		t.Errorf("a rejected plan must not leave notes behind: %d", len(notes))
	}
}

func TestParseReach(t *testing.T) {
	for in, want := range map[string]int{"1": 1, "3 · Desde cero: x": 3, "": 2, "9": 2, "hola": 2} {
		if got := parseReach(in); got != want {
			t.Errorf("parseReach(%q) = %d, want %d", in, got, want)
		}
	}
}

// newRoadmap builds the Probabilidad roadmap (guide 1 written) with its scope already answered;
// f's own replies come after.
func newRoadmap(t *testing.T, f *fake) *Session {
	t.Helper()
	f.replies = append([]provider.Response{jsonReply(scopeReply), jsonReply(planReply), reply(prepReply, 10)}, f.replies...)
	s, _ := newSession(t, f, "a\nb\n")
	if _, err := s.Roadmap(context.Background(), "Probabilidad", RoadmapOpts{Reach: 2}); err != nil {
		t.Fatal(err)
	}
	return s
}

const quizGen = "```json\n" + `{"questions":[{"id":1,"type":"open","question":"¿Por qué?","answer":"porque"}]}` + "\n```"

func quizGrade(score int) provider.Response {
	return jsonReply(fmt.Sprintf(`{"score":%d,"results":[{"id":1,"correct":true,"feedback":"ok"}],"gaps":[]}`, score))
}

func TestRoadmapUnlocksTheNextGuideOnPassing(t *testing.T) {
	f := &fake{replies: []provider.Response{reply(quizGen, 10), quizGrade(50), reply(quizGen, 10), quizGrade(85), reply(prepReply, 10)}}
	s := newRoadmap(t, f)
	var out strings.Builder
	s.Out = &out

	// a later topic is locked until the one before it is passed
	if _, err := s.Guide(context.Background(), GuideReq{Topic: "Bayes"}); !IsInputError(err) || !strings.Contains(err.Error(), "Probabilidad condicional") {
		t.Fatalf("Bayes must be locked behind topic 1, got %v", err)
	}

	// under the passing score: nothing is written
	s.In = newReader("no sé\n")
	if err := s.Quiz(context.Background(), "probabilidad-condicional", 1, "media"); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.V.Load("bayes"); hasGuide(b) {
		t.Fatal("a failed topic must not unlock the next guide")
	}
	if !strings.Contains(out.String(), "necesitas 70/100") {
		t.Errorf("the learner must be told what unlocks the next topic:\n%s", out.String())
	}

	// passing writes the next guide, and only that one
	s.In = newReader("porque\n")
	if err := s.Quiz(context.Background(), "probabilidad-condicional", 1, "media"); err != nil {
		t.Fatal(err)
	}
	b, _ := s.V.Load("bayes")
	inf, _ := s.V.Load("inferencia")
	if !hasGuide(b) || hasGuide(inf) {
		t.Errorf("passing topic 1 must write topic 2 only: bayes=%v inferencia=%v", hasGuide(b), hasGuide(inf))
	}
	if !strings.Contains(out.String(), "Desbloqueaste el tema 2") {
		t.Errorf("output: %s", out.String())
	}
	if len(f.replies) != 0 {
		t.Errorf("%d replies left unused", len(f.replies))
	}
}

func TestPassScoreIsConfigurable(t *testing.T) {
	f := &fake{replies: []provider.Response{reply(quizGen, 10), quizGrade(50), reply(prepReply, 10)}}
	s := newRoadmap(t, f)
	s.Cfg.PassScore = 50
	s.In = newReader("x\n")
	if err := s.Quiz(context.Background(), "probabilidad-condicional", 1, "media"); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.V.Load("bayes"); !hasGuide(b) {
		t.Error("with 50 to pass, a 50 unlocks the next topic")
	}
}

func TestNextSuggestsTheCurrentTopic(t *testing.T) {
	s := newRoadmap(t, &fake{})
	s.In = newReader("n\n") // "n" at the confirmation: only the suggestion is printed
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
	// the UI's button opens the guide already written, without asking the model
	slug, err := s.NextGuide(context.Background(), "probabilidad")
	if err != nil || slug != "probabilidad-condicional" {
		t.Errorf("NextGuide = %q, %v", slug, err)
	}
}

func TestRoadmapCardInTheWeb(t *testing.T) {
	f := &fake{replies: []provider.Response{jsonReply(scopeReply), jsonReply(planReply), reply(prepReply, 10)}}
	s, out := newSession(t, f, "a\nb\n2 · Con bases\n")
	var cards []Card
	s.Card = func(c Card) { cards = append(cards, c) }
	if _, err := s.Roadmap(context.Background(), "Probabilidad", RoadmapOpts{}); err != nil {
		t.Fatal(err)
	}
	var rm *Card
	for i, c := range cards {
		if c.Kind == CardQuestion && len(c.Choices) == 3 && !strings.HasPrefix(c.Choices[1], "2") {
			t.Errorf("each reach choice starts with its value: %v", c.Choices)
		}
		if c.Kind == CardRoadmap {
			rm = &cards[i]
		}
	}
	if rm == nil || len(rm.Steps) != 3 || rm.Steps[0].State != "current" || rm.Steps[1].State != "locked" || rm.Score != 70 {
		t.Fatalf("roadmap card = %+v", rm)
	}
	if strings.Contains(out.String(), "(3 temas)") {
		t.Errorf("the card replaces the text list:\n%s", out.String())
	}
	if last := cards[len(cards)-1]; last.Kind != CardGuide || last.Slug != "probabilidad-condicional" {
		t.Errorf("the run must end on the first guide: %+v", last)
	}
}

func TestMaterialBuildsRoadmapAndGroundsTheFirstGuide(t *testing.T) {
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
	rslug, err := s.Material(context.Background(), []string{path}, RoadmapOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if rslug != "apuntes-de-estadística" {
		t.Fatalf("slug = %q", rslug)
	}
	if p := f.reqs[0].Prompt; !strings.Contains(p, "SOURCE MATERIAL OUTLINE") || !strings.Contains(p, "Cadenas de Markov") || !strings.Contains(p, "Cover the whole material") || !strings.Contains(p, "ROADMAP DEPTH: 1") {
		t.Errorf("planning prompt wrong:\n%s", p)
	}
	n, _ := s.V.Load("cadenas-de-markov")
	if n.Meta.Material != "apuntes-de-estadística" || n.Meta.Source != "material" || hasGuide(n) {
		t.Errorf("topic 2 must be a stub from the material: %+v", n.Meta)
	}

	prep := f.reqs[1].Prompt
	if !strings.Contains(prep, "SOURCE MATERIAL") || !strings.Contains(prep, "Es la base de todo") || !strings.Contains(prep, "Entender P(A|B)") {
		t.Errorf("the first guide must be grounded on its excerpt and carry its objective:\n%s", prep)
	}
	first, _ := s.V.Load("probabilidad-condicional")
	if first.Meta.Source != "material" || first.Meta.Roadmap != rslug || !strings.Contains(first.Body, "## Conexiones") || !strings.Contains(first.Body, "## Guía") {
		t.Errorf("the first guide must keep the roadmap data:\n%+v\n%s", first.Meta, first.Body)
	}
	if _, err := s.Prepare(context.Background(), "Cadenas de Markov", "ignored"); !IsInputError(err) {
		t.Errorf("topic 2 is locked until topic 1 is passed, got %v", err)
	}
}
