package vault

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	MaterialDir   = "material"
	FlashcardsDir = "flashcards"
)

var safeMatName = regexp.MustCompile(`[^\p{L}\p{N}._ -]+`)

// MaterialFile is one source file stored under vault/material/<topic>/<file>.
type MaterialFile struct {
	Topic   string    `json:"topic"`
	Name    string    `json:"name"`
	Rel     string    `json:"rel"` // "material/<topic>/<name>"
	Updated time.Time `json:"updated"`
	Size    int64     `json:"size"`
}

func cleanMaterialName(raw string) string {
	base := filepath.Base(raw)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	stem = strings.Trim(safeMatName.ReplaceAllString(stem, "_"), ". ")
	if stem == "" {
		stem = "archivo"
	}
	ext = strings.ToLower(safeMatName.ReplaceAllString(ext, ""))
	return stem + ext
}

// CopyMaterial copies a source file into vault/material/<topicSlug>/<file> (never moving the
// original) and returns its vault-relative slash path "material/<topicSlug>/<file>".
func (v *Vault) CopyMaterial(topicSlug, srcPath string) (string, error) {
	topicSlug = Slugify(topicSlug)
	if topicSlug == "" {
		topicSlug = "general"
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer src.Close()

	name := cleanMaterialName(srcPath)
	dir := filepath.Join(v.Root, MaterialDir, topicSlug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dstPath := filepath.Join(dir, name)
	// Avoid clobbering if srcPath is already the exact same file.
	if absSrc, err1 := filepath.Abs(srcPath); err1 == nil {
		if absDst, err2 := filepath.Abs(dstPath); err2 == nil && strings.EqualFold(absSrc, absDst) {
			return MaterialDir + "/" + topicSlug + "/" + name, nil
		}
	}
	dst, err := os.Create(dstPath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(dstPath)
		return "", err
	}
	if err := dst.Close(); err != nil {
		return "", err
	}
	return MaterialDir + "/" + topicSlug + "/" + name, nil
}

// MaterialFilePath validates topic and file name and returns the absolute path on disk.
func (v *Vault) MaterialFilePath(topicSlug, name string) (string, error) {
	if topicSlug == "" || Slugify(topicSlug) != topicSlug {
		return "", fmt.Errorf("tema de material inválido")
	}
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." || filepath.Base(name) != name {
		return "", fmt.Errorf("archivo de material inválido")
	}
	p := filepath.Join(v.Root, MaterialDir, topicSlug, name)
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

// ListMaterials lists all files inside vault/material/<topic>/, newest first.
func (v *Vault) ListMaterials() ([]MaterialFile, error) {
	root := filepath.Join(v.Root, MaterialDir)
	var out []MaterialFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 2 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, MaterialFile{
			Topic:   parts[0],
			Name:    parts[1],
			Rel:     MaterialDir + "/" + parts[0] + "/" + parts[1],
			Updated: info.ModTime(),
			Size:    info.Size(),
		})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, err
}
