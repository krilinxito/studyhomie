package vault

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestTrashRestoresGuideWithCompanionsAndLinks(t *testing.T) {
	v, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = v.SaveConcept(Concept{Slug: "ondas", Meta: ConceptMeta{Title: "Ondas", Guides: []string{"acustica"}}, Body: "# Ondas\n"})
	_ = v.Save(Note{Slug: "acustica", Meta: Meta{Title: "Acústica", Concepts: []string{"ondas"}}, Body: "# Acústica\n"})
	_ = v.Save(Note{Slug: "musica", Meta: Meta{Title: "Música", Requires: []string{"acustica"}}, Body: "# Música\n"})
	_ = v.SaveHandoff("acustica", "resumen")
	_ = v.SaveMaterial("acustica", "texto fuente")
	if err := os.MkdirAll(filepath.Join(v.Root, MaterialDir, "acustica"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(v.Root, MaterialDir, "acustica", "apuntes.pdf"), []byte("pdf"), 0o644)

	item, err := v.DeleteGuide("acustica", nil, map[string][]byte{"extra.json": []byte(`{"a":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if v.Exists("acustica") || v.LoadHandoff("acustica") != "" || v.LoadMaterial("acustica") != "" {
		t.Fatal("guide or its companions still in place")
	}
	if _, err := os.Stat(filepath.Join(v.Root, MaterialDir, "acustica")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("material folder still in place")
	}
	if c, _ := v.LoadConcept("ondas"); slices.Contains(c.Meta.Guides, "acustica") {
		t.Fatal("concept still links the deleted guide")
	}
	if n, _ := v.Load("musica"); slices.Contains(n.Meta.Requires, "acustica") {
		t.Fatal("guide still requires the deleted guide")
	}
	list, _ := v.TrashList()
	if len(list) != 1 || list[0].ID != item.ID || list[0].Title != "Acústica" || list[0].Bytes == 0 {
		t.Fatalf("trash list = %+v", list)
	}

	got, extra, err := v.Restore(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != GuidesDir || string(extra["extra.json"]) != `{"a":1}` {
		t.Fatalf("restored %+v extra %v", got, extra)
	}
	if !v.Exists("acustica") || v.LoadHandoff("acustica") != "resumen" || v.LoadMaterial("acustica") != "texto fuente" {
		t.Fatal("guide or companions not restored")
	}
	if _, err := os.Stat(filepath.Join(v.Root, MaterialDir, "acustica", "apuntes.pdf")); err != nil {
		t.Fatal("material not restored")
	}
	if c, _ := v.LoadConcept("ondas"); !slices.Contains(c.Meta.Guides, "acustica") {
		t.Fatal("concept link not restored")
	}
	if n, _ := v.Load("musica"); !slices.Contains(n.Meta.Requires, "acustica") {
		t.Fatal("requires link not restored")
	}
	if list, _ := v.TrashList(); len(list) != 0 {
		t.Fatalf("trash not emptied after restore: %+v", list)
	}
}

func TestRestoreConflictAndPurge(t *testing.T) {
	v, _ := Open(t.TempDir())
	_ = v.SaveConcept(Concept{Slug: "ondas", Meta: ConceptMeta{Title: "Ondas"}, Body: "# Ondas\n"})
	item, err := v.DeleteConcept("ondas")
	if err != nil {
		t.Fatal(err)
	}
	_ = v.SaveConcept(Concept{Slug: "ondas", Meta: ConceptMeta{Title: "Ondas nuevas"}, Body: "# Ondas\n"})
	if _, _, err := v.Restore(item.ID); !errors.Is(err, ErrRestoreConflict) {
		t.Fatalf("restore over a new note: err = %v", err)
	}
	if c, _ := v.LoadConcept("ondas"); c.Meta.Title != "Ondas nuevas" {
		t.Fatal("conflicting restore overwrote the new note")
	}
	if n := v.TrashPurgeOlder(time.Hour, time.Now()); n != 0 {
		t.Fatalf("purged %d fresh items", n)
	}
	if n := v.TrashPurgeOlder(time.Hour, time.Now().Add(2*time.Hour)); n != 1 {
		t.Fatalf("purged %d old items, want 1", n)
	}
	if _, err := v.DeleteConcept("nada"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete missing concept: %v", err)
	}
}

func TestItemsAndOrphans(t *testing.T) {
	v, _ := Open(t.TempDir())
	_ = v.Save(Note{Slug: "acustica", Meta: Meta{Title: "Acústica"}, Body: "# Acústica\n"})
	_ = v.SaveHandoff("acustica", "vivo")
	_ = v.SaveHandoff("borrada", "huérfano")
	_ = v.SaveInteractive("borrada", "<html></html>")
	up := filepath.Join(v.Root, ".studyhomie", "uploads", "0123456789abcdef")
	_ = os.MkdirAll(up, 0o755)
	_ = os.WriteFile(filepath.Join(up, "viejo.pdf"), []byte("x"), 0o644)
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(filepath.Join(up, "viejo.pdf"), old, old)
	_ = os.Chtimes(up, old, old)

	items, _ := v.Items(CatGuides)
	if len(items) != 1 || items[0].ID != "acustica" || items[0].Title != "Acústica" {
		t.Fatalf("guides = %+v", items)
	}
	ups, _ := v.Items(CatUploads)
	if len(ups) != 1 || ups[0].Title != "viejo.pdf" {
		t.Fatalf("uploads = %+v", ups)
	}
	var got []string
	for _, o := range v.Orphans(time.Now()) {
		got = append(got, o.Cat+":"+o.ID)
	}
	slices.Sort(got)
	want := []string{"handoffs:borrada", "interactive:borrada", "uploads:0123456789abcdef"}
	if !slices.Equal(got, want) {
		t.Fatalf("orphans = %v, want %v", got, want)
	}
	if _, err := v.ItemPath(CatHandoffs, "../stats"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ItemPath accepted a path outside its folder")
	}
	if _, err := v.RemoveItem(CatState, "stats", nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removing a missing state file: %v", err)
	}
}
