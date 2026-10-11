import React, { useEffect, useRef, useState } from 'react';
import './TCPanel.css';
import { API_BASE_URL } from '../config';
import { ReactComponent as SlidersIcon } from '../assets/images/icons/sliders.svg';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import {
  RATE_UNITS,
  SIZE_UNITS,
  formatRate,
  formatSize,
  isManagedQdisc,
  isShapedQdisc,
  parseRate,
  parseSize,
  percentToNumber,
  timeToMs,
} from './tcUnits';

// The panel keeps numbers and units apart. The API speaks tc strings ("50ms",
// "500Kbit", "4Kb"), so a read-back is parsed on the way in and formatted on
// the way out.
const defaultNetem = {
  qdisc: 'netem',
  delay: 0,
  jitter: 0,
  loss: 0,
  duplicate: 0,
  corrupt: 0,
  limit: 1000,
};

const defaultTBF = {
  qdisc: 'tbf',
  rate: { value: 1, unit: 'mbit' },
  burst: { value: 32, unit: 'kbit' },
  latency: 400,
};

const fromApi = (tcparams) => {
  const kind = tcparams?.qdisc;
  if (!isShapedQdisc(kind)) return null;
  if (kind === 'netem') {
    return {
      qdisc: 'netem',
      delay: timeToMs(tcparams.delay),
      jitter: timeToMs(tcparams.jitter),
      loss: percentToNumber(tcparams.loss),
      duplicate: percentToNumber(tcparams.duplicate),
      corrupt: percentToNumber(tcparams.corrupt),
      limit: Number(tcparams.limit) || 1000,
    };
  }
  if (kind === 'tbf') {
    return {
      qdisc: 'tbf',
      rate: parseRate(tcparams.rate),
      burst: parseSize(tcparams.burst),
      latency: timeToMs(tcparams.latency),
    };
  }
  return { qdisc: kind };
};

const toApi = (qd) =>
  qd.qdisc === 'netem'
    ? {
        qdisc: 'netem',
        delay: `${qd.delay}ms`,
        jitter: `${qd.jitter}ms`,
        loss: `${qd.loss}%`,
        duplicate: `${qd.duplicate}%`,
        corrupt: `${qd.corrupt}%`,
        limit: qd.limit,
      }
    : {
        qdisc: 'tbf',
        rate: formatRate(qd.rate),
        burst: formatSize(qd.burst),
        latency: `${qd.latency}ms`,
      };

const validateNetem = (qd) => {
  const errors = [];
  if (qd.delay < 0 || qd.delay > 60000) errors.push('Delay must be between 0 and 60000 ms');
  if (qd.jitter < 0 || qd.jitter > 10000) errors.push('Jitter must be between 0 and 10000 ms');
  for (const [key, label] of [
    ['loss', 'Loss'],
    ['duplicate', 'Duplicate'],
    ['corrupt', 'Corrupt'],
  ]) {
    if (qd[key] < 0 || qd[key] > 100) errors.push(`${label} must be between 0% and 100%`);
  }
  if (qd.limit < 1 || qd.limit > 1000000) errors.push('Limit must be between 1 and 1000000');
  if (qd.jitter > 0 && qd.delay <= 0) errors.push('Jitter requires a non-zero Delay');
  return errors;
};

const validateTBF = (qd) => {
  const errors = [];
  if (!(qd.rate.value > 0)) errors.push('Rate must be greater than zero');
  if (!(qd.burst.value > 0)) errors.push('Burst must be greater than zero');
  if (qd.latency < 1 || qd.latency > 5000) errors.push('Latency must be between 1 and 5000 ms');
  return errors;
};

// Outcome of a configure call carrying one action. The endpoint answers 200
// even when the action failed, so the status inside is what counts.
const actionOutcome = async (res) => {
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const data = await res.json();
  const result = data?.action_results?.[0];
  if (!result) throw new Error('the backend returned no result');
  if (result.status === 'failed') {
    throw new Error(result.error || data?.errors?.[0]?.error || 'the backend rejected the action');
  }
  return result;
};

