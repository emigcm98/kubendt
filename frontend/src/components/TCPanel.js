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
//
// netem is the composite impairment: delay with jitter and its distribution,
// loss (random or bursty), duplicate, corrupt, reorder and a rate, all in one
// qdisc. tbf stays as a plain token bucket.
const DISTRIBUTIONS = ['uniform', 'normal', 'pareto', 'paretonormal'];

const defaultNetem = {
  qdisc: 'netem',
  delay: 0,
  jitter: 0,
  distribution: 'uniform',
  lossModel: 'random',
  loss: 0,
  gemodel: { p: 1, r: 10, h: 100, k: 0 },
  duplicate: 0,
  corrupt: 0,
  reorder: 0,
  reorderCorrelation: 0,
  rate: { value: 0, unit: 'mbit' },
  limit: 1000,
  extras: [],
};

const defaultTBF = {
  qdisc: 'tbf',
  rate: { value: 1, unit: 'mbit' },
  burst: { value: 32, unit: 'kbit' },
  latency: 400,
  extras: [],
};

// Starting points for a netem draft. Delay is one-way, per direction.
const PRESETS = [
  { id: 'custom', label: 'Custom', values: {} },
  {
    id: 'wan',
    label: 'WAN, 50 ms',
    values: { delay: 50, jitter: 5, rate: { value: 100, unit: 'mbit' } },
  },
  {
    id: 'satellite',
    label: 'Satellite, 300 ms',
    values: { delay: 300, jitter: 10, loss: 0.5, rate: { value: 10, unit: 'mbit' } },
  },
  {
    id: '3g',
    label: '3G, 2 Mbit/s',
    values: { delay: 100, jitter: 20, loss: 1, rate: { value: 2, unit: 'mbit' } },
  },
  {
    id: 'wifi',
    label: 'Lossy wifi',
    values: { delay: 10, jitter: 15, distribution: 'normal', loss: 5 },
  },
];

// Settings the panel has no field for (correlations on delay and loss, a
// child qdisc). They are shown so an Update, which rebuilds the whole qdisc,
// does not drop them unnoticed.
const extrasOf = (tcparams) => {
  const extras = [];
  if (tcparams.delay_correlation) extras.push(`delay correlation ${tcparams.delay_correlation}`);
  if (tcparams.loss_correlation) extras.push(`loss correlation ${tcparams.loss_correlation}`);
  (tcparams.children || []).forEach((c) => extras.push(`child ${c.qdisc}`));
  return extras;
};

