package material

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeZip(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, body := range files {
		w, _ := zw.Create(n)
		w.Write([]byte(body))
	}
	zw.Close()
	f.Close()
	return path
}

func TestClassify(t *testing.T) {
	for p, want := range map[string]Kind{"a.PDF": Text, "b.pptx": Text, "c.md": Text, "d.PNG": Image, "e.jpeg": Image} {
		if got, err := Classify(p); err != nil || got != want {
			t.Errorf("Classify(%s) = %v, %v", p, got, err)
		}
	}
	if _, err := Classify("x.exe"); err == nil {
		t.Error("exe must be rejected")
	}
}

func TestExtractPPTXOrdersSlides(t *testing.T) {
	slide := func(text string) string {
		return `<p:sld xmlns:p="p" xmlns:a="a"><a:p><a:r><a:t>` + text + `</a:t></a:r></a:p></p:sld>`
	}
	path := writeZip(t, "deck.pptx", map[string]string{
		"ppt/slides/slide10.xml": slide("diez"),
		"ppt/slides/slide2.xml":  slide("dos"),
		"ppt/slides/slide1.xml":  slide("uno"),
		"ppt/theme/theme1.xml":   slide("ignorado"),
	})
	got, err := Extract(path)
	if err != nil {
		t.Fatal(err)
	}
	i1, i2, i10 := strings.Index(got, "uno"), strings.Index(got, "dos"), strings.Index(got, "diez")
	if i1 < 0 || !(i1 < i2 && i2 < i10) || strings.Contains(got, "ignorado") {
		t.Errorf("slides wrong:\n%s", got)
	}
}

func TestExtractDOCX(t *testing.T) {
	path := writeZip(t, "a.docx", map[string]string{
		"word/document.xml": `<w:document xmlns:w="w"><w:body><w:p><w:r><w:t>Hola</w:t></w:r><w:r><w:t> mundo</w:t></w:r></w:p><w:p><w:r><w:t>Segundo</w:t></w:r></w:p></w:body></w:document>`,
	})
	got, err := Extract(path)
	if err != nil || got != "Hola mundo\nSegundo" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestExtractCorruptOffice(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.pptx")
	os.WriteFile(p, []byte("no soy un zip"), 0o644)
	if _, err := Extract(p); err == nil {
		t.Error("expected an error for a corrupt file")
	}
}

const doc = `# Probabilidad

La probabilidad mide la incertidumbre de los sucesos.

# Teorema de Bayes

El teorema de Bayes actualiza una creencia previa con nueva evidencia.

# Cadenas de Markov

Las cadenas de Markov modelan transiciones entre estados.`

func TestSplitKeepsHeadings(t *testing.T) {
	chunks := Split(doc, 200)
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d: %+v", len(chunks), chunks)
	}
	if chunks[1].Heading != "Teorema de Bayes" || !strings.Contains(chunks[1].Text, "creencia previa") {
		t.Errorf("chunk 1 = %+v", chunks[1])
	}
}

func TestSplitHugeParagraph(t *testing.T) {
	long := strings.Repeat("Una frase de relleno. ", 400)
	chunks := Split(long, 1000)
	if len(chunks) < 8 {
		t.Fatalf("expected the paragraph to be cut, got %d chunks", len(chunks))
	}
	for _, c := range chunks {
		if len(c.Text) > 1000 {
			t.Errorf("chunk too long: %d", len(c.Text))
		}
	}
}

func TestTopFindsRelevantChunk(t *testing.T) {
	chunks := Split(doc, 200)
	top := Top(chunks, "cadena de markov y estados", 1)
	if len(top) != 1 || top[0].Heading != "Cadenas de Markov" {
		t.Errorf("top = %+v", top)
	}
	if got := Top(chunks, "algo totalmente ajeno xyzzy", 2); len(got) != 0 {
		t.Errorf("no match should return nothing, got %+v", got)
	}
	both := Top(chunks, "probabilidad bayes", 2)
	if len(both) != 2 || both[0].ID > both[1].ID {
		t.Errorf("results must come back in document order: %+v", both)
	}
}

func TestOutlineRespectsBudget(t *testing.T) {
	var chunks []Chunk
	for i := 0; i < 100; i++ {
		chunks = append(chunks, Chunk{ID: i, Heading: "H", Text: strings.Repeat("palabra ", 50)})
	}
	if out := Outline(chunks, 5000); len(out) > 6000 {
		t.Errorf("outline too long: %d", len(out))
	}
	if out := Outline(chunks, 0); strings.Count(out, "\n") != 100 {
		t.Error("budget 0 means unlimited")
	}
}
