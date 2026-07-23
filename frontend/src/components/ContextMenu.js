import React, { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import './ContextMenu.css';
import { ReactComponent as TraceIcon } from '../assets/images/icons/trace-icon.svg';
import { ReactComponent as TerminalIcon } from '../assets/images/icons/terminal.svg';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as InfoIcon } from '../assets/images/icons/info.svg';

const ContextMenu = ({
  x,
  y,
  node,
  onClose,
  onOpenShell,
  onRestartPod,
  onOpenInfoPanel,
  onStartTrace,
  onDelete,
}) => {
  const menuRef = useRef(null);
  const leaveTimerRef = useRef(null);
  const isExternal = node?.type === 'external';
  const isSerialCapablePod = useMemo(() => {
    if (!node?.fullInfo) return false;
    return Boolean(
      node.fullInfo.runtime === 'qemu' ||
      node.fullInfo.serialShell ||
      node.fullInfo.shellMode === 'serial'
    );
  }, [node]);

  const isFRRPod = useMemo(() => {
    return node?.fullInfo?.driver === 'FRRRouterDriver';
  }, [node]);

  useEffect(() => {
    const AWAY_MARGIN = 50; // px around the menu before considering the cursor "away"
    const CLOSE_DELAY = 250; // ms after the cursor stays outside

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

  if (!node) return null;

  const handleOptionClick = (action) => {
    action();
    onClose();
  };

  return (
    <div
      ref={menuRef}
      className="graph-context-menu"
      style={{
        position: 'fixed',
        top: `${pos.top}px`,
        left: `${pos.left}px`,
      }}
    >
      <div className="graph-context-menu-header">
        <span className="graph-context-menu-title">{node.label}</span>
      </div>
      <div className="graph-context-menu-divider" />

      {onOpenInfoPanel && (
        <div className="graph-context-menu-item" onClick={() => handleOptionClick(onOpenInfoPanel)}>
          <InfoIcon className="graph-context-menu-icon" />
          <span className="graph-context-menu-label">Open info panel</span>
        </div>
      )}

      {!isExternal && onOpenShell && (
        <div
          className="graph-context-menu-item"
          onClick={() => handleOptionClick(() => onOpenShell('sh'))}
        >
          <TerminalIcon className="graph-context-menu-icon" />
          <span className="graph-context-menu-label">Open pod shell (sh)</span>
        </div>
      )}

      {!isExternal && isSerialCapablePod && (
        <div
          className="graph-context-menu-item"
          onClick={() => handleOptionClick(() => onOpenShell('serial'))}
        >
          <TerminalIcon className="graph-context-menu-icon" />
          <span className="graph-context-menu-label">Open serial shell (attach)</span>
        </div>
      )}

      {!isExternal && isFRRPod && (
        <div
          className="graph-context-menu-item"
          onClick={() => handleOptionClick(() => onOpenShell('vtysh'))}
        >
          <TerminalIcon className="graph-context-menu-icon" />
          <span className="graph-context-menu-label">Open vtysh console</span>
        </div>
      )}

      {!isExternal && onStartTrace && node?.fullInfo?.l3capable && (
        <div className="graph-context-menu-item" onClick={() => handleOptionClick(onStartTrace)}>
          <TraceIcon className="graph-context-menu-icon" />
          <span className="graph-context-menu-label">Traceroute from here</span>
        </div>
      )}

      {!isExternal && onRestartPod && (
        <div className="graph-context-menu-item" onClick={() => handleOptionClick(onRestartPod)}>
          <RefreshIcon className="graph-context-menu-icon" />
          <span className="graph-context-menu-label">Restart pod</span>
        </div>
      )}

      {onDelete && (
        <div
          className="graph-context-menu-item graph-context-menu-item-danger"
          onClick={() => handleOptionClick(onDelete)}
        >
          <TrashIcon className="graph-context-menu-icon" />
          <span className="graph-context-menu-label">
            {isExternal ? 'Delete external network' : 'Delete pod'}
          </span>
        </div>
      )}
    </div>
  );
};

export default ContextMenu;
