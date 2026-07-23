import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import './CapturePanel.css';
import { WS_BASE_URL, API_BASE_URL } from '../config';
import { ReactComponent as PcapIcon } from '../assets/images/icons/pcap.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';

const MAX_PACKETS = 5000; // ring-buffer cap held in memory
const MAX_RENDERED = 2000; // most recent rows actually rendered
const FLUSH_MS = 150; // batch UI updates to survive high packet rates

const COLS = [
  { key: 'num', label: 'No.', w: 58, min: 42, align: 'right', num: true },
  { key: 'time', label: 'Time', w: 104, min: 64 },
  { key: 'src', label: 'Source', w: 160, min: 70 },
  { key: 'dst', label: 'Destination', w: 160, min: 70 },
  { key: 'proto', label: 'Protocol', w: 92, min: 56 },
  { key: 'len', label: 'Len', w: 58, min: 42, align: 'right' },
  { key: 'info', label: 'Info', w: 360, min: 90 },
];

// Coarse protocol → colour class, à la Wireshark's colouring rules.
const protoClass = (proto) => {
  const p = (proto || '').toUpperCase();
  if (p.includes('OSPF')) return 'cap-p-ospf';
  if (p === 'ICMP' || p === 'ICMPV6') return 'cap-p-icmp';
  if (p === 'ARP') return 'cap-p-arp';
  if (p === 'TCP' || p === 'BGP' || p === 'TLS' || p === 'SSL') return 'cap-p-tcp';
  if (p === 'DNS' || p === 'MDNS') return 'cap-p-dns';
  if (p === 'UDP' || p === 'VRRP' || p === 'GRE') return 'cap-p-udp';
  return 'cap-p-other';
};

// Lightweight display filter. Bare substring terms and "<field><op><value>"
// terms (field ∈ proto|src|dst|ip|info, op ∈ == != ~), ANDed together.
const applyDisplayFilter = (pkt, filter) => {
  const f = (filter || '').trim().toLowerCase();
  if (!f) return true;
  const row = `${pkt.src} ${pkt.dst} ${pkt.proto} ${pkt.info}`.toLowerCase();
  return f.split(/\s+/).every((term) => {
    const m = term.match(/^(proto|src|dst|ip|info)(==|!=|~)(.+)$/);
    if (!m) return row.includes(term);
    const [, key, op, val] = m;
    if (key === 'ip') {
      const s = pkt.src.toLowerCase(),
        d = pkt.dst.toLowerCase();
      if (op === '==') return s === val || d === val;
      if (op === '!=') return s !== val && d !== val;
      return s.includes(val) || d.includes(val);
    }
    const hay = { proto: pkt.proto, src: pkt.src, dst: pkt.dst, info: pkt.info }[key].toLowerCase();
    if (op === '==') return hay === val;
    if (op === '!=') return hay !== val;
    return hay.includes(val);
  });
};

// Compress a list of frame numbers into editcap-style ranges ("1-5,9,12-20").
const compressRanges = (nums) => {
  const sorted = [...new Set(nums.map(Number))]
    .filter((n) => Number.isFinite(n))
    .sort((a, b) => a - b);
  const out = [];
  let start = null,
    prev = null;
  for (const n of sorted) {
    if (start === null) {
      start = prev = n;
      continue;
    }
    if (n === prev + 1) {
      prev = n;
      continue;
    }
    out.push(start === prev ? `${start}` : `${start}-${prev}`);
    start = prev = n;
  }
  if (start !== null) out.push(start === prev ? `${start}` : `${start}-${prev}`);
  return out.join(',');
};

const humanizeKey = (k) => k.replace(/_tree$/, '');

// Detail tree node. Collapsed by default; open/closed state is owned by the
// panel (via `expanded`), so it is remembered when switching packets and
// discarded when the panel unmounts.
const DetailNode = ({ label, value, depth, path, expanded, onToggle }) => {
  const isObj = value && typeof value === 'object' && !Array.isArray(value);
  if (!isObj) {
    return (
      <div className="cap-detail-leaf" style={{ paddingLeft: depth * 14 + 8 }}>
        <span className="cap-detail-key">{humanizeKey(label)}</span>
        <span className="cap-detail-val">{String(value)}</span>
      </div>
    );
  }
  const open = expanded.has(path);
  return (
    <div className="cap-detail-branch">
      <div
        className="cap-detail-node"
        style={{ paddingLeft: depth * 14 }}
        onClick={() => onToggle(path)}
      >
        <span className="cap-detail-caret">{open ? '▾' : '▸'}</span>
        <span className="cap-detail-key cap-detail-key-branch">{humanizeKey(label)}</span>
      </div>
      {open &&
        Object.entries(value).map(([k, v]) => (
          <DetailNode
            key={k}
            label={k}
            value={v}
            depth={depth + 1}
            path={`${path}/${k}`}
            expanded={expanded}
            onToggle={onToggle}
          />
        ))}
    </div>
  );
};

