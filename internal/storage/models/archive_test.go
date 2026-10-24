package models

import (
	"reflect"
	"testing"
)

func TestPagesFromCPages_PdfUsesRedirOrder(t *testing.T) {
	// Minimal shape of a formatVersion 2 .content file for a pdf, as written
	// by newer reMarkable software: no flat "pages" array, page order and
	// identity live under cPages, keyed by "redir" (the underlying pdf page).
	content := []byte(`{
		"fileType": "pdf",
		"formatVersion": 2,
		"pageCount": 3,
		"cPages": {
			"pages": [
				{"id": "page-c", "idx": {"value": "bc"}, "redir": {"value": 2}, "template": {"value": "P Grid medium"}},
				{"id": "page-a", "idx": {"value": "ba"}, "redir": {"value": 0}},
				{"id": "page-b", "idx": {"value": "bb"}, "redir": {"value": 1}}
			]
		}
	}`)

	got := pagesFromCPages(content)
	want := []cPageInfo{{ID: "page-a"}, {ID: "page-b"}, {ID: "page-c", Template: "P Grid medium"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPagesFromCPages_NotebookSkipsDeletedUsesIdxOrder(t *testing.T) {
	// Notebook pages have no "redir" (there's no underlying document to map
	// to), only a lexicographically-ordered "idx", and deleted pages are kept
	// around tombstoned with a "deleted" marker instead of being removed.
	content := []byte(`{
		"fileType": "notebook",
		"formatVersion": 2,
		"pageCount": 2,
		"cPages": {
			"pages": [
				{"id": "page-removed", "idx": {"value": "aa"}, "deleted": {"value": 1}},
				{"id": "page-2", "idx": {"value": "be"}},
				{"id": "page-1", "idx": {"value": "ba"}}
			]
		}
	}`)

	got := pagesFromCPages(content)
	want := []cPageInfo{{ID: "page-1"}, {ID: "page-2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPagesFromCPages_NoCPagesReturnsNil(t *testing.T) {
	content := []byte(`{"fileType": "pdf", "pages": ["a", "b"]}`)
	if got := pagesFromCPages(content); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}
