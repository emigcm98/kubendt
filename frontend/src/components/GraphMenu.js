import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import './ContextMenu.css';
import { ReactComponent as ChevronIcon } from '../assets/images/icons/chevron-right.svg';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';

// The context menu of the graph, shared by nodes and links. Entries are
// {label, icon, onClick, danger} or a group with children, shown as a
// submenu to the right of the entry (to the left when it would not fit).
// A group with one visible child collapses into that child, so callers can
// declare groups without counting what applies to the node. An entry with
// {disabled, note} is shown dimmed with a warning and the note as its
// tooltip: the node cannot do it, and the menu says so instead of hiding it.

export const DIVIDER = 'divider';

const MARGIN = 8; // px kept from the viewport edge
const AWAY_MARGIN = 50; // px around the menu before the cursor counts as away
const CLOSE_DELAY = 250; // ms the cursor stays away before the menu closes

const tidy = (items) => {
  const out = [];
  for (const it of items) {
    if (!it) continue;
    if (it === DIVIDER) {
      if (out.length && out[out.length - 1] !== DIVIDER) out.push(DIVIDER);
      continue;
    }
    if (it.children) {
      const kids = it.children.filter(Boolean);
      if (kids.length === 0) continue;
      if (kids.length === 1) {
        out.push({
          icon: it.icon,
          wideIcon: it.wideIcon,
          ...kids[0],
          label: kids[0].alone || kids[0].label,
        });
        continue;
      }
      out.push({ ...it, children: kids });
      continue;
    }
    out.push(it);
  }
  while (out[out.length - 1] === DIVIDER) out.pop();
  return out;
};

// Sizes come from offsetWidth and offsetHeight: the entrance animation scales
// the box, so a bounding rect taken on mount is smaller than the menu.
const clampToViewport = (el, x, y) => {
  let top = y;
  let left = x;
  if (left + el.offsetWidth > window.innerWidth - MARGIN)
    left = window.innerWidth - el.offsetWidth - MARGIN;
  if (top + el.offsetHeight > window.innerHeight - MARGIN)
    top = window.innerHeight - el.offsetHeight - MARGIN;
  return { top: Math.max(MARGIN, top), left: Math.max(MARGIN, left) };
};

const outsideRect = (e, r) =>
  e.clientX < r.left - AWAY_MARGIN ||
  e.clientX > r.right + AWAY_MARGIN ||
  e.clientY < r.top - AWAY_MARGIN ||
  e.clientY > r.bottom + AWAY_MARGIN;

const Icon = ({ entry }) =>
  entry.icon ? (
    <span
      className={`graph-context-menu-icon${entry.wideIcon ? ' graph-context-menu-icon-pcap' : ''}`}
    >
      {entry.icon}
    </span>
  ) : null;

