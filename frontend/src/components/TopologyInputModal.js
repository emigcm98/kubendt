import React, { useState, useEffect, useRef, useCallback } from 'react';
import Modal from 'react-modal';
import CodeMirror from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import { ReactComponent as ErrorIcon } from '../assets/images/icons/error.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import './TopologyInputModal.css';

const TopologyInputModal = ({
  isOpen,
  title,
  description,
  warningText,
  confirmLabel = 'Submit',
  confirmVariant = 'primary', // 'primary' | 'warning'
  placeholder,
  sampleSnippet,
  semanticValidator, // optional: (parsedJson) => void; throws Error on invalid payload
  onClose,
  onSubmit,
}) => {
  const [text, setText] = useState('');
  const [parsed, setParsed] = useState(null);
  const [parseError, setParseError] = useState(null);
  const [semanticError, setSemanticError] = useState(null);
  const [fileName, setFileName] = useState(null);
  const [isDragging, setIsDragging] = useState(false);
  // Modal-frame drag offset (relative to its centered position). Persists for
  // the lifetime of the modal so users can shove it aside, edit the JSON,
  // pan the graph behind it, and the modal stays put.
  const [pos, setPos] = useState({ x: 0, y: 0 });
  const dragRef = useRef({ dragging: false, startX: 0, startY: 0, origX: 0, origY: 0 });
  const modalRef = useRef(null);
  const fileInputRef = useRef(null);

  useEffect(() => {
    if (!isOpen) {
      setText('');
      setParsed(null);
      setParseError(null);
      setSemanticError(null);
      setFileName(null);
      setIsDragging(false);
      setPos({ x: 0, y: 0 });
    }
  }, [isOpen]);

  // Global mousemove/mouseup so a drag keeps tracking even when the cursor
  // leaves the header area or the modal itself. The new position is clamped
  // so the modal stays fully contained within the viewport, dragging
  // beyond an edge stops at the edge instead of letting the modal slip
  // off-screen.
  useEffect(() => {
    const onMove = (e) => {
      if (!dragRef.current.dragging) return;
      const rawX = dragRef.current.origX + (e.clientX - dragRef.current.startX);
      const rawY = dragRef.current.origY + (e.clientY - dragRef.current.startY);
      const modal = modalRef.current;
      if (modal) {
        const w = modal.offsetWidth;
        const h = modal.offsetHeight;
        // When pos = (0,0) the modal is centered at (vw/2, vh/2). For
        // the modal to remain fully inside the viewport its edges must
        // satisfy:
        //   left  = vw/2 + pos.x - w/2 >= 0
        //   right = vw/2 + pos.x + w/2 <= vw
        // which gives pos.x ∈ [-(vw-w)/2, (vw-w)/2]. If the modal is
        // already wider than the viewport, lock movement on that axis.
        const limX = Math.max(0, (window.innerWidth - w) / 2);
        const limY = Math.max(0, (window.innerHeight - h) / 2);
        setPos({
          x: Math.max(-limX, Math.min(limX, rawX)),
          y: Math.max(-limY, Math.min(limY, rawY)),
        });
      } else {
        setPos({ x: rawX, y: rawY });
      }
    };
    const onUp = () => {
      dragRef.current.dragging = false;
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
  }, []);

  const handleHeaderMouseDown = (e) => {
    if (e.button !== 0) return;
    // Don't initiate a drag when the user is going for the close button.
    if (e.target.closest && e.target.closest('.modal-close-btn')) return;
    dragRef.current = {
      dragging: true,
      startX: e.clientX,
      startY: e.clientY,
      origX: pos.x,
      origY: pos.y,
    };
    e.preventDefault();
  };

  const runSemanticValidation = useCallback(
    (obj) => {
      if (!semanticValidator || obj === null || obj === undefined) {
        setSemanticError(null);
        return;
      }
      try {
        semanticValidator(obj);
        setSemanticError(null);
      } catch (err) {
        setSemanticError(err.message || String(err));
      }
    },
    [semanticValidator]
  );

  const handleChange = (newText) => {
    setText(newText);
    if (!newText || !newText.trim()) {
      setParsed(null);
      setParseError(null);
      setSemanticError(null);
      return;
    }
    try {
      const obj = JSON.parse(newText);
      setParsed(obj);
      setParseError(null);
      runSemanticValidation(obj);
    } catch (err) {
      setParsed(null);
      setParseError(err.message);
      setSemanticError(null);
    }
  };

  const readFileAsText = (file) => {
    setFileName(file.name);
    const reader = new FileReader();
    reader.onload = (ev) => {
      handleChange(ev.target.result || '');
    };
    reader.readAsText(file);
  };

  const handleFile = (e) => {
    const file = e.target.files?.[0];
    if (!file) return;
    readFileAsText(file);
    e.target.value = '';
  };

  const isSubmittable = parsed !== null && parseError === null && semanticError === null;

  const handleSubmit = () => {
    if (!isSubmittable) return;
    onSubmit(parsed);
  };

  const handleClear = () => {
    setText('');
    setParsed(null);
    setParseError(null);
    setSemanticError(null);
    setFileName(null);
  };

  const handleInsertSample = () => {
    if (sampleSnippet) {
      handleChange(sampleSnippet);
    }
  };

  // Ctrl/Cmd+Enter submits when the payload is fully valid.
  const handleEditorKeyDown = (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault();
      e.stopPropagation();
      handleSubmit();
    }
  };

  // Drag-and-drop a .json file onto the editor wrapper.
  const handleDrop = (e) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(false);
    const file = e.dataTransfer?.files?.[0];
    if (!file) return;
    if (!/\.json$/i.test(file.name) && file.type !== 'application/json') {
      // Best-effort: still try to read it as text; user pasted a .txt with JSON inside?
      // Keep behaviour conservative, only accept JSON-like.
      return;
    }
    readFileAsText(file);
  };

  if (!isOpen) return null;

  const confirmClass = `modal-btn modal-btn-confirm${confirmVariant === 'warning' ? ' modal-btn-warning' : ''}`;
  const submitDisabledReason = !text
    ? 'Provide a JSON to continue'
    : parseError
      ? 'Fix the JSON syntax to continue'
      : semanticError
        ? 'Fix the payload errors to continue'
        : undefined;

  return (
    <Modal
      isOpen={isOpen}
      onRequestClose={onClose}
      className="topology-input-modal"
      overlayClassName="topology-input-overlay"
      shouldCloseOnOverlayClick={false}
      shouldCloseOnEsc
      style={{ content: { transform: `translate(${pos.x}px, ${pos.y}px)` } }}
      contentRef={(node) => {
        modalRef.current = node;
      }}
    >
      <div
        className="modal-header topology-input-drag-handle"
        onMouseDown={handleHeaderMouseDown}
        title="Drag to move"
      >
        <h2>{title}</h2>
        <button className="modal-close-btn" onClick={onClose} title="Close (Esc)">
          <CloseIcon className="app-icon" />
        </button>
      </div>

      <div className="modal-body">
        {description && <p className="tinput-description">{description}</p>}
        {warningText && (
          <div className="tinput-warning">
            <WarningIcon className="app-icon" /> {warningText}
          </div>
        )}

        <div className="tinput-toolbar">
          <input
            ref={fileInputRef}
            type="file"
            accept=".json"
            style={{ display: 'none' }}
            onChange={handleFile}
          />
          <button
            type="button"
            className="tinput-toolbar-btn"
            onClick={() => fileInputRef.current?.click()}
            title="Load JSON from a file into the editor"
          >
            Choose file
          </button>
          {fileName && (
            <span className="tinput-filename" title={fileName}>
              {fileName}
            </span>
          )}
          <div className="tinput-toolbar-spacer" />
          {sampleSnippet && (
            <button
              type="button"
              className="tinput-toolbar-btn"
              onClick={handleInsertSample}
              title="Insert a minimal example payload"
            >
              Sample
            </button>
          )}
          <button
            type="button"
            className="tinput-toolbar-btn"
            onClick={handleClear}
            disabled={!text}
            title="Clear editor"
          >
            Clear
          </button>
        </div>

        <div
          className={`tinput-editor-wrapper${isDragging ? ' is-dragging' : ''}`}
          onKeyDown={handleEditorKeyDown}
          onDragOver={(e) => {
            e.preventDefault();
            e.stopPropagation();
            setIsDragging(true);
          }}
          onDragLeave={(e) => {
            e.preventDefault();
            e.stopPropagation();
            setIsDragging(false);
          }}
          onDrop={handleDrop}
        >
          <CodeMirror
            value={text}
            placeholder={placeholder || ''}
            extensions={[json()]}
            onChange={handleChange}
            basicSetup={{
              lineNumbers: true,
              foldGutter: true,
              autocompletion: true,
              tabSize: 2,
            }}
          />
          {isDragging && <div className="tinput-drop-hint">Drop a .json file here</div>}
        </div>

        <div className="tinput-status">
          {parseError ? (
            <div className="tinput-status-error" title={parseError}>
              <ErrorIcon className="app-icon" /> Invalid JSON: {parseError}
            </div>
          ) : semanticError ? (
            <div className="tinput-status-error" title={semanticError}>
              <ErrorIcon className="app-icon" /> {semanticError}
            </div>
          ) : parsed ? (
            <div className="tinput-status-ok">✓ Valid payload. Ctrl+Enter to submit</div>
          ) : (
            <div className="tinput-status-empty">
              Paste your JSON, drop a file, or load it from your computer…
            </div>
          )}
        </div>
      </div>

      <div className="modal-footer">
        <button className="modal-btn modal-btn-cancel" onClick={onClose}>
          Cancel
        </button>
        <button
          className={confirmClass}
          onClick={handleSubmit}
          disabled={!isSubmittable}
          title={submitDisabledReason}
        >
          {confirmLabel}
        </button>
      </div>
    </Modal>
  );
};

export default TopologyInputModal;
