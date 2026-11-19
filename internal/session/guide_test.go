package session

import (
	"context"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func noVault(string) (string, bool) { return "", false }

const fourierPlan = `{"title":"Transformada de Fourier","requires":["Números complejos","Integral","Función seno"],"concepts":[
 {"id":"numeros-complejos","title":"Números complejos","summary":"z = a + bi","category":"Álgebra","requires":["plano-cartesiano"]},
 {"id":"integral","title":"Integral","summary":"área bajo la curva","category":"calculo","requires":["funcion","limite"]},
 {"id":"funcion-seno","title":"Función seno","summary":"oscila","category":"trigonometria","requires":["funcion","integral"]},
 {"id":"plano-cartesiano","title":"Plano cartesiano","requires":["numeros-reales"]},
 {"id":"funcion","title":"Función","requires":[]},
 {"id":"limite","title":"Límite","requires":["funcion","integral"]},
 {"id":"numeros-reales","title":"Números reales","requires":[]},
 {"id":"fantasma","title":"Nadie lo pide","requires":[]}
]}`

func planFrom(t *testing.T, js string, depth int, vt func(string) (string, bool)) ConceptPlan {
	t.Helper()
	var raw rawPlan
	if err := decodeJSON(js, &raw); err != nil {
		t.Fatal(err)
	}
	return normalizePlan(raw, "Transformada de Fourier", depth, vt)
}

func ids(p ConceptPlan) map[string]int {
	m := map[string]int{}
	for _, c := range p.Concepts {
		m[c.ID] = c.Level
	}
	return m
}

func TestPlanCutsAtDepthAndDropsUnreachable(t *testing.T) {
	p := planFrom(t, fourierPlan, 1, noVault)
	got := ids(p)
	if len(got) != 3 || got["numeros-complejos"] != 1 || got["integral"] != 1 || got["funcion-seno"] != 1 {
		t.Fatalf("depth 1 = %v", got)
	}
	for _, c := range p.Concepts {
		if len(c.Requires) > 0 && c.ID != "funcion-seno" {
			t.Errorf("%s keeps requires to concepts cut by depth: %v", c.ID, c.Requires)
		}
	}

	p = planFrom(t, fourierPlan, 2, noVault)
	got = ids(p)
	if got["plano-cartesiano"] != 2 || got["funcion"] != 2 || got["limite"] != 2 || got["numeros-reales"] != 0 {
		t.Errorf("depth 2 = %v", got)
	}
	if _, ok := got["fantasma"]; ok {
		t.Error("a concept nothing requires must be dropped")
	}
	// the cycle integral -> limite -> integral is harmless: each concept keeps its shortest level
	if got["integral"] != 1 {
		t.Errorf("integral level = %d", got["integral"])
	}
	// levels come in order, and the category is normalized
	for i := 1; i < len(p.Concepts); i++ {
		if p.Concepts[i].Level < p.Concepts[i-1].Level {
			t.Errorf("concepts out of level order: %+v", p.Concepts)
		}
	}
	if p.Concepts[0].Category != "algebra" {
		t.Errorf("category = %q", p.Concepts[0].Category)
	}

	if got := ids(planFrom(t, fourierPlan, 4, noVault)); got["numeros-reales"] != 3 {
		t.Errorf("depth 4 should reach the basics: %v", got)
	}
}

func TestPlanReusesVaultConceptsWithoutExpandingThem(t *testing.T) {
	vt := func(id string) (string, bool) {
		if id == "integral" {
			return "Integral definida", true
		}
		return "", false
	}
	p := planFrom(t, fourierPlan, 2, vt)
	got := ids(p)
	if _, ok := got["limite"]; ok {
		t.Errorf("an existing concept's prerequisites are already written: %v", got)
	}
	for _, c := range p.Concepts {
		if c.ID == "integral" && (!c.Exists || c.Title != "Integral definida") {
			t.Errorf("integral = %+v", c)
		}
	}
}

func TestApplyEditSkipsAndAdds(t *testing.T) {
	p := planFrom(t, fourierPlan, 2, noVault)
	e := parseEditLine("-numeros-complejos +Series de Fourier discretas")
	if len(e.Skip) != 1 || len(e.Add) != 1 || e.Add[0] != "Series de Fourier discretas" {
		t.Fatalf("parsed edit = %+v", e)
	}
	p = applyEdit(p, e, noVault)
	got := ids(p)
	if _, ok := got["numeros-complejos"]; ok {
		t.Error("skipped concept is still there")
	}
	if _, ok := got["plano-cartesiano"]; ok {
		t.Error("a concept only the skipped one needed must go too")
	}
	if got["series-de-fourier-discretas"] != 1 || got["funcion"] != 2 {
		t.Errorf("after edit: %v", got)
	}
}

func TestGuideWritesGuideAndConceptNotes(t *testing.T) {
	plan := `{"title":"Bayes","requires":["probabilidad-condicional"],"concepts":[
	 {"id":"probabilidad-condicional","title":"Probabilidad condicional","summary":"P(A|B)","category":"probabilidad","requires":["probabilidad"]},
	 {"id":"probabilidad","title":"Probabilidad","summary":"frecuencia a la larga","category":"probabilidad","requires":[]}]}`
	guide := "Un test da positivo. Ver [[probabilidad-condicional|condicional]].\n\n```viz\n" +
		`{"type":"plot","params":[{"name":"p","min":0,"max":1,"value":0.5}],"curves":[{"expr":"p*x"}]}` +
		"\n```\n\n```viz\n{\"type\":\"plot\",\"curves\":[{\"expr\":\"zz\"}]}\n```\n\n```json\n{\"resources\":[],\"handoff\":\"h\"}\n```"
	concept := func(title string) provider.Response {
		return reply("## En una frase\n\n"+title+" es algo.\n\n## Relacionado\n\n[[probabilidad|Probabilidad]]\n\n```json\n{\"aliases\":[\"X\"],\"tags\":[\"prob\"]}\n```", 10)
	}
	f := &fake{replies: []provider.Response{jsonReply(plan), reply(guide, 10), concept("Condicional")}}
	// "probabilidad" is already in the vault: it is linked, not written again
	s, out := newSession(t, f, "s\n")
	s.Cfg.Depth = 2
	if err := s.V.SaveConcept(vault.Concept{Slug: "probabilidad", Meta: vault.ConceptMeta{Title: "Probabilidad"}, Body: "# Probabilidad\n\n## Aparece en\n\n- [[otra|Otra]]\n"}); err != nil {
		t.Fatal(err)
	}
	var cards []Card
	s.Card = func(c Card) { cards = append(cards, c) }

	slug, err := s.Prepare(context.Background(), "Bayes", "adhoc")
	if err != nil || slug != "bayes" {
		t.Fatalf("prepare: %q %v\n%s", slug, err, out)
	}
	if len(f.reqs) != 3 {
		t.Fatalf("requests = %d (plan, guide, one new concept)", len(f.reqs))
	}
	if !strings.Contains(f.reqs[0].Prompt, "DEPTH: 2") || !strings.Contains(f.reqs[0].Prompt, "probabilidad: Probabilidad") {
		t.Errorf("plan prompt lacks depth or existing concepts:\n%s", f.reqs[0].Prompt)
	}
	if !strings.Contains(f.reqs[1].Prompt, "CONCEPT NOTES:\nprobabilidad-condicional: Probabilidad condicional") {
		t.Errorf("guide prompt lacks the concept list:\n%s", f.reqs[1].Prompt)
	}

	g, err := s.V.Load("bayes")
	if err != nil {
		t.Fatal(err)
	}
	if g.Meta.Depth != 2 || strings.Join(g.Meta.Concepts, ",") != "probabilidad-condicional,probabilidad" || strings.Join(g.Meta.Base, ",") != "probabilidad-condicional" {
		t.Errorf("guide meta = %+v", g.Meta)
	}
	if strings.Count(g.Body, "```viz") != 1 || !strings.Contains(out.String(), "variable desconocida") {
		t.Errorf("the broken widget must be dropped and reported:\n%s\n%s", g.Body, out)
	}

	c, err := s.V.LoadConcept("probabilidad-condicional")
	if err != nil {
		t.Fatal(err)
	}
	if c.Meta.Category != "probabilidad" || strings.Join(c.Meta.Requires, ",") != "probabilidad" || c.Meta.Guides[0] != "bayes" || c.Meta.Aliases[0] != "X" {
		t.Errorf("concept meta = %+v", c.Meta)
	}
	if !strings.Contains(c.Body, "## Aparece en\n\n- [[bayes|Bayes]]") {
		t.Errorf("concept body:\n%s", c.Body)
	}
	old, _ := s.V.LoadConcept("probabilidad")
	if strings.Join(old.Meta.Guides, ",") != "bayes" || !strings.Contains(old.Body, "- [[otra|Otra]]\n- [[bayes|Bayes]]") {
		t.Errorf("existing concept not linked:\n%+v\n%s", old.Meta, old.Body)
	}

	kinds := map[string]int{}
	for _, c := range cards {
		kinds[c.Kind]++
	}
	if kinds[CardPlan] != 1 || kinds[CardProgress] < 3 || kinds[CardGuide] != 1 {
		t.Errorf("cards = %v", kinds)
	}
	last := Card{}
	for _, c := range cards {
		if c.Kind == CardProgress {
			last = c
		}
	}
	for _, it := range last.Items {
		if it.State != "done" && it.State != "linked" {
			t.Errorf("progress left unfinished: %+v", last.Items)
		}
	}
}

func TestGuideStopsWhenThePlanIsRejected(t *testing.T) {
	plan := `{"title":"X","requires":["a"],"concepts":[{"id":"a","title":"A","requires":[]}]}`
	f := &fake{replies: []provider.Response{jsonReply(plan)}}
	s, _ := newSession(t, f, "n\n")
	s.Cfg.Depth = 1
	slug, err := s.Prepare(context.Background(), "X", "adhoc")
	if err != nil || slug != "" || s.V.Exists("x") || len(f.reqs) != 1 {
		t.Errorf("a rejected plan writes nothing: %q %v %d", slug, err, len(f.reqs))
	}
}

func TestConceptIDFoldsAccents(t *testing.T) {
	if got := vault.ConceptID("Estandarización (Z-score)"); got != "estandarizacion-z-score" {
		t.Errorf("got %q", got)
	}
}
