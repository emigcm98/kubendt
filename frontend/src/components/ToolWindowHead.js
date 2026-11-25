import React from 'react';
import { ReactComponent as MinimizeIcon } from '../assets/images/icons/minimize.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import './ToolWindow.css';

// The header every tool window shares: what it is, on which pod and
// interface, its state, then its own buttons and minimize and close. The
// window drags by it, its buttons do not.
export default function ToolWindowHead({
  icon: Icon,
  title,
  context,
  status,
  statusTitle,
  tone = 'idle',
  onMouseDown,
  onMinimize,
  onClose,
  closeTitle = 'Close',
  children,
}) {
  return (
    <div className="tw-head" onMouseDown={onMouseDown}>
      {Icon && <Icon className="tw-head-icon" aria-hidden="true" />}
      <span className="tw-title">{title}</span>
      {context && (
        <span className="tw-ctx" title={context}>
          {context}
        </span>
      )}
      {status && (
        <span className={`tw-chip tw-chip-${tone}`} title={statusTitle}>
          {status}
        </span>
      )}
      <span className="tw-space" />
      {children}
      {onMinimize && (
        <button type="button" className="tw-icon-btn" onClick={onMinimize} title="Minimize">
          <MinimizeIcon className="app-icon" />
        </button>
      )}
      <button type="button" className="tw-icon-btn" onClick={onClose} title={closeTitle}>
        <CloseIcon className="app-icon" />
      </button>
    </div>
  );
}
