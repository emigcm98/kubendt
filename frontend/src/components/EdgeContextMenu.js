import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import './ContextMenu.css';
import { ReactComponent as PcapIcon } from '../assets/images/pcap.svg';

const EdgeContextMenu = ({
  x,
  y,
  edge,
  sourceLabel,
  targetLabel,
  onClose,
  onOpenInfoPanel,
  onDelete,
  onCaptureSource,
  onCaptureTarget,
}) => {
  const menuRef = useRef(null);
  const leaveTimerRef = useRef(null);

  useEffect(() => {
    const AWAY_MARGIN = 50;
    const CLOSE_DELAY = 250;

    const handleClick = (e) => {
      if (menuRef.current && !menuRef.current.contains(e.target)) {
        onClose();
      }
    };
    const handleKey = (e) => {
      if (e.key === 'Escape') onClose();
    };
    const handleMouseMove = (e) => {
      if (!menuRef.current) return;
      const r = menuRef.current.getBoundingClientRect();
      const outside =
        e.clientX < r.left - AWAY_MARGIN ||
        e.clientX > r.right + AWAY_MARGIN ||
        e.clientY < r.top - AWAY_MARGIN ||
        e.clientY > r.bottom + AWAY_MARGIN;

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
    };
  }, [onClose]);

  const [pos, setPos] = useState({ top: y, left: x });

  useLayoutEffect(() => {
    if (!menuRef.current) return;
    const rect = menuRef.current.getBoundingClientRect();
    const MARGIN = 8;
    let top = y;
    let left = x;
    if (left + rect.width > window.innerWidth - MARGIN)
      left = window.innerWidth - rect.width - MARGIN;
    if (top + rect.height > window.innerHeight - MARGIN)
      top = window.innerHeight - rect.height - MARGIN;
    if (left < MARGIN) left = MARGIN;
    if (top < MARGIN) top = MARGIN;
    setPos({ top, left });
  }, [x, y]);

  if (!edge) return null;

  const handleOptionClick = (action) => {
    action();
    onClose();
  };

  const linkLabel = `${sourceLabel || edge.source}:${edge.data?.localIntf || '?'} ↔ ${targetLabel || edge.target}:${edge.data?.peerIntf || '?'}`;

  return (
    <div
      ref={menuRef}
      className="graph-context-menu"
      style={{ position: 'fixed', top: `${pos.top}px`, left: `${pos.left}px` }}
    >
      <div className="graph-context-menu-header">
        <span className="graph-context-menu-title">{linkLabel}</span>
      </div>
      <div className="graph-context-menu-divider" />

      {onOpenInfoPanel && (
        <div className="graph-context-menu-item" onClick={() => handleOptionClick(onOpenInfoPanel)}>
          <span className="graph-context-menu-icon">ℹ️</span>
          <span className="graph-context-menu-label">Open info panel</span>
        </div>
      )}

      {onCaptureSource && (
        <div className="graph-context-menu-item" onClick={() => handleOptionClick(onCaptureSource)}>
          <span className="graph-context-menu-icon graph-context-menu-icon-pcap">
            <PcapIcon />
          </span>
          <span className="graph-context-menu-label">
            Capture on {sourceLabel || edge.source}:{edge.data?.localIntf || '?'}
          </span>
        </div>
      )}

      {onCaptureTarget && (
        <div className="graph-context-menu-item" onClick={() => handleOptionClick(onCaptureTarget)}>
          <span className="graph-context-menu-icon graph-context-menu-icon-pcap">
            <PcapIcon />
          </span>
          <span className="graph-context-menu-label">
            Capture on {targetLabel || edge.target}:{edge.data?.peerIntf || '?'}
          </span>
        </div>
      )}

      {onDelete && (
        <div
          className="graph-context-menu-item graph-context-menu-item-danger"
          onClick={() => handleOptionClick(onDelete)}
        >
          <span className="graph-context-menu-icon">🗑️</span>
          <span className="graph-context-menu-label">Delete link</span>
        </div>
      )}
    </div>
  );
};

export default EdgeContextMenu;
