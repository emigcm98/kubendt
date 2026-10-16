import React, { useEffect, useMemo, useRef, useState } from 'react';
import './IperfPanel.css';
import { API_BASE_URL, WS_BASE_URL } from '../config';
import useDraggableWindow from './useDraggableWindow';
import { ReactComponent as GaugeIcon } from '../assets/images/icons/gauge.svg';
import { ReactComponent as DownloadIcon } from '../assets/images/icons/download.svg';
import { ReactComponent as MinusIcon } from '../assets/images/icons/minus.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { ReactComponent as ChevronDownIcon } from '../assets/images/icons/chevron-down.svg';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';

// Throughput test window: an iperf3 run from the source pod toward an
// address, streamed by /iperf/ws. The backend starts the server on the node
// that owns the address unless the lab already runs one there. One bar per
// second while it runs, the end totals below. The window keeps one size from
// open to done: the chart and the totals are laid out before anything arrives.

const STATUS_LABEL = {
  idle: 'ready',
  connecting: 'connecting…',
  starting: 'starting toolbox…',
  running: 'running…',
  done: 'done',
  lost: 'disconnected',
  error: 'error',
};

export const DURATION_RANGE = [1, 120];
export const STREAMS_RANGE = [1, 8];

export const formatBps = (bps) => {
  if (!(bps > 0)) return '0 bit/s';
  if (bps >= 1e9) return `${(bps / 1e9).toFixed(2)} Gbit/s`;
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(1)} Mbit/s`;
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(0)} kbit/s`;
  return `${Math.round(bps)} bit/s`;
};

export const formatBytes = (b) => {
  if (!(b > 0)) return '0 B';
  if (b >= 1e9) return `${(b / 1e9).toFixed(2)} GB`;
  if (b >= 1e6) return `${(b / 1e6).toFixed(1)} MB`;
  if (b >= 1e3) return `${(b / 1e3).toFixed(0)} kB`;
  return `${b} B`;
};

// iperf3 -b: a number of bits per second with an optional k, M or G.
export const BANDWIDTH_RE = /^[0-9]+(\.[0-9]+)?[kKmMgG]?$/;

const inRange = (text, [min, max]) => {
  const n = parseInt(String(text).trim(), 10);
  return !Number.isNaN(n) && String(n) === String(text).trim() && n >= min && n <= max;
};

// The first thing wrong with the form, or null. Numeric fields are edited as
// text, so a value can be cleared and retyped; what is out of range is said in
// red and Run waits.
export const validate = ({ duration, parallel, bandwidth, serverMode, port }) => {
  if (!inRange(duration, DURATION_RANGE))
    return `duration must be between ${DURATION_RANGE[0]} and ${DURATION_RANGE[1]} seconds`;
  if (!inRange(parallel, STREAMS_RANGE))
    return `streams must be between ${STREAMS_RANGE[0]} and ${STREAMS_RANGE[1]}`;
  if (bandwidth.trim() && !BANDWIDTH_RE.test(bandwidth.trim()))
    return 'rate must be a number of bit/s with an optional k, M or G, like 20M';
  if (serverMode === 'none' && port.trim() && !inRange(port, [1, 65535]))
    return 'port must be between 1 and 65535';
  return null;
};

// One bar per interval over the whole duration, so the chart fills in as the
// run goes. Percent widths keep it right whatever the panel's width; the axis
// labels live in a column of their own so they never sit on a bar.
const CHART_H = 110;
const CHART_TOP = 6;
const CHART_BOTTOM = 14;

