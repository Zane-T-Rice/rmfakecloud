import { useEffect, useRef, useState } from "react";
import { BsArrowDown } from "react-icons/bs";

// How far to pull down, in pixels, to reload
const THRESHOLD = 80;
const MAX_PULL = 130;

// True when something the touch started in is scrolled, then the swipe scrolls it
function scrolledAncestor(el) {
  for (; el && el !== document.body; el = el.parentElement) {
    if (el.scrollTop > 0) {
      return true;
    }
  }
  return false;
}

// Reloads the page when it is pulled down from the top. The browser does not do
// that here, the page itself never scrolls (the parts inside it do).
export default function PullToRefresh() {
  const [pull, setPull] = useState(0);
  const start = useRef(null);
  const pulled = useRef(0);

  useEffect(() => {
    const onStart = (e) => {
      start.current = null;
      if (e.touches.length !== 1) return;
      const t = e.target;
      // sliders and inputs are for dragging
      if (t.closest && t.closest("input, textarea, select")) return;
      if (scrolledAncestor(t)) return;
      start.current = { x: e.touches[0].clientX, y: e.touches[0].clientY };
    };

    const onMove = (e) => {
      const s = start.current;
      if (!s) return;
      if (e.touches.length !== 1) {
        start.current = null;
        pulled.current = 0;
        setPull(0);
        return;
      }
      const dx = e.touches[0].clientX - s.x;
      const dy = e.touches[0].clientY - s.y;
      // up, or mostly sideways: not a pull
      if (dy < 0 || Math.abs(dx) > Math.abs(dy)) {
        start.current = null;
        pulled.current = 0;
        setPull(0);
        return;
      }
      pulled.current = Math.min(dy * 0.5, MAX_PULL);
      setPull(pulled.current);
    };

    const onEnd = () => {
      const done = pulled.current >= THRESHOLD;
      start.current = null;
      pulled.current = 0;
      setPull(0);
      if (done) {
        window.location.reload();
      }
    };

    document.addEventListener("touchstart", onStart, { passive: true });
    document.addEventListener("touchmove", onMove, { passive: true });
    document.addEventListener("touchend", onEnd);
    document.addEventListener("touchcancel", onEnd);
    return () => {
      document.removeEventListener("touchstart", onStart);
      document.removeEventListener("touchmove", onMove);
      document.removeEventListener("touchend", onEnd);
      document.removeEventListener("touchcancel", onEnd);
    };
  }, []);

  if (pull <= 0) return null;
  const ready = pull >= THRESHOLD;
  return (
    <div
      style={{
        position: "fixed",
        top: 0,
        left: 0,
        right: 0,
        display: "flex",
        justifyContent: "center",
        zIndex: 2000,
        pointerEvents: "none",
        transform: `translateY(${pull - 40}px)`,
      }}
    >
      <div
        style={{
          width: 40,
          height: 40,
          borderRadius: "50%",
          background: ready ? "#0d6efd" : "#495057",
          color: "#fff",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          transition: "transform 0.1s, background 0.1s",
          transform: `rotate(${ready ? 180 : 0}deg)`,
        }}
      >
        <BsArrowDown />
      </div>
    </div>
  );
}
