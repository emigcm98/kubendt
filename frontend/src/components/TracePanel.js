import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import './TracePanel.css';
import { WS_BASE_URL, API_BASE_URL } from '../config';
import { ReactComponent as TraceIcon } from '../assets/images/icons/trace-icon.svg';
import { ReactComponent as GlobeIcon } from '../assets/images/icons/globe.svg';
import { ReactComponent as ClusterIcon } from '../assets/images/icons/cluster.svg';

// TracePanel is the remote control for the traceroute visualization. It owns the
// WebSocket, the hop list and the scrubber (play, step forward, step back). The
// packet and the lit path are drawn on the graph by InnerGraph from the `viz`
// object this panel pushes up through onViz, so this component draws no packet.
//
// The trace is one-shot. Run it, watch the packet go, then scrub back and forth
// over the finished path. Running again replays from the source.

const STATUS_LABEL = {
  idle: 'ready',
  connecting: 'connecting…',
  starting: 'starting container…',
  resolving: 'resolving topology…',
  tracing: 'tracing…',
  measuring: 'measuring…',
  done: 'done',
  lost: 'disconnected',
  error: 'error',
};

// Private ranges (RFC1918, CGNAT, link-local, loopback) still count as the way
// out through the external network, unless the trace already left through the
// cluster fabric (kind "cluster" from the backend). A public address is the
// internet.
const isPrivateIp = (ip) => {
  if (!ip) return false;
  const o = ip.split('.').map(Number);
  if (o.length !== 4 || o.some((n) => Number.isNaN(n))) return false;
  const [a, b] = o;
  return (
    a === 10 ||
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && b === 168) ||
    (a === 169 && b === 254) ||
    (a === 100 && b >= 64 && b <= 127) ||
    a === 127
  );
};

// Full per-hop stat block shown when hovering the metrics cell. Any field may
// be missing on an older cached result, so it falls back to 0.
const metricsTitle = (m) => {
  const ms = (v) => (typeof v === 'number' ? v : 0).toFixed(2);
  return [
    `Sent: ${m.sent ?? 0}    Loss: ${(m.loss ?? 0).toFixed(1)}%`,
    `Avg: ${ms(m.avg)} ms    Last: ${ms(m.last)} ms`,
    `Best: ${ms(m.best)} ms    Worst: ${ms(m.worst)} ms`,
    `Jitter (StDev): ${ms(m.stdev)} ms    Gmean: ${ms(m.gmean)} ms`,
  ].join('\n');
};

