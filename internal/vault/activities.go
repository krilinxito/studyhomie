package vault

import (
	"fmt"
	"regexp"
	"strings"
)

// Activity difficulty levels, ordered from easy warm-up to open-ended challenge.
const (
	ActivityEasy      = "easy"
	ActivityMedium    = "medium"
	ActivityChallenge = "challenge"
)

// Activity is one practical hands-on task stored inside a guide's "## Actividades"
// section as a standard Obsidian-compatible Markdown checklist item ("- [ ] ...").
type Activity struct {
	Index      int    `json:"index"`
	Done       bool   `json:"done"`
	Level      string `json:"level"`                // easy | medium | challenge
	Kind       string `json:"kind,omitempty"`       // e.g. Experimento, Ejercicio, Mini-proyecto
	Title      string `json:"title"`                // short title
	Prompt     string `json:"prompt"`               // concrete hands-on instructions
	Submission string `json:"submission,omitempty"` // optional learner result/notes
	Feedback   string `json:"feedback,omitempty"`   // optional brief AI feedback
	XP         int    `json:"xp"`                   // XP reward for completing this activity
}

// NormalizeActivityLevel maps English or localized difficulty names into canonical levels.
func NormalizeActivityLevel(raw string) string {
	s := strings.ToLower(strings.TrimSpace(foldAccents(raw)))
	switch {
	case s == "" || s == "medium" || s == "media" || s == "medio" || s == "moyen" || s == "intermedio" || s == "中等":
		return ActivityMedium
	case s == "easy" || s == "facil" || s == "facile" || s == "basica" || s == "basico" || s == "简单":
		return ActivityEasy
	case s == "challenge" || s == "reto" || s == "dificil" || s == "avanzada" || s == "avanzado" || s == "defi" || s == "desafio" || s == "挑战":
		return ActivityChallenge
	default:
		return ActivityMedium
	}
}

// ActivityXP returns the XP reward for completing an activity of the given difficulty level.
func ActivityXP(level string) int {
	switch NormalizeActivityLevel(level) {
	case ActivityEasy:
		return 15
	case ActivityChallenge:
		return 30
	default:
		return 20
	}
}

var (
	taskLineRe    = regexp.MustCompile(`^[-*]\s+\[([ xX])\]\s+(.+)$`)
	metaBracketRe = regexp.MustCompile(`^\*\*\[([^\]]+)\]\*\*\s*(.*)$`)
	boldTitleRe   = regexp.MustCompile(`^\*\*([^*]+?)\*\*\s*(?:[—–:\-]\s*)?(.*)$`)
	quoteFieldRe  = regexp.MustCompile(`^\s+>\s*\*\*([^:*]+?):?\*\*:?\s*(.*)$`)
)

// ParseActivities extracts the Markdown task checklist from the note's "Actividades"
// section (in any supported language). Hand-edited Obsidian tasks without tags are
// accepted gracefully so users can always edit notes directly in Obsidian.
func ParseActivities(body string) []Activity {
	sec := Section(body, "Actividades")
	if strings.TrimSpace(sec) == "" {
		return nil
	}
	var out []Activity
	lines := strings.Split(strings.ReplaceAll(sec, "\r\n", "\n"), "\n")
	for _, raw := range lines {
		if m := taskLineRe.FindStringSubmatch(raw); m != nil {
			a := parseActivityLine(m[1] != " ", strings.TrimSpace(m[2]))
			a.Index = len(out)
			a.XP = ActivityXP(a.Level)
			out = append(out, a)
			continue
		}
		if len(out) == 0 {
			continue
		}
		// Indented blockquote lines right below a checklist item store optional result & feedback.
		if qm := quoteFieldRe.FindStringSubmatch(raw); qm != nil {
			key := strings.ToLower(strings.TrimSpace(foldAccents(qm[1])))
			val := strings.TrimSpace(qm[2])
			cur := &out[len(out)-1]
			switch {
			case strings.Contains(key, "feedback") || strings.Contains(key, "retroalimentacion") || strings.Contains(key, "retour") || strings.Contains(key, "反馈"):
				cur.Feedback = appendLine(cur.Feedback, val)
			default:
				cur.Submission = appendLine(cur.Submission, val)
			}
			continue
		}
		// Continuation lines inside an indented blockquote (e.g. multi-line submission or feedback).
		if strings.HasPrefix(raw, "  >") || strings.HasPrefix(raw, "\t>") {
			cont := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), ">"))
			if cont == "" {
				continue
			}
			cur := &out[len(out)-1]
			if cur.Feedback != "" {
				cur.Feedback = appendLine(cur.Feedback, cont)
			} else if cur.Submission != "" {
				cur.Submission = appendLine(cur.Submission, cont)
			}
		}
	}
	return out
}

