package exporter

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/unidoc/unipdf/v3/creator"
)

func TestEvalExpr(t *testing.T) {
	sc := scope{"templateWidth": 1620, "templateHeight": 2160, "mobileMaxWidth": 1000}
	tests := []struct {
		expr string
		want float64
	}{
		{"1 + 2 * 3", 7},
		{"(1 + 2) * 3", 9},
		{"templateWidth / 2 - templateHeight / 2", -270},
		{"templateWidth > mobileMaxWidth ? 10 : 20", 10},
		{"templateWidth < mobileMaxWidth ? 10 : 20", 20},
		{"templateWidth < 100 || templateHeight > 100 ? 1 : 2", 1},
		{"-5 + 78.3", 73.3},
		{"templateWidth == 1620 && templateHeight != 1", 1},
		{"2 * (templateWidth > 1 ? 3 : 4) + 1", 7},
	}
	for _, tt := range tests {
		got, err := evalExpr(tt.expr, sc)
		if err != nil {
			t.Errorf("%q: %v", tt.expr, err)
			continue
		}
		if math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("%q = %v, want %v", tt.expr, got, tt.want)
		}
	}

	for _, bad := range []string{"", "1 +", "nope", "(1", "1 ? 2", "None", "#000000", "1 / 0"} {
		if _, err := evalExpr(bad, sc); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func parseTemplate(t *testing.T, src string) *template {
	t.Helper()
	var tpl template
	if err := json.Unmarshal([]byte(src), &tpl); err != nil {
		t.Fatal(err)
	}
	return &tpl
}

// each line is "x y m" followed by "x y l", count the strokes
func strokes(ops string) int {
	return strings.Count(ops, "\nS") + strings.Count(ops, " S")
}

func TestRenderRepeatedGrid(t *testing.T) {
	tpl := parseTemplate(t, `{
		"constants": [{"gridSize": 100}, {"half": "gridSize / 2"}],
		"items": [{
			"type": "group",
			"boundingBox": {"x": 0, "y": 0, "width": "templateWidth", "height": "gridSize"},
			"repeat": {"rows": "infinite", "columns": "infinite"},
			"children": [{"type": "path", "data": ["M", 0, "half", "L", "parentWidth", "half"]}]
		}]
	}`)

	c := creator.New()
	c.NewPage()
	// 1000x500 canvas, a tile is the full width and 100 high: 5 rows
	ops, err := tpl.Render(c, 1000, 500, 500, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if got := strokes(ops); got != 5 {
		t.Fatalf("got %d strokes, want 5:\n%s", got, ops)
	}
}

func TestRenderNestedGroupsAndCounts(t *testing.T) {
	tpl := parseTemplate(t, `{
		"items": [{
			"type": "group",
			"boundingBox": {"x": 0, "y": 0, "width": "templateWidth", "height": "templateHeight"},
			"children": [{
				"type": "group",
				"boundingBox": {"x": 0, "y": 0, "width": "parentWidth / 2", "height": 100},
				"repeat": {"rows": 3, "columns": 2},
				"children": [
					{"type": "path", "data": ["M", 0, 0, "L", "parentWidth", 0, "M", 0, 10, "L", "parentWidth", 10]},
					{"type": "text", "text": "Hi", "fontSize": 20, "position": {"x": "parentWidth / 2 - textWidth / 2", "y": 50}}
				]
			}]
		}]
	}`)

	c := creator.New()
	c.NewPage()
	ops, err := tpl.Render(c, 1000, 1000, 1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	// 3 rows * 2 columns * 2 subpaths
	if got := strokes(ops); got != 12 {
		t.Fatalf("got %d strokes, want 12:\n%s", got, ops)
	}
}

func TestRenderSkipsBrokenItems(t *testing.T) {
	tpl := parseTemplate(t, `{
		"items": [
			{"type": "path", "data": ["M", 0, 0, "L", "oops", 0]},
			{"type": "path", "data": ["M", 0, 0, "L", 10, 10]}
		]
	}`)
	c := creator.New()
	c.NewPage()
	ops, err := tpl.Render(c, 100, 100, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := strokes(ops); got != 1 {
		t.Fatalf("got %d strokes, want 1", got)
	}
}

// the margin of the college template is computed from templateHeight, it must
// stay where the screen puts it however tall the page is
func TestRenderTallPageKeepsScreenSizeInExpressions(t *testing.T) {
	tpl := parseTemplate(t, `{
		"constants": [{"offsetX": "templateWidth / 2 - templateHeight / 2 + 464"}],
		"items": [{
			"type": "group",
			"boundingBox": {"x": 0, "y": 0, "width": "templateHeight", "height": 62},
			"repeat": {"rows": "down"},
			"children": [{"type": "path", "data": ["M", "offsetX", 0, "L", "offsetX", "parentHeight - 1"]}]
		}]
	}`)
	c := creator.New()
	c.NewPage()
	// a 1620x2160 screen on a 6000px tall page: the line is at 810-1080+464 = 194
	ops, err := tpl.Render(c, 1620, 2160, 6000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ops, "194 ") {
		t.Fatalf("margin not at x=194:\n%s", ops)
	}
	// and it is drawn all the way down the page
	if got := strokes(ops); got < 90 {
		t.Fatalf("got %d strokes, want a line per 62px row", got)
	}
}

func TestRenderColors(t *testing.T) {
	tpl := parseTemplate(t, `{
		"items": [
			{"type": "path", "fillColor": "#ffff00", "strokeColor": "#00000000", "data": ["M", 0, 0, "L", 10, 0, "L", 10, 10, "Z"]},
			{"type": "path", "strokeColor": "#ff0000", "data": ["M", 0, 0, "L", 10, 10]},
			{"type": "path", "strokeColor": "#00000000", "data": ["M", 0, 0, "L", 10, 10]}
		]
	}`)
	c := creator.New()
	c.NewPage()
	ops, err := tpl.Render(c, 100, 100, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	// the filled shape without a border (f), the red line (S), nothing for the invisible one
	if strings.Count(ops, "\nf") != 1 || strokes(ops) != 1 {
		t.Fatalf("unexpected painting:\n%s", ops)
	}
	if !strings.Contains(ops, "1 0 0 RG") || !strings.Contains(ops, "1 1 0 rg") {
		t.Fatalf("colors not set:\n%s", ops)
	}
}
