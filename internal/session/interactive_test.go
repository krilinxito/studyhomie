package session

import (
	"context"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
)

const goodHTML = "<!doctype html><html><body><input type=range id=p><canvas id=c></canvas><script>const p=document.getElementById('p');p.oninput=()=>{};</script></body></html>"

func htmlReply(h string) provider.Response { return reply("```html\n"+h+"\n```", 10) }

func TestValidateInteractive(t *testing.T) {
	bad := map[string]string{
		"cdn script":   `<html><script src="https://cdn.example/lib.js"></script></html>`,
		"protocol rel": `<html><script src="//cdn.example/lib.js"></script></html>`,
		"fetch":        `<html><script>fetch('/x')</script></html>`,
		"import":       `<html><style>@import 'a.css';</style><script></script></html>`,
		"storage":      `<html><script>localStorage.x=1</script></html>`,
		"no script":    `<html><body>hola</body></html>`,
		"truncated":    `<html><script>var a=1;`,
		"huge":         `<html><script>` + strings.Repeat("a", maxInteractiveBytes) + `</script></html>`,
	}
	for name, h := range bad {
		if err := validateInteractive(h); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
	if err := validateInteractive(goodHTML); err != nil {
		t.Errorf("good page rejected: %v", err)
	}
}

func TestExtractHTML(t *testing.T) {
	if got := extractHTML("Aquí va:\n```html\n<html>x</html>\n```\nlisto"); got != "<html>x</html>" {
		t.Errorf("got %q", got)
	}
	if got := extractHTML("  <html>raw</html> "); got != "<html>raw</html>" {
		t.Errorf("got %q", got)
	}
}

func TestInteractiveRetriesAfterInvalidPageAndLinksIt(t *testing.T) {
	cdn := `<html><script src="https://cdn.example/x.js"></script></html>`
	f := &fake{replies: []provider.Response{reply(prepReply, 10), htmlReply(cdn), htmlReply(goodHTML)}}
	s, _ := newSession(t, f, "")
	slug, _ := s.Prepare(context.Background(), "Bayes", "adhoc")
	if err := s.Interactive(context.Background(), slug, "mover la prior"); err != nil {
		t.Fatal(err)
	}
	if p := f.reqs[2].Prompt; !strings.Contains(p, "rejected") || f.reqs[2].SessionID == "" {
		t.Errorf("retry must explain the rejection inside the same conversation: %+v", f.reqs[2])
	}
	if h, err := s.V.LoadInteractive(slug); err != nil || h != goodHTML {
		t.Errorf("saved page = %q, %v", h, err)
	}
	n, _ := s.V.Load(slug)
	got := localInteractive(n.Meta.Resources)
	if len(got) != 1 || got[0].URL != "interactive/bayes.html" {
		t.Fatalf("resources = %+v", n.Meta.Resources)
	}
	if !strings.Contains(n.Body, "](../interactive/bayes.html)") {
		t.Errorf("body must link the simulation relative to topics/:\n%s", n.Body)
	}
}

func TestInteractiveGivesUpAfterTwoInvalidPages(t *testing.T) {
	bad := htmlReply(`<html><script>fetch('x')</script></html>`)
	f := &fake{replies: []provider.Response{reply(prepReply, 10), bad, bad}}
	s, _ := newSession(t, f, "")
	slug, _ := s.Prepare(context.Background(), "Bayes", "adhoc")
	if err := s.Interactive(context.Background(), slug, ""); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := s.V.LoadInteractive(slug); err == nil {
		t.Error("an invalid page must not be saved")
	}
}

const prepWithSim = "Guía.\n\n```json\n" + `{"resources":[{"type":"interactive","title":"Sim","why":"mover la prior y ver el posterior"}],"handoff":"h"}` + "\n```"

func TestStudyOffersPendingSimulationAndKeepsItOnRePrepare(t *testing.T) {
	f := &fake{replies: []provider.Response{reply(prepWithSim, 10), htmlReply(goodHTML)}}
	s, out := newSession(t, f, "s\n") // accept the simulation, then EOF at the quiz question
	if err := s.Study(context.Background(), "Bayes", "adhoc"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "mover la prior y ver el posterior") {
		t.Errorf("the proposal must be shown: %s", out.String())
	}
	n, _ := s.V.Load("bayes")
	if len(localInteractive(n.Meta.Resources)) != 1 {
		t.Fatalf("simulation not linked: %+v", n.Meta.Resources)
	}
	for _, r := range n.Meta.Resources {
		if r.Type == "interactive" && r.URL == "" {
			t.Error("the pending proposal must be replaced by the generated page")
		}
	}

	// preparing again: the model proposes a simulation again, but we already have one
	f.replies = []provider.Response{reply(prepWithSim, 10)}
	if _, err := s.Prepare(context.Background(), "Bayes", "adhoc"); err != nil {
		t.Fatal(err)
	}
	n, _ = s.V.Load("bayes")
	count := 0
	for _, r := range n.Meta.Resources {
		if r.Type == "interactive" {
			count++
			if r.URL != "interactive/bayes.html" {
				t.Errorf("unexpected interactive resource %+v", r)
			}
		}
	}
	if count != 1 {
		t.Errorf("want exactly the generated simulation, got %d: %+v", count, n.Meta.Resources)
	}
}

func TestStudyContinuesWhenSimulationFails(t *testing.T) {
	bad := htmlReply(`<html><script>fetch('x')</script></html>`)
	f := &fake{replies: []provider.Response{reply(prepWithSim, 10), bad, bad}}
	s, out := newSession(t, f, "s\n")
	if err := s.Study(context.Background(), "Bayes", "adhoc"); err != nil {
		t.Fatalf("a failed simulation must not abort studying: %v", err)
	}
	if !strings.Contains(out.String(), "No pude generar la simulación") {
		t.Errorf("output: %s", out.String())
	}
}
