package web

import (
	"testing"

	"github.com/krilinxito/studyhomie/internal/session"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestChatParamsCaptureSelectedOptions(t *testing.T) {
	s, _ := newTestServer(t, &scripted{})
	if err := s.V.Save(vault.Note{
		Slug: "fourier",
		Meta: vault.Meta{Title: "Transformada de Fourier", Status: "prepared"},
		Body: "# Transformada de Fourier\n\n## Guía\n\nTexto.\n",
	}); err != nil {
		t.Fatal(err)
	}

	d2 := 2
	params := s.chatParams(chatBody{
		Text:  "Explícame convolución",
		Depth: &d2,
		Guide: "fourier",
		Files: []string{"abc123/apuntes.pdf"},
	})
	if len(params) != 3 {
		t.Fatalf("got %d params (%+v), want 3", len(params), params)
	}
	if params[0].Text != "2 niveles" || params[1].Text != "Transformada de Fourier" || params[2].Text != "apuntes.pdf" {
		t.Fatalf("unexpected params: %+v", params)
	}

	quizParams := s.chatParams(chatBody{
		Guide:  "fourier",
		Action: &session.ChatAction{Action: "quiz", N: 5, Difficulty: "avanzada"},
	})
	if len(quizParams) != 3 || quizParams[0].Text != "5 preguntas" || quizParams[1].Text != "avanzada" || quizParams[2].Text != "Transformada de Fourier" {
		t.Fatalf("unexpected quiz params: %+v", quizParams)
	}
}
