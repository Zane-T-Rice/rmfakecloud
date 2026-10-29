package exporter

import (
	"encoding/binary"
	"math"
	"sort"
)

const (
	rootTextBlock = 0x07
	// the styles of a paragraph
	styleHeading = 2
	// the height in pixels of a paragraph of one line, and of a heading
	lineHeight    = 70.0
	headingHeight = 150.0
)

// a character of the text: the pair of the author and the counter of its id
type charID struct {
	author byte
	n      uint64
}

// Paragraph is a line of the typed text of a page
type Paragraph struct {
	Text  string
	Style int
	// the top of the paragraph, in pixels from the origin of the page
	Y float64
}

// PageText is the typed text of a v6 page: the text box and its paragraphs
type PageText struct {
	// the left of the text box, from the center of the page, and its top
	X, Y, Width float64
	Paragraphs  []Paragraph
	// the paragraph each character is in, by its id (also the ones that were deleted)
	paragraphOf map[charID]int
}

type textChar struct {
	id      charID
	left    charID
	text    rune
	deleted bool
}

func readCharID(b []byte, o int) (charID, int, bool) {
	if o >= len(b) {
		return charID{}, o, false
	}
	n, next, ok := varuintAt(b, o+1)
	return charID{author: b[o], n: n}, next, ok
}

// the id a tagged field starts with: its tag, then the id
func readTaggedID(b []byte, o int, index int) (charID, int, bool) {
	tag, next, ok := varuintAt(b, o)
	if !ok || tag != uint64(index<<4|0xf) {
		return charID{}, o, false
	}
	return readCharID(b, next)
}

// the length of a sub block that starts with its tag
func readSub(b []byte, o int, index int) (start, end int, ok bool) {
	tag, next, ok := varuintAt(b, o)
	if !ok || tag != uint64(index<<4|0xc) || next+4 > len(b) {
		return 0, 0, false
	}
	start = next + 4
	end = start + int(binary.LittleEndian.Uint32(b[next:]))
	return start, end, end <= len(b)
}

// ParseText reads the root text block of a v6 page. It gives nil when the page has no text.
func ParseText(data []byte) *PageText {
	for o := v6HeaderSize; o+v6BlockHeaderSize <= len(data); {
		end := o + v6BlockHeaderSize + int(binary.LittleEndian.Uint32(data[o:]))
		if end > len(data) {
			return nil
		}
		if data[o+7] == rootTextBlock {
			return parseTextBlock(data[o+v6BlockHeaderSize : end])
		}
		o = end
	}
	return nil
}

func parseTextBlock(b []byte) *PageText {
	// the id of the block, then the text and its styles in a sub block
	_, o, ok := readTaggedID(b, 0, 1)
	if !ok {
		return nil
	}
	body, bodyEnd, ok := readSub(b, o, 2)
	if !ok {
		return nil
	}
	// the characters, in a sub block of a sub block, with the number of items first
	itemsOuter, outerEnd, ok := readSub(b, body, 1)
	if !ok {
		return nil
	}
	items, itemsEnd, ok := readSub(b, itemsOuter, 1)
	if !ok {
		return nil
	}
	count, o, ok := varuintAt(b, items)
	if !ok {
		return nil
	}
	var chars []textChar
	for i := uint64(0); i < count; i++ {
		if o >= itemsEnd || b[o] != 0x0c || o+5 > itemsEnd {
			return nil
		}
		stop := o + 5 + int(binary.LittleEndian.Uint32(b[o+1:]))
		if stop > itemsEnd {
			return nil
		}
		p := o + 5
		id, p, ok1 := readTaggedID(b, p, 2)
		left, p, ok2 := readTaggedID(b, p, 3)
		_, p, ok3 := readTaggedID(b, p, 4)
		if !ok1 || !ok2 || !ok3 || p+5 > stop || b[p] != 0x54 {
			return nil
		}
		deleted := binary.LittleEndian.Uint32(b[p+1:])
		p += 5
		if p < stop {
			// the string: its length, if it is ascii, the characters
			start, strEnd, ok := readSub(b, p, 6)
			if !ok || strEnd > stop {
				return nil
			}
			_, q, ok := varuintAt(b, start)
			if !ok || q >= strEnd {
				return nil
			}
			for k, r := range []rune(string(b[q+1 : strEnd])) {
				c := textChar{id: charID{id.author, id.n + uint64(k)}, text: r}
				c.left = left
				if k > 0 {
					c.left = charID{id.author, id.n + uint64(k) - 1}
				}
				chars = append(chars, c)
			}
		} else {
			for k := uint32(0); k < deleted; k++ {
				c := textChar{id: charID{id.author, id.n + uint64(k)}, left: left, deleted: true}
				if k > 0 {
					c.left = charID{id.author, id.n + uint64(k) - 1}
				}
				chars = append(chars, c)
			}
		}
		o = stop
	}

	t := &PageText{paragraphOf: make(map[charID]int)}
	styles := readStyles(b, outerEnd, bodyEnd)
	// the position and the width of the text box follow its body
	if x, y, w, ok := readTextBox(b, bodyEnd); ok {
		t.X, t.Y, t.Width = x, y, w
	}

	para := 0
	t.Paragraphs = append(t.Paragraphs[:0], Paragraph{Style: styles[charID{}]})
	var text []rune
	flush := func() {
		t.Paragraphs[para].Text = string(text)
		text = nil
	}
	for _, c := range sequence(chars) {
		t.paragraphOf[c.id] = para
		if c.deleted {
			continue
		}
		if c.text == '\n' {
			flush()
			para++
			t.Paragraphs = append(t.Paragraphs, Paragraph{Style: styles[c.id]})
			continue
		}
		text = append(text, c.text)
	}
	flush()

	y := t.Y
	for i := range t.Paragraphs {
		t.Paragraphs[i].Y = y
		// the space a heading has above it is cut off at the top of the text box
		if i == 0 {
			y += lineHeight
			continue
		}
		y += paragraphHeight(t.Paragraphs[i].Style)
	}
	return t
}

