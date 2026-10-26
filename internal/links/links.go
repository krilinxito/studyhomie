// Package links checks that URLs the model cites actually exist.
package links

import (
	"context"
	"net/http"
	"sync"
	"time"
)

var client = &http.Client{Timeout: 10 * time.Second}

// Alive reports whether a URL looks real. Only definite failures (network errors,
// 404, 410) count as dead; 403/429 and similar are kept because many sites block
// bots while the page exists.
func Alive(ctx context.Context, url string) bool {
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return false
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; StudyHomie link check)")
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
			return false
		case resp.StatusCode == http.StatusMethodNotAllowed && method == http.MethodHead:
			continue // some servers reject HEAD; retry with GET
		default:
			return true
		}
	}
	return false
}

// CheckAll checks URLs concurrently and returns a map url -> alive.
func CheckAll(ctx context.Context, urls []string) map[string]bool {
	res := make(map[string]bool, len(urls))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, u := range urls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ok := Alive(ctx, u)
			mu.Lock()
			res[u] = ok
			mu.Unlock()
		}()
	}
	wg.Wait()
	return res
}
