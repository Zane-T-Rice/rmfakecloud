import { useState, useEffect, useRef, useMemo } from "react";
import { Button, ButtonGroup, Dropdown, Form } from "react-bootstrap";
import Navbar from 'react-bootstrap/Navbar';
import { FaChevronRight, FaChevronLeft, } from "react-icons/fa6";
import { AiOutlineDownload, AiOutlineReload } from "react-icons/ai";
import constants from "../../common/constants";

import apiservice from "../../services/api.service"
import NameTag from "../../components/NameTag"

import { pdfjs, Document, Page } from "react-pdf";
// import 'react-pdf/dist/Page/AnnotationLayer.css';
// import 'react-pdf/dist/Page/TextLayer.css';
// import 'react-pdf/dist/Page/TextLayer.css';
//pdfjs.GlobalWorkerOptions.workerSrc = "/pdf.worker.js"
// pdfjs.GlobalWorkerOptions.workerSrc = new URL(
//   'pdfjs-dist/build/pdf.worker.min.mjs',
//   import.meta.url,
// ).toString(); 


// the tablet screen is 3:4, size the page so the viewer height shows what the
// tablet screen shows, whatever the window width. Never wider than the viewer
// and not too small to read.
const TABLET_ASPECT = 3 / 4;

// the page each document was last at, kept in this browser
const pageKey = (id) => `rmfakecloud.page.${id}`;
const loadSavedPage = (id) => {
  try {
    return parseInt(window.localStorage.getItem(pageKey(id)), 10);
  } catch (e) {
    return NaN;
  }
};
const savePage = (id, page) => {
  try {
    window.localStorage.setItem(pageKey(id), String(page));
  } catch (e) {
    // no storage, no memory
  }
};
const MIN_PAGE_WIDTH = 400;

export default function FileViewer({ file, onSelect }) {
  const { data } = file;

  const downloadUrl = `${constants.ROOT_URL}/documents/${file.id}`;

  const [page, setPage] = useState(1);
  const [pages, setPages] = useState(1);
  const [width, setWidth] = useState(100);
  // the document whose page is known, nothing is saved before that
  const loadedId = useRef(null);
  const onLoadSuccess = (pdf) => {
    // back to where it was, unless the document has changed since
    const saved = loadSavedPage(file.id);
    setPage(saved >= 1 && saved <= pdf.numPages ? saved : 1);
    setPages(pdf.numPages);
    loadedId.current = file.id;
  };
  useEffect(() => {
    if (loadedId.current === file.id) {
      savePage(file.id, page);
    }
  }, [page, file.id]);
  const onPrev = () => {
    setPage((p) => {
      return Math.max(p - 1, 1);
    });
  };
  const onNext = () => {
    setPage((p) => {
      return Math.min(p + 1, pages);
    });
  };

  const [pageInput, setPageInput] = useState("1");
  useEffect(() => {
    setPageInput(String(page));
  }, [page]);

  const commitPageInput = () => {
    const value = parseInt(pageInput, 10);
    if (!Number.isNaN(value)) {
      setPage(Math.min(Math.max(value, 1), pages));
    } else {
      setPageInput(String(page));
    }
  };
  const parent = useRef(null);
  useEffect(() => {
    // fit the page to the viewer width, long pages scroll
    const el = parent.current;
    if (!el) return;
    const resizeObserver = new ResizeObserver(() => {
      const fit = Math.max(el.clientHeight * TABLET_ASPECT, MIN_PAGE_WIDTH);
      setWidth(Math.min(el.clientWidth, fit));
    });
    resizeObserver.observe(el);
    return () => resizeObserver.disconnect();
  }, [file]);

  // start every page at the top
  useEffect(() => {
    if (parent.current) parent.current.scrollTop = 0;
  }, [page]);

  // TODO: add loading and error handling
  const triggerDownload = (blob, filename) => {
    var url = window.URL.createObjectURL(blob)
    var a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    a.remove()
  }

  // The browser keeps the pdf and the server checks if it is still the current one
  // (so a big book opens at once). This fetches it again without any cache, e.g. to
  // see the latest scribbles.
  const [reloadKey, setReloadKey] = useState(0);
  const [reloading, setReloading] = useState(false);
  const onReload = () => {
    setReloading(true);
    fetch(downloadUrl, { cache: "reload" })
      .then((r) => r.blob())
      .catch(() => {})
      .finally(() => {
        setReloading(false);
        setReloadKey((k) => k + 1);
      });
  };

  const onDownloadPdf = () => {
    apiservice.download(data.id)
      .then(blob => triggerDownload(blob, data.name + '.pdf'))
      .catch(() => {})
  }

  const onDownloadRmdoc = () => {
    apiservice.download(data.id, 'rmdoc')
      .then(blob => triggerDownload(blob, data.name + '.rmdoc'))
      .catch(() => {})
  }

  let options = useMemo(()=> {
    return {
      worker: new pdfjs.PDFWorker()
    }
  },[pdfjs])

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column" }}>
      <Navbar style={{ marginLeft: '-12px' }}>
        { file && (<div><NameTag node={file} onSelect={onSelect} /></div>) }
      </Navbar>

      <Navbar>
        {pages > 1 && (
          <div>
            <ButtonGroup aria-label="Basic example">
              <Button size="sm" variant="outline-secondary" onClick={onPrev}><FaChevronLeft /></Button>
              <Button size="sm" variant="outline-secondary" onClick={onNext}><FaChevronRight /></Button>
            </ButtonGroup>
            <span style={{ margin: '0 10px', display: 'inline-flex', alignItems: 'center', gap: '6px' }}>
              Page:
              <Form.Control
                size="sm"
                type="number"
                min={1}
                max={pages}
                value={pageInput}
                onChange={(e) => setPageInput(e.target.value)}
                onBlur={commitPageInput}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault();
                    commitPageInput();
                    e.target.blur();
                  }
                }}
                style={{ width: '4.5em' }}
              />
              of {pages}
            </span>
          </div>
        )}
        <div style={{ flex: 1 }}></div>

        <Button
          size="sm"
          variant="outline-secondary"
          style={{ marginRight: '8px' }}
          onClick={onReload}
          disabled={reloading}
          title="Reload without cache"
          aria-label="Reload without cache"
        ><AiOutlineReload /></Button>

        <Dropdown align="end">
          <Dropdown.Toggle size="sm" variant="secondary">
            <AiOutlineDownload />
          </Dropdown.Toggle>
          <Dropdown.Menu>
            <Dropdown.Item onClick={onDownloadPdf}>Download PDF</Dropdown.Item>
            <Dropdown.Item onClick={onDownloadRmdoc}>Download .rmdoc</Dropdown.Item>
          </Dropdown.Menu>
        </Dropdown>

      </Navbar>


      {file && (
        <div ref={parent} style={{flex: "1 1 auto", minHeight: 0, overflowY: "auto"}}>
		  <Document key={reloadKey} file={downloadUrl} onLoadSuccess={onLoadSuccess} options={options}>
            <div style={{ width, margin: "0 auto" }}>
              <Page pageNumber={page}
                width={width}
                renderAnnotationLayer={false}
                renderTextLayer={false}
              />
            </div>
          </Document>
        </div>
      )}

      {pages > 1 && (
        <div style={{ flexShrink: 0, padding: "10px 4px" }}>
          <Form.Range
            min={1}
            max={pages}
            value={page}
            onChange={(e) => setPage(parseInt(e.target.value, 10))}
            aria-label="Page"
          />
        </div>
      )}
    </div>
  );
}