func paragraphHeight(style int) float64 {
	if style == styleHeading {
		return headingHeight
	}
	return lineHeight
}

// the style of the paragraph that follows each character that ends one, the first paragraph has the zero id
func readStyles(b []byte, from, to int) map[charID]int {
	styles := make(map[charID]int)
	start, end, ok := readSub(b, from, 2)
	if !ok || end > to {
		return styles
	}
	inner, innerEnd, ok := readSub(b, start, 1)
	if !ok {
		return styles
	}
	count, o, ok := varuintAt(b, inner)
	if !ok {
		return styles
	}
	for i := uint64(0); i < count; i++ {
		id, p, ok := readCharID(b, o)
		if !ok {
			return styles
		}
		// when it was set, then the value in a sub block of a byte
		if _, p, ok = readTaggedID(b, p, 1); !ok {
			return styles
		}
		vs, ve, ok := readSub(b, p, 2)
		if !ok || ve > innerEnd || vs+2 > ve {
			return styles
		}
		styles[id] = int(b[vs+1])
		o = ve
	}
	return styles
}

// the position of the text box and its width follow the body of the block
func readTextBox(b []byte, o int) (x, y, width float64, ok bool) {
	start, end, ok := readSub(b, o, 3)
	if !ok || end-start != 16 {
		return 0, 0, 0, false
	}
	x = math.Float64frombits(binary.LittleEndian.Uint64(b[start:]))
	y = math.Float64frombits(binary.LittleEndian.Uint64(b[start+8:]))
	tag, next, ok := varuintAt(b, end)
	if !ok || tag != 0x44 || next+4 > len(b) {
		return x, y, 0, true
	}
	return x, y, float64(math.Float32frombits(binary.LittleEndian.Uint32(b[next:]))), true
}

// sequence puts the characters in the order they have in the text: after the one they were
// inserted behind, the most recent first
func sequence(chars []textChar) []textChar {
	children := make(map[charID][]textChar)
	for _, c := range chars {
		children[c.left] = append(children[c.left], c)
	}
	for _, list := range children {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].id.n != list[j].id.n {
				return list[i].id.n > list[j].id.n
			}
			return list[i].id.author > list[j].id.author
		})
	}
	var out []textChar
	var walk func(parent charID)
	walk = func(parent charID) {
		for _, c := range children[parent] {
			out = append(out, c)
			walk(c.id)
		}
	}
	walk(charID{})
	return out
}

// the id a group has when it is anchored to the end of the text, the one before it is the start
const endOfText = 1<<48 - 1

// AnchorY is where the strokes anchored to a character start: the top of its paragraph, the
// same distance below as the first one is below the top of the text box
func (t *PageText) AnchorY(id charID) (float64, bool) {
	if t == nil {
		return 0, false
	}
	i, ok := t.paragraphOf[id]
	if id == (charID{0, endOfText}) {
		i, ok = len(t.Paragraphs)-1, true
	}
	if !ok {
		return 0, false
	}
	return t.Paragraphs[i].Y - t.Paragraphs[0].Y + anchorY, true
}
