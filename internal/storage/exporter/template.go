package exporter

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/sirupsen/logrus"
	"github.com/unidoc/unipdf/v3/contentstream"
	"github.com/unidoc/unipdf/v3/creator"
)

const (
	templateFileExt = ".template"
	// guards against runaway repeats on bad input
	maxTemplateTiles = 20000

	defaultTemplateStrokeGray  = 0.7
	defaultTemplateStrokeWidth = 1.5
	// the baseline sits about this far below the top of the text
	templateTextAscent = 0.8
)

// template is the reMarkable declarative background template (formatVersion 1):
// constants evaluated in order, then a tree of group/path/text items
type template struct {
	Name      string                       `json:"name"`
	Constants []map[string]json.RawMessage `json:"constants"`
	Items     []templateItem               `json:"items"`
}

type templateItem struct {
	Type        string          `json:"type"`
	BoundingBox *templateBox    `json:"boundingBox"`
	Repeat      *templateRepeat `json:"repeat"`
	Children    []templateItem  `json:"children"`
	Data        []interface{}   `json:"data"`
	Text        string          `json:"text"`
	FontSize    interface{}     `json:"fontSize"`
	Position    *templatePoint  `json:"position"`
	StrokeColor string          `json:"strokeColor"`
	FillColor   string          `json:"fillColor"`
	StrokeWidth interface{}     `json:"strokeWidth"`
}

// the values are numbers or expressions
type templateBox struct {
	X      interface{} `json:"x"`
	Y      interface{} `json:"y"`
	Width  interface{} `json:"width"`
	Height interface{} `json:"height"`
}

type templatePoint struct {
	X interface{} `json:"x"`
	Y interface{} `json:"y"`
}

// rows and columns: a number, "infinite" (fill the page in both directions)
// or anything else, e.g. "right" (fill, but only from the origin onwards)
type templateRepeat struct {
	Rows    interface{} `json:"rows"`
	Columns interface{} `json:"columns"`
}

// LoadTemplate reads the template with the given name (as stored in the
// document, e.g. "P Grid medium") from dir
func LoadTemplate(dir, name string) (*template, error) {
	file := filepath.Join(dir, filepath.Base(name)+templateFileExt)
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var t template
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("template %s: %w", name, err)
	}
	return &t, nil
}

// templateRenderer draws a template onto one page. Template coordinates are
// device pixels, top left origin, y down.
type templateRenderer struct {
	c      *creator.Creator
	cc     *contentstream.ContentCreator
	scale  float64 // points per template pixel
	pageH  float64 // page height in points
	width  float64
	height float64
	tiles  int
}

// Render draws t on the current page of c and returns the content stream
// operations for the lines. The text is drawn by the creator itself.
// width and height are the template size in pixels (the screen of the device,
// what templateWidth and templateHeight are on it), pageHeight the height of
// the page, which can be much taller than the screen: repeating groups fill it
// but the expressions keep seeing the screen size. scale is points per pixel.
func (t *template) Render(c *creator.Creator, width, height, pageHeight, scale float64) (string, error) {
	return t.RenderAt(c, width, 0, width, height, pageHeight, scale)
}

// RenderAt is Render for a template laid out in layoutWidth pixels (what
// templateWidth is) starting originX pixels from the left of the canvas, which is
// width pixels wide. The Paper Pro shows its templates like this: laid out for the
// 1404 pixels of the older screen and centered on the wider one.
func (t *template) RenderAt(c *creator.Creator, width, originX, layoutWidth, height, pageHeight, scale float64) (string, error) {
	r := &templateRenderer{
		c:      c,
		cc:     contentstream.NewContentCreator(),
		scale:  scale,
		pageH:  pageHeight * scale,
		width:  width,
		height: pageHeight,
	}

	// paperOriginX is where the paper starts, the whole canvas for us
	scope := scope{"templateWidth": layoutWidth, "templateHeight": height, "paperOriginX": 0}
	for _, entry := range t.Constants {
		for name, raw := range entry {
			v, err := evalRaw(raw, scope)
			if err != nil {
				// not a number, e.g. a color or "None"
				logrus.Debugf("template constant %s: %v", name, err)
				continue
			}
			scope[name] = v
		}
	}

	root := scope.with(map[string]float64{
		"parentWidth":  layoutWidth,
		"parentHeight": height,
		"parentX":      0,
		"parentY":      0,
	})
	r.cc.Add_q()
	if err := r.items(t.Items, root, originX, 0); err != nil {
		return "", err
	}
	r.cc.Add_Q()
	return r.cc.Operations().String(), nil
}

