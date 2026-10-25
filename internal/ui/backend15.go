package ui

import (
	"io"

	"github.com/ddvk/rmfakecloud/internal/app/hub"
	"github.com/ddvk/rmfakecloud/internal/storage"
	"github.com/ddvk/rmfakecloud/internal/ui/viewmodel"
	"github.com/google/uuid"
)

type backend15 struct {
	blobHandler blobHandler
	h           *hub.Hub
}

func (b *backend15) GetDocumentTree(uid string) (tree *viewmodel.DocumentTree, err error) {
	hashTree, err := b.blobHandler.GetCachedTree(uid)
	if err != nil {
		return nil, err
	}

	return viewmodel.DocTreeFromHashTree(hashTree), nil
}
func (b *backend15) Export(uid, docid, exporttype string, opt storage.ExportOption) (r io.ReadCloser, err error) {
	if exporttype == "rmdoc" {
		return b.blobHandler.ExportRmDoc(uid, docid)
	}
	r, err = b.blobHandler.Export(uid, docid)
	return
}

// ExportKey identifies the current pdf of a document, see FileSystemStorage
func (b *backend15) ExportKey(uid, docid string) (string, error) {
	return b.blobHandler.ExportKey(uid, docid)
}

// ExportCached gives the pdf of a document from the cache when it is unchanged
func (b *backend15) ExportCached(uid, docid string, refresh bool) (io.ReadCloser, string, error) {
	return b.blobHandler.ExportCached(uid, docid, refresh)
}

func (b *backend15) CreateDocument(uid, filename, parent string, stream io.Reader) (doc *storage.Document, err error) {
	doc, err = b.blobHandler.CreateBlobDocument(uid, filename, parent, stream)
	return
}

func (b *backend15) UpdateDocument(uid, docID, name, parent string) (err error) {
	return b.blobHandler.UpdateBlobDocument(uid, docID, name, parent)
}
func (b *backend15) CreateFolder(uid, name, parent string) (doc *storage.Document, err error) {
	return b.blobHandler.CreateBlobFolder(uid, name, parent)
}

func (b *backend15) DeleteDocument(uid, docID string) (err error) {
	return b.blobHandler.DeleteBlobDocument(uid, docID)
}

func (b *backend15) Sync(uid string) {
	b.h.NotifySync(uid, uuid.NewString())
}
