import React, { useEffect, useRef, useState } from 'react';
import './TCPanel.css';
import { API_BASE_URL } from '../config';
import { ReactComponent as SlidersIcon } from '../assets/images/icons/sliders.svg';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';

const defaultNetem = {
  qdisc: 'netem',
  delay: '0ms',
  jitter: '0ms',
  loss: '0%',
  duplicate: '0%',
  corrupt: '0%',
  limit: 1000,
};

const defaultTBF = {
  qdisc: 'tbf',
  rate: '1mbit',
  burst: '32Kb',
  latency: '400ms',
};

const validateNetem = (qd) => {
  const errors = [];
  const delayVal = parseInt(qd.delay) || 0;
  const jitterVal = parseInt(qd.jitter) || 0;
  const lossVal = parseFloat(qd.loss) || 0;
  const dupVal = parseFloat(qd.duplicate) || 0;
  const corruptVal = parseFloat(qd.corrupt) || 0;
  const limitVal = parseInt(qd.limit) || 0;

  if (delayVal < 0 || delayVal > 60000) errors.push('Delay must be between 0 and 60000 ms');
  if (jitterVal < 0 || jitterVal > 10000) errors.push('Jitter must be between 0 and 10000 ms');
  if (lossVal < 0 || lossVal > 100) errors.push('Loss must be between 0% and 100%');
  if (dupVal < 0 || dupVal > 100) errors.push('Duplicate must be between 0% and 100%');
  if (corruptVal < 0 || corruptVal > 100) errors.push('Corrupt must be between 0% and 100%');
  if (limitVal < 100 || limitVal > 10000) errors.push('Limit must be between 100 and 10000');

  if (qd.jitter && parseInt(qd.jitter) > 0 && (!qd.delay || parseInt(qd.delay) === 0)) {
    errors.push('Jitter requires a non-zero Delay');
  }
  return errors;
};

const validateTBF = (qd) => {
  const errors = [];
  if (!qd.rate || qd.rate.trim() === '') errors.push('Rate is required (e.g., 10mbit)');
  const lat = parseInt(qd.latency) || 0;
  if (lat < 1 || lat > 5000) errors.push('Latency must be between 1 and 5000 ms');
  if (!qd.burst || qd.burst.trim() === '') errors.push('Burst is required (e.g., 32Kb)');
  return errors;
};

