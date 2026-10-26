package models

import (
	"encoding/json"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/storage"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter"
	"github.com/juruen/rmapi/archive"
	"github.com/juruen/rmapi/encoding/rm"
	log "github.com/sirupsen/logrus"
)

// cPagesContent captures the page ordering used by newer reMarkable software
// (formatVersion 2+), which drops the flat "pages" array in favor of a cPages
// structure. Page entries carry a "redir" (index into the underlying pdf/epub)
// or, for plain notebooks, only a lexicographically-ordered "idx". Removed
// pages are kept around with a "deleted" marker rather than being omitted.
type cPagesContent struct {
	CPages *struct {
		Pages []struct {
			ID      string `json:"id"`
			Deleted *struct {
				Value int `json:"value"`
			} `json:"deleted"`
			Idx struct {
				Value string `json:"value"`
			} `json:"idx"`
			Template struct {
				Value string `json:"value"`
			} `json:"template"`
			Redir *struct {
				Value int `json:"value"`
			} `json:"redir"`
		} `json:"pages"`
	} `json:"cPages"`
}

// cPageInfo is a live page of a document
type cPageInfo struct {
	ID       string
	Template string
}

// pagesFromCPages extracts the live (non-deleted) pages in document order
// from the cPages structure, for content files that don't populate the
// legacy flat "pages" array.
func pagesFromCPages(contentBytes []byte) []cPageInfo {
	var withCPages cPagesContent
	if err := json.Unmarshal(contentBytes, &withCPages); err != nil || withCPages.CPages == nil {
		return nil
	}

	type entry struct {
		id       string
		template string
		idx      string
		redir    int
		hasRedir bool
	}
	var entries []entry
	for _, p := range withCPages.CPages.Pages {
		if p.Deleted != nil {
			continue
		}
		e := entry{id: p.ID, template: p.Template.Value, idx: p.Idx.Value}
		if p.Redir != nil {
			e.redir = p.Redir.Value
			e.hasRedir = true
		}
		entries = append(entries, e)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].hasRedir && entries[j].hasRedir {
			return entries[i].redir < entries[j].redir
		}
		return entries[i].idx < entries[j].idx
	})

	pages := make([]cPageInfo, len(entries))
	for i, e := range entries {
		pages[i] = cPageInfo{ID: e.id, Template: e.template}
	}
	return pages
}

// ArchiveFromHashDoc reads an archive
func ArchiveFromHashDoc(doc *HashDoc, rs RemoteStorage) (*exporter.MyArchive, error) {
	uuid := doc.EntryName
	a := exporter.MyArchive{
		Zip: archive.Zip{
			UUID: uuid,
		},
	}

	pageMap := make(map[string]string)
	templates := make(map[string]string)
	for _, f := range doc.Files {
		filext := path.Ext(f.EntryName)
		name := strings.TrimSuffix(path.Base(f.EntryName), filext)
		switch filext {
		case storage.ContentFileExt:
			blob, err := rs.GetReader(f.Hash)
			if err != nil {
				return nil, err
			}
			defer blob.Close()
			contentBytes, err := io.ReadAll(blob)
			if err != nil {
				return nil, err
			}
			err = json.Unmarshal(contentBytes, &a.Content)
			if err != nil {
				return nil, err
			}
			if len(a.Content.Pages) == 0 {
				if pages := pagesFromCPages(contentBytes); len(pages) > 0 {
					log.Debug("pages empty, falling back to cPages, count: ", len(pages))
					for _, pg := range pages {
						a.Content.Pages = append(a.Content.Pages, pg.ID)
						templates[pg.ID] = pg.Template
					}
				}
			}
		case storage.EpubFileExt:
			fallthrough
		case storage.PdfFileExt:
			blob, err := rs.GetReader(f.Hash)
			if err != nil {
				return nil, err
			}
			// defer blob.Close()
			// contentBytes, err := ioutil.ReadAll(blob)
			// if err != nil {
			// 	return nil, err
			// }
			// a.Payload = contentBytes
			//HACK:
			a.PayloadReader = blob.(io.ReadSeekCloser)

		case ".json":
			//metadata
		case storage.RmFileExt:
			log.Debug("adding page ", name)
			pageMap[name] = f.Hash
		}
	}

	for i, p := range a.Content.Pages {
		page := archive.Page{
			Pagedata: pageTemplate(templates[p]),
			DocPage:  i,
		}
		if hash, ok := pageMap[p]; ok {
			log.Debug("page ", hash)
			reader, err := rs.GetReader(hash)
			if err != nil {
				return nil, err
			}
			pageBin, err := io.ReadAll(reader)
			if err != nil {
				return nil, err
			}
			rmpage := rm.New()
			err = rmpage.UnmarshalBinary(pageBin)
			if err != nil {
				return nil, err
			}
			exporter.ApplyAnchors(rmpage, pageBin)
			if colors := exporter.StrokeColors(rmpage, pageBin); colors != nil {
				if a.LineColors == nil {
					a.LineColors = make(map[int][]uint32)
				}
				a.LineColors[i] = colors
			}
			page.Data = rmpage
			if w, _, ok := exporter.PaperSize(pageBin); ok {
				if a.PaperWidths == nil {
					a.PaperWidths = make(map[int]int)
				}
				a.PaperWidths[i] = w
			}
		}
		// A page with no .rm file (never opened/annotated on the device)
		// still needs an entry here so the output page count and page
		// numbering match the underlying pdf/epub; the generator already
		// handles a nil Data as a background-only page.
		a.Pages = append(a.Pages, page)
	}

	return &a, nil
}

// pageTemplate is the template of a page, blank when unknown
func pageTemplate(name string) string {
	if name == "" {
		return "Blank"
	}
	return name
}
