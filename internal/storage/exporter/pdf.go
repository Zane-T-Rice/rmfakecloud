package exporter

import (
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/juruen/rmapi/encoding/rm"
	"github.com/sirupsen/logrus"
	"github.com/unidoc/unipdf/v3/annotator"
	"github.com/unidoc/unipdf/v3/contentstream"
	"github.com/unidoc/unipdf/v3/contentstream/draw"
	"github.com/unidoc/unipdf/v3/core"
	"github.com/unidoc/unipdf/v3/creator"
	pdf "github.com/unidoc/unipdf/v3/model"
)

const (
	DeviceWidth  = 1404
	DeviceHeight = 1872
)

const (
	// Paper Pro screen, used by v6 files
	PaperProWidth  = 1620
	PaperProHeight = 2160
	// the pixels per inch the ink of a pdf is drawn with. The screen is 229 dpi, but
	// the end of long notes only lines up with about 228 (the tablet shows the pdf about
	// 0.4% larger than its real size)
	PaperProDPI = 228.0
	// small corrections to the ink on a pdf, in pixels
	v6PdfOffsetX = -3.0
	// the ink is stretched across the page by this, around the middle
	v6PdfStretchX = 1.0075
	v6PdfOffsetY  = 11.0

	// template-only pages are this wide in points, the height follows the content
	rmPageWidth  = 445.0
	rmPageHeight = 594.0
	// extra room below the lowest stroke, in device pixels
	pageBottomMargin = 100.0
)

// ddvk/rmapi decodes v6 x coordinates (centered on the page) assuming the
// classic 1404px wide screen, so on a Paper Pro they are off by this much
const v6XCorrection = float64(PaperProWidth)/2 - float64(DeviceWidth)/2

type PdfGenerator struct {
	options   PdfGeneratorOptions
	pdfReader *pdf.PdfReader
	template  bool
	templates map[string]*template
	// the document has v6 (Paper Pro) pages
	v6 bool
}

type PdfGeneratorOptions struct {
	AddPageNumbers  bool
	AllPages        bool
	AnnotationsOnly bool //export the annotations without the background/pdf
	// TemplatesDir has the *.template files copied from the device, backgrounds
	// of notebook pages are left blank without them
	TemplatesDir string
}

// isV6 tells if a page is a Paper Pro page. Pages without ink have no version,
// they follow the rest of the document (assumeV6).
func isV6(data *rm.Rm, assumeV6 bool) bool {
	if data == nil {
		return assumeV6
	}
	return data.Version == rm.V6
}

// deviceSize returns the screen size the page was written on
func deviceSize(data *rm.Rm, assumeV6 bool) (float64, float64) {
	if isV6(data, assumeV6) {
		return PaperProWidth, PaperProHeight
	}
	return DeviceWidth, DeviceHeight
}

// xCorrection is the shift to apply to the decoded x coordinates
func xCorrection(data *rm.Rm) float64 {
	if data != nil && data.Version == rm.V6 {
		return v6XCorrection
	}
	return 0
}

// maxContentY is the lowest point drawn on the page
func maxContentY(data *rm.Rm) float64 {
	var max float64
	if data == nil {
		return max
	}
	for _, layer := range data.Layers {
		for _, line := range layer.Lines {
			for _, p := range line.Points {
				if float64(p.Y) > max {
					max = float64(p.Y)
				}
			}
		}
	}
	return max
}

// templatePageHeight is the height in points needed to fit the whole content
// of a template page (notebook pages can scroll far past one screen)
func templatePageHeight(data *rm.Rm, assumeV6 bool) float64 {
	w, h := deviceSize(data, assumeV6)
	scale := rmPageWidth / w
	needed := scale * math.Max(h, maxContentY(data)+pageBottomMargin)
	return math.Max(rmPageHeight, needed)
}

// strokeWidthFactor scales the width of the strokes, 1 is the width in the file, which looks too heavy
const strokeWidthFactor = 0.6

// minStrokeWidth is the thinnest line drawn, in points
const minStrokeWidth = 0.3

// strokeWidth is the width of a line in points: the average width of its points,
// which is what the tablet draws with, in the pixels of the device
func strokeWidth(line rm.Line, scale float64) float64 {
	var sum float64
	for _, p := range line.Points {
		sum += float64(p.Width)
	}
	if len(line.Points) == 0 || sum == 0 {
		return float64(line.BrushSize / 10)
	}
	return math.Max(sum/float64(len(line.Points))*scale*strokeWidthFactor, minStrokeWidth)
}

// strokeColor is the color of a pen, they are the ones of the tablet
func strokeColor(c rm.BrushColor) (r, g, b float64) {
	switch c {
	case rm.Grey, rm.GreyOverlap:
		return 0.5, 0.5, 0.5
	case rm.White:
		return 1, 1, 1
	case rm.Blue:
		return 0.1, 0.25, 0.85
	case rm.Red:
		return 0.85, 0.1, 0.1
	case rm.Green2:
		return 0.1, 0.6, 0.25
	case rm.Cyan:
		return 0.0, 0.7, 0.85
	case rm.Magenta:
		return 0.85, 0.1, 0.7
	case rm.Yellow2:
		return 0.95, 0.8, 0.0
	}
	// black, and the colors we don't know
	return 0, 0, 0
}