func (r *templateRenderer) items(items []templateItem, sc scope, tx, ty float64) error {
	for i := range items {
		it := &items[i]
		var err error
		switch it.Type {
		case "group":
			err = r.group(it, sc, tx, ty)
		case "path":
			err = r.path(it, sc, tx, ty)
		case "text":
			err = r.text(it, sc, tx, ty)
		}
		if err != nil {
			// a broken item shouldn't take the whole page down
			logrus.Debugf("template item %q (%s): %v", it.Type, it.Text, err)
		}
	}
	return nil
}

func (r *templateRenderer) group(it *templateItem, sc scope, tx, ty float64) error {
	x, y, w, h := sc["parentX"], sc["parentY"], sc["parentWidth"], sc["parentHeight"]
	if b := it.BoundingBox; b != nil {
		var err error
		if x, err = evalAny(b.X, sc, x); err != nil {
			return err
		}
		if y, err = evalAny(b.Y, sc, y); err != nil {
			return err
		}
		if w, err = evalAny(b.Width, sc, w); err != nil {
			return err
		}
		if h, err = evalAny(b.Height, sc, h); err != nil {
			return err
		}
	}

	child := sc.with(map[string]float64{
		"parentWidth":  w,
		"parentHeight": h,
		"parentX":      x,
		"parentY":      y,
	})

	rowMin, rowMax := 0, 0
	colMin, colMax := 0, 0
	if it.Repeat != nil {
		if w <= 0 || h <= 0 {
			return fmt.Errorf("cannot repeat a %vx%v box", w, h)
		}
		// absolute position of the parent origin on the canvas
		absX, absY := tx+x, ty+y
		var err error
		if rowMin, rowMax, err = repeatRange(it.Repeat.Rows, absY, h, r.height, sc); err != nil {
			return err
		}
		if colMin, colMax, err = repeatRange(it.Repeat.Columns, absX, w, r.width, sc); err != nil {
			return err
		}
	}

	for row := rowMin; row <= rowMax; row++ {
		for col := colMin; col <= colMax; col++ {
			r.tiles++
			if r.tiles > maxTemplateTiles {
				return fmt.Errorf("too many tiles")
			}
			ox := tx + x + float64(col)*w
			oy := ty + y + float64(row)*h
			if err := r.items(it.Children, child, ox, oy); err != nil {
				return err
			}
		}
	}
	return nil
}

// repeatRange gives the first and last tile index to draw, for tiles of the
// given size starting at origin on a canvas of the given extent
func repeatRange(v interface{}, origin, size, extent float64, sc scope) (int, int, error) {
	if v == nil {
		return 0, 0, nil
	}
	if s, ok := v.(string); ok {
		switch s {
		case "infinite":
			return int(math.Floor(-origin / size)), int(math.Ceil((extent-origin)/size)) - 1, nil
		}
		if n, err := evalAny(v, sc, 1); err == nil {
			return 0, int(n) - 1, nil
		}
		// "right" and friends, fill from the origin onwards
		return 0, int(math.Ceil((extent-origin)/size)) - 1, nil
	}
	n, err := evalAny(v, sc, 1)
	if err != nil {
		return 0, 0, err
	}
	return 0, int(n) - 1, nil
}

