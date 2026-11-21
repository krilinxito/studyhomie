// Package cards manages collectible spaced-repetition flashcards stored in Obsidian-compatible
// Markdown (vault/flashcards/<deck>.md) with FSRS review state in vault/.studyhomie/cards.json.
package cards

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/krilinxito/studyhomie/internal/vault"
	"gopkg.in/yaml.v3"
)

// Rarity tiers mapped directly to question difficulty.
const (
	RarityCommon    = "common"
	RarityRare      = "rare"
	RarityEpic      = "epic"
	RarityLegendary = "legendary"
)

// Rarities lists tiers in ascending difficulty order.
var Rarities = []string{RarityCommon, RarityRare, RarityEpic, RarityLegendary}

// NormalizeRarity returns one of Rarities, defaulting to common.
func NormalizeRarity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "common", "comun", "común", "basic", "basica", "básica", "easy", "facil", "fácil":
		return RarityCommon
	case "rare", "rara", "medium", "media", "intermediate":
		return RarityRare
	case "epic", "epica", "épica", "hard", "dificil", "difícil", "advanced", "avanzada":
		return RarityEpic
	case "legendary", "legendaria", "master", "challenge", "reto":
		return RarityLegendary
	default:
		return RarityCommon
	}
}

// BaseXP returns the base XP for answering a card of this rarity.
func BaseXP(rarity string) int {
	switch NormalizeRarity(rarity) {
	case RarityRare:
		return 8
	case RarityEpic:
		return 12
	case RarityLegendary:
		return 18
	default:
		return 5
	}
}

// XPForReview computes XP earned for reviewing a card of the given rarity and rating,
// including a first-time collection bonus.
func XPForReview(rarity string, rating int, newlyCollected bool) int {
	base := BaseXP(rarity)
	var xp int
	switch rating {
	case RatingAgain:
		xp = 2 // effort XP even when missed
	case RatingHard:
		xp = max(3, (base*4)/5)
	case RatingEasy:
		xp = base + 2
	default:
		xp = base
	}
	if newlyCollected {
		xp += 5
	}
	return xp
}

// DeckMeta is the YAML frontmatter of vault/flashcards/<slug>.md.
type DeckMeta struct {
	Title   string `yaml:"title" json:"title"`
	Topic   string `yaml:"topic,omitempty" json:"topic,omitempty"`   // linked guide slug, if any
	Area    string `yaml:"area,omitempty" json:"area,omitempty"`     // canonical area
	Source  string `yaml:"source,omitempty" json:"source,omitempty"` // guide | anki | manual
	Updated string `yaml:"updated,omitempty" json:"updated,omitempty"`
}

// Card is one flashcard inside a deck.
type Card struct {
	ID     string   `json:"id"`
	Deck   string   `json:"deck"`
	Front  string   `json:"front"`
	Back   string   `json:"back"`
	Rarity string   `json:"rarity"` // common | rare | epic | legendary
	Tags   []string `json:"tags,omitempty"`
}

// Deck is one Markdown deck file in vault/flashcards/.
type Deck struct {
	Slug  string   `json:"slug"`
	Meta  DeckMeta `json:"meta"`
	Cards []Card   `json:"cards"`
}

// ReviewState holds FSRS scheduling + collectible unlock status for one card.
type ReviewState struct {
	CardID      string    `json:"cardId"`
	Deck        string    `json:"deck"`
	State       string    `json:"state"`
	Stability   float64   `json:"stability"`
	Difficulty  float64   `json:"difficulty"`
	Reps        int       `json:"reps"`
	Lapses      int       `json:"lapses"`
	Due         time.Time `json:"due"`
	LastReview  time.Time `json:"lastReview,omitempty"`
	Collected   bool      `json:"collected"`
	CollectedAt time.Time `json:"collectedAt,omitempty"`
}

// StateStore lives at vault/.studyhomie/cards.json.
type StateStore struct {
	States map[string]ReviewState `json:"states"` // key: "<deck>:<cardId>"
}

