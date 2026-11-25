import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import './HoverCard.css';

const OPEN_DELAY = 250;
const CLOSE_DELAY = 150;
const GAP = 6;
const MARGIN = 8;

// A card that opens on hovering (or focusing, or clicking) its trigger, or
// only on a click with openOn="click", as a menu does. It is drawn on the
// body with fixed coordinates, so a scrolling list cannot clip it, and it
// closes when anything scrolls since it would no longer line up.
// `children` gets a close function and is rendered only while open, so its
// work (a fetch, say) starts on demand.
const HoverCard = ({
  trigger,
  children,
  align = 'start',
  className = '',
  label,
  openOn = 'hover',
}) => {
  const hover = openOn === 'hover';
  const anchor = useRef(null);
  const card = useRef(null);
  const timer = useRef(null);
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState(null);

  const later = (fn, ms) => {
    clearTimeout(timer.current);
    timer.current = setTimeout(fn, ms);
  };
  const show = () => later(() => setOpen(true), OPEN_DELAY);
  const hide = () => later(() => setOpen(false), CLOSE_DELAY);
  const close = useCallback(() => {
    clearTimeout(timer.current);
    setOpen(false);
  }, []);

  useEffect(() => () => clearTimeout(timer.current), []);

  useEffect(() => {
    if (!open) return undefined;
    const onKey = (e) => e.key === 'Escape' && close();
    const onDown = (e) => {
      if (anchor.current?.contains(e.target) || card.current?.contains(e.target)) return;
      close();
    };
    window.addEventListener('scroll', close, true);
    window.addEventListener('resize', close);
    window.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onDown);
    return () => {
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('resize', close);
      window.removeEventListener('keydown', onKey);
      document.removeEventListener('mousedown', onDown);
    };
  }, [open, close]);

  // Below the trigger when it fits, above otherwise, and kept on screen;
  // placed again when its content grows, as a fetch lands.
  useLayoutEffect(() => {
    if (!open || !anchor.current || !card.current) {
      setPos(null);
      return undefined;
    }
    const place = () => {
      if (!anchor.current || !card.current) return;
      const a = anchor.current.getBoundingClientRect();
      const c = card.current.getBoundingClientRect();
      const vw = window.innerWidth;
      const vh = window.innerHeight;
      let top = a.bottom + GAP;
      if (top + c.height > vh - MARGIN && a.top - GAP - c.height > MARGIN) {
        top = a.top - GAP - c.height;
      }
      let left = align === 'end' ? a.right - c.width : a.left;
      left = Math.max(MARGIN, Math.min(left, vw - c.width - MARGIN));
      setPos({ top, left });
    };
    place();
    if (typeof ResizeObserver === 'undefined') return undefined;
    const ro = new ResizeObserver(place);
    ro.observe(card.current);
    return () => ro.disconnect();
  }, [open, align]);

  return (
    <>
      <span
        ref={anchor}
        className="hover-card-trigger"
        onMouseEnter={hover ? show : undefined}
        onMouseLeave={hover ? hide : undefined}
        onFocus={hover ? show : undefined}
        onBlur={hover ? hide : undefined}
        onClick={(e) => {
          e.stopPropagation();
          clearTimeout(timer.current);
          setOpen((o) => !o);
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            e.stopPropagation();
            setOpen((o) => !o);
          }
        }}
        role="button"
        tabIndex={0}
        aria-label={label}
        aria-expanded={open}
      >
        {trigger}
      </span>
      {open &&
        createPortal(
          <div
            ref={card}
            className={`hover-card ${className}`}
            style={
              pos ? { top: pos.top, left: pos.left } : { top: 0, left: 0, visibility: 'hidden' }
            }
            onMouseEnter={hover ? () => clearTimeout(timer.current) : undefined}
            onMouseLeave={hover ? hide : undefined}
            onClick={(e) => e.stopPropagation()}
          >
            {children(close)}
          </div>,
          document.body
        )}
    </>
  );
};

export default HoverCard;
