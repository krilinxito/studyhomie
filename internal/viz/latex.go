package viz

import "strings"

// latexWords are LaTeX commands that start with a letter JSON would read as an escape
// (\b \f \n \r \t \u): "\frac" is a valid JSON string, a form feed followed by "rac".
var latexWords = []string{
	"frac", "begin", "bar", "beta", "big", "binom", "bmatrix", "boldsymbol", "bf", "bot",
	"forall", "flat", "neq", "nabla", "nu", "not", "ne", "notin", "nless", "newline",
	"right", "rho", "rangle", "rceil", "rfloor", "rm", "rightarrow",
	"theta", "tau", "times", "text", "tan", "tanh", "to", "top", "tilde", "textbf", "tfrac",
	"underline", "upsilon", "uparrow",
}

// fixLatexEscapes doubles the backslashes of LaTeX commands written inside JSON strings with
// a single one, the most common slip in model-written widgets. Valid escapes are left alone.
func fixLatexEscapes(s string) string {
	var b strings.Builder
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inString = !inString
			b.WriteByte(c)
			continue
		}
		if c != '\\' || !inString || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		next := s[i+1]
		switch {
		case next == '\\' || next == '"' || next == '/':
			b.WriteByte(c) // an escape pair: keep it whole
			b.WriteByte(next)
			i++
		case strings.IndexByte("bfnrtu", next) >= 0 && !isLatexWord(s[i+1:]):
			b.WriteByte(c) // a real escape such as \n
		case next >= 'a' && next <= 'z' || next >= 'A' && next <= 'Z' || strings.IndexByte("{}[](),;:!| ", next) >= 0:
			b.WriteString(`\\`) // a LaTeX command or symbol: \sin, \{, \,
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isLatexWord(rest string) bool {
	for _, w := range latexWords {
		if strings.HasPrefix(rest, w) {
			end := len(w)
			if end == len(rest) || !(rest[end] >= 'a' && rest[end] <= 'z' || rest[end] >= 'A' && rest[end] <= 'Z') {
				return true
			}
		}
	}
	return false
}
