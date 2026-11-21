package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/krilinxito/studyhomie/internal/cards"
	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// GenerateCards creates 6-10 rarity-tiered flashcards from a study guide and its concept notes,
// saving them to vault/flashcards/<slug>.md in Obsidian Spaced Repetition format.
func (s *Session) GenerateCards(ctx context.Context, slug string) (cards.Deck, error) {
	note, title, err := s.loadGuided(slug)
	if err != nil {
		return cards.Deck{}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "TOPIC: %s\n", title)
	if area := noteArea(note); area != "" {
		fmt.Fprintf(&b, "AREA: %s\n", area)
	}
	b.WriteString("\nGUIDE CONTENT:\n" + head(s.noteContext(slug, note), 5000) + "\n")
	if len(note.Meta.Concepts) > 0 {
		b.WriteString("\nCONCEPT NOTES:\n")
		for _, cid := range note.Meta.Concepts {
			if c, err := s.V.LoadConcept(cid); err == nil {
				one := vault.Section(c.Body, "En una frase")
				if one == "" {
					one = c.Meta.Summary
				}
				fmt.Fprintf(&b, "- %s: %s\n", c.Meta.Title, one)
			}
		}
	}

	resp, err := s.quickConvo(prompts.Get("flashcards", s.aiLang())).Say(ctx, b.String(), nil)
	if err != nil {
		return cards.Deck{}, err
	}
	var out struct {
		Cards []struct {
			Front  string   `json:"front"`
			Back   string   `json:"back"`
			Rarity string   `json:"rarity"`
			Tags   []string `json:"tags"`
		} `json:"cards"`
	}
	if err := decodeJSON(resp.Text, &out); err != nil {
		return cards.Deck{}, failf("no pude leer las tarjetas generadas: %w", err)
	}
	if len(out.Cards) == 0 {
		return cards.Deck{}, failf("no se generó ninguna tarjeta")
	}

	deck := cards.Deck{
		Slug: slug,
		Meta: cards.DeckMeta{
			Title:  title,
			Topic:  slug,
			Area:   noteArea(note),
			Source: "guide",
		},
	}
	for _, c := range out.Cards {
		front := strings.TrimSpace(c.Front)
		back := strings.TrimSpace(c.Back)
		if front == "" || back == "" {
			continue
		}
		deck.Cards = append(deck.Cards, cards.Card{
			ID:     cards.CardID(front),
			Deck:   slug,
			Front:  front,
			Back:   back,
			Rarity: cards.NormalizeRarity(c.Rarity),
			Tags:   c.Tags,
		})
	}
	if len(deck.Cards) == 0 {
		return cards.Deck{}, failf("no se generó ninguna tarjeta válida")
	}
	if err := cards.SaveDeck(s.V.Root, deck); err != nil {
		return cards.Deck{}, err
	}
	return cards.LoadDeck(s.V.Root, slug)
}

// GuideFromDeck generates a study guide grounded in an imported flashcard deck, mapping its
// prerequisite concepts and leaving them in pending (under water in the iceberg) to explain on demand.
func (s *Session) GuideFromDeck(ctx context.Context, deckSlug string) (string, error) {
	deckSlug = vault.Slugify(deckSlug)
	if deckSlug == "" {
		return "", inputErrorf("falta el mazo de tarjetas")
	}
	deck, err := cards.LoadDeck(s.V.Root, deckSlug)
	if err != nil {
		return "", inputErrorf("no existe el mazo «%s»", deckSlug)
	}
	if len(deck.Cards) == 0 {
		return "", inputErrorf("el mazo «%s» no tiene tarjetas", deck.Meta.Title)
	}

	var src strings.Builder
	fmt.Fprintf(&src, "# %s\n\n", deck.Meta.Title)
	for i, c := range deck.Cards {
		fmt.Fprintf(&src, "## Tarjeta %d (%s)\n**Pregunta:** %s\n**Respuesta:** %s\n\n", i+1, c.Rarity, c.Front, c.Back)
	}
	mslug := "deck-" + deckSlug
	if err := s.V.SaveMaterial(mslug, src.String()); err != nil {
		return "", err
	}

	topicTitle := strings.TrimSpace(deck.Meta.Title)
	guideSlug := vault.Slugify(topicTitle)
	if guideSlug == "" {
		guideSlug = deckSlug
	}
	if !s.V.Exists(guideSlug) {
		n := vault.Note{
			Slug: guideSlug,
			Meta: vault.Meta{
				Title:    topicTitle,
				Status:   "new",
				Source:   "material",
				Material: mslug,
				Area:     deck.Meta.Area,
			},
		}
		if err := s.V.Save(n); err != nil {
			return "", err
		}
	} else {
		_ = s.V.Update(guideSlug, func(n *vault.Note) error {
			n.Meta.Material = mslug
			return nil
		})
	}

	// Ensure Iceberg is enabled for this run so Depth=0 maps prerequisite concepts into pending.
	prevIceberg := s.Cfg.Iceberg
	s.Cfg.Iceberg = true
	slug, err := s.Guide(ctx, GuideReq{
		Topic:  topicTitle,
		Source: "material",
		Depth:  0,
		Auto:   true,
	})
	s.Cfg.Iceberg = prevIceberg
	if err != nil {
		return "", err
	}

	// Link the deck to the newly generated guide and copy its detected area.
	if n, err := s.V.Load(slug); err == nil {
		deck.Meta.Topic = slug
		if deck.Meta.Area == "" && n.Meta.Area != "" {
			deck.Meta.Area = n.Meta.Area
		}
		_ = cards.SaveDeck(s.V.Root, deck)
	}
	return slug, nil
}
