package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/vault"
)

// fakeCatalogues answers like the real services, without the network.
func fakeCatalogues(t *testing.T) (*Client, *[]string) {
	var hits []string
	mux := http.NewServeMux()
	mux.HandleFunc("/es/w/rest.php/v1/search/page", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "wikipedia:"+r.URL.Query().Get("q"))
		w.Write([]byte(`{"pages":[{"key":"Teorema_de_Bayes","title":"Teorema de Bayes","description":"resultado de probabilidad"}]}`))
	})
	mux.HandleFunc("/works", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "openalex")
		w.Write([]byte(`{"results":[{"display_name":"A tutorial on Bayes","publication_year":2011,"doi":"https://doi.org/10.1000/XYZ123","cited_by_count":250,"authorships":[{"author":{"display_name":"Ana"}},{"author":{"display_name":"Luis"}}],"primary_location":{"source":{"display_name":"Journal of Stats"}}},{"display_name":"Sin enlace"}]}`))
	})
	mux.HandleFunc("/works/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "missing") {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"message":{"title":["Real title"],"issued":{"date-parts":[[2019,3]]},"author":[{"given":"Ada","family":"Lovelace"}],"is-referenced-by-count":40}}`))
	})
	mux.HandleFunc("/search.json", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "openlibrary")
		w.Write([]byte(`{"docs":[{"key":"/works/OL1W","title":"Probability Theory","author_name":["E. T. Jaynes"],"first_publish_year":2003,"isbn":["9780521592710"],"language":["eng"]}]}`))
	})
	mux.HandleFunc("/oembed", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("url"), "AAAAAAAAAAA") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"title":"Bayes theorem, the geometry of changing beliefs","author_name":"3Blue1Brown"}`))
	})
	mux.HandleFunc("/yt/search", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "youtube")
		if r.URL.Query().Get("key") != "ykey" {
			t.Errorf("youtube key missing")
		}
		w.Write([]byte(`{"items":[{"id":{"videoId":"HZGCoVF3YvM"},"snippet":{"title":"Bayes &amp; you","channelTitle":"3Blue1Brown","publishedAt":"2019-12-22T00:00:00Z"}}]}`))
	})
	mux.HandleFunc("/yt/videos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"items":[{"id":"HZGCoVF3YvM","contentDetails":{"duration":"PT15M11S"}}]}`))
	})
	mux.HandleFunc("/res/v1/web/search", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "brave")
		if r.Header.Get("X-Subscription-Token") != "bkey" {
			t.Errorf("brave key missing")
		}
		w.Write([]byte(`{"web":{"results":[{"title":"<b>Bayes</b> explained","url":"https://www.khanacademy.org/bayes?utm_source=x","description":"intro"},{"title":"Dup","url":"http://` + r.Host + `/es/wiki/Teorema_de_Bayes"}]}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := New("ykey", "bkey")
	c.Wikipedia = srv.URL + "/%s"
	c.OpenAlex, c.Crossref, c.OpenLibrary = srv.URL, srv.URL, srv.URL
	c.YouTubeOEmbed, c.YouTubeAPI, c.Brave = srv.URL+"/oembed", srv.URL+"/yt", srv.URL
	return c, &hits
}

func TestSearchAsksEveryCatalogueAndDedups(t *testing.T) {
	c, hits := fakeCatalogues(t)
	rs := c.Search(context.Background(), Query{Text: "teorema de bayes", Lang: "es"})
	byType := map[string][]vault.Resource{}
	for _, r := range rs {
		byType[r.Type] = append(byType[r.Type], r)
	}
	if len(*hits) != 5 {
		t.Errorf("catalogues asked: %v", *hits)
	}
	if w := byType["web"]; len(w) != 2 {
		t.Errorf("web (the Brave copy of the Wikipedia page is dropped): %+v", w)
	}
	p := byType["paper"]
	if len(p) != 1 || p[0].DOI != "10.1000/xyz123" || p[0].Reliability != "alta" || p[0].Author != "Ana, Luis" || p[0].Level != "avanzado" {
		t.Errorf("paper (the one without a link is dropped): %+v", p)
	}
	b := byType["book"]
	if len(b) != 1 || b[0].ISBN != "9780521592710" || b[0].Lang != "en" || !strings.HasSuffix(b[0].URL, "/works/OL1W") {
		t.Errorf("book: %+v", b)
	}
	v := byType["video"]
	if len(v) != 1 || v[0].Duration != "15:11" || v[0].Title != "Bayes & you" || v[0].Reliability != "alta" {
		t.Errorf("video: %+v", v)
	}

	// filters: only videos; «easier» never brings papers
	*hits = nil
	c.Search(context.Background(), Query{Text: "x", Kinds: []string{"video"}})
	if len(*hits) != 1 || (*hits)[0] != "youtube" {
		t.Errorf("videos only: %v", *hits)
	}
	*hits = nil
	c.Search(context.Background(), Query{Text: "x", Easy: true})
	for _, h := range *hits {
		if h == "openalex" {
			t.Error("easy material must not search papers")
		}
	}
	// without keys the keyed catalogues are not asked
	*hits = nil
	c.YouTubeKey, c.BraveKey = "", ""
	c.Search(context.Background(), Query{Text: "x"})
	for _, h := range *hits {
		if h == "youtube" || h == "brave" {
			t.Errorf("asked %s without a key", h)
		}
	}
}

func TestVerifyChecksVideosAndDOIs(t *testing.T) {
	c, _ := fakeCatalogues(t)
	ctx := context.Background()
	r, ok := c.Verify(ctx, vault.Resource{Type: "video", Title: "made-up title", URL: "https://youtu.be/HZGCoVF3YvM"})
	if !ok || r.Title != "Bayes theorem, the geometry of changing beliefs" || r.Author != "3Blue1Brown" || r.Reliability != "alta" {
		t.Errorf("video: %+v %v", r, ok)
	}
	if _, ok := c.Verify(ctx, vault.Resource{Type: "video", URL: "https://www.youtube.com/watch?v=AAAAAAAAAAA"}); ok {
		t.Error("a video oEmbed does not know must be dropped")
	}
	r, ok = c.Verify(ctx, vault.Resource{Type: "web", Title: "x", URL: "https://doi.org/10.1234/abc"})
	if !ok || r.Type != "paper" || r.Title != "Real title" || r.Year != 2019 || r.Author != "Ada Lovelace" || r.Citations != 40 {
		t.Errorf("doi: %+v", r)
	}
	if _, ok := c.Verify(ctx, vault.Resource{Type: "paper", URL: "https://doi.org/10.1234/missing"}); ok {
		t.Error("a DOI Crossref does not know must be dropped")
	}
	// a network failure keeps what the model proposed
	c.YouTubeOEmbed = "http://127.0.0.1:1/nothing"
	if _, ok := c.Verify(ctx, vault.Resource{Type: "video", URL: "https://youtu.be/HZGCoVF3YvM"}); !ok {
		t.Error("an unreachable catalogue must not drop a source")
	}
}

func TestReliability(t *testing.T) {
	cases := map[string]vault.Resource{
		"alta":  {Type: "web", URL: "https://ocw.mit.edu/courses/x"},
		"baja":  {Type: "web", URL: "https://someone.medium.com/post"},
		"media": {Type: "web", URL: "https://example.com/post"},
	}
	cases["alta "] = vault.Resource{Type: "web", URL: "https://fisica.uba.edu.ar/apunte"}
	cases["media "] = vault.Resource{Type: "video", URL: "https://youtu.be/x", Author: "Random Channel"}
	for want, r := range cases {
		if got := Reliability(r); got != strings.TrimSpace(want) {
			t.Errorf("%+v: %s, want %s", r, got, want)
		}
	}
	if isoDuration("PT1H2M3S") != "1:02:03" || isoDuration("PT45S") != "0:45" {
		t.Error("durations")
	}
}
