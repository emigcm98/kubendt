import React, { useState } from 'react';
import Modal from 'react-modal';
import './ErrorModal.css';

Modal.setAppElement('#root');

// message: short human intro (optional). details: raw error shown in a copyable
// monospace box (optional). note: calm follow-up line below the box (optional,
// e.g. a rollback notice). Callers passing only `message` keep the old layout.
const ErrorModal = ({ isOpen, message, details, note, onClose, title = 'Error' }) => {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(details || '');
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable, ignore */
    }
  };

  return (
    <Modal
      isOpen={isOpen}
      onRequestClose={onClose}
      className="error-modal"
      overlayClassName="error-modal-overlay"
    >
      <div className="error-modal-header">
        <div className="error-modal-icon">❌</div>
        <h2>{title}</h2>
        <button className="error-modal-close-btn" onClick={onClose}>
          ✖
        </button>
      </div>

      <div className="error-modal-body">
        {message && <p>{message}</p>}
        {details && (
          <div className="error-modal-details">
            <div className="error-modal-details-bar">
              <button
                className="error-modal-copy-btn"
                onClick={handleCopy}
                title="Copy error to clipboard"
              >
                {copied ? '✓ Copied' : 'Copy'}
              </button>
            </div>
            <pre className="error-modal-details-text">{details}</pre>
          </div>
        )}
        {note && <p className="error-modal-note">{note}</p>}
      </div>

      <div className="error-modal-footer">
        <button className="error-modal-btn" onClick={onClose}>
          ✓ Entendido
        </button>
      </div>
    </Modal>
  );
};

export default ErrorModal;