// highlightColor is the color of a highlighter, yellow unless it has another
func highlightColor(c rm.BrushColor) (r, g, b float64) {
	switch c {
	case rm.HighlightGreen:
		return 0.5, 1, 0.4
	case rm.HighlightPink:
		return 1, 0.5, 0.8
	case rm.Blue, rm.Cyan:
		return 0.4, 0.8, 1
	case rm.Red, rm.Magenta:
		return 1, 0.4, 0.6
	case rm.Green2:
		return 0.5, 1, 0.4
	}
	return 1, 1, 0
}

// v6PdfTransform gives the scale (points per pixel) and the x shift (pixels, before
// scaling) of the ink on a pdf page of the given width. The Paper Pro shows the pdf
// at its real size, centered on x (0 is the middle of the page) with the top of the
// page at y=0. The decoded x is 1404/2 off from the middle, see v6XCorrection.
func v6PdfTransform(pageWidth float64) (scale, xShift, yShift, xStretch float64) {
	scale = 72.0 / PaperProDPI
	// the ink was still a little high and left of the writing on the tablet,
	// measured by eye on the printed text of a rulebook
	return scale, pageWidth/2/scale - float64(DeviceWidth)/2 + v6PdfOffsetX, v6PdfOffsetY, v6PdfStretchX
}

// stretchAround scales x away from the middle of the page cx
func stretchAround(x, cx, stretch float64) float64 {
	return cx + (x-cx)*stretch
}

func normalized(p1 rm.Point, ratioX float64, xShift, yShift float64) (float64, float64) {
	return (float64(p1.X) + xShift) * ratioX, (float64(p1.Y) + yShift) * ratioX
}

func (p *PdfGenerator) Generate(zip *MyArchive, output io.Writer, options PdfGeneratorOptions) (err error) {

	p.options = options
	for _, pg := range zip.Pages {
		if pg.Data != nil && pg.Data.Version == rm.V6 {
			p.v6 = true
		}
	}

	if len(zip.Pages) == 0 {
		if zip.PayloadReader != nil {
			_, err := io.Copy(output, zip.PayloadReader)
			return err
		}

		return errors.New("the document has no pages")
	}

	if err = p.initBackgroundPages(zip.PayloadReader); err != nil {
		return err
	}

	c := creator.New()

	if p.pdfReader != nil && p.options.AllPages {
		logrus.Info("generating all pages")
		outlines := p.pdfReader.GetOutlineTree()
		c.SetOutlineTree(outlines)
	}

	for i, pageAnnotations := range zip.Pages {
		hasContent := pageAnnotations.Data != nil

		// do not add a page when there are no annotations
		if !p.options.AllPages && !hasContent {
			continue
		}

		page, err := p.addBackgroundPage(c, i+1, templatePageHeight(pageAnnotations.Data, p.v6))
		if err != nil {
			return err
		}

		deviceW, deviceH := deviceSize(pageAnnotations.Data, p.v6)
		xShift := xCorrection(pageAnnotations.Data)
		yShift := 0.0
		xStretch := 1.0

		var scale float64
		if p.template {
			// the page is as wide as the screen, the height was fitted to the content
			scale = c.Width() / deviceW
		} else if ratio := c.Height() / c.Width(); ratio < 1.33 {
			scale = c.Width() / deviceW
		} else {
			scale = c.Height() / deviceH
		}
		if !p.template && pageAnnotations.Data != nil && pageAnnotations.Data.Version == rm.V6 {
			scale, xShift, yShift, xStretch = v6PdfTransform(c.Width())
		}
		if page == nil {
			logrus.Fatal("page is null")
		}

		if p.template && !p.options.AnnotationsOnly {
			p.drawTemplate(c, page, pageAnnotations.Pagedata, deviceW, deviceH, scale)
		}

		if err != nil {
			return err
		}
		if !hasContent {
			continue
		}

		contentCreator := contentstream.NewContentCreator()
		contentCreator.Add_q()
		// like a pen, not a wire
		contentCreator.AddOperand(contentstream.ContentStreamOperation{Operand: "J", Params: []core.PdfObject{core.MakeInteger(1)}})
		contentCreator.AddOperand(contentstream.ContentStreamOperation{Operand: "j", Params: []core.PdfObject{core.MakeInteger(1)}})

		for _, layer := range pageAnnotations.Data.Layers {
			for _, line := range layer.Lines {
				if len(line.Points) < 1 {
					continue
				}
				if line.BrushType == rm.Eraser || line.BrushType == rm.EraseArea {
					continue
				}

				if line.BrushType == rm.HighlighterV5 {
					last := len(line.Points) - 1
					x1, y1 := normalized(line.Points[0], scale, xShift, yShift)
					x1 = stretchAround(x1, c.Width()/2, xStretch)
					x2, _ := normalized(line.Points[last], scale, xShift, yShift)
					x2 = stretchAround(x2, c.Width()/2, xStretch)
					// make horizontal lines only, use y1
					width := scale * 30
					y1 += width / 2

					lineDef := annotator.LineAnnotationDef{X1: x1 - 1, Y1: c.Height() - y1, X2: x2, Y2: c.Height() - y1}
					hr, hg, hb := highlightColor(line.BrushColor)
					lineDef.LineColor = pdf.NewPdfColorDeviceRGB(hr, hg, hb)
					lineDef.Opacity = 0.5
					lineDef.LineWidth = width
					ann, err := annotator.CreateLineAnnotation(lineDef)
					if err != nil {
						return err
					}
					page.AddAnnotation(ann)
				} else {
					path := draw.NewPath()
					for i := 0; i < len(line.Points); i++ {
						x1, y1 := normalized(line.Points[i], scale, xShift, yShift)
						x1 = stretchAround(x1, c.Width()/2, xStretch)
						path = path.AppendPoint(draw.NewPoint(x1, c.Height()-y1))
					}

					contentCreator.Add_w(strokeWidth(line, scale))

					r, g, b := strokeColor(line.BrushColor)
					contentCreator.Add_RG(r, g, b)

					//TODO: use bezier
					draw.DrawPathWithCreator(path, contentCreator)

					contentCreator.Add_S()
				}
			}
		}
		contentCreator.Add_Q()
		drawingOperations := contentCreator.Operations().String()
		pageContentStreams, err := page.GetAllContentStreams()
		if err != nil {
			return err
		}
		//hack: wrap the page content in a context to prevent transformation matrix misalignment
		wrapper := []string{"q", pageContentStreams, "Q", drawingOperations}
		page.SetContentStreams(wrapper, core.NewFlateEncoder())
	}

	return c.Write(output)
}

