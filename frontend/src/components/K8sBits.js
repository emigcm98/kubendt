import React from 'react';
import './K8sBits.css';

// A usage bar, CPU in the primary blue and RAM in green.
export const Meter = ({ value, kind }) => (
  <div className={`k8s-meter${kind ? ` k8s-meter-${kind}` : ''}`}>
    <i style={{ width: `${Math.max(0, Math.min(100, value || 0))}%` }} />
  </div>
);

// The Meshnet dot of a node: running, not running, or not known.
export const meshnetDot = (state) =>
  state === 'running' ? 'ok' : state === 'not-running' ? 'bad' : 'off';

// Kubernetes quantities ("250m", "6", "3.8Gi", "4012345Ki") as numbers.
export const vcpu = (q) => {
  if (!q || q === 'N/A') return null;
  if (q.endsWith('m')) return parseFloat(q) / 1000;
  if (q.endsWith('n')) return parseFloat(q) / 1e9;
  return parseFloat(q);
};

export const gib = (q) => {
  if (!q || q === 'N/A') return null;
  const n = parseFloat(q);
  if (q.endsWith('Gi')) return n;
  if (q.endsWith('Mi')) return n / 1024;
  if (q.endsWith('Ki')) return n / (1024 * 1024);
  return n / 1024 ** 3;
};

export const fixed = (n, digits) =>
  n === null || n === undefined || Number.isNaN(n) ? '?' : n.toFixed(digits);
