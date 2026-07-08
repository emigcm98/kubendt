import React from 'react';
import './AlertModal.css';

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
}) => {
  if (!isOpen) return null;

  const messageLines =
    typeof message === 'string'
      ? message
          .split('\n')
          .map((line) => line.trim())
          .filter(Boolean)
      : [];

  const getIcon = () => {
    switch (type) {
      case 'success':
        return '✅';
      case 'error':
        return '❌';
      case 'warning':
        return '⚠️';
      case 'confirm':
        return '❓';
      case 'loading':
        return '⏳';
      default:
        return 'ℹ️';
    }
  };

  return (
    <div className="alert-modal-overlay" onClick={onCancel}>
      <div className="alert-modal-container" onClick={(e) => e.stopPropagation()}>
        <div className="alert-modal-header">
          <div className={`alert-modal-icon alert-modal-icon-${type}`}>
            {type === 'loading' ? <div className="spinner"></div> : <span>{getIcon()}</span>}
          </div>
          <div className="alert-modal-content">
            <h2 className="alert-modal-title">{title}</h2>
          </div>
        </div>

        {message && (
          <div className="alert-modal-body">
            {messageLines.length <= 1 ? (
              <p className="alert-modal-message">{message}</p>
            ) : (
              <>
                <p className="alert-modal-message">{messageLines[0]}</p>
                {messageLines.slice(1).map((line, idx) => (
                  <p
                    key={`${line}-${idx}`}
                    className="alert-modal-message alert-modal-message-secondary"
                  >
                    {line}
                  </p>
                ))}
              </>
            )}
            {extraContent && <div className="alert-modal-extra">{extraContent}</div>}
          </div>
        )}

        {type !== 'loading' && (
          <div className="alert-modal-actions">
            {onCancel && type === 'confirm' && (
              <button className="alert-modal-btn alert-modal-btn-cancel" onClick={onCancel}>
                {cancelText}
              </button>
            )}
            <button
              className={`alert-modal-btn alert-modal-btn-${type === 'confirm' ? 'confirm' : 'primary'}`}
              onClick={onConfirm}
            >
              {confirmText}
            </button>
          </div>
        )}
      </div>
    </div>
  );
};

export default AlertModal;