func (p *PdfGenerator) initBackgroundPages(r io.ReadSeeker) error {
	if r != nil {
		pdfReader, err := pdf.NewPdfReader(r)
		if err != nil {
			return err
		}

		encrypted, err := pdfReader.IsEncrypted()
		if err != nil {
			return nil
		}
		if encrypted {
			valid, err := pdfReader.Decrypt([]byte(""))
			if err != nil {
				return err
			}
			if !valid {
				return fmt.Errorf("cannot decrypt")
			}

		}

		p.pdfReader = pdfReader
		p.template = false
		return nil
	}

	logrus.Info("template")
	p.template = true
	return nil
}

func (p *PdfGenerator) addBackgroundPage(c *creator.Creator, pageNum int, templateHeight float64) (*pdf.PdfPage, error) {
	var page *pdf.PdfPage

	if !p.template && !p.options.AnnotationsOnly {
		tmpPage, err := p.pdfReader.GetPage(pageNum)
		if err != nil {
			return nil, err
		}
		mbox, err := tmpPage.GetMediaBox()
		if err != nil {
			return nil, err
		}

		// TODO: adjust the page if cropped
		pageHeight := mbox.Ury - mbox.Lly
		pageWidth := mbox.Urx - mbox.Llx
		// use the pdf's page size
		c.SetPageSize(creator.PageSize{pageWidth, pageHeight})
		c.AddPage(tmpPage)
		page = tmpPage
	} else {
		if p.template {
			c.SetPageSize(creator.PageSize{rmPageWidth, templateHeight})
		}
		page = c.NewPage()
	}

	if p.options.AddPageNumbers {
		c.DrawFooter(func(block *creator.Block, args creator.FooterFunctionArgs) {
			p := c.NewParagraph(fmt.Sprintf("%d", args.PageNum))
			p.SetFontSize(8)
			w := block.Width() - 20
			h := block.Height() - 10
			p.SetPos(w, h)
			block.Draw(p)
		})
	}
	return page, nil
}

// drawTemplate draws the background template of a notebook page, if the
// template files are available. Pages are left blank otherwise.
func (p *PdfGenerator) drawTemplate(c *creator.Creator, page *pdf.PdfPage, name string, deviceW, deviceH, scale float64) {
	if p.options.TemplatesDir == "" || name == "" || name == "Blank" {
		return
	}
	t, ok := p.templates[name]
	if !ok {
		var err error
		t, err = LoadTemplate(p.options.TemplatesDir, name)
		if err != nil {
			logrus.Warnf("cannot load template %q: %v", name, err)
		}
		if p.templates == nil {
			p.templates = make(map[string]*template)
		}
		// remember failures too
		p.templates[name] = t
	}
	if t == nil {
		return
	}

	ops, err := t.Render(c, deviceW, deviceH, c.Height()/scale, scale)
	if err != nil {
		logrus.Warnf("cannot render template %q: %v", name, err)
		return
	}
	existing, err := page.GetAllContentStreams()
	if err != nil {
		logrus.Warnf("template %q: %v", name, err)
		return
	}
	page.SetContentStreams([]string{existing, ops}, core.NewFlateEncoder())
}