const CapturePanel = ({
  captureId,
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
  const ws = useRef(null);
  const packetsRef = useRef([]);
  const dirtyRef = useRef(false);
  const followRef = useRef(true);
  const tableWrapRef = useRef(null);
  const capContainerRef = useRef(null); // ephemeral container id (for reuse on reconnect)
  const capPcapRef = useRef(null); // current run's pcap id (per-run file)
  const intentionalCloseRef = useRef(false);
  const isDraggingRef = useRef(false);
  const dragOffsetRef = useRef({ x: 0, y: 0 });
  const resizeRef = useRef(null);
  const saveWrapRef = useRef(null);
  const positionRef = useRef({ x: 130 + Math.random() * 150, y: 80 + Math.random() * 110 });

  const [tick, setTick] = useState(0);
  const [state, setState] = useState('connecting'); // connecting|starting|capturing|stopped|lost|error
  const [errorMsg, setErrorMsg] = useState(null);
  const [container, setContainer] = useState(null);
  const [displayFilter, setDisplayFilter] = useState('');
  const [filterInput, setFilterInput] = useState('');
  const [selected, setSelected] = useState(null);
  const [detail, setDetail] = useState({ num: null, loading: false, tree: null, error: null });
  const [expanded, setExpanded] = useState(() => new Set());
  const [count, setCount] = useState(0);
  const [widths, setWidths] = useState(() => COLS.map((c) => c.w));
  const [saveMenuOpen, setSaveMenuOpen] = useState(false);

  const togglePath = useCallback((path) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }, []);

  const pushPacket = useCallback((line) => {
    const parts = line.split('\t');
    if (parts.length < 6) return;
    const arr = packetsRef.current;
    arr.push({
      num: parts[0],
      time: parts[1],
      src: parts[2] || '',
      dst: parts[3] || '',
      proto: parts[4] || '',
      len: parts[5] || '',
      info: parts.slice(6).join(' ') || '',
    });
    if (arr.length > MAX_PACKETS) arr.splice(0, arr.length - MAX_PACKETS);
    dirtyRef.current = true;
  }, []);

  const clearMemory = useCallback(() => {
    packetsRef.current = [];
    dirtyRef.current = true;
    setSelected(null);
    setDetail({ num: null, loading: false, tree: null, error: null });
    setTick((t) => t + 1);
    setCount(0);
  }, []);

  const cleanupSocket = useCallback(() => {
    if (ws.current) {
      try {
        ws.current.onopen = null;
        ws.current.onmessage = null;
        ws.current.onerror = null;
        ws.current.onclose = null;
        if (
          ws.current.readyState === WebSocket.OPEN ||
          ws.current.readyState === WebSocket.CONNECTING
        ) {
          ws.current.close(1000, 'Client closing');
        }
      } catch (e) {
        /* ignore */
      }
      ws.current = null;
    }
  }, []);

  const connect = useCallback(() => {
    if (ws.current) return;
    intentionalCloseRef.current = false;
    setErrorMsg(null);
    setState('connecting');
    const reuse = capContainerRef.current ? `?container=${capContainerRef.current}` : '';
    const url = `${WS_BASE_URL}/capture/ws/${namespace}/${encodeURIComponent(pod)}/${encodeURIComponent(iface)}${reuse}`;
    const socket = new WebSocket(url);
    ws.current = socket;

    socket.onmessage = (event) => {
      if (typeof event.data !== 'string') return;
      const text = event.data;
      if (text.length && text[0] === '{') {
        try {
          const msg = JSON.parse(text);
          if (msg && msg.type) {
            if (msg.type === 'meta') {
              capContainerRef.current = msg.container;
              capPcapRef.current = msg.pcap;
              setContainer(msg.container);
            } else if (msg.type === 'status') setState(msg.state);
            else if (msg.type === 'error') {
              setErrorMsg(msg.message);
              setState('error');
            }
            return;
          }
        } catch (e) {
          /* fall through to packet */
        }
      }
      pushPacket(text);
    };
    socket.onclose = () => {
      ws.current = null;
      if (!intentionalCloseRef.current) setState('lost');
    };
  }, [namespace, pod, iface, pushPacket]);

  // Start / resume: a fresh capture run (tshark truncates the pcap on -w), so
  // clear the in-memory list to keep it consistent with the file on disk.
  const start = useCallback(() => {
    intentionalCloseRef.current = true;
    cleanupSocket();
    clearMemory();
    connect();
  }, [cleanupSocket, clearMemory, connect]);

  const stop = useCallback(() => {
    intentionalCloseRef.current = true;
    cleanupSocket();
    setState('stopped');
  }, [cleanupSocket]);

  const handleClear = useCallback(() => {
    const live = state === 'capturing' || state === 'connecting' || state === 'starting';
    if (live) {
      // Restart the run so tshark truncates the pcap → export stays in sync.
      start();
    } else {
      clearMemory();
      if (capContainerRef.current && capPcapRef.current) {
        fetch(
          `${API_BASE_URL}/capture/clear/${namespace}/${encodeURIComponent(pod)}/${capContainerRef.current}?pcap=${capPcapRef.current}`,
          { method: 'POST' }
        ).catch(() => {
          /* best-effort */
        });
      }
    }
  }, [state, start, clearMemory, namespace, pod]);

  const handleClose = useCallback(() => {
    intentionalCloseRef.current = true;
    cleanupSocket();
    onClose && onClose();
  }, [cleanupSocket, onClose]);

  const handleSelect = useCallback(
    (num) => {
      setSelected(num);
      if (!capContainerRef.current || !capPcapRef.current) return;
      setDetail({ num, loading: true, tree: null, error: null });
      fetch(
        `${API_BASE_URL}/capture/packet/${namespace}/${encodeURIComponent(pod)}/${capContainerRef.current}/${num}?pcap=${capPcapRef.current}`
      )
        .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
        .then((arr) => {
          const layers = arr?.[0]?._source?.layers || null;
          setDetail({ num, loading: false, tree: layers, error: layers ? null : 'No dissection' });
        })
        .catch((err) =>
          setDetail({ num, loading: false, tree: null, error: String(err.message || err) })
        );
    },
    [namespace, pod]
  );

  const downloadPcap = useCallback(
    async (filtered) => {
      if (!capContainerRef.current || !capPcapRef.current) return;
      let url = `${API_BASE_URL}/capture/pcap/${namespace}/${encodeURIComponent(pod)}/${capContainerRef.current}?pcap=${capPcapRef.current}`;
      // Filename: <ns>_<host>-<interface>_<timestamp>.pcap
      const d = new Date();
      const p2 = (n) => String(n).padStart(2, '0');
      const ts = `${d.getFullYear()}${p2(d.getMonth() + 1)}${p2(d.getDate())}-${p2(d.getHours())}${p2(d.getMinutes())}${p2(d.getSeconds())}`;
      let name = `${namespace}_${pod}-${iface}_${ts}.pcap`;
      if (filtered) {
        const frames = compressRanges(
          packetsRef.current.filter((p) => applyDisplayFilter(p, displayFilter)).map((p) => p.num)
        );
        if (!frames) return;
        url += `&frames=${encodeURIComponent(frames)}`;
        name = `${namespace}_${pod}-${iface}_${ts}_filtered.pcap`;
      }
      // Fetch into a blob and download via an object URL (same-origin). A
      // direct cross-origin <a download> would be treated as a top-level
      // navigation by the browser (the download attr is ignored cross-origin),
      // which tears down every WebSocket on the page — including this capture
      // and any open pod shell.
      try {
        const res = await fetch(url);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const blob = await res.blob();
        const objUrl = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = objUrl;
        a.download = name;
        document.body.appendChild(a);
        a.click();
        a.remove();
        URL.revokeObjectURL(objUrl);
      } catch (e) {
        console.error('pcap download failed', e);
      }
    },
    [namespace, pod, iface, displayFilter]
  );

  // Column resize.
  const startResize = useCallback(
    (e, i) => {
      e.preventDefault();
      e.stopPropagation();
      resizeRef.current = { i, startX: e.clientX, startW: widths[i] };
      const onMove = (ev) => {
        if (!resizeRef.current) return;
        const { i, startX, startW } = resizeRef.current;
        const dx = ev.clientX - startX;
        setWidths((prev) =>
          prev.map((w, idx) => (idx === i ? Math.max(COLS[i].min, startW + dx) : w))
        );
      };
      const onUp = () => {
        resizeRef.current = null;
        document.removeEventListener('mousemove', onMove);
        document.removeEventListener('mouseup', onUp);
      };
      document.addEventListener('mousemove', onMove);
      document.addEventListener('mouseup', onUp);
    },
    [widths]
  );

  // Flush ring buffer into the UI at a fixed cadence.
  useEffect(() => {
    const id = setInterval(() => {
      if (!dirtyRef.current) return;
      dirtyRef.current = false;
      setCount(packetsRef.current.length);
      setTick((t) => t + 1);
    }, FLUSH_MS);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    connect();
    return () => {
      intentionalCloseRef.current = true;
      cleanupSocket();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (followRef.current && tableWrapRef.current) {
      tableWrapRef.current.scrollTop = tableWrapRef.current.scrollHeight;
    }
  }, [tick]);

  // Close the Save menu on an outside click.
  useEffect(() => {
    if (!saveMenuOpen) return;
    const onDown = (e) => {
      if (saveWrapRef.current && !saveWrapRef.current.contains(e.target)) setSaveMenuOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, [saveMenuOpen]);

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
    if (e.target.closest('button') || e.target.closest('input') || e.target.closest('a')) return;
    isDraggingRef.current = true;
    dragOffsetRef.current = {
      x: e.clientX - positionRef.current.x,
      y: e.clientY - positionRef.current.y,
    };
    e.preventDefault();
  };

  const onTableScroll = () => {
    const el = tableWrapRef.current;
    if (!el) return;
    followRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
  };

  const rows = useMemo(() => {
    const all = packetsRef.current;
    const filtered = displayFilter ? all.filter((p) => applyDisplayFilter(p, displayFilter)) : all;
    return filtered.length > MAX_RENDERED
      ? filtered.slice(filtered.length - MAX_RENDERED)
      : filtered;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tick, displayFilter]);

  const live = state === 'capturing' || state === 'connecting' || state === 'starting';
  const statusLabel =
    {
      connecting: 'connecting…',
      starting: 'starting container…',
      capturing: 'capturing',
      stopped: 'stopped',
      lost: 'disconnected',
      error: 'error',
    }[state] || state;
  const tableWidth = widths.reduce((a, b) => a + b, 0);

  return (
    <div
      ref={containerRef}
      className={`cap-panel${minimized ? ' cap-panel-min' : ''}`}
      onClick={() => {
        if (!minimized && onBringToFront) onBringToFront();
      }}
    >
      <div className="cap-header" onMouseDown={onHeaderMouseDown}>
        <span className={`cap-dot cap-dot-${state}`} title={statusLabel} />
        <PcapIcon style={{ color: '#9ecbff' }} />
        <span className="cap-title">
          <span className="cap-title-pod">{pod}</span>
          <span className="cap-title-iface">· {iface}</span>
        </span>
        <span className="cap-meta">
          {count} pkts · {statusLabel}
        </span>
        <div className="cap-header-btns">
          {live ? (
            <button className="cap-btn cap-btn-stop" onClick={stop} title="Stop capture">
              <span className="cap-glyph-stop" /> Stop
            </button>
          ) : (
            <button
              className="cap-btn cap-btn-start"
              onClick={start}
              title="Start / resume capture"
            >
              <span className="cap-glyph-play" /> Start
            </button>
          )}
          <div className="cap-save-wrap" ref={saveWrapRef}>
            <button
              className={`cap-btn cap-btn-save${container ? '' : ' cap-btn-disabled'}`}
              onClick={() => container && setSaveMenuOpen((o) => !o)}
              disabled={!container}
              title="Export capture (.pcap)"
            >
              ⤓ Save ▾
            </button>
            {saveMenuOpen && (
              <div className="cap-save-menu">
                <button
                  className="cap-save-item"
                  onClick={() => {
                    setSaveMenuOpen(false);
                    downloadPcap(false);
                  }}
                >
                  Whole capture
                </button>
                <button
                  className={`cap-save-item${displayFilter ? '' : ' cap-save-item-disabled'}`}
                  disabled={!displayFilter}
                  onClick={() => {
                    if (!displayFilter) return;
                    setSaveMenuOpen(false);
                    downloadPcap(true);
                  }}
                  title={
                    displayFilter
                      ? 'Export only the filtered packets'
                      : 'Apply a display filter first'
                  }
                >
                  Filtered only
                </button>
              </div>
            )}
          </div>
          <button className="cap-btn cap-btn-clear" onClick={handleClear} title="Clear packet list">
            Clear
          </button>
          {onMinimize && (
            <button className="cap-btn" onClick={onMinimize} title="Minimize">
              −
            </button>
          )}
          <button className="cap-btn cap-btn-close" onClick={handleClose} title="Close capture">
            <CloseIcon className="app-icon" />
          </button>
        </div>
      </div>

      <div className="cap-filterbar">
        <input
          className="cap-filter-input"
          placeholder="display filter — e.g. ospf   icmp   ip==10.0.0.2   proto==tcp"
          spellCheck={false}
          value={filterInput}
          onChange={(e) => setFilterInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') setDisplayFilter(filterInput);
          }}
        />
        <button
          className="cap-btn"
          onClick={() => setDisplayFilter(filterInput)}
          title="Apply filter"
        >
          Apply
        </button>
        {displayFilter && (
          <button
            className="cap-btn"
            onClick={() => {
              setDisplayFilter('');
              setFilterInput('');
            }}
            title="Clear filter"
          >
            <CloseIcon className="app-icon" />
          </button>
        )}
      </div>

      <div className="cap-body">
        <div className="cap-table-wrap" ref={tableWrapRef} onScroll={onTableScroll}>
          <table className="cap-table" style={{ width: tableWidth, tableLayout: 'fixed' }}>
            <colgroup>
              {COLS.map((c, i) => (
                <col key={c.key} style={{ width: widths[i] }} />
              ))}
            </colgroup>
            <thead>
              <tr>
                {COLS.map((c, i) => (
                  <th key={c.key} style={{ textAlign: c.align || 'left' }}>
                    {c.label}
                    {i < COLS.length - 1 && (
                      <span className="cap-col-resizer" onMouseDown={(e) => startResize(e, i)} />
                    )}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((p) => (
                <tr
                  key={p.num}
                  className={`${protoClass(p.proto)}${selected === p.num ? ' cap-row-sel' : ''}`}
                  onClick={() => handleSelect(p.num)}
                >
                  <td className="cap-c-num">{p.num}</td>
                  <td>{Number.isFinite(Number(p.time)) ? Number(p.time).toFixed(6) : p.time}</td>
                  <td>{p.src}</td>
                  <td>{p.dst}</td>
                  <td className="cap-c-proto">{p.proto}</td>
                  <td className="cap-c-num">{p.len}</td>
                  <td className="cap-c-info">{p.info}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {rows.length === 0 && (
            <div className="cap-empty">
              {state === 'capturing'
                ? 'Waiting for packets…'
                : state === 'connecting' || state === 'starting'
                  ? 'Starting capture container — the first use pulls the image, this can take a bit…'
                  : state === 'error'
                    ? errorMsg || 'Capture error'
                    : state === 'stopped'
                      ? 'Capture stopped. Press Start to capture again.'
                      : state === 'lost'
                        ? 'Disconnected. Press Start to reconnect.'
                        : 'No packets'}
            </div>
          )}
        </div>

        <div className="cap-detail">
          {detail.num == null ? (
            <div className="cap-detail-hint">Select a packet to inspect its layers.</div>
          ) : detail.loading ? (
            <div className="cap-detail-hint">Dissecting packet {detail.num}…</div>
          ) : detail.error ? (
            <div className="cap-detail-hint cap-detail-err">{detail.error}</div>
          ) : detail.tree ? (
            <div className="cap-detail-tree">
              {Object.entries(detail.tree).map(([k, v]) => (
                <DetailNode
                  key={k}
                  label={k}
                  value={v}
                  depth={0}
                  path={k}
                  expanded={expanded}
                  onToggle={togglePath}
                />
              ))}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
};

export default CapturePanel;
