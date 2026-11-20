package i18n

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"
)

// goCalls are the functions whose first string argument is a message to translate.
var goCalls = map[string]int{
	"printf": 0, "termf": 0, "status": 0, "readLine": 0, "confirm": 0, "tr": 0, "trf": 0,
	"inputErrorf": 0, "failf": 0, "N": 0, "need": 0, "fail": 2, "Sprintf": -1,
}

// literal evaluates a string literal or a sum of them.
func literal(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		a, ok1 := literal(x.X)
		b, ok2 := literal(x.Y)
		return a + b, ok1 && ok2
	case *ast.ParenExpr:
		return literal(x.X)
	}
	return "", false
}

// key is how a message is looked up: without its surrounding whitespace. Messages without
// letters ("%s", "\n") are not keys.
func key(s string) (string, bool) {
	s = strings.TrimSpace(s)
	for _, r := range regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]|\{\w+\}`).ReplaceAllString(s, "") {
		if unicode.IsLetter(r) {
			return s, true
		}
	}
	return "", false
}

// goKeys collects the messages of the Go packages that talk to the learner.
func goKeys(t *testing.T, keys map[string]string) {
	t.Helper()
	fset := token.NewFileSet()
	for _, dir := range []string{"../session", "../web", "../providers"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var name string
				switch fn := call.Fun.(type) {
				case *ast.Ident:
					name = fn.Name
				case *ast.SelectorExpr:
					name = fn.Sel.Name
					if x, ok := fn.X.(*ast.Ident); ok && x.Name == "fmt" {
						return true // fmt.Sprintf is not a translation
					}
				}
				arg, ok := goCalls[name]
				if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && (name == "T" || name == "Sprintf") {
					if x, ok2 := sel.X.(*ast.Ident); ok2 && x.Name == "i18n" {
						arg, ok = 1, true // i18n.T(lang, msg), i18n.Sprintf(lang, format, …)
					}
				}
				if !ok || arg < 0 || len(call.Args) <= arg {
					return true
				}
				if s, ok := literal(call.Args[arg]); ok {
					if k, ok := key(s); ok {
						keys[k] = fset.Position(call.Pos()).String()
					}
				}
				return true
			})
		}
	}
}

var (
	jsCall = regexp.MustCompile(`\b(?:t|tr)\('((?:[^'\\]|\\.)*)'`)
	jsAttr = regexp.MustCompile(`data-i18n(?:-[a-z]+)?="([^"]*)"`)
	// tn(n, 'one', 'many'): both forms
	jsPlural = regexp.MustCompile(`\btn\([^,]+,\s*'((?:[^'\\]|\\.)*)',\s*'((?:[^'\\]|\\.)*)'`)
)

// jsKeys collects t('…') in the web app's scripts and data-i18n attributes in its page.
func jsKeys(t *testing.T, keys map[string]string) {
	t.Helper()
	files, _ := filepath.Glob("../web/static/*.js")
	files = append(files, "../web/static/index.html")
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		re := jsCall
		if strings.HasSuffix(path, ".html") {
			re = jsAttr
		}
		matches := re.FindAllStringSubmatch(string(data), -1)
		if re == jsCall {
			for _, m := range jsPlural.FindAllStringSubmatch(string(data), -1) {
				matches = append(matches, []string{m[0], m[1]}, []string{m[0], m[2]})
			}
		}
		for _, m := range matches {
			s := strings.NewReplacer(`\'`, `'`, `\\`, `\`, `\n`, "\n").Replace(m[1])
			if re == jsAttr {
				s = html.UnescapeString(m[1])
			}
			if k, ok := key(s); ok {
				keys[k] = path
			}
		}
	}
}

var verbs = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]|\{\w+\}`)

func placeholders(s string) string {
	v := verbs.FindAllString(s, -1)
	if strings.Contains(s, "{") {
		sort.Strings(v) // named placeholders may move in a translation
	}
	return strings.Join(v, " ")
}

// Every message the program shows has a translation in every catalog, with the same
// placeholders, and no catalog keeps translations of messages that are gone.
// I18N_DUMP=dir writes the missing ones to dir/<code>.json, to fill in.
func TestCatalogsCoverEveryMessage(t *testing.T) {
	keys := map[string]string{}
	goKeys(t, keys)
	jsKeys(t, keys)
	if len(keys) < 50 {
		t.Fatalf("found only %d messages: is the extraction broken?", len(keys))
	}
	for _, l := range Langs {
		if l.Code == Source {
			continue
		}
		cat := Catalog(l.Code)
		missing := map[string]string{}
		for k, where := range keys {
			tr, ok := cat[k]
			switch {
			case !ok || strings.TrimSpace(tr) == "":
				missing[k] = ""
				if len(missing) <= 5 {
					t.Errorf("%s: missing %q (%s)", l.Code, k, where)
				}
			case placeholders(tr) != placeholders(k):
				t.Errorf("%s: %q has placeholders %q, want %q", l.Code, tr, placeholders(tr), placeholders(k))
			}
		}
		if len(missing) > 5 {
			t.Errorf("%s: %d more missing", l.Code, len(missing)-5)
		}
		for k := range cat {
			if _, ok := keys[k]; !ok {
				t.Errorf("%s: %q is not used anymore", l.Code, k)
			}
		}
		if dir := os.Getenv("I18N_DUMP"); dir != "" && len(missing) > 0 {
			data, _ := json.MarshalIndent(missing, "", "  ")
			os.WriteFile(filepath.Join(dir, l.Code+".json"), data, 0o644)
		}
	}
}

func TestLanguages(t *testing.T) {
	cases := map[string][2]string{
		"español": {"es", "español"}, "Español": {"es", "español"}, "": {"es", "español"},
		"en": {"en", "English"}, "pt-PT": {"pt", "português do Brasil"}, "zh-CN": {"zh", "简体中文 (Simplified Chinese)"},
		"Deutsch": {"en", "Deutsch"}, // the interface falls back to English; the model writes German
	}
	for in, want := range cases {
		if got := UI(in); got != want[0] {
			t.Errorf("UI(%q) = %q, want %q", in, got, want[0])
		}
		if got := AI(in); got != want[1] {
			t.Errorf("AI(%q) = %q, want %q", in, got, want[1])
		}
	}
}

func TestTranslateKeepsWhitespaceAndFallsBack(t *testing.T) {
	catalogs = map[string]map[string]string{"en": {"Listo.": "Done."}}
	once.Do(func() {}) // the fake catalog stays
	t.Cleanup(func() { once = sync.Once{}; catalogs = nil })
	if got := T("en", "\nListo.\n"); got != "\nDone.\n" {
		t.Errorf("T = %q", got)
	}
	if got := T("en", "Otra cosa"); got != "Otra cosa" {
		t.Errorf("a missing message stays in Spanish: %q", got)
	}
	if got := T("es", "Listo."); got != "Listo." {
		t.Errorf("Spanish is the source: %q", got)
	}
}

func TestHeadings(t *testing.T) {
	if Heading("en", "Guía") != "Guide" || Heading("es", "Guía") != "Guía" || Heading("en", "Otra") != "Otra" {
		t.Error("Heading")
	}
	if !SameHeading("Guide", "Guía") || !SameHeading("学习指南", "Guia") || SameHeading("Guía", "Dudas") {
		t.Error("SameHeading")
	}
	for name, hs := range Headings() {
		if len(hs) != len(Langs) || hs[0] != name {
			t.Errorf("%s: %v must list one heading per language, Spanish first", name, hs)
		}
	}
}
