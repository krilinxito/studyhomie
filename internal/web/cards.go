package web

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/krilinxito/studyhomie/internal/cards"
	"github.com/krilinxito/studyhomie/internal/stats"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// listCards serves the flashcards overview, due FSRS queue, and collectible album.
func (s *Server) listCards(w http.ResponseWriter, r *http.Request) {
	deck := vault.Slugify(r.URL.Query().Get("deck"))
	ov, err := cards.Overview(s.vault(), deck, s.Now())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ov)
}

// generateCards uses the AI to create 6-10 rarity-tiered flashcards for a guide.
func (s *Server) generateCards(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug string `json:"slug"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	slug := vault.Slugify(body.Slug)
	if slug == "" {
		fail(w, 400, "tema inválido")
		return
	}
	drain(r)
	ctx, cancel := context.WithTimeout(r.Context(), answerTimeout)
	defer cancel()
	deck, added, err := s.shortSession().GenerateMoreCards(ctx, slug)
	if err != nil {
		fail(w, shortStatus(err), s.errText(err))
		return
	}
	writeJSON(w, 200, map[string]any{"deck": deck.Slug, "added": added, "count": len(deck.Cards)})
}

// reviewCard records one FSRS review, unlocks the card in the collection on Good/Easy,
// and logs the rarity-based XP event in the vault's stats.
func (s *Server) reviewCard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Deck   string `json:"deck"`
		CardID string `json:"cardId"`
		Rating int    `json:"rating"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	deckSlug := vault.Slugify(body.Deck)
	if deckSlug == "" || strings.TrimSpace(body.CardID) == "" {
		fail(w, 400, "tarjeta inválida")
		return
	}
	now := s.Now()
	out, err := cards.Review(s.vault().Root, deckSlug, strings.TrimSpace(body.CardID), body.Rating, now)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	area := out.Deck.Meta.Area
	topic := out.Deck.Meta.Topic
	if topic == "" {
		topic = deckSlug
	}
	_, _ = stats.Record(s.vault().Root, stats.Event{
		At:      now,
		Kind:    stats.KindFlashcard,
		Topic:   topic,
		Title:   out.Deck.Meta.Title,
		Area:    area,
		XP:      out.XPGained,
		Seconds: 30,
	})
	writeJSON(w, 200, out)
}

// importCards parses an Anki .txt/.tsv/.csv export, groups cards by deck/tag, and saves
// them under vault/flashcards/.
func (s *Server) importCards(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		fail(w, 400, "subida inválida: "+err.Error())
		return
	}
	defer r.MultipartForm.RemoveAll()
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		files = r.MultipartForm.File["files"]
	}
	if len(files) == 0 {
		fail(w, 400, "no llegó ningún archivo")
		return
	}
	type importedInfo struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
		Count int    `json:"count"`
	}
	var imported []importedInfo
	totalCards := 0
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		raw, err := io.ReadAll(io.LimitReader(f, 16<<20))
		f.Close()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		decks := cards.ImportAnki(fh.Filename, string(raw))
		for _, d := range decks {
			if len(d.Cards) == 0 {
				continue
			}
			// Merge into an existing deck if one with the same slug already exists.
			if prev, err := cards.LoadDeck(s.vault().Root, d.Slug); err == nil {
				seen := map[string]bool{}
				for _, c := range prev.Cards {
					seen[c.ID] = true
				}
				for _, c := range d.Cards {
					if !seen[c.ID] {
						prev.Cards = append(prev.Cards, c)
						seen[c.ID] = true
					}
				}
				d = prev
			}
			if err := cards.SaveDeck(s.vault().Root, d); err != nil {
				fail(w, 500, err.Error())
				return
			}
			imported = append(imported, importedInfo{Slug: d.Slug, Title: d.Meta.Title, Count: len(d.Cards)})
			totalCards += len(d.Cards)
		}
	}
	if len(imported) == 0 {
		fail(w, 400, "no encontré tarjetas válidas en el archivo (usa un export .txt o .tsv de Anki)")
		return
	}
	writeJSON(w, 200, map[string]any{"decks": imported, "total": totalCards})
}

// exportCards downloads one deck (or all decks) in Anki 2.1.54+ TSV import format.
func (s *Server) exportCards(w http.ResponseWriter, r *http.Request) {
	deckSlug := vault.Slugify(r.URL.Query().Get("deck"))
	var decks []cards.Deck
	filename := "studyhomie-anki.txt"
	if deckSlug != "" {
		d, err := cards.LoadDeck(s.vault().Root, deckSlug)
		if err != nil {
			fail(w, 404, "no existe ese mazo")
			return
		}
		decks = []cards.Deck{d}
		filename = deckSlug + "-anki.txt"
	} else {
		var err error
		decks, err = cards.ListDecks(s.vault().Root)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
	}
	tsv := cards.ExportAnki(decks)
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	h.Set("X-Content-Type-Options", "nosniff")
	io.WriteString(w, tsv)
}
