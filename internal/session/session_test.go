package session

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// fake replies from a script and records every request.
type fake struct {
	replies []provider.Response
	reqs    []provider.Request
}

func (f *fake) Name() string { return "fake" }
func (f *fake) Complete(_ context.Context, req provider.Request, _ func(string)) (provider.Response, error) {
	f.reqs = append(f.reqs, req)
	r := f.replies[0]
	f.replies = f.replies[1:]
	if r.SessionID == "" {
		r.SessionID = "s" + string(rune('0'+len(f.reqs)))
	}
	return r, nil
}

func reply(text string, tokens int) provider.Response {
	return provider.Response{Text: text, Usage: provider.Usage{InputTokens: tokens}}
}

func TestConvoCompactsWhenOverThreshold(t *testing.T) {
	f := &fake{replies: []provider.Response{
		reply("one", 700), // over 60% of 1000 -> compaction follows
		reply("SUMMARY", 50),
		reply("two", 100),
	}}
	c := &Convo{P: f, System: "sys", Language: "es", Window: 1000, CompactAt: 0.6}
	if _, err := c.Say(context.Background(), "a", nil); err != nil {
		t.Fatal(err)
	}
	if c.Compactions != 1 {
		t.Fatalf("compactions = %d, want 1", c.Compactions)
	}
	if _, err := c.Say(context.Background(), "b", nil); err != nil {
		t.Fatal(err)
	}
	// compaction call resumes the old session; the next turn starts fresh with the summary
	if f.reqs[1].SessionID == "" {
		t.Error("compaction must resume the existing session")
	}
	last := f.reqs[2]
	if last.SessionID != "" {
		t.Error("turn after compaction must start a fresh session")
	}
	if !strings.Contains(last.Prompt, "SUMMARY") || !strings.Contains(last.Prompt, "b") || last.System != "sys" {
		t.Errorf("fresh turn must carry system prompt + summary + new input: %+v", last)
	}
}

func TestConvoNoCompactionUnderThreshold(t *testing.T) {
	f := &fake{replies: []provider.Response{reply("one", 100), reply("two", 100)}}
	c := &Convo{P: f, Window: 1000, CompactAt: 0.6}
	c.Say(context.Background(), "a", nil)
	c.Say(context.Background(), "b", nil)
	if c.Compactions != 0 || f.reqs[1].SessionID == "" {
		t.Errorf("should keep resuming: compactions=%d req=%+v", c.Compactions, f.reqs[1])
	}
}

func TestSplitJSONAndScore(t *testing.T) {
	text, payload := splitJSON("guía **aquí**\n\n```json\n{\"a\":1}\n```")
	if text != "guía **aquí**" || payload != `{"a":1}` {
		t.Errorf("got %q / %q", text, payload)
	}
	s, body, ok := extractScore("Bien.\nSCORE: 85")
	if !ok || s != 85 || body != "Bien." {
		t.Errorf("got %d %q %v", s, body, ok)
	}
	if _, _, ok := extractScore("sin puntaje"); ok {
		t.Error("expected no score")
	}
}

func newSession(t *testing.T, f *fake, input string) (*Session, *bytes.Buffer) {
	t.Helper()
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cfg := config.Default()
	cfg.Language = "español"
	cfg.Depth = 0 // a guide without concept notes unless a test asks for them
	cfg.Parallel = 1
	cfg.Iceberg = false
	return &Session{
		P: f, V: v, Cfg: cfg, In: bufio.NewReader(strings.NewReader(input)), Out: &out,
		CheckLinks: func(_ context.Context, urls []string) map[string]bool {
			m := map[string]bool{}
			for _, u := range urls {
				m[u] = !strings.Contains(u, "dead")
			}
			return m
		},
	}, &out
}

