import React, { useEffect, useMemo, useRef, useState } from 'react';
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

// Settings the panel has no field for (netem rate, reorder, a loss model, a
// child qdisc). They are shown so an Update, which rebuilds the whole qdisc,
// does not drop them unnoticed.
const extrasOf = (tcparams) => {
  const extras = [];
  if (tcparams.qdisc === 'netem' && tcparams.rate) extras.push(`rate ${tcparams.rate}`);
  if (tcparams.reorder) extras.push(`reorder ${tcparams.reorder}`);
  if (tcparams.loss_model === 'gemodel') extras.push('Gilbert-Elliott loss');
  (tcparams.children || []).forEach((c) => extras.push(`child ${c.qdisc}`));
  return extras;
};

const fromApi = (tcparams) => {
  const kind = tcparams?.qdisc;
  const shaped = typeof tcparams?.shaped === 'boolean' ? tcparams.shaped : isShapedQdisc(kind);
  if (!shaped) return null;
  const extras = extrasOf(tcparams);
  if (kind === 'netem') {
    return {
      qdisc: 'netem',
      delay: timeToMs(tcparams.delay),
      jitter: timeToMs(tcparams.jitter),
      loss: percentToNumber(tcparams.loss),
      duplicate: percentToNumber(tcparams.duplicate),
      corrupt: percentToNumber(tcparams.corrupt),
      limit: Number(tcparams.limit) || 1000,
      extras,
    };
  }
  if (kind === 'tbf') {
    return {
      qdisc: 'tbf',
      rate: parseRate(tcparams.rate),
      burst: parseSize(tcparams.burst),
      latency: timeToMs(tcparams.latency),
      extras,
    };
  }
  return { qdisc: kind, extras };
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

// TCPanel is a floating, draggable traffic-control window for one pod, a peer
// of the capture and trace panels. It lists the pod's interfaces with their
// shaping state, read in one call, and edits the one picked. `iface` is the
// interface to select, `focusToken` changes when the panel is opened again
// so the selection follows even if the same interface is asked for.
const TCPanel = ({
  namespace,
  pod,
  iface = null,
  focusToken = 0,
  interfaces = null,
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

  const [states, setStates] = useState(null); // iface -> state from the pod-wide read
  const [selected, setSelected] = useState(iface);
  const [qdiscData, setQdiscData] = useState(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState(null);
  const [feedback, setFeedback] = useState(null); // {type:'ok'|'error', text}
  const [isDraft, setIsDraft] = useState(false); // created in the UI, not yet applied
  const [newQdiscType, setNewQdiscType] = useState('netem');

  // The topology's interfaces of the pod, in link order. Without that list,
  // whatever the pod has except its cluster interface, which cannot be shaped.
  const ifaceList = useMemo(() => {
    if (interfaces && interfaces.length > 0) return [...new Set(interfaces.filter(Boolean))];
    return Object.keys(states || {})
      .filter((name) => name !== 'eth0')
      .sort();
  }, [interfaces, states]);

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

  const fetchStates = async () => {
    setLoading(true);
    setLoadError(null);
    try {
      const res = await fetch(`${API_BASE_URL}/pods/tc/${namespace}/${pod}`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      setStates(data.interfaces || {});
    } catch (err) {
      console.error('❌ Error fetching qdiscs:', err);
      setLoadError('Error reading the traffic control state');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchStates();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namespace, pod]);

  // Follow the interface the caller asked for, every time it asks.
  useEffect(() => {
    if (iface) {
      setSelected(iface);
      setIsDraft(false);
      setFeedback(null);
    }
  }, [iface, focusToken]);

  // Nothing selected yet: take the first interface once the list is known.
  useEffect(() => {
    if (!selected && ifaceList.length > 0) setSelected(ifaceList[0]);
  }, [ifaceList, selected]);

  // The form mirrors the read state of the selected interface, unless the
  // user is in the middle of a draft.
  useEffect(() => {
    if (isDraft) return;
    setQdiscData(states && selected ? fromApi(states[selected]) : null);
  }, [states, selected, isDraft]);

  // Auto-dismiss the feedback banner.
  useEffect(() => {
    if (!feedback) return;
    const t = setTimeout(() => setFeedback(null), feedback.type === 'error' ? 8000 : 4000);
    return () => clearTimeout(t);
  }, [feedback]);

  const selectIface = (name) => {
    setSelected(name);
    setIsDraft(false);
    setFeedback(null);
  };

  const handleCreateQdisc = () => {
    setQdiscData(newQdiscType === 'netem' ? { ...defaultNetem } : { ...defaultTBF });
    setIsDraft(true);
    setFeedback(null);
  };

  const handleCancelDraft = () => {
    setIsDraft(false);
    setFeedback(null);
  };

  const handleApplyQdisc = async () => {
    if (!qdiscData || !selected) return;

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
        iface: selected,
        tcparams: toApi(qdiscData),
      });
      const outcome = await actionOutcome(res);
      // Read the new state before leaving draft mode, so the form switches
      // straight to what was applied instead of flashing the old state.
      await fetchStates();
      setIsDraft(false);
      setFeedback({
        type: 'ok',
        text:
          outcome.status === 'skipped'
            ? `${qdiscData.qdisc} already applied to ${selected} with these settings`
            : `${qdiscData.qdisc} applied to ${selected}`,
      });
    } catch (err) {
      console.error('❌ Error applying qdisc:', err);
      setFeedback({ type: 'error', text: `Could not apply qdisc: ${err.message}` });
    }
  };

  const handleDeleteQdisc = async () => {
    if (!selected) return;
    try {
      const res = await postAction(namespace, pod, { type: 'del_qdisc', iface: selected });
      await actionOutcome(res);
      setIsDraft(false);
      setNewQdiscType('netem');
      setFeedback({ type: 'ok', text: `Traffic shaping removed from ${selected}` });
      fetchStates();
    } catch (err) {
      console.error('❌ Error deleting qdisc:', err);
      setFeedback({ type: 'error', text: `Could not remove qdisc: ${err.message}` });
    }
  };

  const renderIfaces = () => (
    <div className="tc-ifaces">
      {ifaceList.length === 0 && !loading && (
        <span className="tc-ifaces-empty">This node has no interface to shape.</span>
      )}
      {ifaceList.map((name) => {
        const st = states?.[name];
        const missing = !!states && !st;
        const shaped = !!st?.shaped;
        const title = missing
          ? 'not present in the pod'
          : shaped
            ? `${st.qdisc} active`
            : 'no shaping';
        return (
          <button
            key={name}
            type="button"
            className={`tc-iface-chip${selected === name ? ' selected' : ''}${shaped ? ' shaped' : ''}`}
            disabled={missing}
            title={title}
            onClick={() => selectIface(name)}
          >
            {name}
            {shaped && <span className="tc-iface-dot" />}
          </button>
        );
      })}
    </div>
  );

  const renderNoQdisc = () => (
    <div className="tc-empty">
      <p className="tc-empty-text">
        <code>{selected}</code> forwards traffic without shaping (<code>noqueue</code>).
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
        <code>{selected}</code> has a <code>{qdiscData.qdisc}</code> qdisc that was not set from
        here. Remove it to shape the interface from this panel.
      </p>
      <div className="tc-actions">
        <button className="tc-form-btn danger" onClick={handleDeleteQdisc}>
          Remove
        </button>
        <button className="tc-form-btn ghost" title="Reload current settings" onClick={fetchStates}>
          Refresh
        </button>
      </div>
    </div>
  );

  const renderExtras = () =>
    qdiscData?.extras?.length > 0 && (
      <p className="tc-hint">
        Also set outside this panel: {qdiscData.extras.join(', ')}. Update rebuilds the qdisc
        without them.
      </p>
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
      <button className="tc-form-btn ghost" title="Reload current settings" onClick={fetchStates}>
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
    if (!selected) return null;
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
          {renderExtras()}
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
        {renderExtras()}
        {renderActions()}
      </div>
    );
  };

  const shapedCount = Object.values(states || {}).filter((s) => s?.shaped).length;
  const statusLabel = qdiscData
    ? isDraft
      ? `${qdiscData.qdisc} · draft`
      : `${qdiscData.qdisc} · active`
    : shapedCount > 0
      ? `${shapedCount} shaped`
      : 'No shaping';
  const statusClass = qdiscData ? (isDraft ? 'draft' : 'on') : shapedCount > 0 ? 'on' : 'off';

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
          {selected && <span className="tc-title-iface">· {selected}</span>}
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
        {renderIfaces()}
        {feedback && (
          <div className={`tc-feedback ${feedback.type}`}>
            {feedback.type === 'ok' ? '✓ ' : <WarningIcon className="app-icon" />} {feedback.text}
          </div>
        )}
        {loading && !states && <p className="tc-loading">Reading traffic control state…</p>}
        {loadError && <p className="tc-error">{loadError}</p>}
        {states && renderForm()}
      </div>
    </div>
  );
};

export default TCPanel;
