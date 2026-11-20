package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApuntes(t *testing.T) {
	v, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := v.LoadApunte("integral")
	if err != nil || a.Body != "" {
		t.Fatalf("missing note must load empty: %+v %v", a, err)
	}
	if _, err := v.SaveApunte("integral", "Integral", "Área bajo la curva.\n\n![[foto.png]]"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(v.Root, ApuntesDir, "integral.md"))
	if !strings.Contains(string(data), "de: '[[integral]]'") && !strings.Contains(string(data), `de: "[[integral]]"`) {
		t.Errorf("frontmatter must link the note:\n%s", data)
	}
	a, err = v.LoadApunte("integral")
	if err != nil || !strings.Contains(a.Body, "![[foto.png]]") || a.Meta.Created == "" {
		t.Fatalf("reload: %+v %v", a, err)
	}
	if !v.HasApunte("integral") {
		t.Error("HasApunte")
	}
	// an empty body removes the file
	if _, err := v.SaveApunte("integral", "Integral", "  \n"); err != nil || v.HasApunte("integral") {
		t.Errorf("empty note must be removed: %v", err)
	}
	if _, err := v.LoadApunte("../x"); err == nil {
		t.Error("bad slug accepted")
	}
}

func TestSaveAttachment(t *testing.T) {
	v, _ := Open(t.TempDir())
	n1, err := v.SaveAttachment(`C:\fotos\Mi Pizarrón.PNG`, strings.NewReader("png"), 1<<20)
	if err != nil || n1 != "Mi-Pizarron.png" {
		t.Fatalf("name: %q %v", n1, err)
	}
	n2, _ := v.SaveAttachment("Mi Pizarrón.png", strings.NewReader("png"), 1<<20)
	if n2 != "Mi-Pizarron-2.png" {
		t.Errorf("taken name must get a number: %q", n2)
	}
	if _, err := v.SaveAttachment("grande.bin", strings.NewReader("12345"), 4); err == nil {
		t.Error("over the limit must fail")
	}
	if _, err := os.Stat(filepath.Join(v.Root, AttachDir, "grande.bin")); err == nil {
		t.Error("a failed upload must not be left behind")
	}
	if _, err := v.AttachmentPath("../../secreto.txt"); err == nil {
		t.Error("path escape accepted")
	}
	if p, err := v.AttachmentPath(n1); err != nil || !strings.HasSuffix(p, n1) {
		t.Errorf("path: %q %v", p, err)
	}
}
