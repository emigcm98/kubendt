import React, { useState, useRef, useEffect, useLayoutEffect } from 'react';
import ReactDOM from 'react-dom';
import './Tooltip.css';

// Portal-based tooltip with fixed positioning calculated from the
// trigger's bounding rect on hover/focus. Escapes any overflow or
// stacking-context trap that a CSS-pseudo tooltip would fall into.
function Tooltip({ children, text, position = 'bottom', delay = 150 }) {
  const triggerRef = useRef(null);
  const tooltipRef = useRef(null);
  const showTimerRef = useRef(null);
  const [show, setShow] = useState(false);
  const [coords, setCoords] = useState({ top: 0, left: 0 });

  const compute = () => {
    const el = triggerRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const gap = 8;
    if (position === 'top') {
      setCoords({ top: rect.top - gap, left: rect.left + rect.width / 2 });
    } else {
      setCoords({ top: rect.bottom + gap, left: rect.left + rect.width / 2 });
    }
  };

  // Clamp the tooltip horizontally so it never bleeds past the viewport
  // edges. Runs once per show after the tooltip is mounted and measurable.
  useLayoutEffect(() => {
    if (!show || !tooltipRef.current) return;
    const ttRect = tooltipRef.current.getBoundingClientRect();
    const margin = 8;
    let delta = 0;
    if (ttRect.left < margin) {
      delta = margin - ttRect.left;
    } else if (ttRect.right > window.innerWidth - margin) {
      delta = window.innerWidth - margin - ttRect.right;
    }
    if (Math.abs(delta) > 0.5) {
      setCoords((prev) => ({ ...prev, left: prev.left + delta }));
    }
  }, [show, coords.left, coords.top]);

  const scheduleShow = () => {
    clearTimeout(showTimerRef.current);
    showTimerRef.current = setTimeout(() => {
      compute();
      setShow(true);
    }, delay);
  };

  const hide = () => {
    clearTimeout(showTimerRef.current);
    setShow(false);
  };

  useEffect(() => {
    if (!show) return;
    const onScroll = () => hide();
    window.addEventListener('scroll', onScroll, true);
    window.addEventListener('resize', onScroll);
    return () => {
      window.removeEventListener('scroll', onScroll, true);
      window.removeEventListener('resize', onScroll);
    };
  }, [show]);

  useEffect(() => () => clearTimeout(showTimerRef.current), []);

  if (!text) return children;

  const child = React.Children.only(children);
  const wrapped = React.cloneElement(child, {
    ref: triggerRef,
    onMouseEnter: (e) => {
      scheduleShow();
      child.props.onMouseEnter?.(e);
    },
    onMouseLeave: (e) => {
      hide();
      child.props.onMouseLeave?.(e);
    },
    onFocus: (e) => {
      scheduleShow();
      child.props.onFocus?.(e);
    },
    onBlur: (e) => {
      hide();
      child.props.onBlur?.(e);
    },
    onClick: (e) => {
      hide();
      child.props.onClick?.(e);
    },
  });

  return (
    <>
      {wrapped}
      {show &&
        ReactDOM.createPortal(
          <div
            ref={tooltipRef}
            className={`kbn-tooltip kbn-tooltip-${position}`}
            style={{ top: `${coords.top}px`, left: `${coords.left}px` }}
            role="tooltip"
          >
            {text}
          </div>,
          document.body
        )}
    </>
  );
}

export default Tooltip;
