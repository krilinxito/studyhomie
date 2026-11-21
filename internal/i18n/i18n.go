// Package i18n translates what StudyHomie says to the learner. Spanish is the source language: a
// message's key is its Spanish text, so Spanish needs no catalog and the code reads as it shows.
// The other languages live in locales/<code>.json, one flat object per language, and the web app
// receives the same catalog.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Lang is a language the interface speaks.
type Lang struct {
	Code   string `json:"code"`   // es, en, fr, zh, pt
	Name   string `json:"name"`   // how it reads in a language menu, in itself
	AI     string `json:"-"`      // how the prompts name it to the model
	Locale string `json:"locale"` // BCP 47 tag for dates and numbers
}

// Langs are the interface languages, Spanish first.
var Langs = []Lang{
	{Code: "es", Name: "Español", AI: "español", Locale: "es"},
	{Code: "en", Name: "English", AI: "English", Locale: "en"},
	{Code: "fr", Name: "Français", AI: "français", Locale: "fr"},
	{Code: "zh", Name: "中文（简体）", AI: "简体中文 (Simplified Chinese)", Locale: "zh-CN"},
	{Code: "pt", Name: "Português (Brasil)", AI: "português do Brasil", Locale: "pt-BR"},
}

// Source is the language the messages are written in.
const Source = "es"

// Fallback is the interface language for a configured language the interface does not speak
// (the model still writes in it).
const Fallback = "en"

// aliases are the ways a config may name each language: older configs say "español".
var aliases = map[string]string{
	"es": "es", "español": "es", "espanol": "es", "spanish": "es", "castellano": "es",
	"en": "en", "english": "en", "inglés": "en", "ingles": "en",
	"fr": "fr", "français": "fr", "francais": "fr", "french": "fr", "francés": "fr",
	"zh": "zh", "zh-cn": "zh", "zh-hans": "zh", "中文": "zh", "简体中文": "zh", "chinese": "zh", "chino": "zh",
	"pt": "pt", "pt-br": "pt", "português": "pt", "portugues": "pt", "portuguese": "pt", "português do brasil": "pt", "portugués": "pt",
}

// Code returns the interface language a configured language names, or "" when it names none
// (empty, or a language the interface does not speak).
func Code(language string) string {
	l := strings.ToLower(strings.TrimSpace(language))
	if c, ok := aliases[l]; ok {
		return c
	}
	if i := strings.IndexAny(l, "-_"); i > 0 { // a browser tag: "pt-PT", "en_US"
		if c, ok := aliases[l[:i]]; ok {
			return c
		}
	}
	return ""
}

// UI is the interface language for a configured language: its own when spoken, Spanish when
// none is set, and English for any other.
func UI(language string) string {
	if c := Code(language); c != "" {
		return c
	}
	if strings.TrimSpace(language) == "" {
		return Source
	}
	return Fallback
}

// Find returns the interface language with code c.
func Find(c string) (Lang, bool) {
	for _, l := range Langs {
		if l.Code == c {
			return l, true
		}
	}
	return Lang{}, false
}

// AI is how the prompts name the configured language to the model. A language the interface
// does not speak is passed through as written.
func AI(language string) string {
	if l, ok := Find(Code(language)); ok {
		return l.AI
	}
	if s := strings.TrimSpace(language); s != "" {
		return s
	}
	l, _ := Find(Source)
	return l.AI
}

//go:embed locales/*.json
var files embed.FS

var (
	once     sync.Once
	catalogs map[string]map[string]string
)

func load() {
	catalogs = map[string]map[string]string{}
	for _, l := range Langs {
		if l.Code == Source {
			continue
		}
		data, err := files.ReadFile("locales/" + l.Code + ".json")
		if err != nil {
			panic("missing catalog " + l.Code) // programming error: catalogs are embedded at build time
		}
		m := map[string]string{}
		if err := json.Unmarshal(data, &m); err != nil {
			panic("bad catalog " + l.Code + ": " + err.Error())
		}
		catalogs[l.Code] = m
	}
}

// Catalog returns the translations of language c (nil for Spanish or an unknown code).
func Catalog(c string) map[string]string {
	once.Do(load)
	return catalogs[c]
}

