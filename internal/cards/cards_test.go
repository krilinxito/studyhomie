package cards

import (
	"strings"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestFSRSSchedulerGrowthAndLapse(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	// New card rated Good -> Review state with positive days interval.
	r1 := Schedule(ReviewState{}, RatingGood, now)
	if r1.State != StateReview || r1.Interval < 24*time.Hour {
		t.Fatalf("first Good: %+v", r1)
	}
	// Second Good after due -> interval grows.
	prev := ReviewState{State: r1.State, Stability: r1.Stability, Difficulty: r1.Difficulty, Reps: r1.Reps, LastReview: now, Due: r1.Due}
	r2 := Schedule(prev, RatingGood, r1.Due)
	if r2.Interval <= r1.Interval {
		t.Errorf("interval should grow on second Good: r1=%v r2=%v", r1.Interval, r2.Interval)
	}
	// Lapse (Again) -> Relearning state, short interval, lapse incremented.
	prev2 := ReviewState{State: r2.State, Stability: r2.Stability, Difficulty: r2.Difficulty, Reps: r2.Reps, LastReview: r1.Due, Due: r2.Due}
	r3 := Schedule(prev2, RatingAgain, r2.Due)
	if r3.State != StateRelearning || r3.Lapses != 1 || r3.Interval != 10*time.Minute {
		t.Errorf("lapse result = %+v", r3)
	}
}

func TestDeckMarkdownRoundTripAndCollectionUnlock(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deck := Deck{
		Slug: "escalas",
		Meta: DeckMeta{Title: "Escalas menores", Topic: "escalas", Area: "musica", Source: "guide"},
		Cards: []Card{
			{ID: "c1", Front: "¿Fórmula de escala menor natural?", Back: "T-S-T-T-S-T-T", Rarity: RarityCommon, Tags: []string{"escalas"}},
			{ID: "c2", Front: "¿Por qué el 7º grado menor no resuelve igual?", Back: "Está a un tono entero de la tónica.\nSe llama subtónica.", Rarity: RarityLegendary, Tags: []string{"armonia"}},
		},
	}
	if err := SaveDeck(v.Root, deck); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDeck(v.Root, "escalas")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cards) != 2 || got.Cards[0].Rarity != RarityCommon || got.Cards[1].Rarity != RarityLegendary {
		t.Fatalf("parsed deck = %+v", got)
	}
	if !strings.Contains(got.Cards[1].Back, "subtónica") {
		t.Errorf("multiline back lost: %q", got.Cards[1].Back)
	}

	now := time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)
	// Rating Again does NOT unlock collection, awards small effort XP.
	outAgain, err := Review(v.Root, "escalas", "c2", RatingAgain, now)
	if err != nil || outAgain.NewlyCollected || outAgain.State.Collected || outAgain.XPGained != 2 {
		t.Fatalf("Again review = %+v, err=%v", outAgain, err)
	}
	// Rating Good unlocks legendary card with base XP (18) + collection bonus (5) = 23 XP!
	outGood, err := Review(v.Root, "escalas", "c2", RatingGood, now.Add(15*time.Minute))
	if err != nil || !outGood.NewlyCollected || !outGood.State.Collected || outGood.XPGained != 23 {
		t.Fatalf("Good review = %+v, err=%v", outGood, err)
	}

	ov, err := Overview(v, "escalas", now.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if ov.Summary.Collected != 1 || ov.Summary.ByRarity[RarityLegendary] != 1 || ov.Summary.Total != 2 {
		t.Errorf("summary = %+v", ov.Summary)
	}
}

func TestAnkiExportAndImportWithSubdecksAndCloze(t *testing.T) {
	decks := []Deck{
		{
			Slug: "fisica", Meta: DeckMeta{Title: "Física"},
			Cards: []Card{
				{ID: "a1", Front: "Segunda ley de Newton", Back: "$F = m a$", Rarity: RarityRare, Tags: []string{"mecanica"}},
			},
		},
		{
			Slug: "musica", Meta: DeckMeta{Title: "Música"},
			Cards: []Card{
				{ID: "b1", Front: "Intervalo de quinta justa", Back: "7 semitonos", Rarity: RarityEpic, Tags: []string{"intervalos"}},
			},
		},
	}
	tsv := ExportAnki(decks)
	if !strings.Contains(tsv, "#separator:tab") || !strings.Contains(tsv, "rarity::epic") {
		t.Fatalf("unexpected export:\n%s", tsv)
	}

	imported := ImportAnki("export.txt", tsv)
	if len(imported) != 2 {
		t.Fatalf("imported %d decks, want 2: %+v", len(imported), imported)
	}
	if imported[1].Cards[0].Rarity != RarityEpic {
		t.Errorf("rarity lost on round-trip: %+v", imported[1].Cards[0])
	}

	// Test cloze Anki import
	clozeRaw := "#separator:tab\n#html:true\n#deck:Biología\nLa {{c1::mitocondria}} produce <b>ATP</b>\t\tcelula\n"
	cDecks := ImportAnki("bio.txt", clozeRaw)
	if len(cDecks) != 1 || len(cDecks[0].Cards) != 1 {
		t.Fatalf("cloze import = %+v", cDecks)
	}
	if !strings.Contains(cDecks[0].Cards[0].Front, "[…]") || cDecks[0].Cards[0].Back != "mitocondria" {
		t.Errorf("cloze card = %+v", cDecks[0].Cards[0])
	}
}
