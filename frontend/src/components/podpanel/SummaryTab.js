import React from 'react';
import { createPortal } from 'react-dom';
import { capabilityCode } from './history';

// The pod's card: identity, placement, status and the metrics the parent
// polls while the tab is open.
const SummaryTab = ({
  pod,
  runtimeLabel,
  driverDisplay,
  driverCaps,
  podMetrics,
  onCopyImage,
  copyFloater,
}) => (
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
      <span className="pod-value">{runtimeLabel}</span>
    </div>
    {driverCaps?.some((cap) => !cap.universal) && (
      <div className="pod-line">
        <span className="pod-label">Capabilities:</span>
        <span
          className="pod-value"
          title={driverCaps
            .filter((cap) => !cap.universal)
            .map((cap) => cap.label || cap.id)
            .join(', ')}
        >
          {driverCaps
            .filter((cap) => !cap.universal)
            .map(capabilityCode)
            .join(', ')}
        </span>
      </div>
    )}
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
    <hr />
    <div className="pod-metrics-hint">updated every 10s</div>
    <div className="pod-metrics-block">
      {[
        ['CPU', (m) => `${(m.cpu_milli / 1000).toFixed(2)}`, 'vCPU'],
        ['RAM', (m) => `${m.memory_mib}`, 'MiB'],
      ].map(([label, value, unit]) => (
        <div className="pod-metrics-item" key={label}>
          <span className="pod-metrics-label">{label}</span>
          {podMetrics === null ? (
            <span className="pod-metrics-value pod-metrics-loading">…</span>
          ) : podMetrics === false ? (
            <span className="pod-metrics-value pod-metrics-na">
              n/a
              <span className="pod-metrics-tooltip">Error: metrics-server not available.</span>
            </span>
          ) : (
            <span className="pod-metrics-value">
              {value(podMetrics)} <em>{unit}</em>
            </span>
          )}
        </div>
      ))}
    </div>
  </>
);

export default SummaryTab;
