package web

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefineEndpointsLookupSaveAndNotes(t *testing.T) {
	ans := "Es la probabilidad antes de ver los datos.\n\n```json\n{\"title\": \"Probabilidad previa\", \"summary\": \"Lo que crees antes de la evidencia.\", \"kind\": \"term\"}\n```"
	p := &scripted{replies: []string{ans, "Porque se actualiza."}}
	s, ts := newTestServer(t, p)
	guidedNote(t, s)

	res, body := do(t, ts, "POST", "/api/define", map[string]any{"text": "prior", "context": "La prior se multiplica por la verosimilitud.", "guide": "cumpleanos"}, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"title":"Probabilidad previa"`) {
		t.Fatalf("define: %d %s", res.StatusCode, body)
	}
	var out struct {
		Definition struct{ Title, Summary, Text string } `json:"definition"`
	}
	json.Unmarshal(body, &out)

	// a follow-up question goes with what was said, and is not a new lookup
	res, body = do(t, ts, "POST", "/api/define", map[string]any{"text": "prior", "question": "¿y por qué cambia?", "history": []map[string]string{{"q": "", "a": out.Definition.Text}}}, nil)
	if res.StatusCode != 200 || !strings.Contains(p.reqs[1].Prompt, "FOLLOW-UP QUESTION") {
		t.Fatalf("follow-up: %d %s", res.StatusCode, body)
	}
	_, body = do(t, ts, "GET", "/api/lookups", nil, nil)
	if strings.Count(string(body), `"text":"prior"`) != 1 {
		t.Errorf("lookups: %s", body)
	}

	// saved as a concept, linked to the guide
	res, body = do(t, ts, "POST", "/api/concepts", map[string]any{"title": out.Definition.Title, "summary": out.Definition.Summary, "text": out.Definition.Text, "guide": "cumpleanos"}, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"slug":"probabilidad-previa"`) || !strings.Contains(string(body), `"created":true`) {
		t.Fatalf("concept: %d %s", res.StatusCode, body)
	}
	if n, _ := s.V.Load("cumpleanos"); len(n.Meta.Concepts) != 1 {
		t.Errorf("guide concepts: %v", n.Meta.Concepts)
	}
	// now the vault has it: asking again shows the note, without the AI
	res, body = do(t, ts, "POST", "/api/define", map[string]any{"text": "Probabilidad previa"}, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"existing"`) || len(p.reqs) != 2 {
		t.Fatalf("existing: %d %s (%d calls)", res.StatusCode, body, len(p.reqs))
	}

	// into the learner's notes on the guide, twice: both blocks stay
	for _, txt := range []string{"### Prior\n\nlo de antes", "### Otra\n\nmás"} {
		if res, body := do(t, ts, "POST", "/api/apuntes/cumpleanos/append", map[string]any{"text": txt}, nil); res.StatusCode != 200 {
			t.Fatalf("append: %d %s", res.StatusCode, body)
		}
	}
	a, _ := s.V.LoadApunte("cumpleanos")
	if !strings.Contains(a.Body, "lo de antes") || !strings.Contains(a.Body, "más") {
		t.Errorf("apunte: %q", a.Body)
	}
	_, body = do(t, ts, "GET", "/api/apuntes", nil, nil)
	if !strings.Contains(string(body), `"slug":"cumpleanos"`) {
		t.Errorf("apuntes list: %s", body)
	}
	if res, _ := do(t, ts, "POST", "/api/apuntes/no-existe/append", map[string]any{"text": "x"}, nil); res.StatusCode != 404 {
		t.Errorf("unknown note: %d", res.StatusCode)
	}
	if res, _ := do(t, ts, "POST", "/api/define", map[string]any{"text": ""}, nil); res.StatusCode != 400 {
		t.Errorf("empty define: %d", res.StatusCode)
	}
}