// TCPanel is a floating, draggable traffic-control window for one pod interface,
// a peer of the capture and trace panels. Shaping is universal, so it opens for
// any real node. It reads and writes the qdisc through the same backend as before.
const TCPanel = ({
  namespace,
  pod,
  iface,
  zIndex = 1000,
  minimized = false,
  onClose,
  onMinimize,
  onBringToFront,
}) => {
  const containerRef = useRef(null);
  const isDraggingRef = useRef(false);
  const dragOffsetRef = useRef({ x: 0, y: 0 });
  const positionRef = useRef({ x: 150 + Math.random() * 160, y: 90 + Math.random() * 110 });

  const [qdiscData, setQdiscData] = useState(null);
  const [loadingQdisc, setLoadingQdisc] = useState(false);
  const [qdiscError, setQdiscError] = useState(null);
  const [feedback, setFeedback] = useState(null); // {type:'ok'|'error', text}
  const [isDraft, setIsDraft] = useState(false); // created in the UI, not yet applied
  const [newQdiscType, setNewQdiscType] = useState('netem');

  // Keep the window at its dragged position and stacking order.
  useEffect(() => {
    if (containerRef.current) {
      containerRef.current.style.left = `${positionRef.current.x}px`;
      containerRef.current.style.top = `${positionRef.current.y}px`;
      containerRef.current.style.zIndex = zIndex;
    }
  }, [zIndex]);

  useEffect(() => {
    const onMove = (e) => {
      if (!isDraggingRef.current || !containerRef.current) return;
      const w = containerRef.current.offsetWidth;
      const h = containerRef.current.offsetHeight;
      positionRef.current = {
        x: Math.max(0, Math.min(window.innerWidth - w, e.clientX - dragOffsetRef.current.x)),
        y: Math.max(0, Math.min(window.innerHeight - h, e.clientY - dragOffsetRef.current.y)),
      };
      containerRef.current.style.left = `${positionRef.current.x}px`;
      containerRef.current.style.top = `${positionRef.current.y}px`;
    };
    const onUp = () => {
      isDraggingRef.current = false;
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
    return () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };
  }, []);

  const onHeaderMouseDown = (e) => {
    if (onBringToFront) onBringToFront();
    if (e.target.closest('button') || e.target.closest('input') || e.target.closest('select'))
      return;
    isDraggingRef.current = true;
    dragOffsetRef.current = {
      x: e.clientX - positionRef.current.x,
      y: e.clientY - positionRef.current.y,
    };
    e.preventDefault();
  };

  const fetchQdisc = async () => {
    setLoadingQdisc(true);
    setQdiscError(null);
    try {
      const res = await fetch(`${API_BASE_URL}/pods/tc/${namespace}/${pod}/${iface}`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      setQdiscData(data.tcparams || null);
      setIsDraft(false);
    } catch (err) {
      console.error('❌ Error fetching qdisc:', err);
      setQdiscError('Error fetching qdisc');
    } finally {
      setLoadingQdisc(false);
    }
  };

  useEffect(() => {
    fetchQdisc();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namespace, pod, iface]);

  // Auto-dismiss the feedback banner.
  useEffect(() => {
    if (!feedback) return;
    const t = setTimeout(() => setFeedback(null), 4000);
    return () => clearTimeout(t);
  }, [feedback]);

  const handleCreateQdisc = () => {
    setQdiscData(newQdiscType === 'netem' ? { ...defaultNetem } : { ...defaultTBF });
    setIsDraft(true);
    setFeedback(null);
  };

  const handleCancelDraft = () => {
    setQdiscData(null);
    setIsDraft(false);
    setFeedback(null);
  };

  const handleApplyQdisc = async () => {
    if (!qdiscData) return;

    let errors = [];
    if (qdiscData.qdisc === 'netem') errors = validateNetem(qdiscData);
    else if (qdiscData.qdisc === 'tbf') errors = validateTBF(qdiscData);
    else errors = ['Unsupported qdisc type'];

    if (errors.length > 0) {
      setFeedback({ type: 'error', text: errors.join('. ') });
      return;
    }

    // Send only the editable fields; the read carries read-only ones (handle,
    // parent) that the backend rejects with strict JSON decoding.
    const cleanParams =
      qdiscData.qdisc === 'netem'
        ? {
            qdisc: 'netem',
            delay: qdiscData.delay,
            jitter: qdiscData.jitter,
            loss: qdiscData.loss,
            duplicate: qdiscData.duplicate,
            corrupt: qdiscData.corrupt,
            limit: qdiscData.limit,
          }
        : {
            qdisc: 'tbf',
            rate: qdiscData.rate,
            burst: qdiscData.burst,
            latency: qdiscData.latency,
          };

    const payload = {
      targets: [{ pod, actions: [{ type: 'add_qdisc', iface, tcparams: cleanParams }] }],
    };

    try {
      const res = await fetch(`${API_BASE_URL}/network/configure/${namespace}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setIsDraft(false);
      setFeedback({ type: 'ok', text: `${qdiscData.qdisc} applied to ${iface}` });
      fetchQdisc();
    } catch (err) {
      console.error('❌ Error applying qdisc:', err);
      setFeedback({ type: 'error', text: `Could not apply qdisc: ${err.message}` });
    }
  };

  const handleDeleteQdisc = async () => {
    const payload = { targets: [{ pod, actions: [{ type: 'del_qdisc', iface }] }] };
    try {
      const res = await fetch(`${API_BASE_URL}/network/configure/${namespace}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setQdiscData(null);
      setIsDraft(false);
      setNewQdiscType('netem');
      setFeedback({ type: 'ok', text: `Traffic shaping removed from ${iface}` });
    } catch (err) {
      console.error('❌ Error deleting qdisc:', err);
      setFeedback({ type: 'error', text: `Could not remove qdisc: ${err.message}` });
    }
  };

  const renderNoQdisc = () => (
    <div className="tc-empty">
      <p className="tc-empty-text">
        This interface forwards traffic without shaping (<code>noqueue</code>).
      </p>
      <div className="tc-add">
        <select
          className="tc-select"
          value={newQdiscType}
          onChange={(e) => setNewQdiscType(e.target.value)}
        >
          <option value="netem">netem</option>
          <option value="tbf">tbf</option>
        </select>
        <button className="tc-form-btn primary" onClick={handleCreateQdisc}>
          Add qdisc
        </button>
      </div>
      <p className="tc-hint">
        <strong>netem</strong> emulates delay, jitter and packet loss. <strong>tbf</strong> limits
        bandwidth.
      </p>
    </div>
  );

  const renderActions = () => (
    <div className="tc-actions">
      {isDraft && (
        <button className="tc-form-btn ghost" onClick={handleCancelDraft}>
          Cancel
        </button>
      )}
      <button className="tc-form-btn primary" onClick={handleApplyQdisc}>
        {isDraft ? 'Apply' : 'Update'}
      </button>
      {!isDraft && (
        <button className="tc-form-btn danger" onClick={handleDeleteQdisc}>
          Remove
        </button>
      )}
      <button className="tc-form-btn ghost" title="Reload current settings" onClick={fetchQdisc}>
        Refresh
      </button>
    </div>
  );

  const renderForm = () => {
    if (!qdiscData) return renderNoQdisc();

    if (qdiscData.qdisc === 'netem') {
      return (
        <div className="tc-config">
          <p className="tc-desc">
            Emulates link impairments: latency, jitter and packet loss, duplication or corruption.
          </p>
          <label>
            Delay (ms):
            <input
              type="number"
              min="0"
              max="60000"
              value={parseInt(qdiscData.delay) || 0}
              onChange={(e) => setQdiscData({ ...qdiscData, delay: e.target.value + 'ms' })}
            />
          </label>
          <label>
            Jitter (ms):
            <input
              type="number"
              min="0"
              max="10000"
              value={parseInt(qdiscData.jitter) || 0}
              onChange={(e) => setQdiscData({ ...qdiscData, jitter: e.target.value + 'ms' })}
            />
          </label>
          <label>
            Loss (%):
            <input
              type="number"
              min="0"
              max="100"
              step="0.1"
              value={parseFloat(qdiscData.loss) || 0}
              onChange={(e) => setQdiscData({ ...qdiscData, loss: e.target.value + '%' })}
            />
          </label>
          <label>
            Duplicate (%):
            <input
              type="number"
              min="0"
              max="100"
              step="0.1"
              value={parseFloat(qdiscData.duplicate) || 0}
              onChange={(e) => setQdiscData({ ...qdiscData, duplicate: e.target.value + '%' })}
            />
          </label>
          <label>
            Corrupt (%):
            <input
              type="number"
              min="0"
              max="100"
              step="0.1"
              value={parseFloat(qdiscData.corrupt) || 0}
              onChange={(e) => setQdiscData({ ...qdiscData, corrupt: e.target.value + '%' })}
            />
          </label>
          <label>
            Limit:
            <input
              type="number"
              min="100"
              max="10000"
              value={qdiscData.limit || 0}
              onChange={(e) => setQdiscData({ ...qdiscData, limit: parseInt(e.target.value) })}
            />
          </label>
          {renderActions()}
        </div>
      );
    }

    if (qdiscData.qdisc === 'tbf') {
      return (
        <div className="tc-config">
          <p className="tc-desc">
            Token Bucket Filter: caps the egress bandwidth of this interface.
          </p>
          <label>
            Rate (Mbit):
            <input
              type="number"
              min="1"
              max="100000"
              value={parseInt(qdiscData.rate) || 0}
              onChange={(e) => setQdiscData({ ...qdiscData, rate: e.target.value + 'Mbit' })}
            />
          </label>
          <label>
            Burst (kbit):
            <input
              type="number"
              min="1"
              max="100000"
              value={parseInt(qdiscData.burst) || 0}
              onChange={(e) => setQdiscData({ ...qdiscData, burst: e.target.value + 'kb' })}
            />
          </label>
          <label>
            Latency (ms):
            <input
              type="number"
              min="1"
              max="5000"
              value={parseInt(qdiscData.latency) || 0}
              onChange={(e) => setQdiscData({ ...qdiscData, latency: e.target.value + 'ms' })}
            />
          </label>
          {renderActions()}
        </div>
      );
    }

    return renderNoQdisc();
  };

  const statusLabel = qdiscData
    ? isDraft
      ? `${qdiscData.qdisc} · draft`
      : `${qdiscData.qdisc} · active`
    : 'No shaping';
  const statusClass = qdiscData ? (isDraft ? 'draft' : 'on') : 'off';

  return (
    <div
      ref={containerRef}
      className={`tc-panel${minimized ? ' tc-panel-min' : ''}`}
      onClick={() => {
        if (!minimized && onBringToFront) onBringToFront();
      }}
    >
      <div className="tc-header" onMouseDown={onHeaderMouseDown}>
        <SlidersIcon className="tc-header-icon" />
        <span className="tc-title">
          <span className="tc-title-pod">{pod}</span>
          <span className="tc-title-iface">· {iface}</span>
        </span>
        <span className={`tc-status ${statusClass}`}>{statusLabel}</span>
        <div className="tc-header-btns">
          <button className="tc-btn" onClick={onMinimize} title="Minimize">
            –
          </button>
          <button className="tc-btn tc-btn-close" onClick={onClose} title="Close">
            ✕
          </button>
        </div>
      </div>

      <div className="tc-body">
        {feedback && (
          <div className={`tc-feedback ${feedback.type}`}>
            {feedback.type === 'ok' ? '✓ ' : <WarningIcon className="app-icon" />} {feedback.text}
          </div>
        )}
        {loadingQdisc && <p className="tc-loading">Loading qdisc…</p>}
        {qdiscError && <p className="tc-error">{qdiscError}</p>}
        {!loadingQdisc && renderForm()}
      </div>
    </div>
  );
};

export default TCPanel;
