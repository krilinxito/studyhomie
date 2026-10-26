package vault

import (
	"slices"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Teorema de Bayes": "teorema-de-bayes",
		"  C++ & Go!  ":    "c-go",
		"Álgebra lineal":   "álgebra-lineal",
		"../../etc/passwd": "etc-passwd",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNoteRoundTrip(t *testing.T) {
	n := Note{Slug: "bayes", Meta: Meta{
		Title: "Bayes", Status: "prepared",
		Resources: []Resource{{Type: "video", Title: "3Blue1Brown", URL: "https://youtu.be/x"}},
	}, Body: "Ver [[probabilidad]] y [[Álgebra lineal|álgebra]].\n"}
	data, err := n.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseNote("bayes", data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.Title != "Bayes" || len(got.Meta.Resources) != 1 || got.Meta.Resources[0].URL != "https://youtu.be/x" {
		t.Errorf("meta lost in round trip: %+v", got.Meta)
	}
	if want := []string{"probabilidad", "álgebra-lineal"}; !slices.Equal(got.Links(), want) {
		t.Errorf("links = %v, want %v", got.Links(), want)
	}
}

func TestParseWithoutFrontmatter(t *testing.T) {
	n, err := ParseNote("x", []byte("solo cuerpo [[a]]"))
	if err != nil || n.Body != "solo cuerpo [[a]]" {
		t.Errorf("got %+v, %v", n, err)
	}
}

func TestVaultSaveGraphBacklinks(t *testing.T) {
	v, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(v.Save(Note{Meta: Meta{Title: "Bayes"}, Body: "Usa [[Probabilidad]]"}))
	must(v.Save(Note{Meta: Meta{Title: "Probabilidad"}, Body: "Base."}))
	if !v.Exists("bayes") {
		t.Fatal("bayes should exist")
	}
	nodes, edges, err := v.Graph()
	must(err)
	if len(nodes) != 2 || len(edges) != 1 || edges[0] != (Edge{"bayes", "probabilidad"}) {
		t.Errorf("nodes=%d edges=%v", len(nodes), edges)
	}
	back, err := v.Backlinks("probabilidad")
	must(err)
	if !slices.Equal(back, []string{"bayes"}) {
		t.Errorf("backlinks = %v", back)
	}
}

func TestSectionContentCannotOpenSections(t *testing.T) {
	body := ReplaceSection("Intro.", "Feynman", "## Mis dudas\ntexto\n```\n## en código\n```\n# Otro")
	body = ReplaceSection(body, "Feynman", "## Nuevo\nfinal")
	if strings.Count(body, "## Feynman") != 1 || strings.Contains(body, "Mis dudas") || !strings.Contains(body, "### Nuevo") {
		t.Errorf("headings leaked or section not replaced:\n%s", body)
	}
	kept := ReplaceSection("", "X", "## A\n```\n## en código\n```")
	if !strings.Contains(kept, "\n## en código\n") {
		t.Errorf("code fence content must not be touched:\n%s", kept)
	}
}

func TestAppendSectionReplaces(t *testing.T) {
	v, _ := Open(t.TempDir())
	if err := v.Save(Note{Meta: Meta{Title: "Tema"}, Body: "Intro."}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"primera", "segunda"} {
		if err := v.AppendSection("tema", "Guía", c); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.AppendSection("tema", "Feynman", "mi explicación"); err != nil {
		t.Fatal(err)
	}
	if err := v.AppendSection("tema", "Guía", "tercera"); err != nil {
		t.Fatal(err)
	}
	n, _ := v.Load("tema")
	if strings.Count(n.Body, "## Guía") != 1 || strings.Contains(n.Body, "primera") || !strings.Contains(n.Body, "tercera") {
		t.Errorf("section not replaced cleanly:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "## Feynman") || !strings.Contains(n.Body, "Intro.") {
		t.Errorf("other content lost:\n%s", n.Body)
	}
}
