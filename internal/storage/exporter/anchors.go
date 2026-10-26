package exporter

import (
	"encoding/binary"
	"math"

	"github.com/juruen/rmapi/encoding/rm"
	"github.com/sirupsen/logrus"
)

const (
	treeNodeBlock = 0x02
	lineItemBlock = 0x05
	// the first byte of the value of a stroke
	lineItemType = 0x03
	// the tags of the fields of a tree node: where the group is anchored, and how far
	// from the origin of the page its x is
	anchorIDField      = 7
	anchorOriginXField = 10
	// the tag of the color of a stroke that has one of its own, after its points
	strokeRGBAField = 8
	// where a group that is anchored to a page without text starts, measured from
	// screenshots of the tablet: its text box starts at 234
	anchorY = 296.0
)

// a field of a block: its data is b[start:stop]
type field struct {
	index, kind, start, stop int
}

func varuintAt(b []byte, o int) (v uint64, next int, ok bool) {
	for shift := 0; o < len(b) && shift < 64; shift += 7 {
		c := b[o]
		o++
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, o, true
		}
	}
	return 0, o, false
}

// taggedFields reads the fields in b[o:end]. A sub block is given by its content.
func taggedFields(b []byte, o, end int) ([]field, bool) {
	var out []field
	for o < end {
		tag, next, ok := varuintAt(b, o)
		if !ok {
			return nil, false
		}
		o = next
		f := field{index: int(tag >> 4), kind: int(tag & 15), start: o}
		switch f.kind {
		case 0x1:
			f.stop = o + 1
		case 0x4:
			f.stop = o + 4
		case 0x8:
			f.stop = o + 8
		case 0xc:
			if o+4 > end {
				return nil, false
			}
			f.start = o + 4
			f.stop = f.start + int(binary.LittleEndian.Uint32(b[o:]))
		case 0xf:
			_, stop, ok := varuintAt(b, o+1)
			if !ok {
				return nil, false
			}
			f.stop = stop
		default:
			return nil, false
		}
		if f.stop > end {
			return nil, false
		}
		out = append(out, f)
		o = f.stop
	}
	return out, true
}

func findField(fields []field, index, kind int) (field, bool) {
	for _, f := range fields {
		if f.index == index && f.kind == kind {
			return f, true
		}
	}
	return field{}, false
}

// the value of a field that has a time stamp with it
func lwwValue(b []byte, f field, kind int) (field, bool) {
	inner, ok := taggedFields(b, f.start, f.stop)
	if !ok {
		return field{}, false
	}
	return findField(inner, 2, kind)
}

type groupAnchor struct{ x, y float32 }

// what the file has of a stroke that the decoder leaves out
type strokeInfo struct {
	// the shift of the group the stroke is in: a group that was moved or written on text is
	// anchored somewhere else than the page's origin, and its strokes are drawn from there
	anchor groupAnchor
	// the color of the stroke as 0xAARRGGBB when it has one of its own, as the shader does
	rgba uint32
}

// pageStrokes gives the strokes of a v6 page in the order the decoder gives them.
func pageStrokes(data []byte) ([]strokeInfo, bool) {
	groups := make(map[string]groupAnchor)
	var parents []string
	var colors []uint32
	for o := v6HeaderSize; o+v6BlockHeaderSize <= len(data); {
		end := o + v6BlockHeaderSize + int(binary.LittleEndian.Uint32(data[o:]))
		if end > len(data) {
			return nil, false
		}
		kind := data[o+7]
		if kind != treeNodeBlock && kind != lineItemBlock {
			o = end
			continue
		}
		fields, ok := taggedFields(data, o+v6BlockHeaderSize, end)
		if !ok {
			return nil, false
		}
		switch kind {
		case treeNodeBlock:
			id, hasID := findField(fields, 1, 0xf)
			originX, hasX := findField(fields, anchorOriginXField, 0xc)
			anchor, hasAnchor := findField(fields, anchorIDField, 0xc)
			if !hasID || !hasX || !hasAnchor {
				break
			}
			x, okX := lwwValue(data, originX, 0x4)
			_, okAnchor := lwwValue(data, anchor, 0xf)
			if okX && okAnchor {
				groups[string(data[id.start:id.stop])] = groupAnchor{
					x: math.Float32frombits(binary.LittleEndian.Uint32(data[x.start:])),
					y: anchorY,
				}
			}
		case lineItemBlock:
			// the same strokes the decoder keeps: not deleted, with the points of a line
			parent, ok1 := findField(fields, 1, 0xf)
			_, ok2 := findField(fields, 2, 0xf)
			_, ok3 := findField(fields, 3, 0xf)
			_, ok4 := findField(fields, 4, 0xf)
			deleted, ok5 := findField(fields, 5, 0x4)
			value, ok6 := findField(fields, 6, 0xc)
			if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || binary.LittleEndian.Uint32(data[deleted.start:]) > 0 {
				break
			}
			if value.start >= value.stop || data[value.start] != lineItemType {
				break
			}
			inner, ok := taggedFields(data, value.start+1, value.stop)
			if !ok {
				break
			}
			_, okPen := findField(inner, 1, 0x4)
			_, okColor := findField(inner, 2, 0x4)
			_, okThickness := findField(inner, 3, 0x8)
			_, okStart := findField(inner, 4, 0x4)
			_, okPoints := findField(inner, 5, 0xc)
			if okPen && okColor && okThickness && okStart && okPoints {
				parents = append(parents, string(data[parent.start:parent.stop]))
				var rgba uint32
				if c, ok := findField(inner, strokeRGBAField, 0x4); ok {
					rgba = binary.LittleEndian.Uint32(data[c.start:])
				}
				colors = append(colors, rgba)
			}
		}
		o = end
	}
	out := make([]strokeInfo, len(parents))
	for i, p := range parents {
		out[i] = strokeInfo{anchor: groups[p], rgba: colors[i]}
	}
	return out, true
}

// ApplyAnchors moves the strokes of a v6 page to where the groups they are in anchor them.
// It leaves the page as it is when the strokes found in the file are not the ones decoded.
func ApplyAnchors(page *rm.Rm, data []byte) {
	if page == nil || page.Version != rm.V6 || len(page.Layers) != 1 {
		return
	}
	strokes, ok := pageStrokes(data)
	lines := page.Layers[0].Lines
	if !ok || len(strokes) != len(lines) {
		logrus.Warnf("not moving the strokes of a page with anchors: %d strokes in the file, %d decoded (%v)", len(strokes), len(lines), ok)
		return
	}
	for i := range lines {
		a := strokes[i].anchor
		if a == (groupAnchor{}) {
			continue
		}
		for j := range lines[i].Points {
			lines[i].Points[j].X += a.x
			lines[i].Points[j].Y += a.y
		}
	}
}

// StrokeColors gives the color of the strokes of a v6 page that have one of their own, as
// 0xAARRGGBB, in the order of the decoded strokes (0 is none). A stroke of the shader, the
// tablet's wide translucent highlighter, has the yellow it was made with here and the
// decoder leaves it out. It gives nothing when the strokes of the file are not the decoded.
func StrokeColors(page *rm.Rm, data []byte) []uint32 {
	if page == nil || page.Version != rm.V6 || len(page.Layers) != 1 {
		return nil
	}
	strokes, ok := pageStrokes(data)
	if !ok || len(strokes) != len(page.Layers[0].Lines) {
		return nil
	}
	colors := make([]uint32, len(strokes))
	any := false
	for i, st := range strokes {
		colors[i] = st.rgba
		any = any || st.rgba != 0
	}
	if !any {
		return nil
	}
	return colors
}
