package vault

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The learner's own notes live apart from what the app writes, so writing a guide again never
// touches them: apuntes/<slug>.md, and the files pasted into them in apuntes/adjuntos/.
const (
	ApuntesDir = "apuntes"
	AttachDir  = "apuntes/adjuntos"
)

// ApunteMeta is the frontmatter of a note of the learner's.
type ApunteMeta struct {
	Title   string `yaml:"titulo"`
	Of      string `yaml:"de"` // a wikilink to the guide or concept: [[slug]]
	Created string `yaml:"creado,omitempty"`
	Updated string `yaml:"actualizado,omitempty"`
}

// Apunte is the learner's note on a guide or a concept.
type Apunte struct {
	Slug string
	Meta ApunteMeta
	Body string
}

func (v *Vault) apuntePath(slug string) string {
	return filepath.Join(v.Root, ApuntesDir, slug+".md")
}

// LoadApunte reads the learner's note on slug; an empty one when there is none yet.
func (v *Vault) LoadApunte(slug string) (Apunte, error) {
	if Slugify(slug) != slug || slug == "" {
		return Apunte{}, fmt.Errorf("nota inválida %q", slug)
	}
	a := Apunte{Slug: slug}
	data, err := os.ReadFile(v.apuntePath(slug))
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	a.Body, err = parseFront(slug, data, &a.Meta)
	return a, err
}

// SaveApunte writes the learner's note on slug (title is the guide's or concept's).
// An empty body removes the file: no empty notes are left in the vault.
func (v *Vault) SaveApunte(slug, title, body string) (Apunte, error) {
	old, err := v.LoadApunte(slug)
	if err != nil {
		return Apunte{}, err
	}
	path := v.apuntePath(slug)
	if strings.TrimSpace(body) == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Apunte{}, err
		}
		return Apunte{Slug: slug}, nil
	}
	now := time.Now().Format("2006-01-02")
	a := Apunte{Slug: slug, Body: strings.TrimRight(body, " \n") + "\n", Meta: ApunteMeta{
		Title: "Apuntes: " + title, Of: "[[" + slug + "]]", Created: old.Meta.Created, Updated: now,
	}}
	if a.Meta.Created == "" {
		a.Meta.Created = now
	}
	data, err := marshalFront(a.Meta, a.Body)
	if err != nil {
		return Apunte{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Apunte{}, err
	}
	return a, os.WriteFile(path, data, 0o644)
}

// HasApunte reports whether the learner wrote something on slug.
func (v *Vault) HasApunte(slug string) bool {
	_, err := os.Stat(v.apuntePath(slug))
	return err == nil
}

// SaveAttachment stores a file pasted into the notes and returns the name it got: the original
// one made safe, with a number added when that name is taken. Obsidian finds it by that name.
func (v *Vault) SaveAttachment(name string, r io.Reader, limit int64) (string, error) {
	dir := filepath.Join(v.Root, AttachDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	base, ext := safeFileName(name)
	for i := 0; i < 1000; i++ {
		n := base + ext
		if i > 0 {
			n = fmt.Sprintf("%s-%d%s", base, i+1, ext)
		}
		f, err := os.OpenFile(filepath.Join(dir, n), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		written, err := io.Copy(f, io.LimitReader(r, limit+1))
		f.Close()
		if err == nil && written > limit {
			err = fmt.Errorf("el archivo pasa de %d MB", limit>>20)
		}
		if err != nil {
			os.Remove(filepath.Join(dir, n))
			return "", err
		}
		return n, nil
	}
	return "", errors.New("demasiados archivos con ese nombre")
}

// AttachmentPath is where a stored attachment is, for a name SaveAttachment gave.
func (v *Vault) AttachmentPath(name string) (string, error) {
	if b, e := safeFileName(name); b+e != name {
		return "", fmt.Errorf("adjunto inválido %q", name)
	}
	return filepath.Join(v.Root, AttachDir, name), nil
}

// safeFileName keeps letters, digits, dashes and underscores of a file name, and a short extension.
func safeFileName(name string) (base, ext string) {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	ext = strings.ToLower(filepath.Ext(name))
	base = strings.TrimSuffix(name, filepath.Ext(name))
	clean := func(s string, extra string) string {
		var b strings.Builder
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune(extra, r):
				b.WriteRune(r)
			case r == ' ' || r == '.':
				b.WriteRune('-')
			}
		}
		return b.String()
	}
	base = strings.Trim(clean(foldAccents(base), "-_"), "-")
	if len(base) > 60 {
		base = base[:60]
	}
	if base == "" {
		base = "adjunto"
	}
	ext = "." + clean(strings.TrimPrefix(ext, "."), "")
	if ext == "." || len(ext) > 8 {
		ext = ""
	}
	return base, ext
}