// CardView is a card enriched with its live FSRS state & collection flag for the UI.
type CardView struct {
	Card
	DeckTitle string      `json:"deckTitle"`
	Area      string      `json:"area,omitempty"`
	Topic     string      `json:"topic,omitempty"`
	State     string      `json:"state"`
	Due       bool        `json:"due"`
	DueAt     *time.Time  `json:"dueAt,omitempty"`
	Reps      int         `json:"reps"`
	Collected bool        `json:"collected"`
	Intervals map[int]int `json:"intervals,omitempty"` // rating 1..4 -> minutes
}

// DeckSummary summarizes one deck for the UI.
type DeckSummary struct {
	Slug      string         `json:"slug"`
	Title     string         `json:"title"`
	Topic     string         `json:"topic,omitempty"`
	Area      string         `json:"area,omitempty"`
	Source    string         `json:"source,omitempty"`
	HasGuide  bool           `json:"hasGuide"`
	Total     int            `json:"total"`
	Collected int            `json:"collected"`
	Due       int            `json:"due"`
	ByRarity  map[string]int `json:"byRarity"`
}

// CollectionSummary aggregates collection counts across the vault (used in Progreso & Practicar).
type CollectionSummary struct {
	Total     int            `json:"total"`
	Collected int            `json:"collected"`
	Due       int            `json:"due"`
	ByRarity  map[string]int `json:"byRarity"` // collected count per rarity
	Totals    map[string]int `json:"totals"`   // total cards per rarity
}

// OverviewResponse is served by GET /api/cards.
type OverviewResponse struct {
	Summary CollectionSummary `json:"summary"`
	Decks   []DeckSummary     `json:"decks"`
	Queue   []CardView        `json:"queue"` // due cards, ready to review
	Cards   []CardView        `json:"cards"` // all cards of selected deck (or all) for the collection album
}

var (
	stateMu     sync.Mutex
	cardMetaRe  = regexp.MustCompile(`^<!--\s*card:([a-zA-Z0-9_-]+)(?:\s+rarity:([a-zA-Z]+))?(?:\s+tags:([^>]*?))?\s*-->$`)
	deckFileExt = ".md"
)

// CardID derives a stable short ID from front text when none is provided.
func CardID(front string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(strings.ToLower(front))))
	return "c" + hex.EncodeToString(sum[:4])
}

func deckPath(vaultRoot, slug string) string {
	return filepath.Join(vaultRoot, vault.FlashcardsDir, slug+deckFileExt)
}

func statePath(vaultRoot string) string {
	return filepath.Join(vaultRoot, ".studyhomie", "cards.json")
}

func stateKey(deckSlug, cardID string) string { return deckSlug + ":" + cardID }

// SaveDeck writes a deck to vault/flashcards/<slug>.md in Obsidian Spaced Repetition format.
func SaveDeck(vaultRoot string, d Deck) error {
	slug := vault.Slugify(d.Slug)
	if slug == "" {
		slug = vault.Slugify(d.Meta.Title)
	}
	if slug == "" {
		return errors.New("deck needs a slug or title")
	}
	d.Slug = slug
	if d.Meta.Title == "" {
		d.Meta.Title = slug
	}
	if d.Meta.Area != "" {
		d.Meta.Area = vault.NormalizeArea(d.Meta.Area)
	}
	d.Meta.Updated = time.Now().Format("2006-01-02")

	var buf bytes.Buffer
	fm, err := yaml.Marshal(d.Meta)
	if err != nil {
		return err
	}
	buf.WriteString("---\n")
	buf.Write(fm)
	buf.WriteString("---\n\n")
	buf.WriteString("# " + d.Meta.Title + "\n\n")

	seen := map[string]bool{}
	for _, c := range d.Cards {
		front := strings.TrimSpace(c.Front)
		back := strings.TrimSpace(c.Back)
		if front == "" || back == "" {
			continue
		}
		id := c.ID
		if id == "" {
			id = CardID(front)
		}
		for seen[id] {
			id += "x"
		}
		seen[id] = true
		rarity := NormalizeRarity(c.Rarity)

		meta := fmt.Sprintf("<!-- card:%s rarity:%s", id, rarity)
		if len(c.Tags) > 0 {
			var clean []string
			for _, tg := range c.Tags {
				if tg = strings.TrimSpace(strings.TrimPrefix(tg, "#")); tg != "" {
					clean = append(clean, tg)
				}
			}
			if len(clean) > 0 {
				meta += " tags:" + strings.Join(clean, ",")
			}
		}
		meta += " -->\n"
		buf.WriteString(meta)

		if strings.Contains(front, "\n") || strings.Contains(back, "\n") || strings.Contains(front, "::") || strings.Contains(back, "::") {
			buf.WriteString(front + "\n?\n" + back + "\n\n")
		} else {
			buf.WriteString(front + "::" + back + "\n\n")
		}
	}

	p := deckPath(vaultRoot, slug)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, buf.Bytes(), 0o644)
}