const prepReply = "La intuición es X, ver [[probabilidad]].\n\n```json\n" + `{
 "resources": [
  {"type":"video","title":"Good","url":"https://ok.example/v","why":"visual"},
  {"type":"web","title":"Broken","url":"https://dead.example/x"},
  {"type":"book","title":"A Book","why":"classic"}
 ],
 "handoff": "resumen corto"
}` + "\n```"

func TestPrepareSavesGuideAndDropsDeadLinks(t *testing.T) {
	f := &fake{replies: []provider.Response{reply(prepReply, 100)}}
	s, _ := newSession(t, f, "")
	slug, err := s.Prepare(context.Background(), "Teorema de Bayes", "adhoc")
	if err != nil {
		t.Fatal(err)
	}
	if slug != "teorema-de-bayes" {
		t.Fatalf("slug = %q", slug)
	}
	n, err := s.V.Load(slug)
	if err != nil {
		t.Fatal(err)
	}
	// external material becomes notes of fuentes/, cited from the guide with why each one helps
	if len(n.Meta.Resources) != 0 || len(n.Meta.Sources) != 2 || n.Meta.Sources[0].ID != "good" || n.Meta.Sources[1].ID != "a-book" || n.Meta.Sources[0].Why != "visual" {
		t.Errorf("sources = %+v, resources = %+v", n.Meta.Sources, n.Meta.Resources)
	}
	if src, err := s.V.LoadSource("good"); err != nil || src.Meta.URL != "https://ok.example/v" {
		t.Errorf("source note: %+v %v", src.Meta, err)
	}
	if !strings.Contains(n.Body, "## Guía") || !strings.Contains(n.Body, "[[fuentes/good|Good]] — visual") || strings.Contains(n.Body, "dead.example") {
		t.Errorf("body wrong:\n%s", n.Body)
	}
	if got := s.V.LoadHandoff(slug); got != "resumen corto" {
		t.Errorf("handoff = %q", got)
	}
	if links := n.Links(); len(links) != 1 || links[0] != "probabilidad" {
		t.Errorf("links = %v", links)
	}
	if f.reqs[0].Tools[0] != "WebSearch" {
		t.Errorf("prepare must allow web search: %v", f.reqs[0].Tools)
	}
}

func TestQuizFlow(t *testing.T) {
	gen := "```json\n" + `{"questions":[{"id":1,"type":"open","question":"¿Por qué?","answer":"porque"},{"id":2,"type":"mcq","question":"¿Cuál?","choices":["A) x","B) y"],"answer":"A"}]}` + "\n```"
	grade := "```json\n" + `{"score":75,"results":[{"id":1,"correct":true,"feedback":"bien"},{"id":2,"correct":false,"feedback":"era A"}],"gaps":["probabilidad condicional"]}` + "\n```"
	f := &fake{replies: []provider.Response{reply(prepReply, 10), reply(gen, 10), reply(grade, 10)}}
	s, _ := newSession(t, f, "porque sí\nB\n")
	slug, _ := s.Prepare(context.Background(), "Bayes", "adhoc")
	if err := s.Quiz(context.Background(), slug, 2, "media"); err != nil {
		t.Fatal(err)
	}
	n, _ := s.V.Load(slug)
	if n.Meta.Status != "tested" || n.Meta.Mastery != 75 || !strings.Contains(n.Body, "## Prueba") || !strings.Contains(n.Body, "era A") {
		t.Errorf("quiz not saved: %+v\n%s", n.Meta, n.Body)
	}
	if !strings.Contains(s.V.LoadHandoff(slug), "probabilidad condicional") {
		t.Error("gaps must be added to the handoff")
	}
	// the grading call must see the learner's answers, in a fresh session (no history)
	if g := f.reqs[2]; !strings.Contains(g.Prompt, "porque sí") || g.SessionID != "" {
		t.Errorf("grading request wrong: %+v", g)
	}
}