func (r *templateRenderer) path(it *templateItem, sc scope, tx, ty float64) error {
	strokeW := defaultTemplateStrokeWidth
	if it.StrokeWidth != nil {
		v, err := evalAny(it.StrokeWidth, sc, strokeW)
		if err != nil {
			return err
		}
		strokeW = v
	}

	hasStroke := it.StrokeColor != "" || it.FillColor == ""
	sr, sg, sb := defaultTemplateStrokeGray, defaultTemplateStrokeGray, defaultTemplateStrokeGray
	if it.StrokeColor != "" {
		if cr, cg, cb, ca, ok := parseHexColor(it.StrokeColor); ok {
			sr, sg, sb = cr, cg, cb
			// a transparent stroke is no stroke
			if ca == 0 {
				hasStroke = false
			}
		}
	}
	fr, fg, fb, fa, hasFill := parseHexColor(it.FillColor)
	if fa == 0 {
		hasFill = false
	}
	if !hasFill && !hasStroke {
		return nil
	}

	r.cc.Add_w(strokeW * r.scale)
	r.cc.Add_RG(sr, sg, sb)
	if hasFill {
		r.cc.Add_rg(fr, fg, fb)
	}

	// pdf wants y up
	px := func(x float64) float64 { return (tx + x) * r.scale }
	py := func(y float64) float64 { return r.pageH - (ty+y)*r.scale }

	open := false // a subpath is being built
	paint := func() {
		if !open {
			return
		}
		switch {
		case hasFill && hasStroke:
			r.cc.Add_B()
		case hasFill:
			r.cc.Add_f()
		default:
			r.cc.Add_S()
		}
		open = false
	}

	// data is a flat list of commands: "M", x, y, "L", x, y, "C", x1, y1, x2, y2, x, y, "Z"
	args := map[string]int{"M": 2, "L": 2, "C": 6, "Z": 0}
	data := it.Data
	var ops []func()
	for i := 0; i < len(data); {
		cmd, ok := data[i].(string)
		n, known := args[cmd]
		if !ok || !known {
			return fmt.Errorf("unsupported path command %v", data[i])
		}
		if n > 0 && i+n >= len(data) {
			return fmt.Errorf("path command %s is missing coordinates", cmd)
		}
		v := make([]float64, n)
		for k := 0; k < n; k++ {
			x, err := evalAny(data[i+1+k], sc, 0)
			if err != nil {
				return err
			}
			v[k] = x
		}
		switch cmd {
		case "M":
			ops = append(ops, func() {
				paint()
				r.cc.Add_m(px(v[0]), py(v[1]))
				open = true
			})
		case "L":
			ops = append(ops, func() { r.cc.Add_l(px(v[0]), py(v[1])) })
		case "C":
			ops = append(ops, func() {
				r.cc.Add_c(px(v[0]), py(v[1]), px(v[2]), py(v[3]), px(v[4]), py(v[5]))
			})
		case "Z":
			ops = append(ops, func() { r.cc.Add_h() })
		}
		i += 1 + n
	}
	// only draw once every coordinate evaluated
	for _, op := range ops {
		op()
	}
	paint()
	return nil
}

func (r *templateRenderer) text(it *templateItem, sc scope, tx, ty float64) error {
	if it.Position == nil || it.Text == "" {
		return nil
	}
	fontSize, err := evalAny(it.FontSize, sc, 0)
	if err != nil || fontSize <= 0 {
		return fmt.Errorf("bad font size")
	}

	p := r.c.NewParagraph(it.Text)
	p.SetFontSize(fontSize * r.scale)
	p.SetEnableWrap(false)

	textSc := sc.with(map[string]float64{"textWidth": p.Width() / r.scale})
	x, err := evalAny(it.Position.X, textSc, 0)
	if err != nil {
		return err
	}
	y, err := evalAny(it.Position.Y, textSc, 0)
	if err != nil {
		return err
	}
	// position is the baseline, the creator wants the top of the text
	p.SetPos((tx+x)*r.scale, (ty+y-fontSize*templateTextAscent)*r.scale)
	return r.c.Draw(p)
}

// parseHexColor reads #rrggbb or #rrggbbaa
func parseHexColor(s string) (r, g, b, a float64, ok bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 && len(s) != 8 {
		return 0, 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	a = 1
	if len(s) == 8 {
		a = float64(v&0xff) / 255
		v >>= 8
	}
	return float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255, a, true
}

// scope holds the variables an expression can use
type scope map[string]float64

// with returns a copy with the extra variables
func (s scope) with(extra map[string]float64) scope {
	n := make(scope, len(s)+len(extra))
	for k, v := range s {
		n[k] = v
	}
	for k, v := range extra {
		n[k] = v
	}
	return n
}

// evalAny evaluates a json value: a number or an expression string.
// nil gives the default.
func evalAny(v interface{}, sc scope, def float64) (float64, error) {
	switch t := v.(type) {
	case nil:
		return def, nil
	case float64:
		return t, nil
	case string:
		return evalExpr(t, sc)
	}
	return 0, fmt.Errorf("unsupported value %v", v)
}

func evalRaw(raw json.RawMessage, sc scope) (float64, error) {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, err
	}
	if v == nil {
		return 0, fmt.Errorf("null")
	}
	return evalAny(v, sc, 0)
}

// evalExpr evaluates the arithmetic used by the templates: numbers,
// variables, + - * /, comparisons, && || !, ?: and parentheses.
// Booleans are 1 and 0.
func evalExpr(src string, sc scope) (float64, error) {
	p := &exprParser{src: src, sc: sc}
	v, err := p.ternary()
	if err != nil {
		return 0, fmt.Errorf("%q: %w", src, err)
	}
	p.skip()
	if p.pos != len(p.src) {
		return 0, fmt.Errorf("%q: unexpected %q", src, p.src[p.pos:])
	}
	return v, nil
}

type exprParser struct {
	src string
	pos int
	sc  scope
}

func (p *exprParser) skip() {
	for p.pos < len(p.src) && unicode.IsSpace(rune(p.src[p.pos])) {
		p.pos++
	}
}

