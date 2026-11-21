package vault

import (
	"sort"
	"strings"
	"unicode"
)

// stopWords are the articles and prepositions a concept's name can carry or drop without becoming
// another concept ("La regla de la cadena" is "Regla cadena"), in the languages the app speaks.
var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		el la los las lo de del al un una unos unas y e o u en por para con
		the of a an and or to in for on with
		le les l d du des et ou au aux pour avec
		os as do da dos das um uma em no na nos nas com`) {
		stopWords[w] = true
	}
}

// stemLen is how much of a word two names must share to count as the same: enough to tell
// "derivada" from "derivacion", little enough to match "condicional" with "condicionada".
const stemLen = 7

// ConceptKey reduces a concept's name or id to what two spellings of the same idea share: no
// accents, no case, no articles, singular words cut to their stem, in any order. "Probabilidad
// condicional", "probabilidades-condicionadas" and "La probabilidad condicionada" all give the same
// key. It is empty when nothing meaningful is left.
func ConceptKey(name string) string {
	words := strings.FieldsFunc(strings.ToLower(foldAccents(name)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var keys []string
	for _, w := range words {
		if stopWords[w] {
			continue
		}
		keys = append(keys, stem(w))
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

// stem drops a plural and keeps the first stemLen letters. Words in other scripts (a Chinese name
// has no spaces to split on) are kept whole.
func stem(w string) string {
	for _, r := range w {
		if r > unicode.MaxASCII {
			return w
		}
	}
	if len(w) > 4 {
		switch {
		case strings.HasSuffix(w, "ces"): // matrices, raices
			w = w[:len(w)-3] + "z"
		case strings.HasSuffix(w, "ies"): // probabilities
			w = w[:len(w)-3] + "y"
		case strings.HasSuffix(w, "es") && strings.ContainsRune("lnrdzj", rune(w[len(w)-3])): // funciones, vectores, redes
			w = w[:len(w)-2]
		case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is"):
			w = w[:len(w)-1]
		}
	}
	if len(w) > stemLen {
		w = w[:stemLen]
	}
	return w
}
