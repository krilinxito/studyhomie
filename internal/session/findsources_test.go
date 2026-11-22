package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/sources"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestFindSourcesPicksOnlyWhatCataloguesReturned(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/es/w/rest.php/v1/search/page", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"pages":[{"key":"Teorema_de_Bayes","title":"Teorema de Bayes","description":"probabilidad"},{"key":"Bayes_viejo","title":"Ya citada","description":"x"}]}`))
	})
	mux.HandleFunc("/search.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"docs":[{"key":"/works/OL1W","title":"Probability Theory","author_name":["Jaynes"],"first_publish_year":2003}]}`))
	})
	mux.HandleFunc("/works", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"results":[]}`)) })
	mux.HandleFunc("/oembed", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cl := sources.New("", "")
	cl.Wikipedia, cl.OpenAlex, cl.OpenLibrary, cl.YouTubeOEmbed = srv.URL+"/%s", srv.URL, srv.URL, srv.URL+"/oembed"

	pick := "```json\n" + `{"picks":[{"n":2,"why":"Para empezar por el libro","level":"intermedio"},{"n":1,"why":"Lo básico","level":"introductorio"},{"n":9,"why":"inventado"}],"extra":[{"type":"video","title":"No existe","url":"https://www.youtube.com/watch?v=AAAAAAAAAAA","why":"x"}]}` + "\n```"
	f := &fake{replies: []provider.Response{reply(pick, 10)}}
	s, _ := newSession(t, f, "")
	s.Sources = cl
	s.Cfg.Language = "es"
	old := vault.Resource{Type: "web", Title: "Ya citada", URL: srv.URL + "/es/wiki/Bayes_viejo"}
	refs, _ := s.V.FileSources([]vault.Resource{old})
	if err := s.V.Save(vault.Note{Slug: "bayes", Meta: vault.Meta{Title: "Teorema de Bayes", Area: "matematicas", Sources: refs}, Body: "## Guía\n\nTexto."}); err != nil {
		t.Fatal(err)
	}

	added, err := s.FindSources(context.Background(), vault.GuidesDir, "bayes", SourceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 || added[0].Type != "book" || added[0].Why != "Para empezar por el libro" || added[0].Level != "intermedio" || added[1].Title != "Teorema de Bayes" {
		t.Fatalf("added (the invented number and the video that does not exist are out): %+v", added)
	}
	p := f.reqs[0].Prompt
	if strings.Contains(p, "1. [web] Ya citada") || !strings.Contains(p, "ALREADY HAVE:\n- Ya citada") {
		t.Errorf("an already cited source must not be offered again:\n%s", p)
	}
	n, _ := s.V.Load("bayes")
	if len(n.Meta.Sources) != 3 || !strings.Contains(vault.Section(n.Body, "Recursos"), "Para empezar por el libro") {
		t.Errorf("guide: %+v\n%s", n.Meta.Sources, n.Body)
	}
	if src, _ := s.V.LoadSource(added[0].ID); src.Meta.Area != "matematicas" {
		t.Errorf("area: %+v", src.Meta)
	}

	// a concept gets its own list
	if err := s.V.SaveConcept(vault.Concept{Slug: "prior", Meta: vault.ConceptMeta{Title: "Prior"}, Body: "## En una frase\n\nx\n"}); err != nil {
		t.Fatal(err)
	}
	f.replies = append(f.replies, reply("```json\n{\"picks\":[{\"n\":1,\"why\":\"base\"}],\"extra\":[]}\n```", 10))
	if _, err := s.FindSources(context.Background(), vault.ConceptsDir, "prior", SourceFilter{Kinds: []string{"web"}}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.V.LoadConcept("prior")
	if len(c.Meta.Sources) != 1 || !strings.Contains(c.Body, "[[fuentes/") {
		t.Errorf("concept: %+v\n%s", c.Meta.Sources, c.Body)
	}

	// without a catalogue client it says so instead of asking the AI
	s.Sources = nil
	if _, err := s.FindSources(context.Background(), vault.GuidesDir, "bayes", SourceFilter{}); !IsInputError(err) {
		t.Errorf("no client: %v", err)
	}
}
