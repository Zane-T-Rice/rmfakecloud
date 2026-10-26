package exporter

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/juruen/rmapi/encoding/rm"
)

// the pieces of a v6 file
func tagVar(index, kind int) []byte {
	var out []byte
	for v := uint64(index<<4 | kind); ; v >>= 7 {
		if v < 0x80 {
			return append(out, byte(v))
		}
		out = append(out, byte(v&0x7f|0x80))
	}
}

func tagID(index int, n byte) []byte { return append(tagVar(index, 0xf), 0, n) }

func tagU32(index int, v uint32) []byte {
	return binary.LittleEndian.AppendUint32(tagVar(index, 0x4), v)
}

func tagF32(index int, v float32) []byte { return tagU32(index, math.Float32bits(v)) }

func tagF64(index int, v float64) []byte {
	return binary.LittleEndian.AppendUint64(tagVar(index, 0x8), math.Float64bits(v))
}

func tagSub(index int, content ...[]byte) []byte {
	var all []byte
	for _, c := range content {
		all = append(all, c...)
	}
	return append(binary.LittleEndian.AppendUint32(tagVar(index, 0xc), uint32(len(all))), all...)
}

func v6Block(kind, version byte, body []byte) []byte {
	return append(append(binary.LittleEndian.AppendUint32(nil, uint32(len(body))), 0, 1, version, kind), body...)
}

// a node anchored to a character of the text, x away from the origin of the page
func treeNode(id byte, x float32) []byte {
	return v6Block(treeNodeBlock, 1, append(append(tagID(1, id),
		tagSub(anchorIDField, tagID(1, 1), tagID(2, 99))...),
		tagSub(anchorOriginXField, tagID(1, 1), tagF32(2, x))...))
}

// a stroke of two points in the node parent
func stroke(parent byte, deleted uint32, x, y float32) []byte {
	var points []byte
	for i := 0; i < 2; i++ {
		points = binary.LittleEndian.AppendUint32(points, math.Float32bits(x+float32(i)))
		points = binary.LittleEndian.AppendUint32(points, math.Float32bits(y))
		points = append(points, make([]byte, 6)...)
	}
	body := append(append(append(append(tagID(1, parent), tagID(2, 1)...), tagID(3, 0)...), tagID(4, 0)...), tagU32(5, deleted)...)
	value := append([]byte{lineItemType}, tagU32(1, 15)...)
	value = append(append(append(append(value, tagU32(2, 0)...), tagF64(3, 1)...), tagF32(4, 0)...), tagSub(5, points)...)
	return v6Block(lineItemBlock, 2, append(body, tagSub(6, value)...))
}

func v6File(blocks ...[]byte) []byte {
	data := []byte("reMarkable .lines file, version=6          ")
	for _, b := range blocks {
		data = append(data, b...)
	}
	return data
}

func decode(t *testing.T, data []byte) *rm.Rm {
	t.Helper()
	page := rm.New()
	if err := page.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestApplyAnchorsMovesTheStrokesOfAnchoredGroups(t *testing.T) {
	data := v6File(
		treeNode(100, -572),
		stroke(100, 0, 700, 10),
		// not anchored: its node is not a tree node with an anchor
		stroke(7, 0, 800, 20),
		// deleted, the decoder leaves it out
		stroke(100, 3, 900, 30),
		stroke(100, 0, 710, 40),
	)
	page := decode(t, data)
	if got := len(page.Layers[0].Lines); got != 3 {
		t.Fatalf("decoded %d strokes, want 3", got)
	}
	before := []float32{page.Layers[0].Lines[0].Points[0].X, page.Layers[0].Lines[1].Points[0].X}
	ApplyAnchors(page, data)
	lines := page.Layers[0].Lines
	if got := lines[0].Points[0].X - before[0]; got != -572 {
		t.Errorf("first stroke moved %v in x, want -572", got)
	}
	if got := lines[0].Points[0].Y; got != 10+anchorY {
		t.Errorf("first stroke at y=%v, want %v", got, 10+anchorY)
	}
	if got := lines[1].Points[0].X - before[1]; got != 0 || lines[1].Points[0].Y != 20 {
		t.Errorf("a stroke that is not anchored moved: x by %v, y=%v", got, lines[1].Points[0].Y)
	}
	// the one after the deleted stroke is the third decoded and in the node
	if got := lines[2].Points[1].Y; got != 40+anchorY {
		t.Errorf("stroke after the deleted one at y=%v, want %v", got, 40+anchorY)
	}
}

func TestApplyAnchorsLeavesAPageAloneWhenTheStrokesDiffer(t *testing.T) {
	data := v6File(treeNode(100, -572), stroke(100, 0, 700, 10))
	page := decode(t, data)
	// a page decoded from another file has other strokes than the file's
	page.Layers[0].Lines = append(page.Layers[0].Lines, page.Layers[0].Lines[0])
	x, y := page.Layers[0].Lines[0].Points[0].X, page.Layers[0].Lines[0].Points[0].Y
	ApplyAnchors(page, data)
	if p := page.Layers[0].Lines[0].Points[0]; p.X != x || p.Y != y {
		t.Fatalf("moved to (%v, %v) though the strokes did not match", p.X, p.Y)
	}
}

// the red "Upcoming" of a page of a notebook, drawn at (52..173, 335..834) on the screen of the
// tablet next to the grid: in the file it is at x 516..637 (decoded), y 39..538, in a group
// anchored -572 from the origin of the page, and the page adds 108 to x itself
func TestAnchorsPutInkWhereTheTabletShowsIt(t *testing.T) {
	data := v6File(treeNode(100, -572), stroke(100, 0, 516-float32(rm.Width)/2, 39), stroke(100, 0, 636-float32(rm.Width)/2, 538))
	page := decode(t, data)
	ApplyAnchors(page, data)
	first, last := page.Layers[0].Lines[0].Points[0], page.Layers[0].Lines[1].Points[1]
	if x := first.X + float32(v6XCorrection); x < 50 || x > 54 {
		t.Errorf("left of the ink at x=%v, the tablet shows it at 52", x)
	}
	if y := last.Y; y < 832 || y > 837 {
		t.Errorf("bottom of the ink at y=%v, the tablet shows it at 834", y)
	}
}
