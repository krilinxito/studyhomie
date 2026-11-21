package vault

import "testing"

func TestConceptKeyMatchesSpellingsOfTheSameIdea(t *testing.T) {
	same := [][]string{
		{"Probabilidad condicional", "probabilidad-condicionada", "La probabilidad condicionada", "Probabilidades condicionales"},
		{"Matriz inversa", "Matrices inversas"},
		{"Funciones", "función"},
		{"Regla de la cadena", "regla-cadena", "La regla de cadena"},
		{"Teorema de Bayes", "Bayes, teorema de"},
		{"Vectores", "vector"},
	}
	for _, group := range same {
		k := ConceptKey(group[0])
		if k == "" {
			t.Fatalf("empty key for %q", group[0])
		}
		for _, other := range group[1:] {
			if got := ConceptKey(other); got != k {
				t.Errorf("ConceptKey(%q) = %q, want %q (as %q)", other, got, k, group[0])
			}
		}
	}
	different := [][2]string{
		{"Derivada", "Derivación"},
		{"Distribución normal", "Distribución binomial"},
		{"Ley 1 de Newton", "Ley 2 de Newton"},
		{"Integral", "Integral definida"},
		{"函数", "函数极限"},
	}
	for _, d := range different {
		if ConceptKey(d[0]) == ConceptKey(d[1]) {
			t.Errorf("%q and %q should not match (key %q)", d[0], d[1], ConceptKey(d[0]))
		}
	}
	if k := ConceptKey("de la"); k != "" {
		t.Errorf("only articles = %q", k)
	}
}
