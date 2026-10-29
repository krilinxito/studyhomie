package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestMergeOutJoinsChunks(t *testing.T) {
	got := mergeOut([]Event{
		{Type: "out", Text: "Ho"}, {Type: "out", Text: "la"}, {Type: "ask", Kind: "line"},
		{Type: "in", Text: "x"}, {Type: "out", Text: "a"}, {Type: "out", Text: "b"}, {Type: "done"},
	})
	if len(got) != 5 || got[0].Text != "Hola" || got[3].Text != "ab" {
		t.Fatalf("chunks not merged: %+v", got)
	}
}

func finishedRun(t *testing.T, s *Server, topic string) int {
	t.Helper()
	run, err := s.StartRun(StartReq{Cmd: "study", Topic: topic})
	if err != nil {
		t.Fatal(err)
	}
	run.cancel()
	run.in.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !run.isDone() {
		if time.Now().After(deadline) {
			t.Fatal("run did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return run.ID
}

func TestSessionsSurviveARestart(t *testing.T) {
	s, _ := newTestServer(t, &scripted{hold: make(chan struct{})})
	id := finishedRun(t, s, "Teorema de Bayes")

	file := filepath.Join(s.V.Root, ".studyhomie", "sessions", itoa(id)+".json")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the session was not written: %v", err)
	}
	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatal(err)
	}
	if sf.Title != "Teorema de Bayes" || sf.Slug != "teorema-de-bayes" || sf.Status != statusCancelled {
		t.Errorf("unexpected stored session: %+v", sf)
	}
	if n := len(sf.Events); n == 0 || sf.Events[n-1].Type != "done" {
		t.Errorf("a stored session must end with its done event: %+v", sf.Events)
	}

	// a new server on the same vault sees it, and new runs do not reuse its id
	s2 := New(config.Default(), s.V, s.P)
	s2.Now = s.Now
	if _, ok := s2.runs[id]; !ok {
		t.Fatalf("the session was not loaded back: %v", s2.runs)
	}
	if s2.seq != id {
		t.Errorf("next id would collide: seq=%d, stored id=%d", s2.seq, id)
	}
}

func TestRunningSessionIsMarkedInterrupted(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(v.Root, ".studyhomie", "sessions")
	os.MkdirAll(dir, 0o755)
	stale, _ := json.Marshal(sessionFile{
		ID: 4, Cmd: "study", Title: "Entropía", Started: time.Now(), Status: statusRunning,
		Events: []Event{{Type: "out", Text: "Empiezo…"}},
	})
	os.WriteFile(filepath.Join(dir, "4.json"), stale, 0o644)
	os.WriteFile(filepath.Join(dir, "notes.json"), []byte("{}"), 0o644) // not a session
	os.WriteFile(filepath.Join(dir, "7.json"), []byte("not json"), 0o644)

	s := New(config.Default(), v, &scripted{})
	run := s.runs[4]
	if run == nil || len(s.runs) != 1 {
		t.Fatalf("expected only session 4, got %v", s.runs)
	}
	sum := run.summary()
	if sum.Status != statusInterrupted || !run.isDone() {
		t.Errorf("a session left running must come back interrupted: %+v", sum)
	}
	evs, done := run.since(0)
	if !done || evs[len(evs)-1].Type != "done" || !strings.Contains(evs[len(evs)-1].Error, "interrumpi") {
		t.Errorf("the replay must end with a done event: %+v", evs)
	}
	// fixed on disk too, so the next start does not do it again
	data, _ := os.ReadFile(filepath.Join(dir, "4.json"))
	if !strings.Contains(string(data), statusInterrupted) {
		t.Errorf("the stored file was not updated: %s", data)
	}
}

func TestSessionsEndpoints(t *testing.T) {
	s, ts := newTestServer(t, &scripted{hold: make(chan struct{})})
	first := finishedRun(t, s, "Bayes")
	second := finishedRun(t, s, "Entropía")

	res, data := do(t, ts, "GET", "/api/sessions", nil, nil)
	var list []summary
	json.Unmarshal(data, &list)
	if res.StatusCode != 200 || len(list) != 2 || list[0].ID != second || list[1].ID != first {
		t.Fatalf("expected both sessions, newest first: %d %s", res.StatusCode, data)
	}

	// replaying a finished session works through the normal events endpoint
	evs := readEvents(t, ts, first, 0, func(e []Event) bool { return has(e, "done") != nil })
	if has(evs, "done") == nil {
		t.Errorf("a finished session must replay up to done: %+v", evs)
	}

	res, _ = do(t, ts, "DELETE", "/api/sessions/"+itoa(first), nil, func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") })
	if res.StatusCode != 403 {
		t.Errorf("a foreign origin must not delete: %d", res.StatusCode)
	}
	res, _ = do(t, ts, "DELETE", "/api/sessions/"+itoa(first), nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("delete failed: %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(s.V.Root, ".studyhomie", "sessions", itoa(first)+".json")); !os.IsNotExist(err) {
		t.Errorf("the file should be gone: %v", err)
	}
	res, _ = do(t, ts, "DELETE", "/api/sessions/"+itoa(first), nil, nil)
	if res.StatusCode != 404 {
		t.Errorf("deleting twice should be 404, got %d", res.StatusCode)
	}
}

func TestRunningSessionCannotBeDeleted(t *testing.T) {
	s, ts := newTestServer(t, &scripted{hold: make(chan struct{})})
	run, err := s.StartRun(StartReq{Cmd: "study", Topic: "Bayes"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { run.cancel(); run.in.Close() }()
	if res, _ := do(t, ts, "DELETE", "/api/sessions/"+itoa(run.ID), nil, nil); res.StatusCode != 409 {
		t.Errorf("a running session must be cancelled first, got %d", res.StatusCode)
	}
}
