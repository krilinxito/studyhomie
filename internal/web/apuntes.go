package web

import (
	"encoding/json"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/krilinxito/studyhomie/internal/vault"
)

// maxAttachment bounds one file pasted into the notes.
const maxAttachment = 25 << 20

// apunteTitle is the title of the guide or concept a note is about, or "" when there is none.
func (s *Server) apunteTitle(slug string) string {
	if n, err := s.V.Load(slug); err == nil {
		if n.Meta.Title != "" {
			return n.Meta.Title
		}
		return slug
	}
	if c, err := s.V.LoadConcept(slug); err == nil {
		if c.Meta.Title != "" {
			return c.Meta.Title
		}
		return slug
	}
	return ""
}

func (s *Server) getApunte(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	a, err := s.V.LoadApunte(slug)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"body": a.Body, "updated": a.Meta.Updated, "file": vault.ApuntesDir + "/" + slug + ".md"})
}

func (s *Server) saveApunte(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	var body struct {
		Body string `json:"body"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 400, "petición inválida")
		return
	}
	title := s.apunteTitle(slug)
	if title == "" {
		fail(w, 404, "no existe esa guía ni ese concepto")
		return
	}
	a, err := s.V.SaveApunte(slug, title, body.Body)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"updated": a.Meta.Updated})
}

// attach stores files pasted or dropped into the notes; each answer says how to embed it.
func (s *Server) attach(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		fail(w, 400, "subida inválida: "+err.Error())
		return
	}
	defer r.MultipartForm.RemoveAll()
	type stored struct {
		Name  string `json:"name"`
		Image bool   `json:"image"`
	}
	var out []stored
	for _, fh := range r.MultipartForm.File["files"] {
		src, err := fh.Open()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		name, err := s.V.SaveAttachment(fh.Filename, src, maxAttachment)
		src.Close()
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		out = append(out, stored{Name: name, Image: isImage(name)})
	}
	if len(out) == 0 {
		fail(w, 400, "no llegó ningún archivo")
		return
	}
	writeJSON(w, 200, map[string]any{"files": out})
}

func isImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp":
		return true
	}
	return false
}

// attachment serves a stored file. Images show inline; anything else downloads, and nothing
// it holds can run in the app's origin.
func (s *Server) attachment(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, err := s.V.AttachmentPath(name)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if isImage(name) {
		h.Set("Content-Type", mime.TypeByExtension(strings.ToLower(filepath.Ext(name))))
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	http.ServeFile(w, r, path)
}
