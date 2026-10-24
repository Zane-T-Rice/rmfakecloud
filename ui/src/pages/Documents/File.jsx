import { useState, useEffect, useRef, useMemo } from "react";
import { Button, ButtonGroup, Dropdown, Form } from "react-bootstrap";
import Navbar from 'react-bootstrap/Navbar';
import { FaChevronRight, FaChevronLeft, } from "react-icons/fa6";
import { AiOutlineDownload } from "react-icons/ai";
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


// the page is shown at this width on any window size, smaller when the viewer is
const MAX_PAGE_WIDTH = 800;

export default function FileViewer({ file, onSelect }) {
  const { data } = file;

  const downloadUrl = `${constants.ROOT_URL}/documents/${file.id}`;

  const [page, setPage] = useState(1);
  const [pages, setPages] = useState(1);
  const [width, setWidth] = useState(100);
  const onLoadSuccess = (pdf) => {
    setPage(1);
    setPages(pdf.numPages);
  };
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
      setWidth(Math.min(el.clientWidth, MAX_PAGE_WIDTH));
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
    <>
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
        <div ref={parent} style={{height: "95%", overflowY: "auto"}}>
		  <Document file={downloadUrl} onLoadSuccess={onLoadSuccess} options={options}>
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
    </>
  );
}
