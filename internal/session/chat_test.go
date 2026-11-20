package session

import (
	"context"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// streamer replies with fixed text, delivered in small chunks like a real backend.
type streamer struct {
	text string
	reqs []provider.Request
}

func (f *streamer) Name() string { return "streamer" }
func (f *streamer) Complete(_ context.Context, req provider.Request, onText func(string)) (provider.Response, error) {
	f.reqs = append(f.reqs, req)
	for i := 0; i < len(f.text); i += 3 {
		if onText != nil {
			onText(f.text[i:min(i+3, len(f.text))])
		}
	}
	return provider.Response{Text: f.text, SessionID: "x"}, nil
}

func TestChatStreamsTheAnswerAndHoldsBackTheAction(t *testing.T) {
	f := &streamer{text: "Es 4, porque `2+2` suma.\n\n```json\n{\"action\":\"none\",\"save\":true}\n```"}
	s, out := newSession(t, nil, `{"text":"¿Cuánto es 2+2?","guide":"bayes","depth":3}`+"\n")
	s.P = f
	if err := s.V.Save(vault.Note{Slug: "bayes", Meta: vault.Meta{Title: "Bayes"}, Body: "## Guía\n\nTexto.\n"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Chat(context.Background(), ChatOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "Es 4, porque `2+2` suma.") || strings.Contains(got, "json") || strings.Contains(got, "action") {
		t.Errorf("streamed output: %q", got)
	}
	p := f.reqs[0].Prompt
	if !strings.Contains(p, "ACTIVE GUIDE: Bayes (id bayes)") || !strings.Contains(p, "DEPTH: 3") || !strings.Contains(p, "¿Cuánto es 2+2?") {
		t.Errorf("chat prompt:\n%s", p)
	}
	n, _ := s.V.Load("bayes")
	if !strings.Contains(vault.Section(n.Body, "Dudas"), "### ¿Cuánto es 2+2?") {
		t.Errorf("doubt not saved:\n%s", n.Body)
	}
}

func TestChatRunsTheGuideAction(t *testing.T) {
	f := &fake{replies: []provider.Response{
		reply("Armo el plan.\n\n```json\n{\"action\":\"guide\",\"topic\":\"Fourier\",\"focus\":\"audio\",\"depth\":0}\n```", 10),
		reply("Guía de Fourier.\n\n```json\n{\"resources\":[],\"handoff\":\"h\"}\n```", 10),
		reply("Respuesta sobre la guía.", 10),
	}}
	s, _ := newSession(t, f, "{\"text\":\"Quiero entender Fourier para audio\"}\n{\"text\":\"¿y la fase?\"}\n")
	var cards []Card
	s.Card = func(c Card) { cards = append(cards, c) }
	if err := s.Chat(context.Background(), ChatOpts{}); err != nil {
		t.Fatal(err)
	}
	g, err := s.V.Load("fourier")
	if err != nil || g.Meta.Focus != "audio" || !strings.Contains(g.Body, "Guía de Fourier.") {
		t.Fatalf("guide: %v %+v", err, g.Meta)
	}
	if !strings.Contains(f.reqs[1].Prompt, "FOCUS: audio") {
		t.Errorf("guide prompt lacks the focus:\n%s", f.reqs[1].Prompt)
	}
	// the next message is about the new guide
	if !strings.Contains(f.reqs[2].Prompt, "ACTIVE GUIDE: Fourier (id fourier)") {
		t.Errorf("the chat must follow the guide it just wrote:\n%s", f.reqs[2].Prompt)
	}
	found := false
	for _, c := range cards {
		found = found || (c.Kind == CardGuide && c.Slug == "fourier")
	}
	if !found {
		t.Errorf("no guide card: %+v", cards)
	}
}

func TestChatButtonsSkipTheModelAndFailSoftly(t *testing.T) {
	f := &fake{}
	s, out := newSession(t, f, `{"action":{"action":"quiz","n":3}}`+"\n")
	if err := s.Chat(context.Background(), ChatOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 0 || !strings.Contains(out.String(), "primero abre o genera una guía") {
		t.Errorf("a quiz without a guide must be refused without calling the model: %d %q", len(f.reqs), out.String())
	}
}

func TestChatResumesWithItsHistory(t *testing.T) {
	f := &fake{replies: []provider.Response{reply("Sigo.", 10)}}
	s, _ := newSession(t, f, "")
	first := ChatMsg{Text: "continúa"}
	if err := s.Chat(context.Background(), ChatOpts{First: &first, History: "Learner: hola\n\nStudyHomie: qué tal"}); err != nil {
		t.Fatal(err)
	}
	if p := f.reqs[0].Prompt; !strings.HasPrefix(p, "CONVERSATION SO FAR:\nLearner: hola") || !strings.Contains(p, "continúa") {
		t.Errorf("resumed prompt:\n%s", p)
	}
}
