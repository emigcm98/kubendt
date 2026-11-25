import React, { useEffect, useRef, useState } from 'react';
import ReactDOM from 'react-dom';
import { ReactComponent as SlidersIcon } from '../assets/images/icons/sliders.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import './Dialog.css';
import './ResultDialog.css';

export default function ResultDialog({ open, onClose, result }) {
  const [pos, setPos] = useState({ x: 0, y: 0 });
  const dragRef = useRef({ dragging: false, startX: 0, startY: 0, origX: 0, origY: 0 });
  const cardRef = useRef(null);

  useEffect(() => {
    if (!open) setPos({ x: 0, y: 0 });
  }, [open]);

  useEffect(() => {
    const onMove = (e) => {
      if (!dragRef.current.dragging) return;
      const rawX = dragRef.current.origX + (e.clientX - dragRef.current.startX);
      const rawY = dragRef.current.origY + (e.clientY - dragRef.current.startY);
      const card = cardRef.current;
      if (card) {
        const w = card.offsetWidth;
        const h = card.offsetHeight;
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
    if (e.target.closest && e.target.closest('.dlg-close')) return;
    dragRef.current = {
      dragging: true,
      startX: e.clientX,
      startY: e.clientY,
      origX: pos.x,
      origY: pos.y,
    };
    e.preventDefault();
  };

  if (!open) return null;

  const {
    status,
    successes = 0,
    failures = 0,
    skipped = 0,
    errors = [],
    action_results = [],
    took_time,
    speedup,
  } = result || {};
  const actionFailures = action_results.filter((a) => a.status === 'failed').length;
  const actionSkipped = action_results.filter((a) => a.status === 'skipped').length;
  const actionSuccesses = action_results.filter((a) => a.status === 'success').length;

  const totalTime = took_time?.total ?? null;
  const seqEquiv = took_time?.sequential_equivalent ?? null;

  const podOrder = [];
  const podGroups = {};
  for (const entry of action_results) {
    const key = entry.resolved_pod || entry.pod;
    if (!podGroups[key]) {
      podOrder.push(key);
      podGroups[key] = {
        actions: [],
        tookMs: entry.pod_took_ms ?? null,
        podLabel: entry.pod,
        resolvedPod: entry.resolved_pod,
      };
    }
    podGroups[key].actions.push(entry);
  }

  const modal = (
    <div className="dlg-overlay dlg-overlay-pass rd-backdrop">
      <div
        className="dlg dlg-mid rd-card"
        role="dialog"
        aria-label="Network configuration"
        ref={cardRef}
        style={{ transform: `translate(${pos.x}px, ${pos.y}px)` }}
      >
        <div className="dlg-head dlg-drag" onMouseDown={handleHeaderMouseDown} title="Drag to move">
          <SlidersIcon className="app-icon" aria-hidden="true" />
          <h2 className="dlg-title">Network configuration</h2>
          <span className={`rd-chip rd-chip-${status === 'success' ? 'ok' : 'warn'}`}>
            {status || 'unknown'}
          </span>
          <button className="dlg-close" onClick={onClose} title="Close">
            <CloseIcon className="app-icon" />
          </button>
        </div>

        <div className="dlg-body rd-body">
          <div className="rd-counters">
            <div className="rd-counter rd-counter-ok">
              <span className="rd-label">Successes</span>
              <span className="rd-value">{successes}</span>
            </div>
            <div className="rd-counter rd-counter-skip">
              <span className="rd-label">Skipped</span>
              <span className="rd-value">{skipped}</span>
            </div>
            <div className="rd-counter rd-counter-err">
              <span className="rd-label">Failures</span>
              <span className="rd-value">{failures}</span>
            </div>
          </div>

          {totalTime && (
            <div className="rd-timing">
              <span>
                Total time <b>{totalTime}</b>
              </span>
              {seqEquiv && (
                <span>
                  Sequential equivalent <b>{seqEquiv}</b>
                  {speedup != null && (
                    <span className="rd-speedup">×{speedup.toFixed(1)} speedup</span>
                  )}
                </span>
              )}
            </div>
          )}

          {errors.length > 0 && (
            <div className="rd-errors">
              <div className="dlg-label">Errors</div>
              <ul>
                {errors.map((e, i) => (
                  <li key={i}>
                    pod=<b>{e.pod}</b> · driver=<b>{e.driver}</b> · error={e.error}
                  </li>
                ))}
              </ul>
            </div>
          )}

          <details className="rd-actions" open>
            <summary>
              Actions ({action_results.length})
              {action_results.length > 0 && (
                <span className="rd-actions-meta">
                  {actionSuccesses} ok · {actionSkipped} skipped · {actionFailures} failed
                </span>
              )}
            </summary>

            {action_results.length === 0 ? (
              <div className="rd-actions-empty">No action details returned by backend.</div>
            ) : (
              <div className="rd-actions-list">
                {podOrder.map((podKey) => {
                  const g = podGroups[podKey];
                  const podOk = g.actions.filter((a) => a.status === 'success').length;
                  const podFail = g.actions.filter((a) => a.status === 'failed').length;
                  const podSkip = g.actions.filter((a) => a.status === 'skipped').length;
                  const podHasError = podFail > 0;
                  const tookSec = g.tookMs != null ? (g.tookMs / 1000).toFixed(2) : null;

                  return (
                    <div key={podKey} className={`rd-pod-group ${podHasError ? 'has-error' : ''}`}>
                      <div className="rd-pod-header">
                        <span className="rd-pod-name">
                          {g.podLabel}
                          {g.resolvedPod && g.resolvedPod !== g.podLabel && (
                            <span className="rd-pod-resolved"> ({g.resolvedPod})</span>
                          )}
                        </span>
                        <span className="rd-pod-stats">
                          <span className="rd-stat ok">{podOk} ok</span>
                          {podSkip > 0 && <span className="rd-stat skip">{podSkip} skip</span>}
                          {podFail > 0 && <span className="rd-stat err">{podFail} fail</span>}
                          {tookSec && <span className="rd-stat time">{tookSec} s</span>}
                        </span>
                      </div>

                      <div className="rd-pod-actions">
                        {g.actions.map((entry, i) => (
                          <div key={i} className="rd-action-item">
                            <div className="rd-action-head">
                              <span
                                className={`rd-action-status ${entry.status === 'success' ? 'ok' : entry.status === 'skipped' ? 'skip' : 'err'}`}
                              >
                                {entry.status}
                              </span>
                              <span className="rd-action-main">
                                <b>{entry.action}</b>
                              </span>
                            </div>

                            {entry.commands?.length > 0 && (
                              <div className="rd-action-cmds">
                                {entry.commands.map((cmd, idx) => (
                                  <code key={idx}>{cmd}</code>
                                ))}
                              </div>
                            )}

                            {entry.error && <div className="rd-action-error">{entry.error}</div>}
                          </div>
                        ))}
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </details>
        </div>

        <div className="dlg-foot">
          <button className="dlg-btn dlg-btn-primary" onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  );

  return ReactDOM.createPortal(modal, document.body);
}
