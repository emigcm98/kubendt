import React, { useEffect } from 'react';
import { ReactComponent as SuccessIcon } from '../assets/images/icons/success.svg';
import { ReactComponent as ErrorIcon } from '../assets/images/icons/error.svg';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import { ReactComponent as QuestionIcon } from '../assets/images/icons/question.svg';
import { ReactComponent as LoadingIcon } from '../assets/images/icons/loading.svg';
import { ReactComponent as InfoIcon } from '../assets/images/icons/info.svg';
import './Dialog.css';
import './AlertModal.css';

const ICONS = {
  success: [SuccessIcon, 'success'],
  error: [ErrorIcon, 'danger'],
  warning: [WarningIcon, 'warning'],
  confirm: [QuestionIcon, ''],
  info: [InfoIcon, ''],
};

const AlertModal = ({
  isOpen,
  type,
  title,
  message,
  onConfirm,
  onCancel,
  confirmText = 'Accept',
  cancelText = 'Cancel',
  extraContent,
  // Destructive confirm: red confirm button + an irreversible-action note.
  danger = false,
  dangerNote = 'This action cannot be undone.',
  // While an async action runs: spinner in the confirm button, both disabled.
  loading = false,
  loadingText = 'Working…',
}) => {
  // Close on Esc (never while an action is running).
  useEffect(() => {
    if (!isOpen) return undefined;
    const onKey = (e) => {
      if (e.key === 'Escape' && !loading && onCancel) onCancel();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [isOpen, loading, onCancel]);

  if (!isOpen) return null;

  const messageLines =
    typeof message === 'string'
      ? message
          .split('\n')
          .map((line) => line.trim())
          .filter(Boolean)
      : [];

  const [Icon, tone] = ICONS[type] || ICONS.info;

  return (
    <div className="dlg-overlay dlg-overlay-top" onClick={loading ? undefined : onCancel}>
      <div
        className="dlg alert-modal"
        role="alertdialog"
        aria-modal="true"
        aria-label={typeof title === 'string' ? title : undefined}
        onClick={(e) => e.stopPropagation()}
      >
        <div className={`dlg-head${tone ? ` dlg-head-${tone}` : ''}`}>
          {type === 'loading' ? (
            <LoadingIcon className="app-icon dlg-spin" aria-hidden="true" />
          ) : (
            <Icon className="app-icon" aria-hidden="true" />
          )}
          <h2 className="dlg-title">{title}</h2>
        </div>

        {(message || danger || extraContent) && (
          <div className="dlg-body">
            {messageLines.length <= 1
              ? message && <p className="dlg-text">{message}</p>
              : (() => (
                  <>
                    <p className="dlg-text">{messageLines[0]}</p>
                    {messageLines.slice(1).map((line, idx) => (
                      <p key={`${line}-${idx}`} className="dlg-text alert-modal-line">
                        {line}
                      </p>
                    ))}
                  </>
                ))()}
            {danger && <p className="alert-modal-danger-note">{dangerNote}</p>}
            {extraContent && <div className="alert-modal-extra">{extraContent}</div>}
          </div>
        )}

        {type !== 'loading' && (
          <div className="dlg-foot">
            {onCancel && (type === 'confirm' || danger) && (
              <button className="dlg-btn dlg-btn-quiet" onClick={onCancel} disabled={loading}>
                {cancelText}
              </button>
            )}
            <button
              className={`dlg-btn ${danger ? 'dlg-btn-danger' : 'dlg-btn-primary'}`}
              onClick={onConfirm}
              disabled={loading}
            >
              {loading ? (
                <>
                  <LoadingIcon className="app-icon dlg-spin" aria-hidden="true" /> {loadingText}
                </>
              ) : (
                confirmText
              )}
            </button>
          </div>
        )}
      </div>
    </div>
  );
};

export default AlertModal;
