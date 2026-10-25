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

func normalized(p1 rm.Point, ratioX float64, xShift float64) (float64, float64) {
	return (float64(p1.X) + xShift) * ratioX, float64(p1.Y) * ratioX
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

		var scale float64
		if p.template {
			// the page is as wide as the screen, the height was fitted to the content
			scale = c.Width() / deviceW
		} else if ratio := c.Height() / c.Width(); ratio < 1.33 {
			scale = c.Width() / deviceW
		} else {
			scale = c.Height() / deviceH
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
					x1, y1 := normalized(line.Points[0], scale, xShift)
					x2, _ := normalized(line.Points[last], scale, xShift)
					// make horizontal lines only, use y1
					width := scale * 30
					y1 += width / 2

					lineDef := annotator.LineAnnotationDef{X1: x1 - 1, Y1: c.Height() - y1, X2: x2, Y2: c.Height() - y1}
					lineDef.LineColor = pdf.NewPdfColorDeviceRGB(1.0, 1.0, 0.0) //yellow
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
						x1, y1 := normalized(line.Points[i], scale, xShift)
						path = path.AppendPoint(draw.NewPoint(x1, c.Height()-y1))
					}

					contentCreator.Add_w(float64(line.BrushSize / 10))

					switch line.BrushColor {
					case rm.Black:
						contentCreator.Add_rg(1.0, 1.0, 1.0)
					case rm.White:
						contentCreator.Add_rg(0.0, 0.0, 0.0)
					case rm.Grey:
						contentCreator.Add_rg(0.8, 0.8, 0.8)
					}

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
