package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/cards"
)

func TestCardsImportReviewExportAndStatsIntegration(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	now := time.Date(2026, 11, 20, 16, 0, 0, 0, time.Local)
	s.Now = func() time.Time { return now }

	// 1. Import Anki TSV via multipart POST /api/cards/import
	tsv := "#separator:tab\n#html:false\n#deck:Armonía\n#tags column:3\n¿Qué es una tríada?\tTres notas apiladas por terceras.\trarity::common musica\n¿Por qué resuelve el tritono?\tPorque la sensible sube y la séptima baja.\trarity::legendary musica\n"
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "armonia.txt")
	fw.Write([]byte(tsv))
	mw.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/cards/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", ts.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("import status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. GET /api/cards returns the imported deck & 2 due cards
	res, body := do(t, ts, "GET", "/api/cards", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("listCards status = %d: %s", res.StatusCode, body)
	}
	var ov cards.OverviewResponse
	json.Unmarshal(body, &ov)
	if len(ov.Decks) != 1 || ov.Summary.Total != 2 || ov.Summary.Due != 2 || ov.Summary.Collected != 0 {
		t.Fatalf("overview = %+v", ov)
	}

	// 3. Review the legendary card with Good (3) -> unlocks collection + awards 23 XP in /api/stats!
	var leg cards.CardView
	for _, c := range ov.Queue {
		if c.Rarity == cards.RarityLegendary {
			leg = c
		}
	}
	res, body = do(t, ts, "POST", "/api/cards/review", map[string]any{
		"deck": leg.Deck, "cardId": leg.ID, "rating": cards.RatingGood,
	}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("review status = %d: %s", res.StatusCode, body)
	}
	var out cards.ReviewOutcome
	json.Unmarshal(body, &out)
	if !out.NewlyCollected || out.XPGained != 23 {
		t.Errorf("review outcome = %+v", out)
	}

	// Verify /api/stats includes the XP and collected legendary card!
	_, statsBody := do(t, ts, "GET", "/api/stats", nil, nil)
	var st struct {
		TodayXP int                     `json:"todayXP"`
		Cards   cards.CollectionSummary `json:"cards"`
	}
	json.Unmarshal(statsBody, &st)
	if st.TodayXP != 23 || st.Cards.Collected != 1 || st.Cards.ByRarity[cards.RarityLegendary] != 1 {
		t.Errorf("stats after card review = %+v", st)
	}

	// 4. Export back to Anki TSV
	res, expBody := do(t, ts, "GET", "/api/cards/export?deck="+ov.Decks[0].Slug, nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(expBody), "rarity::legendary") {
		t.Errorf("export status=%d body=%s", res.StatusCode, expBody)
	}
}

func TestVaultMaterialServingAndListing(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	tmp := filepath.Join(t.TempDir(), "clase1.txt")
	os.WriteFile(tmp, []byte("contenido de clase"), 0o644)
	rel, err := s.V.CopyMaterial("calculo", tmp)
	if err != nil || rel != "material/calculo/clase1.txt" {
		t.Fatalf("CopyMaterial = %q, %v", rel, err)
	}

	res, body := do(t, ts, "GET", "/api/vault/materials", nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), "clase1.txt") {
		t.Fatalf("materials list = %d %s", res.StatusCode, body)
	}
	res, body = do(t, ts, "GET", "/api/material/calculo/clase1.txt", nil, nil)
	if res.StatusCode != 200 || string(body) != "contenido de clase" {
		t.Fatalf("material file = %d %q", res.StatusCode, body)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("material file CSP = %q", csp)
	}
}