// ParseDeck parses an Obsidian Spaced Repetition Markdown deck file.
func ParseDeck(slug string, data []byte) (Deck, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	d := Deck{Slug: slug}
	body := text
	if strings.HasPrefix(text, "---\n") {
		rest := text[4:]
		if end := strings.Index(rest, "\n---\n"); end >= 0 {
			if err := yaml.Unmarshal([]byte(rest[:end]), &d.Meta); err != nil {
				return d, fmt.Errorf("flashcards frontmatter %s: %w", slug, err)
			}
			body = strings.TrimLeft(rest[end+5:], "\n")
		}
	}
	if d.Meta.Title == "" {
		d.Meta.Title = slug
	}

	lines := strings.Split(body, "\n")
	var pendingID, pendingRarity string
	var pendingTags []string
	resetMeta := func() { pendingID, pendingRarity, pendingTags = "", "", nil }

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "# ") {
			continue
		}
		if m := cardMetaRe.FindStringSubmatch(trimmed); m != nil {
			pendingID = m[1]
			pendingRarity = m[2]
			pendingTags = nil
			if strings.TrimSpace(m[3]) != "" {
				for _, tg := range strings.Split(m[3], ",") {
					if tg = strings.TrimSpace(tg); tg != "" {
						pendingTags = append(pendingTags, tg)
					}
				}
			}
			continue
		}

		// Single-line Obsidian Spaced Repetition card: "Front::Back"
		if idx := strings.Index(lines[i], "::"); idx > 0 {
			front := strings.TrimSpace(lines[i][:idx])
			back := strings.TrimSpace(lines[i][idx+2:])
			if front != "" && back != "" {
				id := pendingID
				if id == "" {
					id = CardID(front)
				}
				d.Cards = append(d.Cards, Card{
					ID: id, Deck: slug, Front: front, Back: back,
					Rarity: NormalizeRarity(pendingRarity), Tags: pendingTags,
				})
				resetMeta()
				continue
			}
		}

		// Multi-line Obsidian Spaced Repetition card: Front lines \n ? \n Back lines \n\n
		qIdx := -1
		endIdx := len(lines)
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "?" && qIdx < 0 {
				qIdx = j
				continue
			}
			if t == "" && qIdx >= 0 {
				endIdx = j
				break
			}
			if cardMetaRe.MatchString(t) {
				endIdx = j
				break
			}
		}
		if qIdx > i {
			front := strings.TrimSpace(strings.Join(lines[i:qIdx], "\n"))
			back := strings.TrimSpace(strings.Join(lines[qIdx+1:endIdx], "\n"))
			if front != "" && back != "" {
				id := pendingID
				if id == "" {
					id = CardID(front)
				}
				d.Cards = append(d.Cards, Card{
					ID: id, Deck: slug, Front: front, Back: back,
					Rarity: NormalizeRarity(pendingRarity), Tags: pendingTags,
				})
				resetMeta()
			}
			i = endIdx - 1
		}
	}
	return d, nil
}

