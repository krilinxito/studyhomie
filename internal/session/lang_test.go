package session

import (
	"context"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/i18n"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// In English the session talks in English, asks the model to write English, and writes the note's
// sections with English headings; a note written in Spanish keeps working.
func TestSessionInEnglish(t *testing.T) {
	gen := "```json\n" + `{"questions":[{"id":1,"type":"open","question":"Why?","answer":"because"}]}` + "\n```"
	grade := "```json\n" + `{"score":80,"results":[{"id":1,"correct":true,"feedback":"good"}],"gaps":["priors"]}` + "\n```"
	f := &fake{replies: []provider.Response{reply(prepReply, 10), reply(gen, 10), reply(grade, 10)}}
	s, out := newSession(t, f, "because\n")
	s.Cfg.Language = "en"
	slug, err := s.Prepare(context.Background(), "Bayes", "adhoc")
	if err != nil {
		t.Fatal(err)
	}
	if sys := f.reqs[0].System; !strings.Contains(sys, "English") || strings.Contains(sys, "{{language}}") {
		t.Errorf("the guide prompt must ask for English:\n%s", head(sys, 300))
	}
	if err := s.Quiz(context.Background(), slug, 1, "media"); err != nil {
		t.Fatal(err)
	}
	n, _ := s.V.Load(slug)
	for _, want := range []string{"## Guide\n", "## Resources\n", "## Quiz\n", "Score: **80/100** (difficulty: medium)", "Your answer:", "To review:"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("note missing %q:\n%s", want, n.Body)
		}
	}
	if vault.Section(n.Body, "Guía") == "" {
		t.Error("an English section is found by its Spanish name")
	}
	if !strings.Contains(out.String(), "Generating the quiz...") {
		t.Errorf("the terminal must speak English:\n%s", out.String())
	}

	// a Spanish note gets its sections updated, not duplicated, by an English session
	es := vault.Note{Slug: "vieja", Meta: vault.Meta{Title: "Vieja"}, Body: "## Guía\n\nTexto.\n\n## Dudas\n\n### ¿Por qué?\n\nPorque.\n"}
	if err := s.V.Save(es); err != nil {
		t.Fatal(err)
	}
	s.saveDoubt("vieja", "Why not?", "Because.")
	n, _ = s.V.Load("vieja")
	sections := 0
	for _, l := range strings.Split(n.Body, "\n") {
		if strings.HasPrefix(l, "## ") {
			sections++
		}
	}
	if sections != 2 || !strings.Contains(n.Body, "Porque.") || !strings.Contains(n.Body, "Because.") {
		t.Errorf("the doubts section must be reused across languages:\n%s", n.Body)
	}
}

func TestErrorsAreShownInTheReadersLanguage(t *testing.T) {
	err := inputErrorf("no existe la guía «%s»", "x")
	if !IsInputError(err) || err.Error() != "no existe la guía «x»" {
		t.Errorf("Spanish is the default: %v", err)
	}
	if i18n.Catalog("en") != nil {
		if got := ErrorText(err, "en"); got != "the guide “x” does not exist" {
			t.Errorf("ErrorText(en) = %q", got)
		}
	}
	wrapped := failf("no hay guía para «%s»; ejecuta primero la preparación: %w", "y", inputErrorf("no existe el tema «%s»", "y"))
	if got := ErrorText(wrapped, "en"); !strings.Contains(got, "prepare it first") || !strings.Contains(got, "the topic “y” does not exist") {
		t.Errorf("a wrapped error is translated too: %q", got)
	}
	if IsInputError(wrapped) {
		t.Error("a failure that wraps an input error is still a failure at its top")
	}
}

func TestLabelsKeepTheirYesNoEnding(t *testing.T) {
	if got := Label("en", "¿Empezamos?"+yesNo); got != "Shall we start? (y/n)" {
		t.Errorf("Label = %q", got)
	}
	if got := Label("es", "¿Empezamos?"+yesNo); got != "¿Empezamos? (s/n)" {
		t.Errorf("Label = %q", got)
	}
	for _, a := range []string{"y", "Yes", "oui", "sim", "s", "是"} {
		if !yes(a) {
			t.Errorf("%q means yes", a)
		}
	}
}
