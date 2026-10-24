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
	ops, err := tpl.Render(c, 1000, 500, 0.5)
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
	ops, err := tpl.Render(c, 1000, 1000, 1)
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
	ops, err := tpl.Render(c, 100, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := strokes(ops); got != 1 {
		t.Fatalf("got %d strokes, want 1", got)
	}
}
