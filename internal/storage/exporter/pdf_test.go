package exporter

import (
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