const Chart = ({ intervals, slots, proto, running, empty }) => {
  const plotH = CHART_H - CHART_TOP - CHART_BOTTOM;
  const max = Math.max(1, ...intervals.map((iv) => iv.bitsPerSecond || 0));
  const n = Math.max(slots, intervals.length, 1);
  const slot = 100 / n;
  const retx = proto === 'tcp' && intervals.some((iv) => iv.retransmits > 0);
  const last = intervals[intervals.length - 1];
  const totalBytes = intervals.reduce((acc, iv) => acc + (iv.bytes || 0), 0);
  const totalSeconds = last ? last.end : 0;
  const avg = totalSeconds > 0 ? (totalBytes * 8) / totalSeconds : 0;
  return (
    <div className="iperf-chart-wrap">
      <div className="iperf-chart-head">
        <span>{last ? `second ${intervals.length} of ${n}` : ''}</span>
        <span className="iperf-chart-rate">
          {last
            ? `${running ? 'now' : 'last'} ${formatBps(last.bitsPerSecond)} · avg ${formatBps(avg)}`
            : ''}
        </span>
      </div>
      <div className="iperf-chart-row">
        <div className="iperf-chart-axis" style={{ height: CHART_H }}>
          {last &&
            [1, 0.5].map((f) => (
              <span key={f} style={{ top: CHART_TOP + plotH * (1 - f) }}>
                {formatBps(max * f)}
              </span>
            ))}
        </div>
        {last ? (
          <svg
            className="iperf-chart"
            width="100%"
            height={CHART_H}
            role="img"
            aria-label="throughput per second"
          >
            {[0.5, 1].map((f) => {
              const y = CHART_TOP + plotH * (1 - f);
              return <line key={f} className="iperf-grid" x1="0" x2="100%" y1={y} y2={y} />;
            })}
            {intervals.map((iv, i) => {
              const h = Math.max(1, (plotH * (iv.bitsPerSecond || 0)) / max);
              const bad = proto === 'tcp' && iv.retransmits > 0;
              return (
                <rect
                  key={i}
                  className={bad ? 'iperf-bar iperf-bar-retx' : 'iperf-bar'}
                  x={`${i * slot + slot * 0.15}%`}
                  width={`${slot * 0.7}%`}
                  y={CHART_TOP + plotH - h}
                  height={h}
                >
                  <title>
                    {`${iv.start.toFixed(0)} to ${iv.end.toFixed(0)} s: ${formatBps(iv.bitsPerSecond)}${
                      proto === 'tcp'
                        ? `, ${iv.retransmits || 0} retransmissions`
                        : `, ${iv.packets || 0} datagrams`
                    }`}
                  </title>
                </rect>
              );
            })}
            <line
              className="iperf-grid"
              x1="0"
              x2="100%"
              y1={CHART_TOP + plotH}
              y2={CHART_TOP + plotH}
            />
            <text className="iperf-axis" x="0" y={CHART_H - 2}>
              0 s
            </text>
            <text className="iperf-axis" x="100%" y={CHART_H - 2} textAnchor="end">
              {n} s
            </text>
          </svg>
        ) : (
          <div className="iperf-chart iperf-empty" style={{ height: CHART_H }}>
            {empty}
          </div>
        )}
      </div>
      <div className="iperf-legend">{retx ? 'orange: seconds with retransmissions' : ''}</div>
    </div>
  );
};

