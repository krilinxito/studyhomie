package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/session"
	"github.com/krilinxito/studyhomie/internal/stats"
	"github.com/krilinxito/studyhomie/internal/vault"
)

const threeQuestions = "```json\n" + `{"questions":[{"id":1,"type":"open","question":"¿Uno?","answer":"1"},{"id":2,"type":"open","question":"¿Dos?","answer":"2"},{"id":3,"type":"open","question":"¿Tres?","answer":"3"}]}` + "\n```"

func startPracticeHTTP(t *testing.T, ts *httptest.Server, body any) (string, int) {
	t.Helper()
	res, data := do(t, ts, "POST", "/api/practice", body, nil)
	if res.StatusCode != 200 {
		t.Fatalf("start practice: %d %s", res.StatusCode, data)
	}
	var out struct {
		Practice practiceInfo `json:"practice"`
		Run      int          `json:"run"`
	}
	json.Unmarshal(data, &out)
	return out.Practice.ID, out.Run
}

// asks counts the questions a run has asked so far (answers of a quiz).
func waitAsks(t *testing.T, ts *httptest.Server, id, kind string, run, n int) []Event {
	t.Helper()
	return readEvents(t, ts, run, 0, func(e []Event) bool {
		c := 0
		for _, x := range e {
			if x.Type == "ask" && x.Kind == kind {
				c++
			}
		}
		a := lastAsk(e)
		return c >= n && a != nil && a.Kind == kind
	})
}

