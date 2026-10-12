import React, { useEffect, useMemo, useRef, useState } from 'react';
import { WS_BASE_URL } from '../config';
import useDraggableWindow from './useDraggableWindow';
import { ReactComponent as LogsIcon } from '../assets/images/icons/logs.svg';
import { ReactComponent as DownloadIcon } from '../assets/images/icons/download.svg';
import { ReactComponent as CopyIcon } from '../assets/images/icons/copy.svg';
import './PodLogsModal.css';

// A floating viewer of a pod's container log, of the same family as the
// shell window but a viewer, not a terminal: the stream from
// /pods/logs/ws sends the tail and then follows, the viewer keeps a bounded
// number of lines, follows the end unless the user scrolls up, and filters
// on the client.
const MAX_LINES = 5000;
const TAIL_OPTIONS = [200, 500, 2000, 0];
// Built from the code so the lint rule on control characters in literals
// stays quiet: ESC [ ... letter, the colour and cursor sequences.
const ANSI = new RegExp(`${String.fromCharCode(27)}\\[[0-9;]*[A-Za-z]`, 'g');

const PodLogsModal = ({
  logId,
  podName,
  namespace,
  onClose,
  onMinimize,
  minimized = false,
  zIndex = 1000,
  onBringToFront,
}) => {
  const containerRef = useRef(null);
  const bodyRef = useRef(null);
  const wsRef = useRef(null);
  const decoder = useMemo(() => new TextDecoder(), []);
  const onHeaderMouseDown = useDraggableWindow({ containerRef, onBringToFront });

  const [lines, setLines] = useState([]);
  const [meta, setMeta] = useState(null);
  const [status, setStatus] = useState('connecting'); // connecting, open, ended, lost
  const [follow, setFollow] = useState(true);
  const [filter, setFilter] = useState('');
  const [tail, setTail] = useState(500);
  const [timestamps, setTimestamps] = useState(false);
  const [previous, setPrevious] = useState(false);
  const [container, setContainer] = useState('');

  // One stream per set of options: changing any of them reopens from scratch.
  useEffect(() => {
    if (!namespace || !podName) return undefined;
    setLines([]);
    setStatus('connecting');
    const params = new URLSearchParams({ tail: String(tail) });
    if (timestamps) params.set('timestamps', '1');
    if (previous) params.set('previous', '1');
    if (container) params.set('container', container);
    const ws = new WebSocket(`${WS_BASE_URL}/pods/logs/ws/${namespace}/${podName}?${params}`);
    ws.binaryType = 'arraybuffer';
    wsRef.current = ws;
    let pending = [];
    let flush = null;
    // Lines arrive one per frame; batch them into one state update per tick.
    const schedule = () => {
      if (flush) return;
      flush = setTimeout(() => {
        flush = null;
        const batch = pending;
        pending = [];
        setLines((prev) => {
          const next = prev.concat(batch);
          return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
        });
      }, 50);
    };
    ws.onopen = () => setStatus('open');
    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        try {
          const frame = JSON.parse(ev.data);
          if (frame.kind === 'meta') setMeta(frame.meta);
          else if (frame.kind === 'end') setStatus('ended');
          else if (frame.kind === 'error') {
            pending.push(`[kubendt] ${frame.error}`);
            schedule();
            setStatus('ended');
          }
        } catch {
          /* not a control frame, ignore */
        }
        return;
      }
      pending.push(decoder.decode(ev.data).replace(ANSI, ''));
      schedule();
    };
    ws.onclose = () => setStatus((s) => (s === 'ended' ? s : 'lost'));
    ws.onerror = () => setStatus('lost');
    return () => {
      clearTimeout(flush);
      ws.onopen = ws.onmessage = ws.onclose = ws.onerror = null;
      try {
        ws.close(1000, 'Client closing');
      } catch {
        /* already closed */
      }
      wsRef.current = null;
    };
  }, [namespace, podName, tail, timestamps, previous, container, decoder]);

  // Follow the end unless the user has scrolled up to read.
  useEffect(() => {
    const el = bodyRef.current;
    if (follow && el && !minimized) el.scrollTop = el.scrollHeight;
  }, [lines, follow, minimized]);

  const onScroll = () => {
    const el = bodyRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 8;
    if (!atBottom && follow) setFollow(false);
  };

  const needle = filter.trim().toLowerCase();
  const visible = needle ? lines.filter((l) => l.toLowerCase().includes(needle)) : lines;

  const download = () => {
    const blob = new Blob([visible.join('\n') + '\n'], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${podName}${meta?.container ? `-${meta.container}` : ''}.log`;
    a.click();
    URL.revokeObjectURL(url);
  };

  const copy = () => {
    navigator.clipboard?.writeText(visible.join('\n')).catch(() => {});
  };

  const statusText = {
    connecting: 'connecting…',
    open: 'live',
    ended: 'ended',
    lost: 'connection lost',
  }[status];

  return (
    <div
      ref={containerRef}
      className={`shell-modal-container logs-modal${minimized ? ' shell-modal-minimized' : ''}`}
      style={{ zIndex }}
      onClick={() => {
        if (!minimized && onBringToFront) onBringToFront();
      }}
      data-log-id={logId}
    >
      <div className="shell-modal-header" onMouseDown={onHeaderMouseDown}>
        <div className="shell-modal-title">
          <LogsIcon className="logs-title-icon" aria-hidden="true" />
          <span className="shell-pod-name">{podName}</span>
          <span className="shell-pod-status">
            {' '}
            - Logs{meta?.container ? ` (${meta.container})` : ''} · {statusText}
          </span>
        </div>
        <div className="shell-modal-buttons">
          <button
            className="shell-modal-clear-btn"
            title="Clear the view"
            onClick={() => setLines([])}
          >
            Clear
          </button>
          {onMinimize && (
            <button className="shell-modal-minimize-btn" title="Minimize" onClick={onMinimize}>
              −
            </button>
          )}
          <button onClick={onClose} className="shell-modal-close-btn" title="Close logs">
            ✖
          </button>
        </div>
      </div>

      <div className="logs-toolbar">
        <input
          type="text"
          className="logs-filter"
          placeholder="Filter lines…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <label className="logs-toggle" title="Scroll with new lines">
          <input
            type="checkbox"
            checked={follow}
            onChange={(e) => {
              setFollow(e.target.checked);
              if (e.target.checked && bodyRef.current) {
                bodyRef.current.scrollTop = bodyRef.current.scrollHeight;
              }
            }}
          />
          Follow
        </label>
        <label className="logs-toggle" title="Prefix each line with its time">
          <input
            type="checkbox"
            checked={timestamps}
            onChange={(e) => setTimestamps(e.target.checked)}
          />
          Timestamps
        </label>
        {meta?.restarts > 0 && (
          <label
            className="logs-toggle"
            title={`The container restarted ${meta.restarts} time(s), read the log of the previous instance`}
          >
            <input
              type="checkbox"
              checked={previous}
              onChange={(e) => setPrevious(e.target.checked)}
            />
            Previous
          </label>
        )}
        <select
          className="logs-select"
          value={tail}
          onChange={(e) => setTail(Number(e.target.value))}
          title="How many lines to start with"
        >
          {TAIL_OPTIONS.map((n) => (
            <option key={n} value={n}>
              {n === 0 ? 'whole log' : `last ${n}`}
            </option>
          ))}
        </select>
        {meta?.containers?.length > 1 && (
          <select
            className="logs-select"
            value={container || meta.container}
            onChange={(e) => setContainer(e.target.value)}
            title="Container to read"
          >
            {meta.containers.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        )}
        <span className="logs-count">
          {needle ? `${visible.length} of ${lines.length}` : `${lines.length} lines`}
        </span>
        <button className="logs-icon-btn" title="Copy the visible lines" onClick={copy}>
          <CopyIcon className="app-icon" aria-hidden="true" />
        </button>
        <button className="logs-icon-btn" title="Download the visible lines" onClick={download}>
          <DownloadIcon className="app-icon" aria-hidden="true" />
        </button>
      </div>

      <div ref={bodyRef} className="logs-body" onScroll={onScroll}>
        {visible.length === 0 ? (
          <div className="logs-empty">
            {status === 'connecting'
              ? 'Reading…'
              : needle
                ? 'No line matches the filter'
                : 'The log is empty'}
          </div>
        ) : (
          visible.map((line, i) => (
            <div key={i} className="logs-line">
              {line}
            </div>
          ))
        )}
      </div>
    </div>
  );
};

export default PodLogsModal;
