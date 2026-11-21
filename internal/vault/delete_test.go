package vault

import (
	"slices"
	"testing"
)

func TestDeleteGuideAndDeleteConceptCleanReferences(t *testing.T) {
	v, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := v.SaveConcept(Concept{
		Slug: "vectores",
		Meta: ConceptMeta{Title: "Vectores", Guides: []string{"fisica", "algebra"}},
		Body: "# Vectores\n\nMagnitud y dirección.\n",
	}); err != nil {
		t.Fatal(err)
	}
	if err := v.SaveConcept(Concept{
		Slug: "producto-punto",
		Meta: ConceptMeta{Title: "Producto punto", Requires: []string{"vectores"}, Guides: []string{"algebra"}},
		Body: "# Producto punto\n\nProyección escalar.\n",
	}); err != nil {
		t.Fatal(err)
	}
	if err := v.Save(Note{
		Slug: "fisica",
		Meta: Meta{Title: "Física", Status: "prepared", Concepts: []string{"vectores"}, Base: []string{"vectores"}},
		Body: "# Física\n\n## Guía\n\nFuerzas.\n",
	}); err != nil {
		t.Fatal(err)
	}
	if err := v.Save(Note{
		Slug: "algebra",
		Meta: Meta{Title: "Álgebra", Status: "prepared", Concepts: []string{"vectores", "producto-punto"}, Base: []string{"vectores"}, Requires: []string{"fisica"}},
		Body: "# Álgebra\n\n## Guía\n\nEspacios.\n",
	}); err != nil {
		t.Fatal(err)
	}
	_ = v.SaveHandoff("fisica", "handoff")
	_ = v.SaveInteractive("fisica", "<html></html>")

	// Delete guide "fisica" -> removes guide + companions, cleans references in "vectores" and "algebra".
	if err := v.DeleteGuide("fisica"); err != nil {
		t.Fatalf("DeleteGuide: %v", err)
	}
	if v.Exists("fisica") {
		t.Fatal("guide fisica should be deleted")
	}
	if _, err := v.LoadInteractive("fisica"); err == nil {
		t.Fatal("companion interactive file should be deleted")
	}
	c, err := v.LoadConcept("vectores")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(c.Meta.Guides, "fisica") || !slices.Contains(c.Meta.Guides, "algebra") {
		t.Fatalf("concept guides not cleaned: %v", c.Meta.Guides)
	}
	alg, err := v.Load("algebra")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(alg.Meta.Requires, "fisica") {
		t.Fatalf("guide requires not cleaned: %v", alg.Meta.Requires)
	}

	// Delete concept "vectores" -> removes concept and cleans references from "algebra" and "producto-punto".
	if err := v.DeleteConcept("vectores"); err != nil {
		t.Fatalf("DeleteConcept: %v", err)
	}
	if v.ConceptExists("vectores") {
		t.Fatal("concept vectores should be deleted")
	}
	alg, _ = v.Load("algebra")
	if slices.Contains(alg.Meta.Concepts, "vectores") || slices.Contains(alg.Meta.Base, "vectores") {
		t.Fatalf("algebra still references deleted concept: %+v", alg.Meta)
	}
	pp, _ := v.LoadConcept("producto-punto")
	if slices.Contains(pp.Meta.Requires, "vectores") {
		t.Fatalf("producto-punto still requires deleted concept: %v", pp.Meta.Requires)
	}
}
