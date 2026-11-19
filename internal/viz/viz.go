// Package viz checks the interactive widgets a model writes into a note as ```viz blocks
// (JSON). The browser draws them; this package makes sure each block is well formed, so a
// slip in one widget is dropped instead of showing up broken in the learner's guide.
package viz

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Limits keep widgets small enough to read and cheap to draw.
const (
	maxParams   = 5
	maxCurves   = 5
	maxPoints   = 8
	maxReadouts = 4
	maxVectors  = 4
	maxFrames   = 24
	maxOutcomes = 24
	maxDraws    = 12
	maxOptions  = 6
	maxExpr     = 200
	maxText     = 1200
)

// Param is a slider.
type Param struct {
	Name  string  `json:"name"`
	Label string  `json:"label,omitempty"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Step  float64 `json:"step,omitempty"`
	Value float64 `json:"value"`
}

type Curve struct {
	Expr  string `json:"expr"`
	Label string `json:"label,omitempty"`
}

type Point struct {
	X     string `json:"x"`
	Y     string `json:"y"`
	Label string `json:"label,omitempty"`
}

type Readout struct {
	Label string `json:"label"`
	Expr  string `json:"expr"`
}

type Vector struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Label string  `json:"label,omitempty"`
}

type Table struct {
	Head []string `json:"head,omitempty"`
	Rows [][]any  `json:"rows"`
}

type Frame struct {
	Text      string         `json:"text"`
	Array     []any          `json:"array,omitempty"`
	Table     *Table         `json:"table,omitempty"`
	Formula   string         `json:"formula,omitempty"`
	Highlight []int          `json:"highlight,omitempty"`
	Pointers  map[string]int `json:"pointers,omitempty"`
}

type Outcome struct {
	Label string   `json:"label"`
	Value *float64 `json:"value,omitempty"`
	P     float64  `json:"p"`
}

// Block is one widget. Only the fields of its type are used.
type Block struct {
	Type    string `json:"type"`
	Title   string `json:"title,omitempty"`
	Caption string `json:"caption,omitempty"`

	// plot
	X        []float64 `json:"x,omitempty"`
	Y        []float64 `json:"y,omitempty"`
	Params   []Param   `json:"params,omitempty"`
	Curves   []Curve   `json:"curves,omitempty"`
	Points   []Point   `json:"points,omitempty"`
	Readouts []Readout `json:"readouts,omitempty"`

	// transform2d
	Matrix  [][]any  `json:"matrix,omitempty"`
	Vectors []Vector `json:"vectors,omitempty"`

	// steps
	Frames []Frame `json:"frames,omitempty"`

	// sample
	Outcomes []Outcome `json:"outcomes,omitempty"`
	Draws    int       `json:"draws,omitempty"`
	Measure  string    `json:"measure,omitempty"`

	// predict
	Question string   `json:"question,omitempty"`
	Options  []string `json:"options,omitempty"`
	Answer   *int     `json:"answer,omitempty"`
	Explain  string   `json:"explain,omitempty"`

	// html
	Brief string `json:"brief,omitempty"`
	Src   string `json:"src,omitempty"` // set by the app once the page is generated
}

// Parse decodes and checks one block.
func Parse(data string) (Block, error) {
	var b Block
	dec := json.NewDecoder(strings.NewReader(data))
	if err := dec.Decode(&b); err != nil {
		return b, fmt.Errorf("JSON inválido: %w", err)
	}
	return b, b.Check()
}

// Check reports the first problem of a block, or nil.
func (b *Block) Check() error {
	if err := text("title", b.Title); err != nil {
		return err
	}
	if err := text("caption", b.Caption); err != nil {
		return err
	}
	switch b.Type {
	case "plot":
		return b.checkPlot()
	case "transform2d":
		return b.checkTransform()
	case "steps":
		return b.checkSteps()
	case "sample":
		return b.checkSample()
	case "predict":
		return b.checkPredict()
	case "html":
		if strings.TrimSpace(b.Brief) == "" {
			return fmt.Errorf("html sin brief")
		}
		if b.Src != "" && !strings.HasPrefix(b.Src, "interactive/") {
			return fmt.Errorf("src inválido %q", b.Src)
		}
		return text("brief", b.Brief)
	case "":
		return fmt.Errorf("falta type")
	}
	return fmt.Errorf("tipo desconocido %q", b.Type)
}

func text(field, s string) error {
	if len([]rune(s)) > maxText {
		return fmt.Errorf("%s demasiado largo", field)
	}
	return nil
}

func finite(fs ...float64) bool {
	for _, f := range fs {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
	}
	return true
}

func span(name string, r []float64, def [2]float64) ([]float64, error) {
	if len(r) == 0 {
		return def[:], nil
	}
	if len(r) != 2 || !finite(r...) || r[0] >= r[1] {
		return nil, fmt.Errorf("rango %s inválido %v", name, r)
	}
	return r, nil
}

// checkParams validates sliders and returns the names expressions may use.
func (b *Block) checkParams(extra ...string) (map[string]bool, error) {
	if len(b.Params) > maxParams {
		return nil, fmt.Errorf("demasiados parámetros (%d)", len(b.Params))
	}
	vars := map[string]bool{}
	for _, v := range extra {
		vars[v] = true
	}
	for i := range b.Params {
		p := &b.Params[i]
		if !isIdent(p.Name) || reserved[p.Name] || vars[p.Name] {
			return nil, fmt.Errorf("nombre de parámetro inválido %q", p.Name)
		}
		if !finite(p.Min, p.Max, p.Step, p.Value) || p.Min >= p.Max {
			return nil, fmt.Errorf("rango del parámetro %s inválido", p.Name)
		}
		if p.Step <= 0 || p.Step > p.Max-p.Min {
			p.Step = (p.Max - p.Min) / 100
		}
		p.Value = math.Min(math.Max(p.Value, p.Min), p.Max)
		if p.Label == "" {
			p.Label = p.Name
		}
		vars[p.Name] = true
	}
	return vars, nil
}

func (b *Block) checkPlot() error {
	var err error
	if b.X, err = span("x", b.X, [2]float64{-5, 5}); err != nil {
		return err
	}
	if b.Y, err = span("y", b.Y, [2]float64{-5, 5}); err != nil {
		return err
	}
	vars, err := b.checkParams("x")
	if err != nil {
		return err
	}
	if len(b.Curves) == 0 && len(b.Points) == 0 {
		return fmt.Errorf("plot sin curvas ni puntos")
	}
	if len(b.Curves) > maxCurves || len(b.Points) > maxPoints || len(b.Readouts) > maxReadouts {
		return fmt.Errorf("plot con demasiados elementos")
	}
	for _, c := range b.Curves {
		if err := CheckExpr(c.Expr, vars); err != nil {
			return err
		}
	}
	noX := withoutX(vars)
	for _, p := range b.Points {
		if err := CheckExpr(p.X, noX); err != nil {
			return err
		}
		if err := CheckExpr(p.Y, noX); err != nil {
			return err
		}
	}
	return b.checkReadouts(noX)
}

func withoutX(vars map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range vars {
		if k != "x" {
			out[k] = true
		}
	}
	return out
}

func (b *Block) checkReadouts(vars map[string]bool) error {
	if len(b.Readouts) > maxReadouts {
		return fmt.Errorf("demasiadas lecturas")
	}
	for _, r := range b.Readouts {
		if err := CheckExpr(r.Expr, vars); err != nil {
			return err
		}
	}
	return nil
}

func (b *Block) checkTransform() error {
	vars, err := b.checkParams()
	if err != nil {
		return err
	}
	if len(b.Matrix) != 2 || len(b.Matrix[0]) != 2 || len(b.Matrix[1]) != 2 {
		return fmt.Errorf("la matriz debe ser 2x2")
	}
	for i, row := range b.Matrix {
		for j, cell := range row {
			switch v := cell.(type) {
			case float64:
				if !finite(v) {
					return fmt.Errorf("matriz con valor no finito")
				}
				b.Matrix[i][j] = trimFloat(v)
			case string:
				if err := CheckExpr(v, vars); err != nil {
					return err
				}
			default:
				return fmt.Errorf("celda de matriz inválida")
			}
		}
	}
	if len(b.Vectors) > maxVectors {
		return fmt.Errorf("demasiados vectores")
	}
	for _, v := range b.Vectors {
		if !finite(v.X, v.Y) {
			return fmt.Errorf("vector inválido")
		}
	}
	return b.checkReadouts(vars)
}

func trimFloat(v float64) string { return fmt.Sprint(v) }

func (b *Block) checkSteps() error {
	if len(b.Frames) == 0 || len(b.Frames) > maxFrames {
		return fmt.Errorf("steps necesita entre 1 y %d frames", maxFrames)
	}
	for i, f := range b.Frames {
		if strings.TrimSpace(f.Text) == "" && f.Array == nil && f.Table == nil && f.Formula == "" {
			return fmt.Errorf("frame %d vacío", i+1)
		}
		if err := text("frame", f.Text); err != nil {
			return err
		}
		if len(f.Array) > 40 {
			return fmt.Errorf("array demasiado largo")
		}
		if f.Table != nil && (len(f.Table.Rows) > 20 || len(f.Table.Head) > 8) {
			return fmt.Errorf("tabla demasiado grande")
		}
	}
	return nil
}

func (b *Block) checkSample() error {
	if len(b.Outcomes) < 2 || len(b.Outcomes) > maxOutcomes {
		return fmt.Errorf("sample necesita entre 2 y %d resultados", maxOutcomes)
	}
	total := 0.0
	numeric := true
	for _, o := range b.Outcomes {
		if !finite(o.P) || o.P < 0 {
			return fmt.Errorf("probabilidad inválida")
		}
		total += o.P
		if o.Value == nil {
			numeric = false
		} else if !finite(*o.Value) {
			return fmt.Errorf("valor inválido")
		}
	}
	if total <= 0 {
		return fmt.Errorf("las probabilidades suman 0")
	}
	if b.Draws < 1 {
		b.Draws = 1
	}
	if b.Draws > maxDraws {
		return fmt.Errorf("demasiados draws (%d)", b.Draws)
	}
	if b.Draws > 1 {
		if !numeric {
			return fmt.Errorf("draws > 1 necesita valores numéricos")
		}
		if b.Measure != "mean" {
			b.Measure = "sum"
		}
	} else {
		b.Measure = ""
	}
	return nil
}

func (b *Block) checkPredict() error {
	if strings.TrimSpace(b.Question) == "" {
		return fmt.Errorf("predict sin pregunta")
	}
	if len(b.Options) < 2 || len(b.Options) > maxOptions {
		return fmt.Errorf("predict necesita entre 2 y %d opciones", maxOptions)
	}
	if b.Answer == nil || *b.Answer < 0 || *b.Answer >= len(b.Options) {
		return fmt.Errorf("predict con respuesta fuera de rango")
	}
	for _, s := range append([]string{b.Question, b.Explain}, b.Options...) {
		if err := text("predict", s); err != nil {
			return err
		}
	}
	return nil
}

// Encode renders a block back as compact JSON, the form stored in the note.
func (b Block) Encode() string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(b)
	return strings.TrimSpace(buf.String())
}

// Fence is the info string of a viz block.
const Fence = "viz"

// Clean checks every ```viz block of a Markdown text. Valid blocks are rewritten in
// canonical form; invalid ones are removed and described in the returned warnings.
func Clean(md string) (string, []string) {
	var out []string
	var warnings []string
	lines := strings.Split(md, "\n")
	n := 0
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(t, "```") {
			out = append(out, lines[i])
			continue
		}
		// find the end of this fence
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				end = j
				break
			}
		}
		if end < 0 {
			out = append(out, lines[i:]...)
			break
		}
		if strings.TrimSpace(strings.TrimPrefix(t, "```")) != Fence {
			out = append(out, lines[i:end+1]...)
			i = end
			continue
		}
		n++
		b, err := Parse(strings.Join(lines[i+1:end], "\n"))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("visual %d (%s) descartado: %v", n, b.Type, err))
		} else {
			out = append(out, "```"+Fence, b.Encode(), "```")
		}
		i = end
	}
	return strings.Join(out, "\n"), warnings
}