func appendLine(prev, next string) string {
	if prev == "" {
		return next
	}
	if next == "" {
		return prev
	}
	return prev + "\n" + next
}

func parseActivityLine(done bool, rest string) Activity {
	a := Activity{Done: done, Level: ActivityMedium}
	if bm := metaBracketRe.FindStringSubmatch(rest); bm != nil {
		tag := strings.TrimSpace(bm[1])
		rest = strings.TrimSpace(bm[2])
		if parts := strings.SplitN(tag, "·", 2); len(parts) == 2 {
			a.Level = NormalizeActivityLevel(parts[0])
			a.Kind = strings.TrimSpace(parts[1])
		} else {
			a.Level = NormalizeActivityLevel(tag)
		}
	}
	if tm := boldTitleRe.FindStringSubmatch(rest); tm != nil {
		a.Title = strings.TrimSpace(strings.TrimRight(tm[1], ":-—– "))
		a.Prompt = strings.TrimSpace(tm[2])
	} else if parts := strings.SplitN(rest, " — ", 2); len(parts) == 2 {
		a.Title = strings.TrimSpace(parts[0])
		a.Prompt = strings.TrimSpace(parts[1])
	} else {
		a.Prompt = rest
	}
	if a.Title == "" {
		a.Title = shortTitle(a.Prompt)
	}
	if a.Prompt == "" {
		a.Prompt = a.Title
	}
	return a
}

func shortTitle(s string) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= 56 {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:53])) + "…"
}

// levelDisplay returns a clean human-readable level label for Markdown serialization.
func levelDisplay(level string) string {
	switch NormalizeActivityLevel(level) {
	case ActivityEasy:
		return "Fácil"
	case ActivityChallenge:
		return "Reto"
	default:
		return "Media"
	}
}

// FormatActivities renders activities as a clean, Obsidian-compatible Markdown checklist.
func FormatActivities(items []Activity) string {
	var b strings.Builder
	for i, a := range items {
		if i > 0 {
			b.WriteByte('\n')
		}
		box := " "
		if a.Done {
			box = "x"
		}
		tag := levelDisplay(a.Level)
		if kind := strings.TrimSpace(a.Kind); kind != "" {
			tag += " · " + kind
		}
		title := sanitizeOneLine(a.Title)
		prompt := sanitizeOneLine(a.Prompt)
		if title != "" && prompt != "" && title != prompt {
			fmt.Fprintf(&b, "- [%s] **[%s]** **%s:** %s\n", box, tag, title, prompt)
		} else if prompt != "" {
			fmt.Fprintf(&b, "- [%s] **[%s]** %s\n", box, tag, prompt)
		} else {
			fmt.Fprintf(&b, "- [%s] **[%s]** %s\n", box, tag, title)
		}
		if sub := strings.TrimSpace(a.Submission); sub != "" {
			writeQuoteField(&b, "Resultado", sub)
		}
		if fb := strings.TrimSpace(a.Feedback); fb != "" {
			writeQuoteField(&b, "Feedback", fb)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func sanitizeOneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func writeQuoteField(b *strings.Builder, label, text string) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i == 0 {
			fmt.Fprintf(b, "  > **%s:** %s\n", label, line)
		} else {
			fmt.Fprintf(b, "  > %s\n", line)
		}
	}
}