func practices(t *testing.T, ts *httptest.Server) (open, recent []practiceInfo) {
	t.Helper()
	_, data := do(t, ts, "GET", "/api/practice", nil, nil)
	var out struct {
		Open   []practiceInfo `json:"open"`
		Recent []practiceInfo `json:"recent"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("list: %v %s", err, data)
	}
	return out.Open, out.Recent
}

func TestPracticeRunsNextToTheChatAndFinishesEarly(t *testing.T) {
	grade := "```json\n" + `{"score":90,"results":[{"id":1,"correct":true,"feedback":"bien"}],"gaps":[]}` + "\n```"
	p := &scripted{replies: []string{"Hola.", threeQuestions, "La media es el promedio.", grade}}
	s, ts := newTestServer(t, p)
	guidedNote(t, s)

	code, chat := startRunHTTP(t, ts, map[string]any{"cmd": "chat", "chat": map[string]any{"text": "hola"}})
	if code != 200 {
		t.Fatalf("chat: %d", code)
	}
	waitAsks(t, ts, "", "chat", chat, 1)

	// a quiz starts while the chat is open, and waits for its first answer
	id, run := startPracticeHTTP(t, ts, map[string]any{"kind": "quiz", "slug": "cumpleanos", "n": 3})
	waitAsks(t, ts, id, "line", run, 1)

	// the chat is not blocked by it
	if res, body := do(t, ts, "POST", "/api/runs/"+itoa(chat)+"/input", map[string]any{"text": "¿qué es la media?"}, nil); res.StatusCode != 200 {
		t.Fatalf("chat during a quiz: %d %s", res.StatusCode, body)
	}
	if evs := waitAsks(t, ts, "", "chat", chat, 2); !strings.Contains(outText(evs), "promedio") {
		t.Fatalf("chat answer: %s", outText(evs))
	}

	if res, body := do(t, ts, "POST", "/api/runs/"+itoa(run)+"/input", map[string]any{"text": "1"}, nil); res.StatusCode != 200 {
		t.Fatalf("answer: %d %s", res.StatusCode, body)
	}
	waitAsks(t, ts, id, "line", run, 2)
	if open, _ := practices(t, ts); len(open) != 1 || open[0].Answered != 1 || open[0].Total != 3 || !open[0].Waiting {
		t.Fatalf("open practices: %+v", open)
	}

	// "Terminar ahora": what was answered is graded, the rest counts as blank
	if res, body := do(t, ts, "POST", "/api/practice/"+id+"/finish", map[string]any{}, nil); res.StatusCode != 200 {
		t.Fatalf("finish: %d %s", res.StatusCode, body)
	}
	evs := readEvents(t, ts, run, 0, func(e []Event) bool { return has(e, "done") != nil })
	if d := has(evs, "done"); d == nil || d.Error != "" {
		t.Fatalf("finish ended with %+v: %+v", d, evs)
	}
	var score *session.Card
	for _, e := range evs {
		if e.Type == "card" && e.Card.Kind == session.CardScore {
			score = e.Card
		}
	}
	if score == nil || score.Score != 30 || !strings.Contains(score.Detail, "1 de 3") {
		t.Fatalf("score card: %+v", score)
	}
	n, _ := s.V.Load("cumpleanos")
	if n.Meta.Status != "prepared" || n.Meta.Mastery != 0 {
		t.Errorf("an incomplete quiz must not change mastery: %+v", n.Meta)
	}
	if !strings.Contains(vault.Section(n.Body, "Prueba"), "incompleta: 1 de 3") {
		t.Errorf("Prueba: %s", n.Body)
	}
	st, _ := stats.Load(s.V.Root)
	if len(st.Events) != 1 || !st.Events[0].Partial || st.Events[0].Practice != id || st.Events[0].XP >= stats.XPFor(stats.KindQuiz, 30) {
		t.Errorf("stats event: %+v", st.Events)
	}
	open, recent := practices(t, ts)
	if len(open) != 0 || len(recent) != 1 || recent[0].Status != practicePartial || recent[0].Score != 30 {
		t.Errorf("after finishing: open %+v recent %+v", open, recent)
	}
}

func TestPracticePausesAndResumesAfterARestart(t *testing.T) {
	grade := "```json\n" + `{"score":80,"results":[],"gaps":[]}` + "\n```"
	p := &scripted{replies: []string{threeQuestions}}
	s, ts := newTestServer(t, p)
	guidedNote(t, s)
	id, run := startPracticeHTTP(t, ts, map[string]any{"kind": "quiz", "slug": "cumpleanos", "n": 3, "difficulty": "avanzada"})
	waitAsks(t, ts, id, "line", run, 1)
	do(t, ts, "POST", "/api/runs/"+itoa(run)+"/input", map[string]any{"text": "uno"}, nil)
	waitAsks(t, ts, id, "line", run, 2)

	if res, body := do(t, ts, "POST", "/api/practice/"+id+"/pause", map[string]any{}, nil); res.StatusCode != 200 {
		t.Fatalf("pause: %d %s", res.StatusCode, body)
	}
	evs := readEvents(t, ts, run, 0, func(e []Event) bool { return has(e, "done") != nil })
	if d := has(evs, "done"); d.Error != "" {
		t.Fatalf("a paused practice ends quietly: %q", d.Error)
	}
	s.store.close()

	// the app starts again on the same vault
	p2 := &scripted{replies: []string{grade}}
	cfg := config.Default()
	cfg.Depth, cfg.Iceberg = 0, false
	s2 := New(cfg, s.V, p2)
	t.Cleanup(func() { s2.store.close() })
	ts2 := httptest.NewServer(s2.Handler())
	t.Cleanup(ts2.Close)

	open, _ := practices(t, ts2)
	if len(open) != 1 || open[0].Status != practicePaused || open[0].Answered != 1 {
		t.Fatalf("paused practice after restart: %+v", open)
	}
	res, data := do(t, ts2, "POST", "/api/practice/"+id+"/resume", map[string]any{}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("resume: %d %s", res.StatusCode, data)
	}
	var out struct {
		Run int `json:"run"`
	}
	json.Unmarshal(data, &out)
	// the earlier transcript carries over, and it goes on at question 2 without generating again
	evs = waitAsks(t, ts2, id, "line", out.Run, 2) // the old one for question 1, the new one for 2
	var asked []int
	for _, e := range evs {
		if e.Type == "card" && e.Card.Kind == session.CardQuestion {
			asked = append(asked, e.Card.N)
		}
	}
	if len(asked) != 2 || asked[0] != 1 || asked[1] != 2 {
		t.Fatalf("questions shown: %v", asked)
	}
	for _, a := range []string{"dos", "tres"} {
		do(t, ts2, "POST", "/api/runs/"+itoa(out.Run)+"/input", map[string]any{"text": a}, nil)
		if a == "dos" {
			waitAsks(t, ts2, id, "line", out.Run, 3)
		}
	}
	readEvents(t, ts2, out.Run, 0, func(e []Event) bool { return has(e, "done") != nil })
	if len(p2.reqs) != 1 || !strings.Contains(p2.reqs[0].Prompt, "Learner's answer: uno") || !strings.Contains(p2.reqs[0].Prompt, "Learner's answer: tres") {
		t.Fatalf("grading request: %+v", p2.reqs)
	}
	n, _ := s2.V.Load("cumpleanos")
	if n.Meta.Status != "tested" || n.Meta.Mastery != 80 {
		t.Errorf("a finished quiz sets mastery: %+v", n.Meta)
	}
	if _, recent := practices(t, ts2); len(recent) != 1 || recent[0].Status != practiceDone {
		t.Errorf("recent: %+v", recent)
	}
}

func TestStalePracticeIsPausedAndCanBeDiscarded(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	guidedNote(t, s)
	rec := &practiceRec{ID: "20261027-090000-abcdef", Kind: "feynman", Slug: "cumpleanos", Title: "Paradoja", Status: practiceRunning,
		State: session.Practice{Kind: "feynman", Slug: "cumpleanos"}}
	s.prMu.Lock()
	s.writePractice(rec)
	s.prMu.Unlock()
	s.pauseStalePractices()
	open, _ := practices(t, ts)
	if len(open) != 1 || open[0].Status != practicePaused {
		t.Fatalf("stale practice: %+v", open)
	}
	// finishing one without answers closes it without a grade
	res, data := do(t, ts, "POST", "/api/practice/"+rec.ID+"/finish", map[string]any{}, nil)
	if res.StatusCode != 200 || !strings.Contains(string(data), `"empty":true`) {
		t.Fatalf("finish empty: %d %s", res.StatusCode, data)
	}
	res, data = do(t, ts, "DELETE", "/api/practice/"+rec.ID, nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("discard: %d %s", res.StatusCode, data)
	}
	if _, err := os.Stat(filepath.Join(s.V.Root, filepath.FromSlash(vault.PracticeDir), rec.ID+".json")); !os.IsNotExist(err) {
		t.Errorf("discarded practice still there: %v", err)
	}
	if items, _ := s.V.TrashList(); len(items) != 1 || items[0].Kind != vault.CatPractice {
		t.Errorf("trash: %+v", items)
	}
}

func TestSlotsQueueFlows(t *testing.T) {
	var sl slots
	if err := sl.acquire(t.Context(), 1, nil); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	got := make(chan struct{})
	go func() {
		sl.acquire(t.Context(), 1, func() { close(waited) })
		close(got)
	}()
	<-waited
	select {
	case <-got:
		t.Fatal("a second flow must wait for the slot")
	case <-time.After(30 * time.Millisecond):
	}
	sl.release(1)
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("the slot was not handed over")
	}
}
