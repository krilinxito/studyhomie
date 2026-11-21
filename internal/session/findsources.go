package session

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/krilinxito/studyhomie/internal/prompts"
	"github.com/krilinxito/studyhomie/internal/sources"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// SourceFilter narrows «Buscar más material».
type SourceFilter struct {
	Kinds []string `json:"kinds"` // video | web | book | paper; empty means any
	Easy  bool     `json:"easy"`  // something easier to start with
}

// maxPicks bounds how much material one search adds.
const maxPicks = 5

// FindSources looks for more material on a guide or a concept in public catalogues, lets the AI
// pick the best and say why each one helps, files them in fuentes/ and cites them from the note.
// Only what a catalogue returned (or a link the AI found and the app verified) gets in, so it works
// even when the AI cannot search the web. It returns the sources it added.
func (s *Session) FindSources(ctx context.Context, dir, slug string, f SourceFilter) ([]vault.Resource, error) {
	if s.Sources == nil {
		return nil, inputErrorf("la búsqueda de material no está disponible")
	}
	var title, about, area string
	var have []vault.SourceRef
	switch dir {
	case vault.GuidesDir:
		n, err := s.V.Load(slug)
		if err != nil {
			return nil, inputErrorf("no existe el tema «%s»", slug)
		}
		title, about, area, have = n.Meta.Title, head(s.noteContext(slug, n), 1500), noteArea(n), n.Meta.Sources
	case vault.ConceptsDir:
		c, err := s.V.LoadConcept(slug)
		if err != nil {
			return nil, inputErrorf("no existe el concepto «%s»", slug)
		}
		title, about, area, have = c.Meta.Title, c.Meta.Summary, c.Meta.Category, c.Meta.Sources
	default:
		return nil, failf("no sé buscar material para %q", dir)
	}
	if title == "" {
		title = slug
	}
	cited := map[string]bool{}
	var haveLines []string
	for _, r := range s.V.Expand(have) {
		cited[vault.SourceKey(r)] = true
		haveLines = append(haveLines, "- "+r.Title)
	}

	s.status("Buscando material en catálogos públicos")
	var cands []vault.Resource
	for _, r := range s.Sources.Search(ctx, sources.Query{Text: title, Lang: s.lang(), Kinds: f.Kinds, Easy: f.Easy, Limit: 5}) {
		if !cited[vault.SourceKey(r)] {
			cands = append(cands, r)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "TOPIC: %s\n", title)
	if about != "" {
		fmt.Fprintf(&b, "WHAT THE LEARNER STUDIES: %s\n", about)
	}
	switch {
	case f.Easy:
		b.WriteString("THEY ASKED FOR: something easier, to start with\n")
	case len(f.Kinds) > 0:
		fmt.Fprintf(&b, "THEY ASKED FOR: %s\n", strings.Join(f.Kinds, ", "))
	}
	if len(haveLines) > 0 {
		b.WriteString("\nALREADY HAVE:\n" + strings.Join(haveLines, "\n") + "\n")
	}
	b.WriteString("\nCANDIDATES:\n")
	if len(cands) == 0 {
		b.WriteString("(none)\n")
	}
	for i, r := range cands {
		site := ""
		if u, err := url.Parse(r.URL); err == nil {
			site = u.Hostname()
		}
		fmt.Fprintf(&b, "%d. [%s] %s", i+1, r.Type, r.Title)
		for _, x := range []string{r.Author, yearOf(r.Year), site, r.Duration, "reliability " + r.Reliability} {
			if strings.TrimSpace(x) != "" && x != "reliability " {
				b.WriteString(" · " + x)
			}
		}
		if r.Summary != "" {
			b.WriteString("\n   " + head(r.Summary, 240))
		}
		b.WriteString("\n")
	}

	s.status("Eligiendo lo que más te sirve")
	c := s.quickConvo(prompts.Get("sources", s.aiLang()))
	c.Tools = []string{"WebSearch"} // used only to add an extra the app then checks
	c.Timeout = callTimeout(s.Cfg.Timeout, c.Tools)
	resp, err := c.Say(ctx, b.String(), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Picks []struct {
			N     int    `json:"n"`
			Why   string `json:"why"`
			Level string `json:"level"`
		} `json:"picks"`
		Extra []vault.Resource `json:"extra"`
	}
	_, payload := splitJSON(resp.Text)
	if payload == "" {
		payload = resp.Text
	}
	if err := decodeJSON(payload, &out); err != nil {
		return nil, err
	}
	var chosen []vault.Resource
	used := map[int]bool{}
	for _, p := range out.Picks {
		if p.N < 1 || p.N > len(cands) || used[p.N] || len(chosen) >= maxPicks {
			continue // only what a catalogue returned
		}
		used[p.N] = true
		r := cands[p.N-1]
		r.Why, r.Level = strings.TrimSpace(p.Why), level(p.Level, r.Level)
		chosen = append(chosen, r)
	}
	// links the AI found on its own count only once they are checked
	var extra []vault.Resource
	for _, r := range out.Extra {
		if len(chosen)+len(extra) >= maxPicks || !strings.HasPrefix(r.URL, "http") || cited[vault.SourceKey(r)] {
			continue
		}
		r.Level = level(r.Level, "")
		extra = append(extra, r)
	}
	if len(extra) > 0 {
		for _, r := range s.enrich(ctx, s.verified(ctx, extra)) {
			chosen = append(chosen, r)
		}
	}
	if len(chosen) == 0 {
		return nil, inputErrorf("no encontré material nuevo que valga la pena para esto; prueba con otro filtro")
	}
	for i := range chosen {
		if chosen[i].Area == "" {
			chosen[i].Area = area
		}
	}
	refs, err := s.V.FileSources(chosen)
	if err != nil {
		return nil, err
	}
	if err := s.V.SetSourcesOf(dir, slug, func(h []vault.SourceRef) []vault.SourceRef { return vault.MergeRefs(h, refs) }, s.h("Recursos")); err != nil {
		return nil, err
	}
	return s.V.Expand(refs), nil
}

func yearOf(y int) string {
	if y <= 0 {
		return ""
	}
	return fmt.Sprint(y)
}

// level keeps a reading level the app knows, else the fallback.
func level(l, fallback string) string {
	switch l = strings.ToLower(strings.TrimSpace(l)); l {
	case "introductorio", "intermedio", "avanzado":
		return l
	}
	return fallback
}
