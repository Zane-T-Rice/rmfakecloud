package exporter

import (
	"io"

	"github.com/juruen/rmapi/archive"
	"github.com/juruen/rmapi/log"
)

// rmapi's logging stuff
func init() {
	log.InitLog()
}

// MyArchive but having the payload reader
type MyArchive struct {
	archive.Zip
	PayloadReader io.ReadSeekCloser
	// the width in pixels of the paper of the pages (their index) that have it, see PaperSize
	PaperWidths map[int]int
	// the colors of the strokes of the pages (their index) that have some of their own, see StrokeColors
	LineColors map[int][]uint32
}

func (f *MyArchive) Close() {
	if f.PayloadReader != nil {
		f.PayloadReader.Close()
	}
}