// The node picker is a list of our own rather than a select, so a node that
// cannot host the server shows a warning and its reason on hover instead of
// a sentence inside the option.
const NodePicker = ({ value, nodes, disabled, onPick }) => {
  const [open, setOpen] = useState(false);
  const ref = useRef(null);
  useEffect(() => {
    if (!open) return undefined;
    const onDown = (e) => {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false);
    };
    const onKey = (e) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);
  const current = nodes.find((n) => n.id === value);
  return (
    <div className="iperf-picker" ref={ref}>
      <button
        type="button"
        className={`iperf-select iperf-picker-btn${current ? '' : ' iperf-picker-empty'}`}
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        <span className="iperf-picker-text">{current ? current.label : 'pick a node…'}</span>
        <ChevronDownIcon className="iperf-picker-chevron" />
      </button>
      {open && (
        <ul className="iperf-picker-list" role="listbox">
          {nodes.map((n) => (
            <li
              key={n.id}
              role="option"
              aria-selected={n.id === value}
              aria-disabled={n.blocked || undefined}
              title={n.blocked ? n.note : undefined}
              className={`iperf-picker-item${n.blocked ? ' iperf-picker-item-blocked' : ''}${
                n.id === value ? ' iperf-picker-item-current' : ''
              }`}
              onClick={() => {
                if (n.blocked) return;
                onPick(n.id);
                setOpen(false);
              }}
            >
              <span className="iperf-picker-text">{n.label}</span>
              {n.blocked && <WarningIcon className="iperf-picker-warn" />}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
};

const IperfPanel = ({
  namespace,
  source,
  nodes = [],
  initialDest = null,
  zIndex = 1000,
  minimized = false,
  onClose,
  onMinimize,
  onBringToFront,
}) => {
  const containerRef = useRef(null);
  const wsRef = useRef(null);
  const runRef = useRef({ startedAt: null, finishedAt: null, duration: 10, parallel: 1 });
  const onHeaderMouseDown = useDraggableWindow({ containerRef, onBringToFront });

  const [dest, setDest] = useState('');
  const [destNode, setDestNode] = useState(initialDest?.pod || ''); // the node picked, '' when typed
  const [serverMode, setServerMode] = useState('auto'); // auto: started by the backend; none: already running
  const [port, setPort] = useState('');
  const [proto, setProto] = useState('tcp');
  const [duration, setDuration] = useState('10');
  const [bandwidth, setBandwidth] = useState('');
  const [parallel, setParallel] = useState('1');
  const [reverse, setReverse] = useState(false);
  const [status, setStatus] = useState('idle');
  const [errorMsg, setErrorMsg] = useState(null);
  const [meta, setMeta] = useState(null);
  const [intervals, setIntervals] = useState([]);
  const [summary, setSummary] = useState(null);

  const running = status === 'connecting' || status === 'starting' || status === 'running';
  const formError = validate({ duration, parallel, bandwidth, serverMode, port });
  // Candidates to pick: every node but this one and the external network. With
  // the server on the node, a node whose driver does not provide the tool
  // cannot host it: listed, but disabled and saying why.
  const destNodes = useMemo(
    () =>
      nodes
        .filter((n) => n.data?.type && n.data.type !== 'external' && n.id !== source)
        .map((n) => {
          const info = n.data.fullInfo || {};
          const blocked = serverMode === 'auto' && !(info.tools || []).includes('iperf');
          return {
            id: n.id,
            label: n.data?.label || n.id,
            blocked,
            note: blocked
              ? `${info.driver || 'its driver'} does not provide it, so no server here`
              : '',
          };
        }),
    [nodes, source, serverMode]
  );
  useEffect(() => {
    if (destNode && destNodes.some((n) => n.id === destNode && n.blocked)) setDestNode('');
  }, [destNode, destNodes]);
  const nodeLabel = useMemo(() => {
    const m = {};
    for (const n of nodes) m[n.id] = n.data?.label || n.id;
    return m;
  }, [nodes]);

  // The first data-plane address of a node, or of one interface of it.
  const addressOf = async (pod, iface) => {
    const q = iface ? `?intf=${encodeURIComponent(iface)}` : '';
    const res = await fetch(`${API_BASE_URL}/pods/ips/${namespace}/${encodeURIComponent(pod)}${q}`);
    if (!res.ok) return '';
    const data = await res.json();
    const withIp = (data.interfaces || []).find((i) => i.ipv4);
    return withIp && withIp.ipv4 ? withIp.ipv4.split('/')[0] : '';
  };

  const pickDestNode = async (pod) => {
    if (!pod) return;
    try {
      const ip = await addressOf(pod);
      if (ip) setDest(ip);
    } catch (e) {
      /* leave the field for manual entry */
    }
  };

  // Opened from a link: the other end's address on that link.
  useEffect(() => {
    if (!initialDest?.pod) return undefined;
    let cancelled = false;
    addressOf(initialDest.pod, initialDest.iface)
      .then((ip) => {
        if (!cancelled && ip) setDest(ip);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialDest?.pod, initialDest?.iface]);

  const closeSocket = () => {
    const ws = wsRef.current;
    wsRef.current = null;
    if (!ws) return;
    ws.onopen = ws.onmessage = ws.onclose = ws.onerror = null;
    try {
      ws.close(1000, 'Client closing');
    } catch (e) {
      /* already closed */
    }
  };
  useEffect(() => () => closeSocket(), []);

  // The backend names the API option; the panel calls it Already running.
  const friendlyError = (message) =>
    /server=none/.test(message || '')
      ? `${message.split(';')[0]}. If a server runs there, choose Already running.`
      : message || 'the test failed';

  const run = () => {
    if (!dest.trim() || running || formError) return;
    closeSocket();
    const seconds = parseInt(duration, 10);
    const streams = parseInt(parallel, 10);
    const params = new URLSearchParams({
      dest: dest.trim(),
      proto,
      duration: String(seconds),
      parallel: String(streams),
      server: serverMode,
    });
    if (bandwidth.trim()) params.set('bandwidth', bandwidth.trim());
    if (reverse) params.set('reverse', '1');
    if (serverMode === 'none' && port.trim()) params.set('port', port.trim());
    runRef.current = {
      startedAt: new Date().toISOString(),
      finishedAt: null,
      duration: seconds,
      parallel: streams,
    };
    setIntervals([]);
    setSummary(null);
    setMeta(null);
    setErrorMsg(null);
    setStatus('connecting');
    const ws = new WebSocket(
      `${WS_BASE_URL}/iperf/ws/${namespace}/${encodeURIComponent(source)}?${params}`
    );
    wsRef.current = ws;
    ws.onmessage = (ev) => {
      let m;
      try {
        m = JSON.parse(ev.data);
      } catch (e) {
        return;
      }
      if (m.type === 'meta') setMeta(m);
      else if (m.type === 'status') {
        if (m.state === 'done') runRef.current.finishedAt = new Date().toISOString();
        setStatus(m.state);
      } else if (m.type === 'interval') setIntervals((prev) => [...prev, m]);
      else if (m.type === 'summary') setSummary(m);
      else if (m.type === 'error') {
        setErrorMsg(friendlyError(m.message));
        setStatus('error');
      }
    };
    ws.onclose = () => {
      wsRef.current = null;
      setStatus((s) => (s === 'done' || s === 'error' ? s : 'lost'));
    };
  };

  // Closing the socket is how a run is stopped; the backend kills iperf3.
  const stop = () => {
    closeSocket();
    setStatus('idle');
  };

  const clear = () => {
    closeSocket();
    setIntervals([]);
    setSummary(null);
    setMeta(null);
    setErrorMsg(null);
    setStatus('idle');
  };

  const exportJson = () => {
    const strip = ({ type, ...rest }) => rest;
    const r = runRef.current;
    const doc = {
      source,
      destination: dest.trim(),
      proto,
      duration: r.duration,
      bandwidth: bandwidth.trim() || undefined,
      parallel: r.parallel,
      reverse,
      server: meta?.server || null,
      startedAt: r.startedAt,
      finishedAt: r.finishedAt,
      intervals: intervals.map(strip),
      summary: summary ? strip(summary) : null,
    };
    const blob = new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    const safe = (s) => String(s).replace(/[^A-Za-z0-9._-]/g, '_');
    a.href = url;
    a.download = `iperf-${safe(source)}-${safe(dest.trim())}.json`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  const serverText = meta
    ? meta.server
      ? `${nodeLabel[meta.server.pod] || meta.server.pod}:${meta.server.port}`
      : 'already running'
    : '';
  const dash = '–';
  const rows = [
    [
      'Sent',
      summary
        ? `${formatBps(summary.sentBitsPerSecond)} · ${formatBytes(summary.sentBytes)}`
        : dash,
    ],
    [
      'Received',
      summary
        ? `${formatBps(summary.receivedBitsPerSecond)} · ${formatBytes(summary.receivedBytes)}`
        : dash,
    ],
  ];
  if (proto === 'tcp') {
    rows.push([
      'Retransmissions',
      summary && summary.retransmits != null ? summary.retransmits : dash,
    ]);
  } else {
    rows.push([
      'Jitter',
      summary && summary.jitterMs != null ? `${summary.jitterMs.toFixed(3)} ms` : dash,
    ]);
    rows.push([
      'Lost',
      summary && summary.packets != null
        ? `${summary.lostPackets || 0} of ${summary.packets} datagrams (${(summary.lostPercent || 0).toFixed(2)}%)`
        : dash,
    ]);
  }
  rows.push([
    'CPU',
    summary
      ? `client ${summary.cpuClientPercent.toFixed(1)}% · server ${summary.cpuServerPercent.toFixed(1)}%`
      : dash,
  ]);
  rows.push(['Server', serverText || dash]);

  const numberField = (value, setValue, range, title) => (
    <input
      className={`iperf-input iperf-input-short${inRange(value, range) ? '' : ' iperf-input-bad'}`}
      type="text"
      inputMode="numeric"
      value={value}
      disabled={running}
      title={`${title}, ${range[0]} to ${range[1]}`}
      onChange={(e) => setValue(e.target.value.replace(/[^0-9]/g, ''))}
    />
  );

  return (
    <div
      ref={containerRef}
      className={`iperf-panel${minimized ? ' iperf-panel-min' : ''}`}
      style={{ zIndex }}
      onMouseDown={() => {
        if (!minimized && onBringToFront) onBringToFront();
      }}
    >
      <div className="iperf-header" onMouseDown={onHeaderMouseDown}>
        <span className="iperf-title">
          <GaugeIcon className="iperf-title-icon" />
          Throughput test from {source}
        </span>
        <span className={`iperf-status iperf-status-${status}`}>
          {STATUS_LABEL[status] || status}
        </span>
        <button
          className="iperf-btn iperf-btn-icon"
          onClick={exportJson}
          title="Download the run as JSON"
          disabled={intervals.length === 0}
        >
          <DownloadIcon className="app-icon" />
        </button>
        {onMinimize && (
          <button className="iperf-btn iperf-btn-icon" onClick={onMinimize} title="Minimize">
            <MinusIcon className="app-icon" />
          </button>
        )}
        <button
          className="iperf-btn iperf-btn-icon iperf-btn-close"
          onClick={onClose}
          title="Close"
        >
          <CloseIcon className="app-icon" />
        </button>
      </div>

      <div className="iperf-form">
        <div className="iperf-row">
          <span className="iperf-label">To</span>
          <input
            className="iperf-input"
            type="text"
            placeholder="IP address"
            value={dest}
            disabled={running}
            onChange={(e) => {
              setDest(e.target.value);
              setDestNode('');
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') run();
            }}
          />
          <NodePicker
            value={destNode}
            nodes={destNodes}
            disabled={running}
            onPick={(id) => {
              setDestNode(id);
              pickDestNode(id);
            }}
          />
        </div>
        <div className="iperf-row">
          <span className="iperf-label">Server</span>
          <div className="iperf-seg" role="tablist">
            <button
              className={`iperf-seg-btn${serverMode === 'auto' ? ' iperf-seg-active' : ''}`}
              onClick={() => setServerMode('auto')}
              disabled={running}
              title="KubeNDT starts iperf3 on the node that owns the address"
            >
              On the node
            </button>
            <button
              className={`iperf-seg-btn${serverMode === 'none' ? ' iperf-seg-active' : ''}`}
              onClick={() => setServerMode('none')}
              disabled={running}
              title="A server already listening at the address, inside the topology or beyond the external network"
            >
              Already running
            </button>
          </div>
          {serverMode === 'none' && (
            <>
              <span className="iperf-unit">port</span>
              <input
                className={`iperf-input iperf-input-short${
                  port.trim() && !inRange(port, [1, 65535]) ? ' iperf-input-bad' : ''
                }`}
                type="text"
                inputMode="numeric"
                placeholder="5201"
                value={port}
                disabled={running}
                onChange={(e) => setPort(e.target.value.replace(/[^0-9]/g, ''))}
              />
            </>
          )}
        </div>
        <div className="iperf-row">
          <span className="iperf-label">Test</span>
          <div className="iperf-seg" role="tablist">
            {['tcp', 'udp'].map((p) => (
              <button
                key={p}
                className={`iperf-seg-btn${proto === p ? ' iperf-seg-active' : ''}`}
                onClick={() => setProto(p)}
                disabled={running}
              >
                {p.toUpperCase()}
              </button>
            ))}
          </div>
          {numberField(duration, setDuration, DURATION_RANGE, 'Duration in seconds')}
          <span className="iperf-unit">s</span>
          {numberField(parallel, setParallel, STREAMS_RANGE, 'Parallel streams')}
          <span className="iperf-unit">streams</span>
        </div>
        <div className="iperf-row">
          <span className="iperf-label">Rate</span>
          <input
            className={`iperf-input${
              bandwidth.trim() && !BANDWIDTH_RE.test(bandwidth.trim()) ? ' iperf-input-bad' : ''
            }`}
            type="text"
            placeholder={
              proto === 'udp'
                ? '1M (offered rate, bit/s with k, M or G)'
                : 'unlimited (bit/s with k, M or G)'
            }
            value={bandwidth}
            disabled={running}
            onChange={(e) => setBandwidth(e.target.value)}
            title="The rate each stream aims for, in bit/s with k, M or G (20M is 20 Mbit/s, 100M with 8 streams is 800M in all): the UDP datagram rate, a ceiling for TCP"
          />
          <label className="iperf-check" title="The server sends and this node receives">
            <input
              type="checkbox"
              checked={reverse}
              disabled={running}
              onChange={(e) => setReverse(e.target.checked)}
            />
            reverse
          </label>
        </div>
        {formError && <div className="iperf-form-error">{formError}</div>}
      </div>

      {errorMsg && <div className="iperf-error">{errorMsg}</div>}

      <Chart
        intervals={intervals}
        slots={runRef.current.duration}
        proto={proto}
        running={running}
        empty={running ? 'waiting for the first second…' : 'Pick a destination and press Run.'}
      />

      <div className="iperf-summary">
        {rows.map(([key, val]) => (
          <React.Fragment key={key}>
            <span className="iperf-summary-key">{key}</span>
            <span className="iperf-summary-val">{val}</span>
          </React.Fragment>
        ))}
      </div>

      <div className="iperf-controls">
        {running ? (
          <button className="iperf-btn iperf-btn-stop" onClick={stop} title="Stop the test">
            Stop
          </button>
        ) : (
          <button
            className="iperf-btn iperf-btn-run"
            onClick={run}
            disabled={!dest.trim() || Boolean(formError)}
            title={formError || 'Run the test'}
          >
            Run
          </button>
        )}
        {meta && running && serverText && <span className="iperf-unit">server {serverText}</span>}
        <button
          className="iperf-btn iperf-btn-clear"
          onClick={clear}
          disabled={intervals.length === 0 && !errorMsg && !running}
        >
          Clear
        </button>
      </div>
    </div>
  );
};

export default IperfPanel;
