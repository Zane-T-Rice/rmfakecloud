package exporter

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// the start of the scene info of two pages of the same notebook: the first was made
// on an older tablet, the second on a Paper Pro
func sceneInfoFile(t *testing.T, bodyHex string) []byte {
	t.Helper()
	body, err := hex.DecodeString(bodyHex)
	if err != nil {
		t.Fatal(err)
	}
	data := append([]byte("reMarkable .lines file, version=6          "), make([]byte, 0)...)
	// another block first
	data = append(data, 3, 0, 0, 0, 0, 1, 1, 0x09, 1, 2, 3)
	header := make([]byte, v6BlockHeaderSize)
	binary.LittleEndian.PutUint32(header, uint32(len(body)))
	header[5], header[6], header[7] = 0, 1, sceneInfoBlock
	return append(append(data, header...), body...)
}

func TestPaperSize(t *testing.T) {
	for _, c := range []struct {
		name, body string
		w, h       int
	}{
		{"older tablet", "1c060000001f00002f00002c050000001f000021013c050000001f000021015c080000007c050000500700006c280000", 1404, 1872},
		{"paper pro", "1c060000001f00002f00002c050000001f000021013c050000001f000021015c080000005406000070080000" + "6c280000", 1620, 2160},
	} {
		w, h, ok := PaperSize(sceneInfoFile(t, c.body))
		if !ok || w != c.w || h != c.h {
			t.Errorf("%s: got %dx%d (%v), want %dx%d", c.name, w, h, ok, c.w, c.h)
		}
	}
	if _, _, ok := PaperSize([]byte("short")); ok {
		t.Error("a truncated file has a paper size")
	}
	if _, _, ok := PaperSize(sceneInfoFile(t, "1c060000001f0000")); ok {
		t.Error("a scene info without a paper size has one")
	}
}
