package web

import (
	"encoding/json"
	"testing"

	"github.com/krilinxito/studyhomie/internal/cards"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestDataDeleteGuideWithDeckAndRestore(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	if err := s.V.Save(vault.Note{Slug: "acustica", Meta: vault.Meta{Title: "Acústica"}, Body: "# Acústica\n"}); err != nil {
		t.Fatal(err)
	}
	deck := cards.Deck{Slug: "acustica", Meta: cards.DeckMeta{Title: "Acústica"}, Cards: []cards.Card{{ID: "c1", Deck: "acustica", Front: "Qué es una onda", Back: "Una perturbación"}}}
	if err := cards.SaveDeck(s.V.Root, deck); err != nil {
		t.Fatal(err)
	}
	if _, err := cards.Review(s.V.Root, "acustica", "c1", 3, s.Now()); err != nil {
		t.Fatal(err)
	}

	res, body := do(t, ts, "GET", "/api/data", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("overview %d: %s", res.StatusCode, body)
	}

	res, body = do(t, ts, "DELETE", "/api/data/guias/acustica?with=deck", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("delete %d: %s", res.StatusCode, body)
	}
	var out struct {
		Trash vault.TrashItem `json:"trash"`
	}
	_ = json.Unmarshal(body, &out)
	if s.V.Exists("acustica") {
		t.Fatal("guide still there")
	}
	if _, err := cards.LoadDeck(s.V.Root, "acustica"); err == nil {
		t.Fatal("deck still there")
	}
	if cards.OrphanStates(s.V.Root) != 0 {
		t.Fatal("deck review state left in cards.json")
	}

	res, body = do(t, ts, "POST", "/api/trash/"+out.Trash.ID+"/restore", map[string]any{}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("restore %d: %s", res.StatusCode, body)
	}
	if !s.V.Exists("acustica") {
		t.Fatal("guide not restored")
	}
	if _, err := cards.LoadDeck(s.V.Root, "acustica"); err != nil {
		t.Fatal("deck not restored")
	}
	if st, _ := cards.TakeDeckStates(s.V.Root, "acustica"); len(st) != 1 {
		t.Fatalf("review state not restored: %v", st)
	}

	res, _ = do(t, ts, "DELETE", "/api/data/guias/nada", nil, nil)
	if res.StatusCode != 404 {
		t.Fatalf("missing item: %d", res.StatusCode)
	}
	res, _ = do(t, ts, "GET", "/api/data/inventada", nil, nil)
	if res.StatusCode != 404 {
		t.Fatalf("unknown category: %d", res.StatusCode)
	}
}

func TestDataRefusesItemInUseAndEmptiesCategory(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	for _, slug := range []string{"a", "b"} {
		_ = s.V.Save(vault.Note{Slug: slug, Meta: vault.Meta{Title: slug}, Body: "# x\n"})
	}
	run := &Run{ID: 99, Slug: "a", in: newInputBuf(), cancel: func() {}}
	s.mu.Lock()
	s.runs[99] = run
	s.mu.Unlock()
	res, _ := do(t, ts, "DELETE", "/api/data/guias/a", nil, nil)
	if res.StatusCode != 409 {
		t.Fatalf("deleting a guide in use: %d", res.StatusCode)
	}
	res, _ = do(t, ts, "POST", "/api/data/guias/empty", map[string]any{}, nil)
	if res.StatusCode != 409 {
		t.Fatalf("emptying while a flow runs: %d", res.StatusCode)
	}
	run.emit(Event{Type: "done"})
	res, body := do(t, ts, "POST", "/api/data/guias/empty", map[string]any{}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("empty %d: %s", res.StatusCode, body)
	}
	if s.V.Exists("a") || s.V.Exists("b") {
		t.Fatal("guides not emptied")
	}
	res, body = do(t, ts, "GET", "/api/trash", nil, nil)
	var tr struct {
		Items []vault.TrashItem `json:"items"`
	}
	_ = json.Unmarshal(body, &tr)
	if res.StatusCode != 200 || len(tr.Items) != 2 {
		t.Fatalf("trash = %s", body)
	}
	res, _ = do(t, ts, "POST", "/api/trash/empty", map[string]any{}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("empty trash %d", res.StatusCode)
	}
	if n, _ := s.V.TrashSize(); n != 0 {
		t.Fatal("trash not emptied")
	}
}
