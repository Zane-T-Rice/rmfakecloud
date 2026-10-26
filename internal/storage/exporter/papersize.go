package exporter

import "encoding/binary"

const (
	// the header of a v6 file, "reMarkable .lines file, version=6" padded with spaces
	v6HeaderSize = 43
	// [length(4), unknown(1), min version(1), version(1), type(1)] before each block
	v6BlockHeaderSize = 8
	// the block with the scene info
	sceneInfoBlock = 0x0d
	// the tag of its paper size, a sub block of 8 bytes: width and height
	paperSizeTag = 0x5c
)

// PaperSize reads the size in pixels of the paper a v6 page was made for from its
// scene info. The tablet draws the template of the page on a canvas this big,
// centered on the screen: a page made on an older tablet has 1404x1872.
func PaperSize(data []byte) (width, height int, ok bool) {
	for o := v6HeaderSize; o+v6BlockHeaderSize <= len(data); {
		end := o + v6BlockHeaderSize + int(binary.LittleEndian.Uint32(data[o:]))
		if end > len(data) {
			return 0, 0, false
		}
		if data[o+7] == sceneInfoBlock {
			body := data[o+v6BlockHeaderSize : end]
			for i := 0; i+13 <= len(body); i++ {
				if body[i] != paperSizeTag || binary.LittleEndian.Uint32(body[i+1:]) != 8 {
					continue
				}
				w := int(binary.LittleEndian.Uint32(body[i+5:]))
				h := int(binary.LittleEndian.Uint32(body[i+9:]))
				if w >= 500 && w <= 5000 && h >= 500 && h <= 8000 {
					return w, h, true
				}
			}
			return 0, 0, false
		}
		o = end
	}
	return 0, 0, false
}