// tc does not report the jitter distribution, so it comes from the persisted
// action for the interface (the intent), when there is one.
const fromApi = (tcparams, intent) => {
  const kind = tcparams?.qdisc;
  const shaped = typeof tcparams?.shaped === 'boolean' ? tcparams.shaped : isShapedQdisc(kind);
  if (!shaped) return null;
  const extras = extrasOf(tcparams);
  if (kind === 'netem') {
    const gm = tcparams.gemodel || {};
    return {
      qdisc: 'netem',
      delay: timeToMs(tcparams.delay),
      jitter: timeToMs(tcparams.jitter),
      distribution: DISTRIBUTIONS.includes(intent?.distribution) ? intent.distribution : 'uniform',
      lossModel: tcparams.loss_model === 'gemodel' ? 'gemodel' : 'random',
      loss: percentToNumber(tcparams.loss),
      gemodel: {
        p: percentToNumber(gm.p ?? '1%'),
        r: percentToNumber(gm.r ?? '10%'),
        h: percentToNumber(gm['1-h'] ?? '100%'),
        k: percentToNumber(gm['1-k'] ?? '0%'),
      },
      duplicate: percentToNumber(tcparams.duplicate),
      corrupt: percentToNumber(tcparams.corrupt),
      reorder: percentToNumber(tcparams.reorder),
      reorderCorrelation: percentToNumber(tcparams.reorder_correlation),
      rate: tcparams.rate ? parseRate(tcparams.rate) : { value: 0, unit: 'mbit' },
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

const toApi = (qd) => {
  if (qd.qdisc === 'tbf') {
    return {
      qdisc: 'tbf',
      rate: formatRate(qd.rate),
      burst: formatSize(qd.burst),
      latency: `${qd.latency}ms`,
    };
  }
  const out = {
    qdisc: 'netem',
    delay: `${qd.delay}ms`,
    jitter: `${qd.jitter}ms`,
    duplicate: `${qd.duplicate}%`,
    corrupt: `${qd.corrupt}%`,
    reorder: `${qd.reorder}%`,
    limit: qd.limit,
  };
  if (qd.jitter > 0 && qd.distribution !== 'uniform') out.distribution = qd.distribution;
  if (qd.lossModel === 'gemodel') {
    out.gemodel = {
      p: `${qd.gemodel.p}%`,
      r: `${qd.gemodel.r}%`,
      '1-h': `${qd.gemodel.h}%`,
      '1-k': `${qd.gemodel.k}%`,
    };
  } else {
    out.loss = `${qd.loss}%`;
  }
  if (qd.reorder > 0) out.reorder_correlation = `${qd.reorderCorrelation}%`;
  if (qd.rate.value > 0) out.rate = formatRate(qd.rate);
  return out;
};

const pct = (v, label, errors) => {
  if (v < 0 || v > 100) errors.push(`${label} must be between 0% and 100%`);
};

const validateNetem = (qd) => {
  const errors = [];
  if (qd.delay < 0 || qd.delay > 60000) errors.push('Delay must be between 0 and 60000 ms');
  if (qd.jitter < 0 || qd.jitter > 10000) errors.push('Jitter must be between 0 and 10000 ms');
  if (qd.jitter > 0 && qd.delay <= 0) errors.push('Jitter requires a non-zero Delay');
  if (qd.distribution !== 'uniform' && qd.jitter <= 0)
    errors.push('A jitter distribution needs a non-zero Jitter');
  if (qd.lossModel === 'gemodel') {
    pct(qd.gemodel.p, 'p', errors);
    pct(qd.gemodel.r, 'r', errors);
    pct(qd.gemodel.h, 'Bad-state loss', errors);
    pct(qd.gemodel.k, 'Good-state loss', errors);
  } else {
    pct(qd.loss, 'Loss', errors);
  }
  pct(qd.duplicate, 'Duplicate', errors);
  pct(qd.corrupt, 'Corrupt', errors);
  pct(qd.reorder, 'Reorder', errors);
  pct(qd.reorderCorrelation, 'Reorder correlation', errors);
  if (qd.reorder > 0 && qd.delay <= 0) errors.push('Reorder requires a non-zero Delay');
  if (qd.rate.value < 0) errors.push('Rate cannot be negative');
  if (qd.limit < 1 || qd.limit > 1000000) errors.push('Limit must be between 1 and 1000000');
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
  const [intents, setIntents] = useState({}); // iface -> persisted add_qdisc tcparams
  const [selected, setSelected] = useState(iface);
  const [qdiscData, setQdiscData] = useState(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState(null);
  const [feedback, setFeedback] = useState(null); // {type:'ok'|'error', text}
  const [isDraft, setIsDraft] = useState(false); // created in the UI, not yet applied
  const [newQdiscType, setNewQdiscType] = useState('netem');
  const [preset, setPreset] = useState('custom');

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

  // The live state of every interface, and the persisted actions for what tc
  // cannot report (the jitter distribution).
  const fetchStates = async () => {
    setLoading(true);
    setLoadError(null);
    try {
      const [stRes, histRes] = await Promise.all([
        fetch(`${API_BASE_URL}/pods/tc/${namespace}/${pod}`),
        fetch(`${API_BASE_URL}/drivers/history/${namespace}/${pod}`),
      ]);
      if (!stRes.ok) throw new Error(`HTTP ${stRes.status}`);
      const data = await stRes.json();
      setStates(data.interfaces || {});
      if (histRes.ok) {
        const hist = await histRes.json();
        const byIface = {};
        (hist.operations || []).forEach((op) => {
          if (op.action_type === 'add_qdisc' && op.action?.iface) {
            byIface[op.action.iface] = op.action.tcparams || {};
          }
        });
        setIntents(byIface);
      }
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
    setQdiscData(states && selected ? fromApi(states[selected], intents[selected]) : null);
  }, [states, intents, selected, isDraft]);

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
    if (newQdiscType === 'tbf') {
      setQdiscData({ ...defaultTBF });
    } else {
      const values = PRESETS.find((p) => p.id === preset)?.values || {};
      setQdiscData({ ...defaultNetem, ...values });
    }
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

  const set = (patch) => setQdiscData((prev) => ({ ...prev, ...patch }));

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
        {newQdiscType === 'netem' && (
          <select
            className="tc-select"
            value={preset}
            onChange={(e) => setPreset(e.target.value)}
            title="Starting values"
          >
            {PRESETS.map((p) => (
              <option key={p.id} value={p.id}>
                {p.label}
              </option>
            ))}
          </select>
        )}
        <button className="tc-form-btn primary" onClick={handleCreateQdisc}>
          Add
        </button>
      </div>
      <p className="tc-hint">
        <strong>netem</strong> combines delay, jitter, loss, reordering and a rate in one place.{' '}
        <strong>tbf</strong> is a plain token bucket.
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

  const numberField = (label, key, min, max, step = 1, obj = null) => {
    const value = obj ? qdiscData[obj][key] : qdiscData[key];
    const onChange = (e) => {
      const n = Number(e.target.value);
      if (obj) set({ [obj]: { ...qdiscData[obj], [key]: n } });
      else set({ [key]: n });
    };
    return (
      <label>
        {label}:
        <input type="number" min={min} max={max} step={step} value={value} onChange={onChange} />
      </label>
    );
  };

  const renderNetemForm = () => (
    <div className="tc-config">
      <div className="tc-section">Delay</div>
      {numberField('Delay (ms)', 'delay', 0, 60000)}
      {numberField('Jitter (ms)', 'jitter', 0, 10000)}
      <label>
        Jitter distribution:
        <select
          value={qdiscData.distribution}
          onChange={(e) => set({ distribution: e.target.value })}
          disabled={!(qdiscData.jitter > 0)}
        >
          {DISTRIBUTIONS.map((d) => (
            <option key={d} value={d}>
              {d}
            </option>
          ))}
        </select>
      </label>

      <div className="tc-section">Loss</div>
      <label>
        Model:
        <select value={qdiscData.lossModel} onChange={(e) => set({ lossModel: e.target.value })}>
          <option value="random">random</option>
          <option value="gemodel">bursty (Gilbert-Elliott)</option>
        </select>
      </label>
      {qdiscData.lossModel === 'gemodel' ? (
        <>
          {numberField('p, enter bad state (%)', 'p', 0, 100, 0.1, 'gemodel')}
          {numberField('r, leave bad state (%)', 'r', 0, 100, 0.1, 'gemodel')}
          {numberField('Loss in bad state (%)', 'h', 0, 100, 0.1, 'gemodel')}
          {numberField('Loss in good state (%)', 'k', 0, 100, 0.1, 'gemodel')}
        </>
      ) : (
        numberField('Loss (%)', 'loss', 0, 100, 0.1)
      )}

      <div className="tc-section">Packets</div>
      {numberField('Duplicate (%)', 'duplicate', 0, 100, 0.1)}
      {numberField('Corrupt (%)', 'corrupt', 0, 100, 0.1)}
      {numberField('Reorder (%)', 'reorder', 0, 100, 0.1)}
      {qdiscData.reorder > 0 &&
        numberField('Reorder correlation (%)', 'reorderCorrelation', 0, 100, 1)}

      <div className="tc-section">Bandwidth</div>
      <UnitField
        label="Rate (0 = unlimited)"
        value={qdiscData.rate}
        units={RATE_UNITS}
        onChange={(rate) => set({ rate })}
      />
      {numberField('Queue limit (packets)', 'limit', 1, 1000000)}
      {renderExtras()}
      {renderActions()}
    </div>
  );

  const renderTBFForm = () => (
    <div className="tc-config">
      <p className="tc-desc">Token Bucket Filter: caps the egress bandwidth of this interface.</p>
      <UnitField
        label="Rate"
        value={qdiscData.rate}
        units={RATE_UNITS}
        onChange={(rate) => set({ rate })}
      />
      <UnitField
        label="Burst"
        value={qdiscData.burst}
        units={SIZE_UNITS}
        onChange={(burst) => set({ burst })}
      />
      {numberField('Latency (ms)', 'latency', 1, 5000)}
      {renderExtras()}
      {renderActions()}
    </div>
  );

  const renderForm = () => {
    if (!selected) return null;
    if (!qdiscData) return renderNoQdisc();
    if (!isManagedQdisc(qdiscData.qdisc)) return renderForeignQdisc();
    return qdiscData.qdisc === 'netem' ? renderNetemForm() : renderTBFForm();
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
