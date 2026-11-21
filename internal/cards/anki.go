package cards

import (
	"encoding/csv"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/krilinxito/studyhomie/internal/vault"
)

// ExportAnki renders one or more decks in Anki 2.1.54+ TSV import format with headers
// (#separator:tab, #html:false, #deck:..., #tags column:3).
func ExportAnki(decks []Deck) string {
	var b strings.Builder
	b.WriteString("#separator:tab\n")
	b.WriteString("#html:false\n")
	if len(decks) == 1 {
		b.WriteString("#deck:StudyHomie::" + sanitizeAnkiField(decks[0].Meta.Title) + "\n")
		b.WriteString("#tags column:3\n")
		for _, c := range decks[0].Cards {
			tags := exportTags(c)
			b.WriteString(sanitizeAnkiField(c.Front) + "\t" + sanitizeAnkiField(c.Back) + "\t" + tags + "\n")
		}
		return b.String()
	}
	b.WriteString("#deck column:3\n")
	b.WriteString("#tags column:4\n")
	for _, d := range decks {
		deckName := "StudyHomie::" + sanitizeAnkiField(d.Meta.Title)
		for _, c := range d.Cards {
			b.WriteString(sanitizeAnkiField(c.Front) + "\t" + sanitizeAnkiField(c.Back) + "\t" + deckName + "\t" + exportTags(c) + "\n")
		}
	}
	return b.String()
}

func sanitizeAnkiField(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " / ")
	return strings.TrimSpace(s)
}

func exportTags(c Card) string {
	tags := []string{"studyhomie", "rarity::" + NormalizeRarity(c.Rarity)}
	for _, tg := range c.Tags {
		tg = strings.ReplaceAll(strings.TrimSpace(tg), " ", "_")
		if tg != "" && tg != "studyhomie" && !strings.HasPrefix(tg, "rarity::") {
			tags = append(tags, tg)
		}
	}
	return strings.Join(tags, " ")
}