// LoadDeck reads one deck from vault/flashcards/<slug>.md.
func LoadDeck(vaultRoot, slug string) (Deck, error) {
	data, err := os.ReadFile(deckPath(vaultRoot, slug))
	if err != nil {
		return Deck{}, err
	}
	return ParseDeck(slug, data)
}

// ListDecks returns all decks stored in vault/flashcards/, sorted by title.
func ListDecks(vaultRoot string) ([]Deck, error) {
	dir := filepath.Join(vaultRoot, vault.FlashcardsDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var decks []Deck
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), deckFileExt) {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), deckFileExt)
		d, err := LoadDeck(vaultRoot, slug)
		if err != nil {
			continue
		}
		decks = append(decks, d)
	}
	sort.Slice(decks, func(i, j int) bool {
		return strings.ToLower(decks[i].Meta.Title) < strings.ToLower(decks[j].Meta.Title)
	})
	return decks, nil
}

func loadStatesLocked(vaultRoot string) (StateStore, error) {
	raw, err := os.ReadFile(statePath(vaultRoot))
	if errors.Is(err, os.ErrNotExist) {
		return StateStore{States: map[string]ReviewState{}}, nil
	}
	if err != nil {
		return StateStore{}, err
	}
	var st StateStore
	if err := json.Unmarshal(raw, &st); err != nil {
		return StateStore{}, err
	}
	if st.States == nil {
		st.States = map[string]ReviewState{}
	}
	return st, nil
}

