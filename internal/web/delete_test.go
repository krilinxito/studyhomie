package web

import (
	"encoding/json"
	"testing"

	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestDeleteNoteEndpointRemovesGuidesAndConcepts(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	if err := s.V.SaveConcept(vault.Concept{
		Slug: "ondas",
		Meta: vault.ConceptMeta{Title: "Ondas", Guides: []string{"acustica"}},
		Body: "# Ondas\n\nOscilaciones.\n",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.V.Save(vault.Note{
		Slug: "acustica",
		Meta: vault.Meta{Title: "Acústica", Status: "prepared", Concepts: []string{"ondas"}, Base: []string{"ondas"}},
		Body: "# Acústica\n\n## Guía\n\nSonido.\n",
	}); err != nil {
		t.Fatal(err)
	}

	// Delete guide
	res, body := do(t, ts, "DELETE", "/api/notes/acustica", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("delete guide status %d: %s", res.StatusCode, body)
	}
	var out struct {
		OK   bool   `json:"ok"`
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(body, &out)
	if !out.OK || out.Kind != "guide" || s.V.Exists("acustica") {
		t.Fatalf("guide not deleted: %+v", out)
	}

	// Delete concept
	res, body = do(t, ts, "DELETE", "/api/notes/ondas", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("delete concept status %d: %s", res.StatusCode, body)
	}
	out = struct {
		OK   bool   `json:"ok"`
		Kind string `json:"kind"`
	}{}
	_ = json.Unmarshal(body, &out)
	if !out.OK || out.Kind != "concept" || s.V.ConceptExists("ondas") {
		t.Fatalf("concept not deleted: %+v", out)
	}

	// Deleting again returns 404
	res, _ = do(t, ts, "DELETE", "/api/notes/ondas", nil, nil)
	if res.StatusCode != 404 {
		t.Fatalf("expected 404 on missing note, got %d", res.StatusCode)
	}
}
