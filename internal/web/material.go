package web

import (
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/krilinxito/studyhomie/internal/vault"
)

func (s *Server) vaultMaterials(w http.ResponseWriter, r *http.Request) {
	list, err := s.vault().ListMaterials()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []vault.MaterialFile{}
	}
	writeJSON(w, 200, list)
}

// materialFile serves a file copied into vault/material/<topic>/<file>. Images and PDFs open
// inline inside a strict sandbox CSP; everything else downloads as an attachment.
func (s *Server) materialFile(w http.ResponseWriter, r *http.Request) {
	topic := r.PathValue("topic")
	name := r.PathValue("file")
	p, err := s.vault().MaterialFilePath(topic, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(name))
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	switch {
	case isImage(name):
		h.Set("Content-Type", mime.TypeByExtension(ext))
	case ext == ".pdf":
		h.Set("Content-Type", "application/pdf")
		h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	case ext == ".txt" || ext == ".md":
		h.Set("Content-Type", "text/plain; charset=utf-8")
	default:
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	http.ServeFile(w, r, p)
}
