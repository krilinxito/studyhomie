package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesSearchAndRaw(t *testing.T) {
	v, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Save(Note{Slug: "fourier", Meta: Meta{Title: "Transformada de Fourier"}, Body: "## Guía\n\nUn acorde son tres senos: la fase importa.\n"}); err != nil {
		t.Fatal(err)
	}
	if err := v.SaveConcept(Concept{Slug: "fase", Meta: ConceptMeta{Title: "Fase de una onda"}, Body: "Dónde empieza el ciclo."}); err != nil {
		t.Fatal(err)
	}
	if _, err := v.SaveApunte("fourier", "Transformada de Fourier", "Mi duda sobre la FASE."); err != nil {
		t.Fatal(err)
	}
	files, err := v.Files()
	if err != nil || len(files) != 3 {
		t.Fatalf("files = %+v %v", files, err)
	}
	if files[0].Dir != GuidesDir || files[0].Title != "Transformada de Fourier" || files[1].Title != "Fase de una onda" || files[2].Title != "Apuntes: Transformada de Fourier" {
		t.Errorf("titles/order: %+v", files)
	}
	st := v.Stat()
	if st.Notes[GuidesDir] != 1 || st.Notes[ConceptsDir] != 1 || st.Notes[ApuntesDir] != 1 || st.Bytes == 0 {
		t.Errorf("stats: %+v", st)
	}

	hits, err := v.Search("fase", 10)
	if err != nil || len(hits) != 3 {
		t.Fatalf("hits = %+v %v", hits, err)
	}
	if hits[0].Slug != "fase" || !strings.Contains(hits[1].Snippet+hits[2].Snippet, "la fase importa") {
		t.Errorf("title match first, snippets: %+v", hits)
	}
	if hits, _ := v.Search("DONDE", 10); len(hits) != 1 || hits[0].Slug != "fase" {
		t.Errorf("accents and case must not matter: %+v", hits)
	}

	raw, _, err := v.ReadRaw(ConceptsDir, "fase")
	if err != nil || !strings.HasPrefix(raw, "---\n") || !strings.Contains(raw, "Dónde empieza") {
		t.Fatalf("raw = %q %v", raw, err)
	}
	edited := strings.Replace(raw, "Dónde empieza el ciclo.", "Dónde empieza el ciclo, en radianes.", 1)
	if err := v.WriteRaw(ConceptsDir, "fase", edited); err != nil {
		t.Fatal(err)
	}
	if c, _ := v.LoadConcept("fase"); !strings.Contains(c.Body, "en radianes") || c.Meta.Title != "Fase de una onda" {
		t.Errorf("edited concept: %+v", c)
	}
	if err := v.WriteRaw(ConceptsDir, "fase", "---\ntitulo: [roto\n---\n\nx"); err == nil {
		t.Error("broken YAML accepted")
	}
	if err := v.WriteRaw(ConceptsDir, "fase", "---\ntitulo: x\n\nsin cerrar"); err == nil {
		t.Error("unclosed frontmatter accepted")
	}
	if err := v.WriteRaw(ConceptsDir, "nueva", "x"); err == nil {
		t.Error("only existing notes can be edited")
	}
	if _, _, err := v.ReadRaw("..", "studyhomie"); err == nil {
		t.Error("folder escape accepted")
	}
	if _, _, err := v.ReadRaw(GuidesDir, "../../x"); err == nil {
		t.Error("slug escape accepted")
	}
	if _, err := os.Stat(filepath.Join(v.Root, ConceptsDir, "fase.md")); err != nil {
		t.Error(err)
	}
}

func TestPlainTextForSearch(t *testing.T) {
	body := `## Un acorde

> [!info] Definición
> Usa la [[fase-de-una-onda|fase]] y el [[plano]], ver [web](https://x.y).

` + "```viz\n{\"type\":\"plot\",\"fase\":1}\n```" + `

**Fin** con ` + "`código`" + `.`
	got := strings.Join(strings.Fields(plainText(body)), " ")
	want := "Un acorde Definición Usa la fase y el plano, ver web. Fin con código."
	if got != want {
		t.Errorf("plain:\n got %q\nwant %q", got, want)
	}
}
