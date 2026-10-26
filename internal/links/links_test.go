package links

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAlive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(200)
		case "/gone":
			w.WriteHeader(404)
		case "/blocked":
			w.WriteHeader(403)
		case "/nohead":
			if r.Method == http.MethodHead {
				w.WriteHeader(405)
				return
			}
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()

	cases := map[string]bool{"/ok": true, "/gone": false, "/blocked": true, "/nohead": true}
	for path, want := range cases {
		if got := Alive(context.Background(), srv.URL+path); got != want {
			t.Errorf("Alive(%s) = %v, want %v", path, got, want)
		}
	}
	if Alive(context.Background(), "http://127.0.0.1:1/x") {
		t.Error("unreachable host must be dead")
	}
}

func TestCheckAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	res := CheckAll(context.Background(), []string{srv.URL + "/good", srv.URL + "/bad"})
	if !res[srv.URL+"/good"] || res[srv.URL+"/bad"] {
		t.Errorf("unexpected results: %v", res)
	}
}
