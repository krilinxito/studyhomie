package viz

import (
	"fmt"
	"strings"
	"unicode"
)

// The expression language the widgets share with the browser (static/viz/expr.js):
// numbers, variables, + - * / ^ (or **), parentheses, implicit multiplication ("2x", "3(x+1)"),
// the constants pi and e, and the functions below. Keep both sides in step.
var funcs = map[string]bool{
	"sin": true, "cos": true, "tan": true, "asin": true, "acos": true, "atan": true,
	"sinh": true, "cosh": true, "tanh": true, "exp": true, "log": true, "ln": true, "log10": true, "log2": true,
	"sqrt": true, "abs": true, "floor": true, "ceil": true, "round": true, "sign": true,
	"min": true, "max": true, "atan2": true, "pow": true, "mod": true,
}

var constants = map[string]bool{"pi": true, "e": true}

// reserved names cannot be used as parameters.
var reserved = func() map[string]bool {
	m := map[string]bool{"x": true}
	for k := range funcs {
		m[k] = true
	}
	for k := range constants {
		m[k] = true
	}
	return m
}()

func isIdent(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for i, r := range s {
		if r > unicode.MaxASCII || !(unicode.IsLetter(r) || r == '_' || (i > 0 && unicode.IsDigit(r))) {
			return false
		}
	}
	return true
}

type exprParser struct {
	toks []string
	pos  int
	vars map[string]bool
}

// CheckExpr reports whether s is a valid expression over vars.
func CheckExpr(s string, vars map[string]bool) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("expresión vacía")
	}
	if len(s) > maxExpr {
		return fmt.Errorf("expresión demasiado larga")
	}
	toks, err := tokenize(s)
	if err != nil {
		return fmt.Errorf("expresión %q: %w", s, err)
	}
	p := &exprParser{toks: toks, vars: vars}
	if err := p.sum(); err != nil {
		return fmt.Errorf("expresión %q: %w", s, err)
	}
	if p.pos != len(p.toks) {
		return fmt.Errorf("expresión %q: sobra %q", s, p.toks[p.pos])
	}
	return nil
}

func tokenize(s string) ([]string, error) {
	var toks []string
	r := []rune(s)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case unicode.IsDigit(c) || c == '.':
			j := i
			for j < len(r) && (unicode.IsDigit(r[j]) || r[j] == '.') {
				j++
			}
			if j < len(r) && (r[j] == 'e' || r[j] == 'E') && j+1 < len(r) && (unicode.IsDigit(r[j+1]) || ((r[j+1] == '-' || r[j+1] == '+') && j+2 < len(r) && unicode.IsDigit(r[j+2]))) {
				j += 2
				for j < len(r) && unicode.IsDigit(r[j]) {
					j++
				}
			}
			num := string(r[i:j])
			if strings.Count(num, ".") > 1 || num == "." {
				return nil, fmt.Errorf("número inválido %q", num)
			}
			toks = append(toks, num)
			i = j
		case c < 128 && (unicode.IsLetter(c) || c == '_'):
			j := i
			for j < len(r) && r[j] < 128 && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '_') {
				j++
			}
			toks = append(toks, string(r[i:j]))
			i = j
		case c == '*' && i+1 < len(r) && r[i+1] == '*':
			toks = append(toks, "^")
			i += 2
		case strings.ContainsRune("+-*/^(),", c):
			toks = append(toks, string(c))
			i++
		default:
			return nil, fmt.Errorf("carácter no permitido %q", c)
		}
	}
	return toks, nil
}

func (p *exprParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *exprParser) sum() error {
	if err := p.product(); err != nil {
		return err
	}
	for p.peek() == "+" || p.peek() == "-" {
		p.pos++
		if err := p.product(); err != nil {
			return err
		}
	}
	return nil
}

// startsPrimary reports whether tok can begin an implicit multiplication factor.
func startsPrimary(tok string) bool {
	if tok == "" {
		return false
	}
	c := rune(tok[0])
	return tok == "(" || unicode.IsDigit(c) || c == '.' || unicode.IsLetter(c) || c == '_'
}

func (p *exprParser) product() error {
	if err := p.unary(); err != nil {
		return err
	}
	for {
		switch t := p.peek(); {
		case t == "*" || t == "/":
			p.pos++
		case startsPrimary(t):
			// implicit multiplication
		default:
			return nil
		}
		if err := p.unary(); err != nil {
			return err
		}
	}
}

func (p *exprParser) unary() error {
	if t := p.peek(); t == "-" || t == "+" {
		p.pos++
		return p.unary()
	}
	return p.power()
}

func (p *exprParser) power() error {
	if err := p.primary(); err != nil {
		return err
	}
	if p.peek() == "^" {
		p.pos++
		return p.unary()
	}
	return nil
}

func (p *exprParser) primary() error {
	t := p.peek()
	if t == "" {
		return fmt.Errorf("termina antes de tiempo")
	}
	p.pos++
	switch {
	case t == "(":
		if err := p.sum(); err != nil {
			return err
		}
		if p.peek() != ")" {
			return fmt.Errorf("falta )")
		}
		p.pos++
		return nil
	case unicode.IsDigit(rune(t[0])) || t[0] == '.':
		return nil
	case funcs[t]:
		if p.peek() != "(" {
			return fmt.Errorf("%s necesita paréntesis", t)
		}
		p.pos++
		if err := p.sum(); err != nil {
			return err
		}
		for p.peek() == "," {
			p.pos++
			if err := p.sum(); err != nil {
				return err
			}
		}
		if p.peek() != ")" {
			return fmt.Errorf("falta ) en %s", t)
		}
		p.pos++
		return nil
	case constants[t] || p.vars[t]:
		return nil
	case isIdent(t):
		return fmt.Errorf("variable desconocida %q", t)
	}
	return fmt.Errorf("símbolo inesperado %q", t)
}
