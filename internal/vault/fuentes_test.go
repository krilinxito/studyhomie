package vault

import (
	"strings"
	"testing"
)

func TestSourceKeyNormalizes(t *testing.T) {
	same := [][2]Resource{
		{{URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=30s"}, {URL: "https://youtu.be/dQw4w9WgXcQ"}},
		{{URL: "https://doi.org/10.1038/NATURE14539"}, {DOI: "10.1038/nature14539", URL: "https://www.nature.com/articles/nature14539"}},
		{{URL: "https://www.example.org/a/b/?utm_source=x&id=3#top"}, {URL: "http://example.org/a/b?id=3"}},
		{{Type: "book", Title: "Cálculo de Spivak"}, {Type: "book", Title: "calculo de spivak"}},
		{{ISBN: "978-0-914098-91-1"}, {ISBN: "9780914098911"}},
	}
	for _, p := range same {
		if a, b := SourceKey(p[0]), SourceKey(p[1]); a == "" || a != b {
			t.Errorf("%+v vs %+v: %q != %q", p[0], p[1], a, b)
		}
	}
	if SourceKey(Resource{URL: "https://example.org/a?id=3"}) == SourceKey(Resource{URL: "https://example.org/a?id=4"}) {
		t.Error("real parameters must count")
	}
}

func TestSourcesDedupMigrateAndTrash(t *testing.T) {
	v, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vid := Resource{Type: "video", Title: "Bayes en 15 min", URL: "https://www.youtube.com/watch?v=HZGCoVF3YvM", Why: "Lo visual"}
	sim := Resource{Type: "interactive", Title: "Simulación", URL: "interactive/bayes.html", Why: "Jugar"}
	old := []Note{
		{Slug: "bayes", Meta: Meta{Title: "Bayes", Area: "matematicas", Resources: []Resource{vid, {Type: "book", Title: "Probability Theory", Why: "Profundiza"}, sim}},
			Body: "## Guía\n\nTexto.\n\n## Recursos\n\n- viejo\n\n## Prueba\n\nnota"},
		{Slug: "prior", Meta: Meta{Title: "Prior", Resources: []Resource{{Type: "video", Title: "otro título", URL: "https://youtu.be/HZGCoVF3YvM", Why: "Para la prior"}}},
			Body: "## Guía\n\nTexto."},
	}
	for _, n := range old {
		if err := v.Save(n); err != nil {
			t.Fatal(err)
		}
	}
	n, err := v.MigrateSources()
	if err != nil || n != 2 {
		t.Fatalf("migrate: %d %v", n, err)
	}
	if again, _ := v.MigrateSources(); again != 0 {
		t.Errorf("a second migration changed %d guides", again)
	}
	list, _ := v.ListSources()
	if len(list) != 2 {
		t.Fatalf("one note per source, the video shared: %+v", list)
	}
	b, _ := v.Load("bayes")
	if len(b.Meta.Sources) != 2 || len(b.Meta.Resources) != 1 || b.Meta.Resources[0].Type != "interactive" {
		t.Errorf("bayes meta: %+v %+v", b.Meta.Sources, b.Meta.Resources)
	}
	rec := Section(b.Body, "Recursos")
	if !strings.Contains(rec, "[[fuentes/bayes-en-15-min|Bayes en 15 min]] — Lo visual") || !strings.Contains(rec, "../interactive/bayes.html") || strings.Contains(rec, "viejo") {
		t.Errorf("recursos: %s", rec)
	}
	if !strings.Contains(b.Body, "## Prueba") {
		t.Error("the rest of the guide must stay")
	}
	p, _ := v.Load("prior")
	if len(p.Meta.Sources) != 1 || p.Meta.Sources[0].ID != "bayes-en-15-min" || p.Meta.Sources[0].Why != "Para la prior" {
		t.Errorf("the second guide cites the same note with its own why: %+v", p.Meta.Sources)
	}
	src, _ := v.LoadSource("bayes-en-15-min")
	if src.Meta.Key != "youtube:HZGCoVF3YvM" || !strings.Contains(src.Body, "![](https://www.youtube.com/watch?v=HZGCoVF3YvM)") || !strings.Contains(src.Body, "## Mis notas") || src.Meta.Area != "matematicas" {
		t.Errorf("source note: %+v\n%s", src.Meta, src.Body)
	}
	if res := v.GuideResources(b); len(res) != 3 || res[0].ID != "bayes-en-15-min" || res[0].Why != "Lo visual" {
		t.Errorf("reader resources: %+v", res)
	}

	// deleting the video takes it out of both guides; restoring brings each why back
	item, err := v.DeleteSource("bayes-en-15-min")
	if err != nil || len(item.Refs) != 2 {
		t.Fatalf("delete: %+v %v", item, err)
	}
	if b, _ := v.Load("bayes"); len(b.Meta.Sources) != 1 || strings.Contains(Section(b.Body, "Recursos"), "Bayes en 15 min") {
		t.Errorf("after delete: %+v\n%s", b.Meta.Sources, b.Body)
	}
	if p, _ := v.Load("prior"); len(p.Meta.Sources) != 0 || Section(p.Body, "Recursos") != "" {
		t.Errorf("prior after delete: %+v\n%s", p.Meta.Sources, p.Body)
	}
	if _, _, err := v.Restore(item.ID); err != nil {
		t.Fatal(err)
	}
	if p, _ := v.Load("prior"); len(p.Meta.Sources) != 1 || p.Meta.Sources[0].Why != "Para la prior" || !strings.Contains(Section(p.Body, "Recursos"), "Bayes en 15 min") {
		t.Errorf("after restore: %+v\n%s", p.Meta.Sources, p.Body)
	}
}
