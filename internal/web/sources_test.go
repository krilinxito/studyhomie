package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/sources"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestSourcesEndpointsFindSeenDeleteAndSettings(t *testing.T) {
	cat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/w/rest.php/v1/search/page"):
			w.Write([]byte(`{"pages":[{"key":"Paradoja_del_cumplea%C3%B1os","title":"Paradoja del cumpleaños","description":"Probabilidad de coincidencias."}]}`))
		case strings.HasPrefix(r.URL.Path, "/works"):
			w.Write([]byte(`{"results":[]}`))
		case strings.HasPrefix(r.URL.Path, "/search.json"):
			w.Write([]byte(`{"docs":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer cat.Close()

	reply := "```json\n{\"picks\":[{\"n\":1,\"why\":\"Resume el cálculo paso a paso.\",\"level\":\"introductorio\"}],\"extra\":[]}\n```"
	p := &scripted{replies: []string{reply}}
	s, ts := newTestServer(t, p)
	s.Sources = &sources.Client{
		HTTP:          cat.Client(),
		Wikipedia:     cat.URL + "%.0s",
		OpenAlex:      cat.URL,
		Crossref:      cat.URL,
		OpenLibrary:   cat.URL,
		YouTubeOEmbed: cat.URL + "/oembed",
	}
	guidedNote(t, s)

	// find more material for the guide
	res, body := do(t, ts, "POST", "/api/notes/cumpleanos/sources", map[string]any{"kind": "guide", "kinds": []string{"web"}}, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), "Paradoja del cumpleaños") {
		t.Fatalf("findSources: %d %s", res.StatusCode, body)
	}
	var findOut struct {
		Added     []vault.Resource `json:"added"`
		Resources []vault.Resource `json:"resources"`
	}
	json.Unmarshal(body, &findOut)
	if len(findOut.Added) != 1 || findOut.Added[0].ID == "" {
		t.Fatalf("expected 1 added source with ID, got %+v", findOut.Added)
	}
	srcID := findOut.Added[0].ID

	// GET /api/notes/cumpleanos returns expanded resources
	res, body = do(t, ts, "GET", "/api/notes/cumpleanos", nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"id":"`+srcID+`"`) {
		t.Fatalf("note resources: %d %s", res.StatusCode, body)
	}

	// GET /api/sources lists the library with citations
	res, body = do(t, ts, "GET", "/api/sources", nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"slug":"cumpleanos"`) {
		t.Fatalf("listSources: %d %s", res.StatusCode, body)
	}

	// POST /api/sources/{slug} updates seen and user notes
	res, body = do(t, ts, "POST", "/api/sources/"+srcID, map[string]any{"seen": true, "notes": "Buen ejemplo con 23 personas."}, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"seen":true`) || !strings.Contains(string(body), "23 personas") {
		t.Fatalf("updateSource: %d %s", res.StatusCode, body)
	}

	// GET /api/sources/{slug} returns updated note
	res, body = do(t, ts, "GET", "/api/sources/"+srcID, nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), "23 personas") {
		t.Fatalf("getSource: %d %s", res.StatusCode, body)
	}

	// DELETE /api/sources/{slug} moves to trash and unlinks from guide; restore puts it back
	res, body = do(t, ts, "DELETE", "/api/sources/"+srcID, nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("deleteSource: %d %s", res.StatusCode, body)
	}
	var delOut struct {
		Trash vault.TrashItem `json:"trash"`
	}
	json.Unmarshal(body, &delOut)
	if n, _ := s.V.Load("cumpleanos"); len(n.Meta.Sources) != 0 {
		t.Errorf("guide still cites deleted source: %+v", n.Meta.Sources)
	}
	res, body = do(t, ts, "POST", "/api/trash/"+delOut.Trash.ID+"/restore", map[string]any{}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("restore: %d %s", res.StatusCode, body)
	}
	if n, _ := s.V.Load("cumpleanos"); len(n.Meta.Sources) != 1 {
		t.Errorf("guide did not relink restored source: %+v", n.Meta.Sources)
	}

	// POST /api/settings/sources saves optional keys
	res, body = do(t, ts, "POST", "/api/settings/sources", map[string]any{"youtubeKey": "yt-test", "braveKey": "br-test"}, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"youtubeKey":"yt-test"`) {
		t.Fatalf("saveSourceSettings: %d %s", res.StatusCode, body)
	}
	res, body = do(t, ts, "GET", "/api/settings", nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"braveKey":"br-test"`) {
		t.Fatalf("getSettings sources: %d %s", res.StatusCode, body)
	}
}

func TestNewServerMigratesLegacyResources(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = v.Save(vault.Note{
		Slug: "bayes",
		Meta: vault.Meta{
			Title: "Bayes", Status: "prepared",
			Resources: []vault.Resource{
				{Type: "web", Title: "Wikipedia Bayes", URL: "https://es.wikipedia.org/wiki/Teorema_de_Bayes", Why: "Buen resumen"},
				{Type: "interactive", Title: "Simulación", URL: "interactive/bayes.html"},
			},
		},
		Body: "# Bayes\n\n## Guía\n\nTexto.\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	New(config.Default(), v, &scripted{})
	n, err := v.Load("bayes")
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Meta.Sources) != 1 {
		t.Errorf("expected 1 migrated source, got %+v", n.Meta.Sources)
	}
	if len(n.Meta.Resources) != 1 || n.Meta.Resources[0].Type != "interactive" {
		t.Errorf("expected only local interactive to remain in Resources, got %+v", n.Meta.Resources)
	}
}
