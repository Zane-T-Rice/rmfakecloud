package exporter

import (
	"math"
	"testing"

	"github.com/juruen/rmapi/encoding/rm"
)

func pageWithY(version rm.Version, y float32) *rm.Rm {
	return &rm.Rm{
		Version: version,
		Layers:  []rm.Layer{{Lines: []rm.Line{{Points: []rm.Point{{X: 10, Y: 5}, {X: 20, Y: y}}}}}},
	}
}

func TestDeviceSizeAndCorrection(t *testing.T) {
	w, h := deviceSize(pageWithY(rm.V6, 1), false)
	if w != PaperProWidth || h != PaperProHeight {
		t.Fatalf("v6 size = %vx%v", w, h)
	}
	if got := xCorrection(pageWithY(rm.V6, 1)); got != 108 {
		t.Fatalf("v6 correction = %v, want 108", got)
	}

	w, h = deviceSize(pageWithY(rm.V5, 1), false)
	if w != DeviceWidth || h != DeviceHeight {
		t.Fatalf("v5 size = %vx%v", w, h)
	}
	if got := xCorrection(pageWithY(rm.V5, 1)); got != 0 {
		t.Fatalf("v5 correction = %v, want 0", got)
	}

	w, h = deviceSize(nil, false)
	if w != DeviceWidth || h != DeviceHeight || xCorrection(nil) != 0 {
		t.Fatalf("nil page should use the classic screen")
	}
}

func TestTemplatePageHeight(t *testing.T) {
	if got := templatePageHeight(nil, false); got != rmPageHeight {
		t.Fatalf("empty page height = %v, want %v", got, rmPageHeight)
	}
	if got := templatePageHeight(pageWithY(rm.V6, 500), false); got != rmPageHeight {
		t.Fatalf("short page height = %v, want %v", got, rmPageHeight)
	}
	// a page scrolled to 5400px must be taller than one screen
	if got := templatePageHeight(pageWithY(rm.V6, 5400), false); got <= rmPageHeight*2 {
		t.Fatalf("long page height = %v, expected much taller than %v", got, rmPageHeight)
	}
}

func TestPagesWithoutInkFollowTheDocument(t *testing.T) {
	if w, h := deviceSize(nil, true); w != PaperProWidth || h != PaperProHeight {
		t.Fatalf("blank page in a v6 document = %vx%v", w, h)
	}
}

func TestV6PdfTransform(t *testing.T) {
	scale, xShift, yShift := v6PdfTransform(630)
	// what the decoder gives for the middle of the page (x=0) is 702
	mid := rm.Point{X: float32(DeviceWidth) / 2}
	x, y := normalized(mid, scale, xShift, yShift)
	if want := 315 + v6PdfOffsetX*scale; x < want-0.01 || x > want+0.01 {
		t.Fatalf("middle of the page at %v, want %v", x, want)
	}
	if want := v6PdfOffsetY * scale; y < want-0.01 || y > want+0.01 {
		t.Fatalf("top of the page at %v, want %v", y, want)
	}
	// a dpi worth of pixels is an inch
	_, y0 := normalized(rm.Point{}, scale, xShift, yShift)
	if _, y1 := normalized(rm.Point{Y: float32(PaperProDPI)}, scale, xShift, yShift); y1-y0 < 71.99 || y1-y0 > 72.01 {
		t.Fatalf("229px = %vpt, want 72", y1-y0)
	}
}

func TestStrokeWidth(t *testing.T) {
	scale := 72.0 / PaperProDPI
	line := rm.Line{Points: []rm.Point{{Width: 8}, {Width: 10}}}
	if got, want := strokeWidth(line, scale), 9*scale*strokeWidthFactor; got < want-1e-9 || got > want+1e-9 {
		t.Fatalf("width = %v, want %v", got, want)
	}
	if got := strokeWidth(rm.Line{Points: []rm.Point{{Width: 0.1}}}, scale); got != minStrokeWidth {
		t.Fatalf("thin line = %v, want %v", got, minStrokeWidth)
	}
	// no width in the points, what it was before
	if got := strokeWidth(rm.Line{BrushSize: 2, Points: []rm.Point{{}}}, scale); got < 0.199 || got > 0.201 {
		t.Fatalf("no widths = %v, want 0.2", got)
	}
}