// accept consumes op if it is next
func (p *exprParser) accept(op string) bool {
	p.skip()
	if strings.HasPrefix(p.src[p.pos:], op) {
		p.pos += len(op)
		return true
	}
	return false
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (p *exprParser) ternary() (float64, error) {
	cond, err := p.or()
	if err != nil {
		return 0, err
	}
	if !p.accept("?") {
		return cond, nil
	}
	a, err := p.ternary()
	if err != nil {
		return 0, err
	}
	if !p.accept(":") {
		return 0, fmt.Errorf("missing ':'")
	}
	b, err := p.ternary()
	if err != nil {
		return 0, err
	}
	if cond != 0 {
		return a, nil
	}
	return b, nil
}

func (p *exprParser) or() (float64, error) {
	l, err := p.and()
	if err != nil {
		return 0, err
	}
	for p.accept("||") {
		r, err := p.and()
		if err != nil {
			return 0, err
		}
		l = b2f(l != 0 || r != 0)
	}
	return l, nil
}

func (p *exprParser) and() (float64, error) {
	l, err := p.equality()
	if err != nil {
		return 0, err
	}
	for p.accept("&&") {
		r, err := p.equality()
		if err != nil {
			return 0, err
		}
		l = b2f(l != 0 && r != 0)
	}
	return l, nil
}

func (p *exprParser) equality() (float64, error) {
	l, err := p.relational()
	if err != nil {
		return 0, err
	}
	for {
		switch {
		case p.accept("=="):
			r, err := p.relational()
			if err != nil {
				return 0, err
			}
			l = b2f(l == r)
		case p.accept("!="):
			r, err := p.relational()
			if err != nil {
				return 0, err
			}
			l = b2f(l != r)
		default:
			return l, nil
		}
	}
}

func (p *exprParser) relational() (float64, error) {
	l, err := p.additive()
	if err != nil {
		return 0, err
	}
	for {
		var op string
		switch {
		case p.accept("<="):
			op = "<="
		case p.accept(">="):
			op = ">="
		case p.accept("<"):
			op = "<"
		case p.accept(">"):
			op = ">"
		default:
			return l, nil
		}
		r, err := p.additive()
		if err != nil {
			return 0, err
		}
		switch op {
		case "<=":
			l = b2f(l <= r)
		case ">=":
			l = b2f(l >= r)
		case "<":
			l = b2f(l < r)
		case ">":
			l = b2f(l > r)
		}
	}
}

func (p *exprParser) additive() (float64, error) {
	l, err := p.term()
	if err != nil {
		return 0, err
	}
	for {
		switch {
		case p.accept("+"):
			r, err := p.term()
			if err != nil {
				return 0, err
			}
			l += r
		case p.accept("-"):
			r, err := p.term()
			if err != nil {
				return 0, err
			}
			l -= r
		default:
			return l, nil
		}
	}
}

func (p *exprParser) term() (float64, error) {
	l, err := p.unary()
	if err != nil {
		return 0, err
	}
	for {
		switch {
		case p.accept("*"):
			r, err := p.unary()
			if err != nil {
				return 0, err
			}
			l *= r
		case p.accept("/"):
			r, err := p.unary()
			if err != nil {
				return 0, err
			}
			if r == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			l /= r
		default:
			return l, nil
		}
	}
}

func (p *exprParser) unary() (float64, error) {
	switch {
	case p.accept("-"):
		v, err := p.unary()
		return -v, err
	case p.accept("+"):
		return p.unary()
	case p.accept("!"):
		v, err := p.unary()
		return b2f(v == 0), err
	}
	return p.primary()
}

func (p *exprParser) primary() (float64, error) {
	if p.accept("(") {
		v, err := p.ternary()
		if err != nil {
			return 0, err
		}
		if !p.accept(")") {
			return 0, fmt.Errorf("missing ')'")
		}
		return v, nil
	}

	p.skip()
	start := p.pos
	if p.pos >= len(p.src) {
		return 0, fmt.Errorf("unexpected end")
	}
	c := p.src[p.pos]
	switch {
	case c >= '0' && c <= '9' || c == '.':
		for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == '.') {
			p.pos++
		}
		return strconv.ParseFloat(p.src[start:p.pos], 64)
	case c == '_' || unicode.IsLetter(rune(c)):
		for p.pos < len(p.src) && (p.src[p.pos] == '_' || unicode.IsLetter(rune(p.src[p.pos])) || unicode.IsDigit(rune(p.src[p.pos]))) {
			p.pos++
		}
		name := p.src[start:p.pos]
		v, ok := p.sc[name]
		if !ok {
			return 0, fmt.Errorf("unknown variable %s", name)
		}
		return v, nil
	}
	return 0, fmt.Errorf("unexpected %q", p.src[p.pos:])
}
