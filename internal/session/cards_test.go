package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/cards"
	"github.com/krilinxito/studyhomie/internal/provider"
)

func TestGenerateCardsAndGuideFromAnkiDeckLeavesPendingConcepts(t *testing.T) {
	// 1. Create a guide with attached material file -> copied into vault/material/<slug>/
	tmpSrc := filepath.Join(t.TempDir(), "apuntes-audio.txt")
	if err := os.WriteFile(tmpSrc, []byte("La transformada de Fourier descompone señales."), 0o644); err != nil {
		t.Fatal(err)
	}
	guideReply := "Guía de prueba.\n\n```json\n" + `{"area":"matematicas","resources":[],"handoff":"Fourier descompone frecuencias"}` + "\n```"
	cardsReply := "```json\n" + `{"cards":[
		{"front":"¿Qué hace la transformada de Fourier?","back":"Descompone una señal en frecuencias.","rarity":"common","tags":["fourier"]},
		{"front":"¿Qué pasa si duplicas la frecuencia de muestreo?","back":"Se duplica el límite de Nyquist.","rarity":"legendary","tags":["nyquist"]}
	]}` + "\n```"

	f := &fake{replies: []provider.Response{reply(guideReply, 10), reply(cardsReply, 10)}}
	s, _ := newSession(t, f, "")
	slug, err := s.Guide(context.Background(), GuideReq{Topic: "Fourier", Depth: 0, Files: []string{tmpSrc}})
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.V.Load(slug)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Meta.Materials) != 1 || n.Meta.Materials[0] != "material/fourier/apuntes-audio.txt" {
		t.Fatalf("materials = %v", n.Meta.Materials)
	}
	if !strings.Contains(n.Body, "[[material/fourier/apuntes-audio.txt]]") {
		t.Errorf("guide body missing material link:\n%s", n.Body)
	}
	// Original file must still exist (copied, not moved).
	if _, err := os.Stat(tmpSrc); err != nil {
		t.Errorf("source file should not be moved: %v", err)
	}

	deck, err := s.GenerateCards(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	if len(deck.Cards) != 2 || deck.Cards[1].Rarity != cards.RarityLegendary || deck.Meta.Area != "matematicas" {
		t.Fatalf("generated deck = %+v", deck)
	}

	// 2. GuideFromDeck: imports an Anki deck, generates a guide grounded in it, and leaves
	// prerequisite concepts in pending (under water in the iceberg).
	ankiDeck := cards.Deck{
		Slug: "acustica",
		Meta: cards.DeckMeta{Title: "Acústica musical", Source: "anki"},
		Cards: []cards.Card{
			{ID: "a1", Front: "¿Qué es un armónico?", Back: "Múltiplo entero de la frecuencia fundamental.", Rarity: cards.RarityRare},
		},
	}
	if err := cards.SaveDeck(s.V.Root, ankiDeck); err != nil {
		t.Fatal(err)
	}
	planReply := "```json\n" + `{"title":"Acústica musical","area":"musica","requires":["frecuencia-fundamental"],"concepts":[{"id":"frecuencia-fundamental","title":"Frecuencia fundamental","summary":"vibración más baja","category":"musica","requires":[]}]}` + "\n```"
	deckGuideReply := "Guía basada en tus tarjetas de acústica.\n\n```json\n" + `{"area":"musica","resources":[],"handoff":"armónicos"}` + "\n```"
	f.replies = []provider.Response{reply(planReply, 10), reply(deckGuideReply, 10)}

	gSlug, err := s.GuideFromDeck(context.Background(), "acustica")
	if err != nil {
		t.Fatal(err)
	}
	gn, err := s.V.Load(gSlug)
	if err != nil {
		t.Fatal(err)
	}
	if len(gn.Meta.Pending) != 1 || gn.Meta.Pending[0].ID != "frecuencia-fundamental" {
		t.Errorf("GuideFromDeck must leave concepts in pending: %+v", gn.Meta.Pending)
	}
	if gn.Meta.Area != "musica" {
		t.Errorf("guide area = %q, want musica", gn.Meta.Area)
	}
	// Deck should now be linked to the guide.
	linked, _ := cards.LoadDeck(s.V.Root, "acustica")
	if linked.Meta.Topic != gSlug {
		t.Errorf("deck topic = %q, want %q", linked.Meta.Topic, gSlug)
	}
}