var (
	htmlBRRe  = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>`)
	htmlTagRe = regexp.MustCompile(`<[^>]+>`)
	clozeRe   = regexp.MustCompile(`\{\{c\d+::([^:}]+)(?:::([^}]+))?\}\}`)
)

func cleanAnkiText(s string, isHTML bool) string {
	s = strings.TrimSpace(s)
	if isHTML {
		s = htmlBRRe.ReplaceAllString(s, "\n")
		s = htmlTagRe.ReplaceAllString(s, "")
		s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(s)
	}
	return strings.TrimSpace(s)
}

// estimateRarity assigns an initial rarity tier to imported Anki cards based on explicit
// rarity tags or question complexity (difficulty heuristics).
func estimateRarity(front, back string, tags []string) string {
	for _, tg := range tags {
		low := strings.ToLower(strings.TrimSpace(tg))
		if strings.HasPrefix(low, "rarity::") {
			return NormalizeRarity(strings.TrimPrefix(low, "rarity::"))
		}
		switch low {
		case "legendary", "legendaria", "hardest", "challenge", "reto":
			return RarityLegendary
		case "epic", "epica", "épica", "hard", "dificil", "difícil", "advanced":
			return RarityEpic
		case "rare", "rara", "medium", "media":
			return RarityRare
		case "common", "comun", "común", "easy", "basic":
			return RarityCommon
		}
	}
	score := 0
	totalLen := len([]rune(front)) + len([]rune(back))
	if totalLen > 140 {
		score++
	}
	if totalLen > 280 {
		score++
	}
	if strings.Contains(front, "$") || strings.Contains(back, "$") || strings.Contains(front, `\(`) {
		score++
	}
	if len(clozeRe.FindAllString(front, -1)) >= 2 {
		score++
	}
	lowF := strings.ToLower(front)
	for _, cue := range []string{"por qué", "why", "qué pasa si", "what happens", "diferencia", "compare", "demuestra", "prove", "explica"} {
		if strings.Contains(lowF, cue) {
			score++
			break
		}
	}
	switch {
	case score >= 3:
		return RarityLegendary
	case score == 2:
		return RarityEpic
	case score == 1:
		return RarityRare
	default:
		return RarityCommon
	}
}

// ImportAnki parses an Anki .txt / .tsv / .csv export and groups cards by subdeck or primary tag.
func ImportAnki(filename, raw string) []Deck {
	text := strings.TrimPrefix(strings.ReplaceAll(raw, "\r\n", "\n"), "\ufeff")
	sep := '\t'
	isHTML := true
	defaultDeck := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	if strings.TrimSpace(defaultDeck) == "" {
		defaultDeck = "Mazo importado"
	}
	deckCol := -1
	tagsCol := -1
	notetypeCol := -1

	var dataLines []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			low := strings.ToLower(trimmed)
			switch {
			case strings.HasPrefix(low, "#separator:"):
				v := strings.TrimSpace(low[len("#separator:"):])
				switch v {
				case "tab", `\t`:
					sep = '\t'
				case "comma", ",":
					sep = ','
				case "semicolon", ";":
					sep = ';'
				case "pipe", "|":
					sep = '|'
				}
			case strings.HasPrefix(low, "#html:"):
				isHTML = strings.TrimSpace(low[len("#html:"):]) != "false"
			case strings.HasPrefix(low, "#deck:"):
				if v := strings.TrimSpace(trimmed[len("#deck:"):]); v != "" {
					defaultDeck = lastSegment(v)
				}
			case strings.HasPrefix(low, "#deck column:"):
				if n, err := strconv.Atoi(strings.TrimSpace(low[len("#deck column:"):])); err == nil && n > 0 {
					deckCol = n - 1
				}
			case strings.HasPrefix(low, "#tags column:"):
				if n, err := strconv.Atoi(strings.TrimSpace(low[len("#tags column:"):])); err == nil && n > 0 {
					tagsCol = n - 1
				}
			case strings.HasPrefix(low, "#notetype column:"):
				if n, err := strconv.Atoi(strings.TrimSpace(low[len("#notetype column:"):])); err == nil && n > 0 {
					notetypeCol = n - 1
				}
			}
			continue
		}
		dataLines = append(dataLines, line)
	}
	if len(dataLines) == 0 {
		return nil
	}

	// Auto-detect comma/semicolon if no tabs exist in the first data line.
	if sep == '\t' && !strings.Contains(dataLines[0], "\t") {
		if strings.Contains(dataLines[0], ";") {
			sep = ';'
		} else if strings.Contains(dataLines[0], ",") {
			sep = ','
		}
	}

	r := csv.NewReader(strings.NewReader(strings.Join(dataLines, "\n")))
	r.Comma = sep
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil || len(rows) == 0 {
		return nil
	}

	grouped := map[string]*Deck{}
	order := []string{}
	addCard := func(deckName string, c Card) {
		title := strings.TrimSpace(deckName)
		if title == "" {
			title = defaultDeck
		}
		slug := vault.Slugify(title)
		if slug == "" {
			slug = "mazo-importado"
		}
		d := grouped[slug]
		if d == nil {
			d = &Deck{Slug: slug, Meta: DeckMeta{Title: title, Source: "anki"}}
			grouped[slug] = d
			order = append(order, slug)
		}
		c.Deck = slug
		if c.ID == "" {
			c.ID = CardID(c.Front)
		}
		d.Cards = append(d.Cards, c)
	}

	for _, row := range rows {
		// Determine front and back columns (skipping deck/tags/notetype columns).
		var contentCols []int
		for i := range row {
			if i == deckCol || i == tagsCol || i == notetypeCol {
				continue
			}
			contentCols = append(contentCols, i)
		}
		if len(contentCols) < 1 {
			continue
		}
		front := cleanAnkiText(row[contentCols[0]], isHTML)
		back := ""
		if len(contentCols) >= 2 {
			back = cleanAnkiText(row[contentCols[1]], isHTML)
		}
		// Handle cloze cards where Anki puts {{c1::answer}} in column 1 and empty column 2.
		if back == "" && clozeRe.MatchString(front) {
			var answers []string
			prompt := clozeRe.ReplaceAllStringFunc(front, func(m string) string {
				sub := clozeRe.FindStringSubmatch(m)
				if len(sub) > 1 {
					answers = append(answers, sub[1])
				}
				if len(sub) > 2 && sub[2] != "" {
					return "[" + sub[2] + "]"
				}
				return "[…]"
			})
			front = prompt
			back = strings.Join(answers, " · ")
		}
		if front == "" || back == "" {
			continue
		}

		var tags []string
		if tagsCol >= 0 && tagsCol < len(row) {
			tags = strings.Fields(row[tagsCol])
		} else if tagsCol < 0 && deckCol < 0 && len(contentCols) >= 3 {
			// Classic 3-column Anki export: Front \t Back \t Tags
			tags = strings.Fields(row[contentCols[2]])
		}

		deckName := defaultDeck
		if deckCol >= 0 && deckCol < len(row) && strings.TrimSpace(row[deckCol]) != "" {
			deckName = lastSegment(row[deckCol])
		} else if len(tags) > 0 && defaultDeck == "Mazo importado" {
			if groupTag := primaryGroupTag(tags); groupTag != "" {
				deckName = groupTag
			}
		}

		rarity := estimateRarity(front, back, tags)
		var cleanTags []string
		for _, tg := range tags {
			if !strings.HasPrefix(strings.ToLower(tg), "rarity::") && strings.ToLower(tg) != "studyhomie" {
				cleanTags = append(cleanTags, tg)
			}
		}
		addCard(deckName, Card{Front: front, Back: back, Rarity: rarity, Tags: cleanTags})
	}

	sort.Strings(order)
	out := make([]Deck, 0, len(order))
	for _, slug := range order {
		out = append(out, *grouped[slug])
	}
	return out
}

func lastSegment(deck string) string {
	parts := strings.Split(strings.TrimSpace(deck), "::")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return strings.TrimSpace(deck)
	}
	return last
}

func primaryGroupTag(tags []string) string {
	for _, tg := range tags {
		low := strings.ToLower(tg)
		if strings.HasPrefix(low, "rarity::") || low == "studyhomie" {
			continue
		}
		return strings.ReplaceAll(lastSegment(tg), "_", " ")
	}
	return ""
}