func TestFeynmanFlow(t *testing.T) {
	f := &fake{replies: []provider.Response{
		reply(prepReply, 10),
		reply("Bien, pero ¿por qué?", 10),
		reply("Buen trabajo.\nSCORE: 80", 10),
	}}
	s, out := newSession(t, f, "Es como un filtro.\n.\nMe falta la prior.\n/fin\n")
	slug, _ := s.Prepare(context.Background(), "Bayes", "adhoc")
	if err := s.Feynman(context.Background(), slug); err != nil {
		t.Fatal(err)
	}
	n, _ := s.V.Load(slug)
	if n.Meta.Status != "explained" || n.Meta.Mastery != 80 || !strings.Contains(n.Body, "## Feynman") || !strings.Contains(n.Body, "Es como un filtro.") {
		t.Errorf("feynman not saved: %+v\n%s", n.Meta, n.Body)
	}
	if !strings.Contains(f.reqs[1].Prompt, "resumen corto") {
		t.Error("first Feynman turn must carry the handoff summary")
	}
	if f.reqs[2].SessionID == "" {
		t.Error("second round must continue the same phase session")
	}
	if !strings.Contains(out.String(), "Puntaje Feynman: 80/100") {
		t.Errorf("output: %s", out.String())
	}
}

// In the web app the transcript is shown as chat: terminal conventions («.», /fin), file paths
// and technical notices must not leak into it.
func TestWebSessionHidesTerminalText(t *testing.T) {
	f := &fake{replies: []provider.Response{
		reply(prepReply, 10),
		reply("Bien, pero ¿por qué?", 10),
		reply("Buen trabajo.\nSCORE: 80", 10),
	}}
	s, out := newSession(t, f, "Es como un filtro.\n.\n/fin\n")
	s.Card = func(Card) {}
	slug, err := s.Prepare(context.Background(), "Bayes", "adhoc")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Feynman(context.Background(), slug); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, bad := range []string{"«.»", "/fin", s.V.Root, "Guardado en", "Descarté", "Aviso"} {
		if strings.Contains(got, bad) {
			t.Errorf("web transcript leaks %q:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "Terminar y evaluar") {
		t.Errorf("web transcript should point at the finish button:\n%s", got)
	}
}

// A Feynman resumed after a pause carries its rounds to the model; closing it early evaluates what
// was explained and leaves mastery alone.
func TestFeynmanResumedThenFinishedEarly(t *testing.T) {
	f := &fake{replies: []provider.Response{
		reply(prepReply, 10),
		reply("¿Y la prior?", 10),
		reply("Ibas bien.\nSCORE: 60", 10),
	}}
	s, _ := newSession(t, f, "La prior es lo que creía antes.\n.\n"+FinishNow+"\n")
	slug, _ := s.Prepare(context.Background(), "Bayes", "adhoc")
	var saved []Practice
	s.Checkpoint = func(p Practice) { saved = append(saved, p) }
	p := &Practice{Kind: PracticeFeynman, Slug: slug, Turns: []FeynmanTurn{{Learner: "Es como un filtro.", Reply: "Bien, pero ¿por qué?"}}}
	if err := s.FeynmanFrom(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.reqs[1].Prompt, "CONVERSATION SO FAR") || !strings.Contains(f.reqs[1].Prompt, "Es como un filtro.") {
		t.Errorf("a resumed Feynman must carry its rounds: %s", f.reqs[1].Prompt)
	}
	if len(saved) != 1 || len(saved[0].Turns) != 2 {
		t.Errorf("checkpoints: %+v", saved)
	}
	if p.Outcome != OutcomePartial || p.Score != 60 {
		t.Errorf("outcome %q score %d", p.Outcome, p.Score)
	}
	n, _ := s.V.Load(slug)
	if n.Meta.Status != "prepared" || n.Meta.Mastery != 0 || !strings.Contains(n.Body, "La prior es lo que creía antes.") {
		t.Errorf("an incomplete Feynman must keep mastery: %+v\n%s", n.Meta, n.Body)
	}
}
