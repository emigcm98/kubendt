import React, { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { capabilityCode } from './history';
import { imageKnown } from './images';

// The pod's card: identity, placement, status and the metrics the parent
// polls while the tab is open.
const SummaryTab = ({
  pod,
  runtimeLabel,
  runtimeNote,
  driverDisplay,
  driverCaps,
  driverKnownImages,
  podMetrics,
  onCopyImage,
  copyFloater,
}) => {
  // Three capabilities and a count, so every card has the same rows; the
  // count unfolds the rest in place. Folded again on another pod.
  const [capsOpen, setCapsOpen] = useState(false);
  useEffect(() => setCapsOpen(false), [pod.name]);
  const caps = (driverCaps || []).filter((cap) => !cap.universal);
  const codes = caps.map(capabilityCode);
  const shown = capsOpen ? codes : codes.slice(0, 3);
  const hidden = codes.length - shown.length;
  const capTitle = caps.map((cap) => cap.label || cap.id).join(', ');
  return (
    <>
      <div className="pod-line">
        <span className="pod-label">Node name:</span>
        <span className="pod-value">{pod.baseName || pod.name}</span>
      </div>
      <div className="pod-line">
        <span className="pod-label">Pod name:</span>
        <span className="pod-value">{pod.name}</span>
      </div>
      <div className="pod-line">
        <span className="pod-label">Replica:</span>
        <span className="pod-value">
          {pod.replicaCount > 1 ? `${pod.replicaIndex + 1}/${pod.replicaCount}` : '1/1'}
        </span>
      </div>
      <div className="pod-line">
        <span className="pod-label">Type:</span>
        <span className={`pod-value type-${pod.type.toLowerCase()}`}>{pod.type}</span>
      </div>
      <div className="pod-line">
        <span className="pod-label">Driver:</span>
        <span className="pod-value">{driverDisplay}</span>
      </div>
      <div className="pod-line">
        <span className="pod-label">Runtime:</span>
        <span className="pod-value" title={runtimeNote}>
          {runtimeLabel}
        </span>
      </div>
      <div className="pod-line">
        <span className="pod-label">Capabilities:</span>
        <span className="pod-value" title={capTitle}>
          {!driverCaps ? '…' : codes.length === 0 ? 'none' : shown.join(', ')}
          {(hidden > 0 || capsOpen) && (
            <button type="button" className="pod-value-more" onClick={() => setCapsOpen((v) => !v)}>
              {capsOpen ? 'less' : `and ${hidden} more`}
            </button>
          )}
        </span>
      </div>
      <hr />
      <div className="pod-line">
        <span className="pod-label">K8s Node:</span>
        <span className="pod-value">{pod.node}</span>
      </div>
      <div className="pod-line">
        <span className="pod-label">Image:</span>
        <span className="pod-value pod-value-image" title={pod.image} onClick={onCopyImage}>
          {pod.image}
        </span>
        {driverKnownImages?.length > 0 && !imageKnown(driverKnownImages, pod.image) && (
          <span
            className="pod-image-untested"
            title={`Not an image ${driverDisplay} was tried with: ${driverKnownImages.join(', ')}`}
          >
            untested
          </span>
        )}
      </div>
      {copyFloater &&
        createPortal(
          <span
            key={copyFloater.id}
            className="pod-image-copied-floater"
            style={{ left: copyFloater.x, top: copyFloater.y }}
          >
            Copied!
          </span>,
          document.body
        )}
      <hr />
      <div className="pod-line">
        <span className="pod-label">Status:</span>
        <span className={`pod-value podinfo-status podinfo-status-${pod.status.toLowerCase()}`}>
          {pod.status}
        </span>
      </div>
      <div className="pod-line">
        <span className="pod-label">Created:</span>
        <span className="pod-value">{pod.createdAt}</span>
      </div>
      <div className="pod-line">
        <span className="pod-label">Uptime:</span>
        <span className="pod-value">{pod.uptime}</span>
      </div>
      <div className="pod-line" title="CPU and memory from metrics-server, read every 10 s">
        <span className="pod-label">Usage:</span>
        <span className="pod-value pod-metrics">
          {podMetrics === null ? (
            <span className="pod-metrics-loading">…</span>
          ) : podMetrics === false ? (
            <span className="pod-metrics-na" title="metrics-server is not available">
              n/a
            </span>
          ) : (
            <>
              <span className="pod-metrics-value">
                {(podMetrics.cpu_milli / 1000).toFixed(2)} <em>vCPU</em>
              </span>
              <span className="pod-metrics-value">
                {podMetrics.memory_mib} <em>MiB</em>
              </span>
            </>
          )}
        </span>
      </div>
    </>
  );
};

export default SummaryTab;