func saveStatesLocked(vaultRoot string, st StateStore) error {
	p := statePath(vaultRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// ReviewOutcome reports what happened when the learner rated one card.
type ReviewOutcome struct {
	Card           Card        `json:"card"`
	Deck           Deck        `json:"-"`
	State          ReviewState `json:"state"`
	XPGained       int         `json:"xpGained"`
	NewlyCollected bool        `json:"newlyCollected"`
	NextMinutes    int         `json:"nextMinutes"`
}

// Review applies an FSRS rating (1..4) to a card, updates vault/.studyhomie/cards.json,
// unlocks collection status on Good/Easy, and returns the XP earned.
func Review(vaultRoot, deckSlug, cardID string, rating int, now time.Time) (ReviewOutcome, error) {
	if rating < RatingAgain || rating > RatingEasy {
		return ReviewOutcome{}, fmt.Errorf("calificación inválida %d", rating)
	}
	d, err := LoadDeck(vaultRoot, deckSlug)
	if err != nil {
		return ReviewOutcome{}, err
	}
	var found *Card
	for i := range d.Cards {
		if d.Cards[i].ID == cardID {
			found = &d.Cards[i]
			break
		}
	}
	if found == nil {
		return ReviewOutcome{}, fmt.Errorf("no existe la tarjeta %q", cardID)
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	store, err := loadStatesLocked(vaultRoot)
	if err != nil {
		return ReviewOutcome{}, err
	}
	k := stateKey(deckSlug, cardID)
	prev := store.States[k]
	sr := Schedule(prev, rating, now)

	newlyCollected := false
	collected := prev.Collected
	collectedAt := prev.CollectedAt
	if !collected && (rating == RatingGood || rating == RatingEasy) {
		collected = true
		collectedAt = now
		newlyCollected = true
	}

	next := ReviewState{
		CardID:      cardID,
		Deck:        deckSlug,
		State:       sr.State,
		Stability:   sr.Stability,
		Difficulty:  sr.Difficulty,
		Reps:        sr.Reps,
		Lapses:      sr.Lapses,
		Due:         sr.Due,
		LastReview:  now,
		Collected:   collected,
		CollectedAt: collectedAt,
	}
	store.States[k] = next
	if err := saveStatesLocked(vaultRoot, store); err != nil {
		return ReviewOutcome{}, err
	}

	return ReviewOutcome{
		Card:           *found,
		Deck:           d,
		State:          next,
		XPGained:       XPForReview(found.Rarity, rating, newlyCollected),
		NewlyCollected: newlyCollected,
		NextMinutes:    max(1, int(sr.Interval.Minutes())),
	}, nil
}

// Summary computes the vault-wide collection summary (used by GET /api/stats as well).
func Summary(vaultRoot string, now time.Time) CollectionSummary {
	decks, _ := ListDecks(vaultRoot)
	stateMu.Lock()
	store, _ := loadStatesLocked(vaultRoot)
	stateMu.Unlock()

	sum := CollectionSummary{
		ByRarity: map[string]int{RarityCommon: 0, RarityRare: 0, RarityEpic: 0, RarityLegendary: 0},
		Totals:   map[string]int{RarityCommon: 0, RarityRare: 0, RarityEpic: 0, RarityLegendary: 0},
	}
	for _, d := range decks {
		for _, c := range d.Cards {
			sum.Total++
			r := NormalizeRarity(c.Rarity)
			sum.Totals[r]++
			st := store.States[stateKey(d.Slug, c.ID)]
			if st.Collected {
				sum.Collected++
				sum.ByRarity[r]++
			}
			if isDue(st, now) {
				sum.Due++
			}
		}
	}
	return sum
}

func isDue(st ReviewState, now time.Time) bool {
	if st.State == "" || st.State == StateNew || st.Due.IsZero() {
		return true
	}
	return !st.Due.After(now)
}

// Overview builds the full flashcards view for the UI (optionally filtered to one deck slug).
func Overview(v *vault.Vault, deckFilter string, now time.Time) (OverviewResponse, error) {
	decks, err := ListDecks(v.Root)
	if err != nil {
		return OverviewResponse{}, err
	}
	stateMu.Lock()
	store, err := loadStatesLocked(v.Root)
	stateMu.Unlock()
	if err != nil {
		return OverviewResponse{}, err
	}

	res := OverviewResponse{
		Summary: CollectionSummary{
			ByRarity: map[string]int{RarityCommon: 0, RarityRare: 0, RarityEpic: 0, RarityLegendary: 0},
			Totals:   map[string]int{RarityCommon: 0, RarityRare: 0, RarityEpic: 0, RarityLegendary: 0},
		},
		Decks: []DeckSummary{},
		Queue: []CardView{},
		Cards: []CardView{},
	}

	for _, d := range decks {
		topic := d.Meta.Topic
		if topic == "" && v.Exists(d.Slug) {
			topic = d.Slug
		}
		hasGuide := topic != "" && v.Exists(topic)
		ds := DeckSummary{
			Slug:     d.Slug,
			Title:    d.Meta.Title,
			Topic:    topic,
			Area:     d.Meta.Area,
			Source:   d.Meta.Source,
			HasGuide: hasGuide,
			Total:    len(d.Cards),
			ByRarity: map[string]int{RarityCommon: 0, RarityRare: 0, RarityEpic: 0, RarityLegendary: 0},
		}
		for _, c := range d.Cards {
			r := NormalizeRarity(c.Rarity)
			res.Summary.Total++
			res.Summary.Totals[r]++
			st := store.States[stateKey(d.Slug, c.ID)]
			due := isDue(st, now)
			if st.Collected {
				ds.Collected++
				ds.ByRarity[r]++
				res.Summary.Collected++
				res.Summary.ByRarity[r]++
			}
			if due {
				ds.Due++
				res.Summary.Due++
			}
			if deckFilter == "" || deckFilter == d.Slug || deckFilter == topic {
				var dueAt *time.Time
				if !st.Due.IsZero() {
					t := st.Due
					dueAt = &t
				}
				stateName := st.State
				if stateName == "" {
					stateName = StateNew
				}
				cv := CardView{
					Card:      c,
					DeckTitle: d.Meta.Title,
					Area:      d.Meta.Area,
					Topic:     topic,
					State:     stateName,
					Due:       due,
					DueAt:     dueAt,
					Reps:      st.Reps,
					Collected: st.Collected,
				}
				res.Cards = append(res.Cards, cv)
				if due {
					cv.Intervals = PreviewIntervals(st, now)
					res.Queue = append(res.Queue, cv)
				}
			}
		}
		res.Decks = append(res.Decks, ds)
	}
	return res, nil
}