const postAction = (namespace, pod, action) =>
  fetch(`${API_BASE_URL}/network/configure/${namespace}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ targets: [{ pod, actions: [action] }] }),
  });

const UnitField = ({ label, value, units, onChange }) => (
  <label>
    {label}:
    <span className="tc-unit">
      <input
        type="number"
        min="0"
        step="any"
        value={value.value}
        onChange={(e) => onChange({ ...value, value: Number(e.target.value) })}
      />
      <select value={value.unit} onChange={(e) => onChange({ ...value, unit: e.target.value })}>
        {units.map((u) => (
          <option key={u} value={u}>
            {u}
          </option>
        ))}
      </select>
    </span>
  </label>
);

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
      setQdiscData(fromApi(data.tcparams));
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
    const t = setTimeout(() => setFeedback(null), feedback.type === 'error' ? 8000 : 4000);
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

    try {
      const res = await postAction(namespace, pod, {
        type: 'add_qdisc',
        iface,
        tcparams: toApi(qdiscData),
      });
      const outcome = await actionOutcome(res);
      setIsDraft(false);
      setFeedback({
        type: 'ok',
        text:
          outcome.status === 'skipped'
            ? `${qdiscData.qdisc} already applied to ${iface} with these settings`
            : `${qdiscData.qdisc} applied to ${iface}`,
      });
      fetchQdisc();
    } catch (err) {
      console.error('❌ Error applying qdisc:', err);
      setFeedback({ type: 'error', text: `Could not apply qdisc: ${err.message}` });
    }
  };

  const handleDeleteQdisc = async () => {
    try {
      const res = await postAction(namespace, pod, { type: 'del_qdisc', iface });
      await actionOutcome(res);
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

  // A qdisc KubeNDT does not edit (set by hand, or by something else in the
  // pod). Removing it is the way back to a panel-managed interface.
  const renderForeignQdisc = () => (
    <div className="tc-empty">
      <p className="tc-empty-text">
        This interface has a <code>{qdiscData.qdisc}</code> qdisc that was not set from here. Remove
        it to shape the interface from this panel.
      </p>
      <div className="tc-actions">
        <button className="tc-form-btn danger" onClick={handleDeleteQdisc}>
          Remove
        </button>
        <button className="tc-form-btn ghost" title="Reload current settings" onClick={fetchQdisc}>
          Refresh
        </button>
      </div>
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

  const numberField = (label, key, min, max, step = 1) => (
    <label>
      {label}:
      <input
        type="number"
        min={min}
        max={max}
        step={step}
        value={qdiscData[key]}
        onChange={(e) => setQdiscData({ ...qdiscData, [key]: Number(e.target.value) })}
      />
    </label>
  );

  const renderForm = () => {
    if (!qdiscData) return renderNoQdisc();
    if (!isManagedQdisc(qdiscData.qdisc)) return renderForeignQdisc();

    if (qdiscData.qdisc === 'netem') {
      return (
        <div className="tc-config">
          <p className="tc-desc">
            Emulates link impairments: latency, jitter and packet loss, duplication or corruption.
          </p>
          {numberField('Delay (ms)', 'delay', 0, 60000)}
          {numberField('Jitter (ms)', 'jitter', 0, 10000)}
          {numberField('Loss (%)', 'loss', 0, 100, 0.1)}
          {numberField('Duplicate (%)', 'duplicate', 0, 100, 0.1)}
          {numberField('Corrupt (%)', 'corrupt', 0, 100, 0.1)}
          {numberField('Limit (packets)', 'limit', 1, 1000000)}
          {renderActions()}
        </div>
      );
    }

    return (
      <div className="tc-config">
        <p className="tc-desc">Token Bucket Filter: caps the egress bandwidth of this interface.</p>
        <UnitField
          label="Rate"
          value={qdiscData.rate}
          units={RATE_UNITS}
          onChange={(rate) => setQdiscData({ ...qdiscData, rate })}
        />
        <UnitField
          label="Burst"
          value={qdiscData.burst}
          units={SIZE_UNITS}
          onChange={(burst) => setQdiscData({ ...qdiscData, burst })}
        />
        {numberField('Latency (ms)', 'latency', 1, 5000)}
        {renderActions()}
      </div>
    );
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
