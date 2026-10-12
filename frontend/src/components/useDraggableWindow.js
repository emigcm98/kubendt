import { useEffect, useRef } from 'react';

// Drag a floating window by its header, clamped to the viewport so the header
// can never leave the screen. The window positions itself with absolute
// left/top on the container. Returns the mousedown handler for the header;
// clicks on elements matching ignore (buttons) do not start a drag.
export default function useDraggableWindow({ containerRef, onBringToFront, ignore = 'button' }) {
  const draggingRef = useRef(false);
  const offsetRef = useRef({ x: 0, y: 0 });
  const positionRef = useRef({ x: 120 + Math.random() * 160, y: 90 + Math.random() * 120 });

  useEffect(() => {
    const el = containerRef.current;
    if (el) {
      el.style.left = `${positionRef.current.x}px`;
      el.style.top = `${positionRef.current.y}px`;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const onMove = (e) => {
      const el = containerRef.current;
      if (!draggingRef.current || !el) return;
      const maxX = Math.max(0, window.innerWidth - el.offsetWidth);
      const maxY = Math.max(0, window.innerHeight - el.offsetHeight);
      positionRef.current = {
        x: Math.max(0, Math.min(maxX, e.clientX - offsetRef.current.x)),
        y: Math.max(0, Math.min(maxY, e.clientY - offsetRef.current.y)),
      };
      el.style.left = `${positionRef.current.x}px`;
      el.style.top = `${positionRef.current.y}px`;
    };
    const onUp = () => {
      draggingRef.current = false;
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
    return () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };
  }, [containerRef]);

  return (e) => {
    if (onBringToFront) onBringToFront();
    if (ignore && e.target.closest(ignore)) return;
    draggingRef.current = true;
    offsetRef.current = {
      x: e.clientX - positionRef.current.x,
      y: e.clientY - positionRef.current.y,
    };
    e.preventDefault();
  };
}
