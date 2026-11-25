import React, { useState } from 'react';
import Modal from 'react-modal';
import './Dialog.css';
import './ErrorModal.css';
import { ReactComponent as ErrorIcon } from '../assets/images/icons/error.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { ReactComponent as CopyIcon } from '../assets/images/icons/copy.svg';

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
      className="dlg dlg-mid error-modal"
      overlayClassName="dlg-overlay dlg-overlay-top"
      contentLabel={title}
    >
      <div className="dlg-head dlg-head-danger">
        <ErrorIcon className="app-icon" aria-hidden="true" />
        <h2 className="dlg-title">{title}</h2>
        <button className="dlg-close" onClick={onClose} title="Close">
          <CloseIcon className="app-icon" />
        </button>
      </div>

      <div className="dlg-body">
        {message && <p className="dlg-text">{message}</p>}
        {details && (
          <div className="error-modal-details">
            <pre className="error-modal-details-text">{details}</pre>
            <button
              className="error-modal-copy"
              onClick={handleCopy}
              title="Copy the error to the clipboard"
            >
              <CopyIcon className="app-icon" aria-hidden="true" /> {copied ? 'Copied' : 'Copy'}
            </button>
          </div>
        )}
        {note && <p className="dlg-text dlg-muted error-modal-note">{note}</p>}
      </div>

      <div className="dlg-foot">
        <button className="dlg-btn dlg-btn-primary" onClick={onClose}>
          Close
        </button>
      </div>
    </Modal>
  );
};

export default ErrorModal;
