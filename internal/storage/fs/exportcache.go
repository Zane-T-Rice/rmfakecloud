package fs

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strconv"

	log "github.com/sirupsen/logrus"
)

// bump it when the rendering of the exports changes
const exportCacheVersion = "14"

// templatesStamp changes when a template is added, removed or edited
func (fs *FileSystemStorage) templatesStamp() string {
	var latest int64
	entries, err := os.ReadDir(fs.templatesDir())
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().UnixNano() > latest {
			latest = info.ModTime().UnixNano()
		}
	}
	return strconv.FormatInt(latest, 10)
}

// ExportKey identifies the current rendering of a document, it changes when the
// document does (its hash), or the templates it is drawn with.
func (fs *FileSystemStorage) ExportKey(uid, docid string) (string, error) {
	tree, err := fs.GetCachedTree(uid)
	if err != nil {
		return "", err
	}
	doc, err := tree.FindDoc(docid)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(exportCacheVersion + "|" + doc.Hash + "|" + fs.templatesStamp()))
	return hex.EncodeToString(sum[:8]), nil
}

// ExportCached gives the pdf of a document, from the cache when the document
// is unchanged, unless refresh is set. The key is the same as ExportKey.
func (fs *FileSystemStorage) ExportCached(uid, docid string, refresh bool) (io.ReadCloser, string, error) {
	key, err := fs.ExportKey(uid, docid)
	if err != nil {
		return nil, "", err
	}

	dir := fs.getPathFromUser(uid, CacheDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, "", err
	}
	prefix := sanitizeFileName(docid) + "-export-"
	file := filepath.Join(dir, prefix+key+".pdf")

	if !refresh {
		if f, err := os.Open(file); err == nil {
			return f, key, nil
		}
	}

	r, err := fs.Export(uid, docid)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()

	// render to the side, a failed or half done export must not be served later
	tmp, err := os.CreateTemp(dir, prefix+"*.tmp")
	if err != nil {
		return nil, "", err
	}
	_, err = io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return nil, "", err
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		os.Remove(tmp.Name())
		return nil, "", err
	}

	// older renderings of this document are of no use anymore
	if old, err := filepath.Glob(filepath.Join(dir, prefix+"*.pdf")); err == nil {
		for _, o := range old {
			if o != file {
				if err := os.Remove(o); err != nil {
					log.Warn("cannot remove the old export: ", err)
				}
			}
		}
	}

	f, err := os.Open(file)
	if err != nil {
		return nil, "", err
	}
	return f, key, nil
}