const TracePanel = ({
  namespace,
  source,
  nodes = [],
  edges = [],
  zIndex = 1000,
  onViz,
  onClose,
  onBringToFront,
}) => {
  const containerRef = useRef(null);
  const ws = useRef(null);
  const intentionalCloseRef = useRef(false);
  const lastRunDestRef = useRef(''); // last destination actually traced (to tell replay from re-run)
  const lastRunCyclesRef = useRef(5); // cycles used on the last metrics run (re-measure when changed)
  const startedAtRef = useRef(null); // ISO timestamp when the current run began
  const finishedAtRef = useRef(null); // ISO timestamp when it finished
  const resultsRef = useRef({}); // method → { hops, outcome, dest }; keeps per-method results while open
  const isDraggingRef = useRef(false);
  const dragOffsetRef = useRef({ x: 0, y: 0 });
  const positionRef = useRef({
    x: Math.max(12, Math.round(window.innerWidth / 2 - 170)),
    y: 80,
  });

  const [dest, setDest] = useState('');
  const [method, setMethod] = useState('icmp');
  const [mode, setMode] = useState('trace'); // 'trace' | 'metrics'
  const [cycles, setCycles] = useState(5); // metrics probe rounds
  const [status, setStatus] = useState('idle');
  const [errorMsg, setErrorMsg] = useState(null);
  const [hops, setHops] = useState([]);
  const [step, setStep] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [outcome, setOutcome] = useState(null); // "delivered" | "unreachable" | "unreached"
  const [measureElapsed, setMeasureElapsed] = useState(0); // seconds elapsed while mtr measures

  const running =
    status === 'connecting' ||
    status === 'starting' ||
    status === 'resolving' ||
    status === 'tracing' ||
    status === 'measuring';

  const nodeLabel = useMemo(() => {
    const m = {};
    for (const n of nodes) m[n.id] = n.data?.label || n.id;
    return m;
  }, [nodes]);

  // Destination candidates: any real (non-external) node in the topology.
  const destNodes = useMemo(
    () => nodes.filter((n) => n.data?.type && n.data.type !== 'external'),
    [nodes]
  );

  // Pod to the external-network node ids it attaches to. Used to route a hop
  // that leaves the topology toward the grey node, and to tell a real tunnel
  // apart from two nodes that just share a physical L2 segment (same node).
  const externalNeighborsOf = useMemo(() => {
    const extIds = new Set(nodes.filter((n) => n.data?.type === 'external').map((n) => n.id));
    const m = {};
    const add = (pod, ext) => {
      if (!m[pod]) m[pod] = [];
      if (!m[pod].includes(ext)) m[pod].push(ext);
    };
    for (const e of edges) {
      if (extIds.has(e.target)) add(e.source, e.target);
      if (extIds.has(e.source)) add(e.target, e.source);
    }
    return m;
  }, [nodes, edges]);

  // Build the packet journey as an ordered list of waypoints, one per step,
  // index 0 being the source. Every forward hop is its own step, the internet
  // ones included, so the scrubber and the packet reach the destination instead
  // of stopping at the first internet hop. Each waypoint records how it was
  // reached (link, tunnel, external, internet) for styling, and displayRows
  // mirrors the journey for the list.
  const { waypoints, displayRows, drop } = useMemo(() => {
    const firstExt = (pod) => (externalNeighborsOf[pod] || [])[0];
    const sharedExt = (a, b) =>
      (externalNeighborsOf[a] || []).find((id) => (externalNeighborsOf[b] || []).includes(id));

    const wps = [{ pod: source, seg: 'start' }];
    const rows = [];
    let prev = source;
    let usedExternalNode = false;
    let exitedViaCluster = false;
    for (const h of hops) {
      if (h.kind === 'l3' && h.node) {
        let path = h.path && h.path.length ? h.path : [prev, h.node];
        let viaExternal = false;
        // The backend says "tunnel" whenever it finds no path in the pod-only
        // graph. But two nodes on the same external network (router-0 and gnb
        // over "External Network", say) are not tunnelled, they share a physical
        // L2 segment, so reroute through that grey node.
        if (h.segment === 'tunnel') {
          const extId = sharedExt(prev, h.node);
          if (extId) {
            path = [prev, extId, h.node];
            viaExternal = true;
          }
        }
        for (let i = 1; i < path.length; i++) {
          const endpoint = i === path.length - 1;
          const seg = viaExternal ? 'external' : h.segment === 'tunnel' ? 'tunnel' : 'link';
          wps.push({ pod: path[i], seg });
          rows.push({
            key: `h${h.ttl}-${i}`,
            step: wps.length - 1,
            pod: path[i],
            kind: endpoint ? 'l3' : 'relay',
            ttl: endpoint ? h.ttl : null,
            ip: endpoint ? h.ip : null,
            rtt: endpoint ? h.rtt : null,
            tunnel: endpoint && h.segment === 'tunnel' && !viaExternal,
            drop: endpoint && h.unreachable ? h.unreachable : undefined,
            metrics: endpoint ? h.metrics : undefined,
          });
        }
        prev = h.node;
      } else if (h.kind === 'cluster') {
        // Kubernetes fabric hop (flannel gateway, node IP): the packet left
        // through a router's cluster eth0, never through the external node.
        // It sits at the outward internet point from here on.
        exitedViaCluster = true;
        wps.push({ pod: '__internet__', seg: 'internet' });
        rows.push({
          key: `c${h.ttl}`,
          step: wps.length - 1,
          kind: 'cluster',
          ttl: h.ttl,
          ip: h.ip,
          rtt: h.rtt,
          drop: h.unreachable || undefined,
          metrics: h.metrics,
        });
      } else if (h.kind === 'external') {
        if (isPrivateIp(h.ip) && !exitedViaCluster) {
          // Private address past the topology, still the external network.
          // Route to the grey node the first time, then the packet stays there
          // (waypoint stays prev) while the list keeps advancing.
          const extId = !usedExternalNode ? firstExt(prev) : null;
          if (extId) {
            wps.push({ pod: extId, seg: 'external' });
            prev = extId;
            usedExternalNode = true;
          } else {
            wps.push({ pod: prev, seg: 'external' });
          }
          rows.push({
            key: `x${h.ttl}`,
            step: wps.length - 1,
            pod: extId || undefined,
            kind: 'external-net',
            ttl: h.ttl,
            ip: h.ip,
            rtt: h.rtt,
            drop: h.unreachable || undefined,
            metrics: h.metrics,
          });
        } else {
          // Public address, or anything past the cluster exit: the internet.
          // Every hop is a step and the packet sits at the outward internet
          // point for all of them.
          wps.push({ pod: '__internet__', seg: 'internet' });
          rows.push({
            key: `i${h.ttl}`,
            step: wps.length - 1,
            kind: 'internet',
            ttl: h.ttl,
            ip: h.ip,
            rtt: h.rtt,
            drop: h.unreachable || undefined,
            metrics: h.metrics,
          });
        }
      } else {
        rows.push({
          key: `t${h.ttl}`,
          step: null,
          kind: 'timeout',
          ttl: h.ttl,
          metrics: h.metrics,
        });
      }
    }
    // Where (if anywhere) the packet was dropped, for the graph marker.
    let drop = null;
    const lastRealWp = [...wps].reverse().find((w) => w.pod && w.pod !== '__internet__');
    const lastResolved = lastRealWp ? lastRealWp.pod : source;
    const unreachHop = hops.find((h) => h.unreachable);
    if (unreachHop) {
      drop = { kind: 'unreachable', node: unreachHop.node || lastResolved };
    } else if (outcome === 'unreached' && hops.length > 0) {
      drop = { kind: 'unreached', node: lastResolved };
    }
    return { waypoints: wps, displayRows: rows, drop };
  }, [hops, source, externalNeighborsOf, outcome]);

  const maxStep = waypoints.length - 1;
  const clampedStep = Math.min(step, maxStep);

  // Push the animation state up to the graph on every relevant change.
  useEffect(() => {
    const delivered = status === 'done' && outcome === 'delivered';
    if (onViz) onViz({ source, waypoints, step: clampedStep, drop, delivered });
  }, [onViz, source, waypoints, clampedStep, drop, status, outcome]);

  // Clear the graph overlay when the panel unmounts.
  useEffect(() => {
    return () => {
      if (onViz) onViz(null);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Play advances one segment at a time so the packet crawls along the path.
  // While the trace is still streaming and the packet has caught up, it just
  // waits. A new hop extends maxStep and re-runs this effect, which picks the
  // crawl back up. It only stops once the trace is done and the packet has
  // reached the last hop.
  useEffect(() => {
    if (!playing) return undefined;
    if (clampedStep >= maxStep) {
      if (!running) setPlaying(false);
      return undefined;
    }
    const id = setTimeout(() => setStep((s) => Math.min(maxStep, s + 1)), 650);
    return () => clearTimeout(id);
  }, [playing, clampedStep, maxStep, running]);

  // Arrow keys step the packet instead of panning the graph while the panel is
  // open. Capture phase plus stopPropagation beats the graph's own key handler.
  useEffect(() => {
    const onKey = (e) => {
      if (!maxStep) return;
      const t = e.target;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'SELECT' || t.isContentEditable)) return;
      let handled = true;
      if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
        setPlaying(false);
        setStep((s) => Math.max(0, Math.min(maxStep, s) - 1));
      } else if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
        setPlaying(false);
        setStep((s) => Math.min(maxStep, s + 1));
      } else {
        handled = false;
      }
      if (handled) {
        e.preventDefault();
        e.stopPropagation();
      }
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [maxStep]);

  // While mtr measures, tick a clock so the panel can show elapsed time next to
  // the progress bar. mtr batches, so there is no live hop stream meanwhile.
  useEffect(() => {
    if (status !== 'measuring') {
      setMeasureElapsed(0);
      return undefined;
    }
    const t0 = performance.now();
    const id = setInterval(() => setMeasureElapsed((performance.now() - t0) / 1000), 200);
    return () => clearInterval(id);
  }, [status]);

  const cleanupSocket = useCallback(() => {
    if (ws.current) {
      intentionalCloseRef.current = true;
      try {
        ws.current.close(1000);
      } catch (e) {
        /* ignore */
      }
      ws.current = null;
    }
  }, []);

  const run = useCallback(() => {
    const target = dest.trim();
    if (!target) return;
    cleanupSocket();
    setErrorMsg(null);
    setHops([]);
    setOutcome(null);
    setStep(0);
    setPlaying(true); // crawl the packet forward as hops stream in
    lastRunDestRef.current = target;
    lastRunCyclesRef.current = cycles;
    startedAtRef.current = new Date().toISOString();
    finishedAtRef.current = null;
    setStatus('connecting');
    intentionalCloseRef.current = false;

    const metricsQS = mode === 'metrics' ? `&metrics=1&cycles=${cycles}` : '';
    const url = `${WS_BASE_URL}/trace/ws/${namespace}/${encodeURIComponent(
      source
    )}?dest=${encodeURIComponent(target)}&method=${method}${metricsQS}`;
    const socket = new WebSocket(url);
    ws.current = socket;

    socket.onmessage = (event) => {
      if (typeof event.data !== 'string') return;
      let msg;
      try {
        msg = JSON.parse(event.data);
      } catch (e) {
        return;
      }
      if (!msg || !msg.type) return;
      if (msg.type === 'status') {
        setStatus(msg.state);
        if (msg.outcome) setOutcome(msg.outcome);
        if (msg.state === 'done') finishedAtRef.current = new Date().toISOString();
      } else if (msg.type === 'hop') setHops((prev) => [...prev, msg]);
      else if (msg.type === 'error') {
        setErrorMsg(msg.message);
        setStatus('error');
        finishedAtRef.current = new Date().toISOString();
      }
    };
    socket.onclose = () => {
      ws.current = null;
      if (startedAtRef.current && !finishedAtRef.current) {
        finishedAtRef.current = new Date().toISOString();
      }
      setStatus((s) => (s === 'done' || s === 'error' || intentionalCloseRef.current ? s : 'lost'));
    };
    socket.onerror = () => {
      setStatus('error');
    };
  }, [dest, method, mode, cycles, namespace, source, cleanupSocket]);

  const handleClose = useCallback(() => {
    cleanupSocket();
    if (onClose) onClose();
  }, [cleanupSocket, onClose]);

  // Switching mode or probe method parks the current results under the old key
  // and restores whatever was cached for the new one (same destination).
  // Results live as long as the panel is open, so flipping between Trace and
  // Metrics or between ICMP, UDP and TCP does not lose work.
  const parkAndLoad = (nextMode, nextMethod) => {
    if (nextMode === mode && nextMethod === method) return;
    resultsRef.current[`${mode}:${method}`] = {
      hops,
      outcome,
      dest: lastRunDestRef.current,
      cycles: lastRunCyclesRef.current,
      startedAt: startedAtRef.current,
      finishedAt: finishedAtRef.current,
    };
    cleanupSocket();
    setPlaying(false);
    setMode(nextMode);
    setMethod(nextMethod);
    const cached = resultsRef.current[`${nextMode}:${nextMethod}`];
    if (cached && cached.hops.length && cached.dest && cached.dest === dest.trim()) {
      setHops(cached.hops);
      setOutcome(cached.outcome);
      lastRunDestRef.current = cached.dest;
      lastRunCyclesRef.current = cached.cycles || cycles;
      startedAtRef.current = cached.startedAt;
      finishedAtRef.current = cached.finishedAt;
      setStatus('done');
      setStep(9999); // clamps to the end of the restored path
    } else {
      setHops([]);
      setOutcome(null);
      lastRunDestRef.current = '';
      startedAtRef.current = null;
      finishedAtRef.current = null;
      setStatus('idle');
      setStep(0);
    }
  };

  const stepBack = () => {
    setPlaying(false);
    setStep((s) => Math.max(0, Math.min(maxStep, s) - 1));
  };
  const stepFwd = () => {
    setPlaying(false);
    setStep((s) => Math.min(maxStep, s + 1));
  };
  const restart = () => {
    setPlaying(false);
    setStep(0);
  };
  // Download the full result (metadata + every hop, metrics included) as JSON.
  const exportJson = () => {
    if (hops.length === 0) return;
    const target = lastRunDestRef.current || dest.trim();
    const data = {
      source,
      destination: target,
      method,
      mode,
      cycles: mode === 'metrics' ? cycles : undefined,
      outcome,
      startedAt: startedAtRef.current,
      finishedAt: finishedAtRef.current,
      // Drop the WS-only "type" field so each hop matches the REST report shape.
      hops: hops.map(({ type, ...rest }) => rest),
    };
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    const safe = (s) => String(s).replace(/[^A-Za-z0-9._-]/g, '_');
    a.download = `traceroute-${safe(source)}-to-${safe(target)}${mode === 'metrics' ? '-metrics' : ''}.json`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  // Wipe everything so the experiment can be run again from scratch. Also drops
  // the cached result for this mode and method.
  const clearTrace = () => {
    cleanupSocket();
    setPlaying(false);
    setHops([]);
    setOutcome(null);
    setStep(0);
    setErrorMsg(null);
    setStatus('idle');
    lastRunDestRef.current = '';
    startedAtRef.current = null;
    finishedAtRef.current = null;
    delete resultsRef.current[`${mode}:${method}`];
  };
  // The green transport button. It runs a fresh trace the first time or when the
  // destination changed, pauses or resumes a live trace, and otherwise replays
  // the finished animation. One button that runs the whole thing.
  const handleMainPlay = () => {
    if (playing) {
      setPlaying(false);
      return;
    }
    if (running) {
      setPlaying(true);
      return;
    }
    // Re-measure, not replay, when there is nothing yet, the destination
    // changed, or in metrics mode the cycle count changed on the slider.
    const cyclesChanged = mode === 'metrics' && cycles !== lastRunCyclesRef.current;
    if (hops.length === 0 || dest.trim() !== lastRunDestRef.current || cyclesChanged) {
      run();
      return;
    }
    if (clampedStep >= maxStep) setStep(0);
    setPlaying(true);
  };
  // Jump the packet to a clicked step. Stops play, it is a manual scrub.
  const jumpToStep = (s) => {
    if (s == null) return;
    setPlaying(false);
    setStep(s);
  };

  const pickDestNode = async (podId) => {
    if (!podId) return;
    try {
      const res = await fetch(`${API_BASE_URL}/pods/ips/${namespace}/${encodeURIComponent(podId)}`);
      if (!res.ok) return;
      const data = await res.json();
      const withIp = (data.interfaces || []).find((i) => i.ipv4);
      if (withIp && withIp.ipv4) setDest(withIp.ipv4.split('/')[0]);
    } catch (e) {
      /* best-effort: leave the field for manual entry */
    }
  };

  // ── Dragging ────────────────────────────────────────────────────────────
  useEffect(() => {
    if (containerRef.current) {
      containerRef.current.style.left = `${positionRef.current.x}px`;
      containerRef.current.style.top = `${positionRef.current.y}px`;
    }
  }, []);
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

  useEffect(() => {
    return () => cleanupSocket();
  }, [cleanupSocket]);

  const destReady = dest.trim().length > 0;

  return (
    <div
      className={`trace-panel${mode === 'metrics' ? ' trace-panel-metrics' : ''}`}
      ref={containerRef}
      style={{ zIndex }}
      onMouseDown={onBringToFront}
    >
      <div className="trace-header" onMouseDown={onHeaderMouseDown}>
        <span className="trace-title">
          <TraceIcon className="trace-title-icon" width="15" height="15" />
          Traceroute {source}
        </span>
        <span className={`trace-status trace-status-${status}`}>
          {STATUS_LABEL[status] || status}
        </span>
        <button
          className="trace-btn trace-btn-icon"
          onClick={exportJson}
          title="Download results as JSON"
          disabled={hops.length === 0}
        >
          <svg viewBox="0 0 24 24" width="14" height="14" fill="none" aria-hidden="true">
            <path
              d="M12 3 v11 M8 10 l4 4 4-4"
              stroke="currentColor"
              strokeWidth="1.8"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
            <path d="M5 19 h14" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
          </svg>
        </button>
        <button className="trace-btn trace-btn-close" onClick={handleClose} title="Close">
          ✕
        </button>
      </div>

      <div className="trace-form">
        <div className="trace-row">
          <span className="trace-label">To</span>
          <input
            className="trace-input"
            type="text"
            placeholder="IP or hostname (e.g. 8.8.8.8)"
            value={dest}
            onChange={(e) => setDest(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && destReady && !running) run();
            }}
          />
        </div>
        <div className="trace-row">
          <span className="trace-label">Node</span>
          <select
            className="trace-select"
            value=""
            onChange={(e) => {
              pickDestNode(e.target.value);
              e.target.value = '';
            }}
          >
            <option value="">pick a node…</option>
            {destNodes.map((n) => (
              <option key={n.id} value={n.id}>
                {n.data?.label || n.id}
              </option>
            ))}
          </select>
          <select
            className="trace-select trace-method"
            value={method}
            onChange={(e) => parkAndLoad(mode, e.target.value)}
            title="Probe method"
          >
            <option value="icmp">ICMP</option>
            <option value="udp">UDP</option>
            <option value="tcp">TCP</option>
          </select>
        </div>
        <div className="trace-row">
          <span className="trace-label">Mode</span>
          <div className="trace-modeswitch" role="tablist">
            <button
              className={`trace-mode-btn${mode === 'trace' ? ' trace-mode-active' : ''}`}
              onClick={() => parkAndLoad('trace', method)}
              title="Animated path, one probe per hop"
            >
              Trace
            </button>
            <button
              className={`trace-mode-btn${mode === 'metrics' ? ' trace-mode-active' : ''}`}
              onClick={() => parkAndLoad('metrics', method)}
              title="Per-hop latency & loss (mtr)"
            >
              Metrics
            </button>
          </div>
          {mode === 'metrics' && (
            <label className="trace-cycles" title="Probe rounds (mtr cycles)">
              <input
                className="trace-cycles-slider"
                type="range"
                min={2}
                max={20}
                value={cycles}
                onChange={(e) => setCycles(Number(e.target.value))}
              />
              <span className="trace-cycles-val">{cycles}×</span>
            </label>
          )}
        </div>
      </div>

      {errorMsg && <div className="trace-error">{errorMsg}</div>}

      {displayRows.length > 0 && (
        <div className="trace-hops-head">
          <span className="trace-hop-ttl" title="hop number (TTL)" />
          <span className="trace-hop-ip" title="responding IP address">
            IP
          </span>
          <span className="trace-hop-node" title="topology node this hop resolves to">
            Node
          </span>
          {mode === 'metrics' ? (
            <span className="trace-hop-metrics">
              <span className="trace-m-loss" title="packet loss at this hop">
                Loss
              </span>
              <span className="trace-m-avg" title="average round-trip time">
                Avg
              </span>
              <span className="trace-m-range" title="best / worst round-trip time">
                B–W
              </span>
            </span>
          ) : (
            <span className="trace-hop-rtt" title="round-trip time">
              RTT
            </span>
          )}
          <span className="trace-hop-jump" />
        </div>
      )}

      <div className="trace-hops">
        {status === 'measuring' && (
          <div className="trace-measuring">
            <div className="trace-measuring-row">
              <span className="trace-spinner" />
              measuring {cycles} rounds… {Math.floor(measureElapsed)}s
            </div>
            <div className="trace-measuring-bar trace-measuring-bar-indet">
              <div />
            </div>
          </div>
        )}
        {displayRows.length === 0 && !running && (
          <div className="trace-empty">Enter a destination and press ▶.</div>
        )}
        {displayRows.map((r) => {
          const active = r.step != null && r.step === clampedStep;
          const clickable = r.step != null;
          return (
            <div
              key={r.key}
              className={`trace-hop trace-hop-${r.kind}${active ? ' trace-hop-active' : ''}${clickable ? ' trace-hop-clickable' : ''}${r.ttl != null ? ' trace-hop-num' : ''}${r.drop ? ' trace-hop-drop' : ''}`}
              onClick={clickable ? () => jumpToStep(r.step) : undefined}
            >
              <span className="trace-hop-ttl">{r.ttl != null ? r.ttl : '·'}</span>
              {r.kind === 'timeout' ? (
                <span className="trace-hop-star">* * *</span>
              ) : r.drop ? (
                <>
                  <span className="trace-hop-ip">{r.ip}</span>
                  <span className="trace-hop-node trace-hop-drop-label">
                    ✖ unreachable ({r.drop})
                  </span>
                </>
              ) : r.kind === 'relay' ? (
                <span className="trace-hop-node trace-hop-relay-label">
                  ↳ {nodeLabel[r.pod] || r.pod} <span className="trace-hop-l2">L2</span>
                </span>
              ) : r.kind === 'internet' ? (
                <>
                  <span className="trace-hop-ip">{r.ip}</span>
                  <span className="trace-hop-node trace-hop-internet-label">
                    <GlobeIcon className="trace-hop-inline-icon" /> internet
                  </span>
                </>
              ) : r.kind === 'cluster' ? (
                <>
                  <span className="trace-hop-ip">{r.ip}</span>
                  <span className="trace-hop-node trace-hop-cluster-label">
                    <ClusterIcon className="trace-hop-inline-icon" /> cluster
                  </span>
                </>
              ) : r.kind === 'external-net' ? (
                <>
                  <span className="trace-hop-ip">{r.ip}</span>
                  <span className="trace-hop-node trace-hop-external-label">
                    ⇥ {nodeLabel[r.pod] || 'external network'}
                  </span>
                </>
              ) : (
                <>
                  <span className="trace-hop-ip">{r.ip}</span>
                  <span className="trace-hop-node">
                    {nodeLabel[r.pod] || r.pod}
                    {r.tunnel && <span className="trace-hop-tunnel"> ⤳ tunnel</span>}
                  </span>
                </>
              )}
              {/* Final column, reserved on every row so nothing overlaps it. Metric
                  stats in Metrics mode, the RTT in Trace mode. */}
              {mode === 'metrics' ? (
                <span
                  className="trace-hop-metrics"
                  title={r.metrics ? metricsTitle(r.metrics) : undefined}
                >
                  <span
                    className={`trace-m-loss${r.metrics && r.metrics.loss > 0 ? ' trace-m-loss-bad' : ''}`}
                  >
                    {r.metrics ? `${r.metrics.loss.toFixed(0)}%` : ''}
                  </span>
                  <span className="trace-m-avg">
                    {r.metrics ? `${r.metrics.avg.toFixed(1)}ms` : ''}
                  </span>
                  <span className="trace-m-range">
                    {r.metrics ? `${r.metrics.best.toFixed(0)}–${r.metrics.worst.toFixed(0)}` : ''}
                  </span>
                </span>
              ) : (
                <span className="trace-hop-rtt">{r.rtt ? `${r.rtt.toFixed(1)} ms` : ''}</span>
              )}
              <span className="trace-hop-jump">{clickable ? '▸' : ''}</span>
            </div>
          );
        })}
      </div>

      {status === 'done' &&
        (() => {
          const flag = hops.find((h) => h.unreachable)?.unreachable;
          if (outcome === 'delivered') {
            return <div className="trace-verdict trace-verdict-ok">✓ destination reached</div>;
          }
          if (outcome === 'unreachable') {
            return (
              <div className="trace-verdict trace-verdict-bad">
                ✕ unreachable{flag ? ` (${flag})` : ''}
              </div>
            );
          }
          return <div className="trace-verdict trace-verdict-bad">✕ timed out, no reply</div>;
        })()}

      <div className="trace-controls">
        <button className="trace-btn" onClick={restart} title="Back to source" disabled={!maxStep}>
          ⏮
        </button>
        <button
          className="trace-btn"
          onClick={stepBack}
          title="Step back"
          disabled={clampedStep <= 0}
        >
          ◀
        </button>
        <button
          className="trace-btn trace-btn-play"
          onClick={handleMainPlay}
          title={playing ? 'Pause' : running ? 'Resume' : 'Run & play'}
          disabled={!destReady && hops.length === 0}
        >
          {playing ? '⏸' : '▶'}
        </button>
        <button
          className="trace-btn"
          onClick={stepFwd}
          title="Step forward"
          disabled={clampedStep >= maxStep}
        >
          ▶
        </button>
        <input
          className="trace-scrubber"
          type="range"
          min={0}
          max={maxStep}
          value={clampedStep}
          onChange={(e) => {
            setPlaying(false);
            setStep(Number(e.target.value));
          }}
          disabled={!maxStep}
        />
        <span className="trace-step-count">
          {clampedStep}/{maxStep}
        </span>
        <button
          className="trace-btn trace-btn-clear"
          onClick={clearTrace}
          title="Clear and reset for a fresh run"
          disabled={hops.length === 0 && status === 'idle' && !errorMsg}
        >
          Clear
        </button>
      </div>
    </div>
  );
};

export default TracePanel;