// T translates msg into language c. Leading and trailing whitespace is kept out of the key, so
// "\nListo.\n" is looked up as "Listo.". A message the catalog lacks stays in Spanish.
func T(c, msg string) string {
	cat := Catalog(c)
	if cat == nil {
		return msg
	}
	core := strings.TrimSpace(msg)
	tr, ok := cat[core]
	if !ok || tr == "" {
		return msg
	}
	start := strings.Index(msg, core)
	return msg[:start] + tr + msg[start+len(core):]
}

// N marks a message to translate later, where it is defined apart from where it is shown (a
// list of choices, a map of names). It returns s as it is.
func N(s string) string { return s }

// Sprintf translates format into language c and fills it in.
func Sprintf(c, format string, a ...any) string { return fmt.Sprintf(T(c, format), a...) }

// headings are the sections the program looks for in notes, by their Spanish name, in every
// language. A note keeps working when the language changes: any of these finds the section.
var headings = map[string][]string{
	"Guía":           {"Guía", "Guide", "Guide d'étude", "学习指南", "Guia"},
	"Conexiones":     {"Conexiones", "Connections", "Connexions", "关联", "Conexões"},
	"Dudas":          {"Dudas", "Questions asked", "Questions posées", "疑问", "Dúvidas"},
	"Conceptos base": {"Conceptos base", "Background", "Notions de base", "基础概念", "Conceitos base"},
	"Prueba":         {"Prueba", "Quiz", "Quiz", "测验", "Teste"},
	"Feynman":        {"Feynman", "Feynman", "Feynman", "费曼讲解", "Feynman"},
	"Recursos":       {"Recursos", "Resources", "Ressources", "资源", "Recursos"},
	"Material":       {"Material", "Material", "Matériel", "学习资料", "Material"},
	"Aparece en":     {"Aparece en", "Appears in", "Apparaît dans", "出现于", "Aparece em"},
	"Alcance":        {"Alcance", "Scope", "Portée", "范围", "Escopo"},
	"Cómo avanzar":   {"Cómo avanzar", "How to move on", "Comment avancer", "如何推进", "Como avançar"},
	"Ruta":           {"Ruta", "Path", "Parcours", "路线", "Trilha"},
	// the sections of a concept note, which the model writes
	"En una frase":    {"En una frase", "In one sentence", "En une phrase", "一句话", "Em uma frase"},
	"Intuición":       {"Intuición", "Intuition", "Intuition", "直觉", "Intuição"},
	"Cómo funciona":   {"Cómo funciona", "How it works", "Comment ça marche", "原理", "Como funciona"},
	"Ejemplo":         {"Ejemplo", "Example", "Exemple", "例子", "Exemplo"},
	"Errores comunes": {"Errores comunes", "Common mistakes", "Erreurs fréquentes", "常见错误", "Erros comuns"},
	"Relacionado":     {"Relacionado", "Related", "Voir aussi", "相关", "Relacionado"},
}

// LocalizeHeadings rewrites the "## Heading" lines of a text written in Spanish (a prompt that
// shows the model which sections to write) into language c, and quoted section names with them.
func LocalizeHeadings(text, c string) string {
	if c == Source {
		return text
	}
	for name := range headings {
		h := Heading(c, name)
		text = strings.ReplaceAll(text, "## "+name+"\n", "## "+h+"\n")
		text = strings.ReplaceAll(text, `"`+name+`"`, `"`+h+`"`)
	}
	return text
}

// Heading is the section named name (its Spanish heading) as written in language c.
func Heading(c, name string) string {
	hs, ok := headings[name]
	if !ok {
		return name
	}
	for i, l := range Langs {
		if l.Code == c {
			return hs[i]
		}
	}
	return name
}

// SameHeading reports whether two section headings name the same section, in any languages.
func SameHeading(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == b {
		return true
	}
	for _, hs := range headings {
		inA, inB := false, false
		for _, h := range hs {
			inA = inA || h == a
			inB = inB || h == b
		}
		if inA && inB {
			return true
		}
	}
	return false
}

// Headings returns every section heading by its Spanish name, in the order of Langs.
func Headings() map[string][]string { return headings }