const GraphMenu = ({ x, y, title, items, onClose }) => {
  const menuRef = useRef(null);
  const subRef = useRef(null);
  const leaveTimerRef = useRef(null);
  const closeSubTimerRef = useRef(null);
  const [pos, setPos] = useState({ top: y, left: x });
  const [open, setOpen] = useState(null); // index of the entry whose submenu shows
  const [subPos, setSubPos] = useState(null);
  const entries = tidy(items);
  const openEntry = open !== null && entries[open]?.children ? entries[open] : null;

  useEffect(() => {
    const inMenu = (target) =>
      (menuRef.current && menuRef.current.contains(target)) ||
      (subRef.current && subRef.current.contains(target));
    const handleClick = (e) => {
      if (!inMenu(e.target)) onClose();
    };
    const handleKey = (e) => {
      if (e.key === 'Escape') onClose();
    };
    const handleMouseMove = (e) => {
      if (!menuRef.current) return;
      const outside =
        outsideRect(e, menuRef.current.getBoundingClientRect()) &&
        (!subRef.current || outsideRect(e, subRef.current.getBoundingClientRect()));
      if (outside) {
        if (!leaveTimerRef.current) {
          leaveTimerRef.current = setTimeout(() => {
            leaveTimerRef.current = null;
            onClose();
          }, CLOSE_DELAY);
        }
      } else if (leaveTimerRef.current) {
        clearTimeout(leaveTimerRef.current);
        leaveTimerRef.current = null;
      }
    };
    document.addEventListener('mousedown', handleClick);
    document.addEventListener('keydown', handleKey);
    document.addEventListener('mousemove', handleMouseMove);
    return () => {
      document.removeEventListener('mousedown', handleClick);
      document.removeEventListener('keydown', handleKey);
      document.removeEventListener('mousemove', handleMouseMove);
      if (leaveTimerRef.current) clearTimeout(leaveTimerRef.current);
      if (closeSubTimerRef.current) clearTimeout(closeSubTimerRef.current);
    };
  }, [onClose]);

  useLayoutEffect(() => {
    if (!menuRef.current) return;
    setPos(clampToViewport(menuRef.current, x, y));
  }, [x, y]);

  // The submenu sits flush with the right edge of its entry and flips to the
  // left edge when the viewport ends there.
  useLayoutEffect(() => {
    if (!openEntry || !menuRef.current || !subRef.current) return;
    const item = menuRef.current.querySelector(`[data-index="${open}"]`);
    if (!item) return;
    const root = menuRef.current.getBoundingClientRect();
    const width = subRef.current.offsetWidth;
    const height = subRef.current.offsetHeight;
    let left = root.right - 2;
    if (left + width > window.innerWidth - MARGIN) left = root.left - width + 2;
    let top = item.getBoundingClientRect().top - 4;
    if (top + height > window.innerHeight - MARGIN) top = window.innerHeight - MARGIN - height;
    const next = { top: Math.max(MARGIN, top), left: Math.max(MARGIN, left) };
    setSubPos((prev) => (prev && prev.top === next.top && prev.left === next.left ? prev : next));
    // Entries are rebuilt on every render, so only the open index can drive this.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const run = (entry) => {
    entry.onClick();
    onClose();
  };

  const keepSub = () => {
    if (closeSubTimerRef.current) {
      clearTimeout(closeSubTimerRef.current);
      closeSubTimerRef.current = null;
    }
  };

  // Hovering a plain entry closes the submenu after a short grace, so a
  // diagonal move from the entry into its submenu does not lose it.
  const showSub = (i) => {
    keepSub();
    if (i === null) {
      closeSubTimerRef.current = setTimeout(() => {
        closeSubTimerRef.current = null;
        setOpen(null);
      }, 150);
      return;
    }
    if (i !== open) setSubPos(null);
    setOpen(i);
  };

  const renderEntry = (entry, i, inSub) => {
    const parent = Boolean(entry.children);
    const cls = ['graph-context-menu-item'];
    if (entry.danger) cls.push('graph-context-menu-item-danger');
    if (parent) cls.push('graph-context-menu-item-parent');
    if (entry.disabled) cls.push('graph-context-menu-item-disabled');
    if (!inSub && open === i && parent) cls.push('graph-context-menu-item-open');
    const onClick = () => {
      if (entry.disabled) return;
      if (parent) showSub(open === i ? null : i);
      else run(entry);
    };
    return (
      <div
        key={i}
        data-index={inSub ? undefined : i}
        className={cls.join(' ')}
        title={entry.disabled ? entry.note : undefined}
        onMouseEnter={inSub ? undefined : () => showSub(parent ? i : null)}
        onClick={onClick}
      >
        <Icon entry={entry} />
        <span className="graph-context-menu-label">{entry.label}</span>
        {parent && <ChevronIcon className="graph-context-menu-chevron" />}
        {entry.disabled && <WarningIcon className="graph-context-menu-warn" />}
      </div>
    );
  };

  return (
    <>
      <div
        ref={menuRef}
        className="graph-context-menu"
        style={{ position: 'fixed', top: `${pos.top}px`, left: `${pos.left}px` }}
      >
        <div className="graph-context-menu-header">
          <span className="graph-context-menu-title">{title}</span>
        </div>
        <div className="graph-context-menu-divider" />
        {entries.map((entry, i) =>
          entry === DIVIDER ? (
            <div key={i} className="graph-context-menu-divider" />
          ) : (
            renderEntry(entry, i, false)
          )
        )}
      </div>
      {openEntry &&
        createPortal(
          <div
            ref={subRef}
            className="graph-context-menu graph-context-menu-sub"
            onMouseEnter={keepSub}
            style={{
              position: 'fixed',
              top: `${subPos ? subPos.top : 0}px`,
              left: `${subPos ? subPos.left : 0}px`,
              visibility: subPos ? 'visible' : 'hidden',
            }}
          >
            {openEntry.children.map((child, j) => renderEntry(child, j, true))}
          </div>,
          document.body
        )}
    </>
  );
};

export default GraphMenu;
