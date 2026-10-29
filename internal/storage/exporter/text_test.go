package exporter

import (
	"encoding/hex"
	"testing"
)

// the text block of a page with nine empty lines and then "Brug" as a heading and "Brug" as plain text
const textBlockHex = "1f00002cfa0100001ccf0100001cca010000130c0f0000002f01d7043f00004f000054010000000c190000002f0181063f01d7044f01d80454000000006c0300000001010a0c100000002f01d8043f01d7044f000054020000000c1a0000002f0189063f01d9044f01da0454000000006c0400000002010a0a0c100000002f01da043f01d9044f000054060000000c1e0000002f018b063f01df044f01e60554000000006c0800000006010a0a0a0a0a0a0c110000002f01e6053f01df044f01cd0554010000000c110000002f01e8053f01e6054f01cd0554010000000c110000002f01ea053f01e8054f01cd0554010000000c110000002f01ec053f01ea054f01cd0554010000000c110000002f01ee053f01ec054f01cd0554010000000c110000002f01f0053f01ee054f01cd0554010000000c110000002f01f2053f01f0054f01cd0554010000000c110000002f01f4053f01f2054f01cd0554010000000c110000002f01f6053f01f4054f01cd0554010000000c100000002f01cd053f01df044f000054040000000c110000002f01fe063f01d0054f01d80554040000000c100000002f01d8053f01d0054f000054040000000c200000002f0182073f01db054f000054000000006c0b0000000901427275670a427275672c210000001c1c000000020190061f01fd062c02000000110200001f01d6042c0200000011023c1000000000000000000082c00000000000406d4044000090445c0e0000001c090000001f01f3062400006a44"

func textPage(t *testing.T) []byte {
	body, err := hex.DecodeString(textBlockHex)
	if err != nil {
		t.Fatal(err)
	}
	return append(make([]byte, v6HeaderSize), v6Block(rootTextBlock, 1, body)...)
}

func TestParseText(t *testing.T) {
	text := ParseText(textPage(t))
	if text == nil {
		t.Fatal("no text")
	}
	if text.X != -576 || text.Y != 234 || text.Width != 1152 {
		t.Errorf("box %v %v %v", text.X, text.Y, text.Width)
	}
	if len(text.Paragraphs) != 11 {
		t.Fatalf("%d paragraphs", len(text.Paragraphs))
	}
	heading, plain := text.Paragraphs[9], text.Paragraphs[10]
	if heading.Text != "Brug" || heading.Style != styleHeading || heading.Y != 234+9*lineHeight {
		t.Errorf("heading %+v", heading)
	}
	if plain.Text != "Brug" || plain.Style != 0 || plain.Y != heading.Y+headingHeight {
		t.Errorf("plain %+v", plain)
	}
}

func TestAnchorYFollowsParagraph(t *testing.T) {
	text := ParseText(textPage(t))
	// the fifth empty line, 780: four lines below the first
	if y, ok := text.AnchorY(charID{1, 780}); !ok || y != anchorY+4*lineHeight {
		t.Errorf("anchor %v %v", y, ok)
	}
	if _, ok := text.AnchorY(charID{0, 1 << 40}); ok {
		t.Error("an id that is not in the text has an anchor")
	}
	if ParseText(make([]byte, v6HeaderSize)) != nil {
		t.Error("text on a page without")
	}
}
