// Package httpx holds what the HTTP backends share: posting JSON, reading a
// Server-Sent Events stream, attaching images, and a conversation memory for the
// APIs that keep no state of their own.
package httpx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Client is the HTTP client the backends use; tests swap the transport.
var Client = &http.Client{}

// PostStream sends a JSON body and calls onData with each "data:" payload of the
// SSE response, until the stream ends or onData returns an error.
func PostStream(ctx context.Context, url string, headers map[string]string, body any, onData func([]byte) error) error {
	resp, err := post(ctx, url, headers, body, "text/event-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	var data bytes.Buffer
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		payload := bytes.TrimSpace(data.Bytes())
		data.Reset()
		if string(payload) == "[DONE]" {
			return io.EOF
		}
		return onData(payload)
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func post(ctx context.Context, url string, headers map[string]string, body any, accept string) (*http.Response, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
	}
	return resp, nil
}

// StatusError is a non-2xx answer from an API.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	body := e.Body
	if len(body) > 600 {
		body = body[:600] + "…"
	}
	return fmt.Sprintf("la API respondió %d: %s", e.Code, body)
}

// Image is an attached picture, ready to inline in a request.
type Image struct {
	MIME string
	Data []byte
}

// DataURL renders the image as a data: URL.
func (i Image) DataURL() string {
	return "data:" + i.MIME + ";base64," + base64.StdEncoding.EncodeToString(i.Data)
}

// Base64 is the image's bytes encoded.
func (i Image) Base64() string { return base64.StdEncoding.EncodeToString(i.Data) }

// imageTypes are spelled out: the system's MIME table differs between machines (Windows reads the registry).
var imageTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif"}

// Attachments reads the files of a request: images come back as images, anything else is
// inlined as text after the prompt (a PDF the backend cannot read is named, not sent).
func Attachments(files []string) (images []Image, text string, err error) {
	var b strings.Builder
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, "", err
		}
		ext := strings.ToLower(filepath.Ext(f))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".webp", ".gif":
			images = append(images, Image{MIME: imageTypes[ext], Data: data})
		case ".pdf":
			fmt.Fprintf(&b, "\n(El archivo %s es un PDF que este modelo no puede leer.)\n", filepath.Base(f))
		default:
			fmt.Fprintf(&b, "\nArchivo %s:\n%s\n", filepath.Base(f), data)
		}
	}
	return images, b.String(), nil
}

// Memory keeps conversations for stateless APIs, by session id.
type Memory[M any] struct {
	mu      sync.Mutex
	prefix  string
	counter int
	byID    map[string][]M
}

// NewMemory makes a memory whose session ids start with prefix.
func NewMemory[M any](prefix string) *Memory[M] {
	return &Memory[M]{prefix: prefix, byID: map[string][]M{}}
}

// Begin returns the session id and a copy of its messages, or opens a new session.
func (m *Memory[M]) Begin(id string) (string, []M, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		m.counter++
		return fmt.Sprintf("%s-%d", m.prefix, m.counter), nil, nil
	}
	h, ok := m.byID[id]
	if !ok {
		return "", nil, fmt.Errorf("unknown session %q", id)
	}
	return id, append([]M(nil), h...), nil
}

// Save stores the messages of a session.
func (m *Memory[M]) Save(id string, msgs []M) {
	m.mu.Lock()
	m.byID[id] = msgs
	m.mu.Unlock()
}

// Key returns the API key in the first of the environment variables that is set.
func Key(envs ...string) string {
	for _, e := range envs {
		if v := strings.TrimSpace(os.Getenv(e)); v != "" {
			return v
		}
	}
	return ""
}

// ListModels reads an OpenAI-style model list ({"data":[{"id":...}]}), sorted.
func ListModels(ctx context.Context, url string, headers map[string]string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		if v != "" && v != "Bearer " {
			req.Header.Set(k, v)
		}
	}
	resp, err := Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return out, nil
}
