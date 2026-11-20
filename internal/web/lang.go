package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/i18n"
)

// i18nPage is what the page learns before app.js runs: its language and that language's catalog.
type i18nPage struct {
	Lang     string              `json:"lang"`   // interface language code
	Locale   string              `json:"locale"` // for dates and numbers
	Chosen   bool                `json:"chosen"` // the learner (or the config) picked it; false: the page may pick from the browser
	Langs    []i18n.Lang         `json:"langs"`
	Catalog  map[string]string   `json:"catalog"`  // Spanish message → translation; empty for Spanish
	Headings map[string][]string `json:"headings"` // note sections by Spanish name, in every language
}

// i18nScript serves the language of the page as a script, so app.js can translate from its first
// line. It is never cached: changing the language reloads the page.
func (s *Server) i18nScript(w http.ResponseWriter, r *http.Request) {
	_, cfg, _ := s.backend()
	code := i18n.UI(cfg.Language)
	l, _ := i18n.Find(code)
	page := i18nPage{
		Lang: code, Locale: l.Locale, Chosen: strings.TrimSpace(cfg.Language) != "",
		Langs: i18n.Langs, Catalog: i18n.Catalog(code), Headings: i18n.Headings(),
	}
	if page.Catalog == nil {
		page.Catalog = map[string]string{}
	}
	data, err := json.Marshal(page)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write([]byte("window.I18N = "))
	w.Write(data)
	w.Write([]byte(";\n" + translateJS))
}

// translateJS gives every script of the page t(msg, vars) and tn(n, one, many). Messages are
// written in Spanish, the Spanish text is the key, and {name} marks a value.
const translateJS = `function t(msg, vars) {
  const core = msg.trim(); // as in Go: the key leaves out the surrounding spaces, the result keeps them
  const tr = (window.I18N.catalog || {})[core];
  const s = tr ? msg.replace(core, () => tr) : msg;
  return vars ? s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m)) : s;
}
function tn(n, one, many) { return t(n === 1 ? one : many, { n }); }
`

// lang is the interface language in use.
func (s *Server) lang() string {
	_, cfg, _ := s.backend()
	return i18n.UI(cfg.Language)
}

// setLanguage saves the language of the interface and of what the AI writes to studyhomie.toml.
// A running session keeps the language it started with.
func (s *Server) setLanguage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Language string `json:"language"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if _, ok := i18n.Find(body.Language); !ok {
		fail(w, 400, "idioma desconocido")
		return
	}
	s.pmu.Lock()
	cfg := s.Cfg
	cfg.Language = body.Language
	if s.ConfigPath != "" {
		if err := config.Write(s.ConfigPath, cfg); err != nil {
			s.pmu.Unlock()
			fail(w, 500, "no pude guardar "+s.ConfigPath+": "+err.Error())
			return
		}
	}
	s.Cfg = cfg
	s.pmu.Unlock()
	writeJSON(w, 200, map[string]string{"language": body.Language})
}