// the ink of two rulebook pages next to where the tablet showed it on its screen:
// the pdf is fitted to the 2160px height (2160/810 px per pt) and centered, so a spot
// at (sx, sy) px is at x = 315 + (sx-810)/2.667 and 810 - sy/2.667 pt, measured from
// screenshots of the tablet
func TestV6PdfTransformMatchesTheTablet(t *testing.T) {
	scale, xShift, yShift := v6PdfTransform(630)
	const pxPerPt = 2160.0 / 810
	for _, p := range []struct{ rx, ry, sx, sy float32 }{
		{190.17, 216.10, 376.29, 182.31},
		{1289.97, 167.23, 1306.96, 141.15},
		{360.78, 799.90, 521.09, 676.00},
		{756.5, 1349.5, 854.35, 1141.15},
		{256.59, 2022.42, 433.10, 1710.32},
		{1446.52, 1839.85, 1439.24, 1555.86},
	} {
		x, y := normalized(rm.Point{X: p.rx, Y: p.ry}, scale, xShift, yShift)
		wantX, wantY := 315+(float64(p.sx)-810)/pxPerPt, float64(p.sy)/pxPerPt
		if math.Abs(x-wantX) > 0.5 || math.Abs(y-wantY) > 0.5 {
			t.Errorf("ink at (%v, %v) is drawn at (%.2f, %.2f)pt, the tablet shows it at (%.2f, %.2f)pt", p.rx, p.ry, x, y, wantX, wantY)
		}
	}
}

func TestShaderColor(t *testing.T) {
	// the color of the highlight on page 25 of a rulebook, as the file has it
	r, g, b, a := shaderColor(rm.HighlightDynamic, 0x73fae719)
	if want := [4]float64{250.0 / 255, 231.0 / 255, 25.0 / 255, 115.0 / 255}; math.Abs(r-want[0]) > 1e-9 || math.Abs(g-want[1]) > 1e-9 || math.Abs(b-want[2]) > 1e-9 || math.Abs(a-want[3]) > 1e-9 {
		t.Errorf("color = %v %v %v %v, want %v", r, g, b, a, want)
	}
	// the colors of the highlighter, as the tablet shows them over white paper
	for c, want := range map[rm.BrushColor][3]float64{
		rm.HighlightGreen: {173, 255, 132}, rm.HighlightPink: {247, 158, 255}, highlightBlue: {189, 235, 255}, rm.HighlightYellow: {255, 247, 154},
	} {
		r, g, b, a = shaderColor(c, 0)
		if math.Abs(r*255-want[0]) > 1e-6 || math.Abs(g*255-want[1]) > 1e-6 || math.Abs(b*255-want[2]) > 1e-6 || a != 1 {
			t.Errorf("color %d = %v %v %v %v, want %v opaque", c, r*255, g*255, b*255, a, want)
		}
	}
	// a color that is not known is a translucent highlight
	if _, _, _, a = shaderColor(rm.BrushColor(77), 0); a != shaderOpacity {
		t.Errorf("unknown color opacity = %v", a)
	}
}

func TestShadedStroke(t *testing.T) {
	for _, c := range []struct {
		brush rm.BrushType
		v6    bool
		want  bool
	}{
		{brushShader, true, true}, {brushShader, false, true},
		{rm.HighlighterV5, true, true}, {rm.HighlighterV5, false, false},
		{rm.MarkerV5, true, false}, {rm.BallPointV5, true, false},
	} {
		if got := shadedStroke(rm.Line{BrushType: c.brush}, c.v6); got != c.want {
			t.Errorf("brush %d (v6 %v) shaded = %v, want %v", c.brush, c.v6, got, c.want)
		}
	}
}

func TestShaderWidth(t *testing.T) {
	line := rm.Line{Points: []rm.Point{{Width: 30}, {Width: 40}}}
	if got, want := shaderWidth(line, 0.5), 17.5; math.Abs(got-want) > 1e-9 {
		t.Errorf("width = %v, want %v", got, want)
	}
	if got := shaderWidth(rm.Line{Points: []rm.Point{{}}}, 0.5); got != 15 {
		t.Errorf("no widths = %v, want 15", got)
	}
}
