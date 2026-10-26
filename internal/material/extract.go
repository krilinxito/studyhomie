// Package material turns user documents into text and finds the parts relevant to a topic.
package material

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
)

// Kind says how a file is handled.
type Kind int

const (
	Text  Kind = iota // text can be extracted locally
	Image             // needs the model to read it
)

// Classify returns how a path will be ingested, or an error for unsupported types.
func Classify(path string) (Kind, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".markdown", ".pptx", ".docx", ".pdf":
		return Text, nil
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return Image, nil
	}
	return Text, fmt.Errorf("tipo de archivo no soportado: %s (acepto txt, md, pdf, pptx, docx y imágenes)", filepath.Ext(path))
}

// Extract returns the text of a Text-kind file.
func Extract(path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".markdown":
		data, err := os.ReadFile(path)
		return string(data), err
	case ".pptx":
		return officeText(path, `^ppt/slides/slide(\d+)\.xml$`, "Slide")
	case ".docx":
		return officeText(path, `^word/document\.xml$`, "")
	case ".pdf":
		return pdfText(path)
	}
	return "", fmt.Errorf("unsupported file %s", path)
}

// officeText reads text runs (<a:t> / <w:t>) from the matching XML parts of an OOXML zip.
func officeText(path, partRe, label string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("%s no es un archivo válido: %w", filepath.Base(path), err)
	}
	defer zr.Close()
	re := regexp.MustCompile(partRe)
	type part struct {
		n int
		f *zip.File
	}
	var parts []part
	for _, f := range zr.File {
		if m := re.FindStringSubmatch(f.Name); m != nil {
			n := 0
			if len(m) > 1 {
				n, _ = strconv.Atoi(m[1])
			}
			parts = append(parts, part{n, f})
		}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].n < parts[j].n })
	var b strings.Builder
	for _, p := range parts {
		rc, err := p.f.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return "", err
		}
		if label != "" {
			fmt.Fprintf(&b, "## %s %d\n\n", label, p.n)
		}
		b.WriteString(xmlText(data))
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String()), nil
}

// xmlText concatenates character data of <t> elements, one paragraph (<p>) per line.
func xmlText(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var b strings.Builder
	inT := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			inT = t.Name.Local == "t"
		case xml.EndElement:
			inT = false
			if t.Name.Local == "p" {
				b.WriteByte('\n')
			}
		case xml.CharData:
			if inT {
				b.Write(t)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func pdfText(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("no pude abrir el PDF: %w", err)
	}
	defer f.Close()
	var b strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		txt, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "## Página %d\n\n%s\n\n", i, strings.TrimSpace(txt))
	}
	out := strings.TrimSpace(b.String())
	if len(strings.Fields(out)) < 20 {
		return "", fmt.Errorf("el PDF casi no tiene texto (¿es un escaneo?). Conviértelo a imágenes y pásalas una a una")
	}
	return out, nil
}
