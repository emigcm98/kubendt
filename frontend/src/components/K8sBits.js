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

const SUPPORT_NOTES = {
  supported: 'Supported by this KubeNDT.',
  development: 'A development build of KubeNDT Meshnet, not a release.',
  'too-old': 'Older than this KubeNDT works with.',
  untested: 'Newer than this KubeNDT was tried with.',
  unsupported: 'Not a Meshnet this KubeNDT knows.',
};

// What a Meshnet is called: the fork is KubeNDT Meshnet, with its release or,
// for a development build, its commit. The API's flavour stays "fork".
export const meshnetName = (m) => {
  if (m.flavour === 'fork') {
    if (m.version) return `KubeNDT Meshnet ${m.version}`;
    return m.commit ? `KubeNDT Meshnet · dev ${m.commit}` : 'KubeNDT Meshnet · dev';
  }
  return m.flavour === 'stock' ? 'Meshnet (networkop)' : 'Meshnet (not recognised)';
};

// Which Meshnet the cluster runs, from the status's meshnet block: the name,
// what a hover tells and whether something about it needs a look.
export const meshnetFlavour = (m) => {
  if (!m || !m.flavour) return null;
  const text = meshnetName(m);
  const images = m.images || [];
  const where =
    images.length > 1
      ? images.map((i) => `${i.image} on ${i.nodes} node${i.nodes === 1 ? '' : 's'}`)
      : [m.image];
  const warnings = m.warnings || [];
  const title = [...where, SUPPORT_NOTES[m.support], ...warnings].filter(Boolean).join('\n');
  // What is worth saying outside the hover: a version out of range and what
  // does not fit between the daemon, the nodes and the CRD.
  const off = ['too-old', 'untested', 'unsupported'].includes(m.support);
  const notes = [off && `${text}: ${SUPPORT_NOTES[m.support]}`, ...warnings].filter(Boolean);
  return { text, title, url: m.url || null, warn: notes.length > 0, notes };
};

// A Meshnet's name, a link to its source when it is one we know.
export const MeshnetLink = ({ text, url, title, warn }) => {
  const cls = `k8s-flavour${warn ? ' k8s-flavour-warn' : ''}`;
  return url ? (
    <a className={cls} href={url} target="_blank" rel="noopener noreferrer" title={title}>
      {text}
    </a>
  ) : (
    <span className={cls} title={title}>
      {text}
    </span>
  );
};

// The line that says which CNI the cluster runs.
export const MeshnetFlavour = ({ meshnet }) => {
  const f = meshnetFlavour(meshnet);
  if (!f) return null;
  return (
    <div className="k8s-cni">
      <span className="k8s-cni-label">CNI</span>
      <MeshnetLink {...f} />
    </div>
  );
};

const isPlane = (node) => (node.roles || []).some((r) => r === 'control-plane' || r === 'master');

// 'plane' for a control plane, 'worker' for the rest.
export const nodeKind = (node) => (isPlane(node) ? 'plane' : 'worker');

// The class that paints a node's tile with its kind's stripe.
export const nodeStripe = (node) => `k8s-stripe-${nodeKind(node)}`;

// One verdict on the nodes: one counts when it is Ready and has the Meshnet
// pod it needs. Where the DaemonSet is fully ready, a node with no pod is one
// it does not target, not one that failed. Without a Meshnet reading the count
// is of the Ready nodes alone. `problems` are what the page says in plain text.
export const clusterHealth = (status) => {
  const nodes = status?.nodes || [];
  if (nodes.length === 0) return null;
  const m = status.meshnet || {};
  const checked = ['ok', 'degraded', 'missing'].includes(m.state);
  const down = (n) => checked && n.meshnet === 'not-running' && m.state !== 'ok';
  const problems = [];
  if (m.state === 'missing') {
    problems.push('No Meshnet found. Topologies deploy but their links are not wired.');
  }
  nodes.forEach((n) => {
    if (n.status !== 'Ready') problems.push(`${n.name} is ${n.status}.`);
    if (down(n) && m.state === 'degraded') {
      problems.push(`No ready Meshnet pod on ${n.name}. Links landing there are not wired.`);
    }
  });
  const working = nodes.filter((n) => n.status === 'Ready' && !down(n)).length;
  const cls =
    m.state === 'missing' || nodes.some(down)
      ? 'bad'
      : working < nodes.length || m.state === 'unknown'
        ? 'warn'
        : 'ok';
  const unchecked = m.state === 'unknown' || !m.state;
  const count = `${working}/${nodes.length} ${unchecked ? 'ready' : 'working'}`;
  // With no Meshnet at all deploys are blocked, so the pill says it in words.
  const text = m.state === 'missing' ? 'No Meshnet' : count;
  const title = problems.length
    ? problems.join('\n')
    : unchecked
      ? `Every node is Ready. ${m.message || 'Meshnet was not checked.'}`
      : 'Every node is Ready and runs Meshnet.';
  return { cls, text, title, problems };
};

// The nodes' state as one pill, the same on the Home and the cluster page.
export const HealthPill = ({ status }) => {
  const h = clusterHealth(status);
  if (!h) return null;
  return (
    <span className={`k8s-pill k8s-pill-${h.cls}`} title={h.title}>
      {h.text}
    </span>
  );
};

// A node's role, the control plane quieter than the workers.
export const NodeRole = ({ node }) => {
  const role = (node.roles || [])[0];
  if (!role) return null;
  return (
    <span className={`k8s-role${isPlane(node) ? ' k8s-role-plane' : ''}`}>
      {role === 'control-plane' ? 'control plane' : role}
    </span>
  );
};

// A word for the node the scheduler keeps lab pods off.
export const NoLabs = ({ node }) =>
  node.schedulable === false ? (
    <span
      className="k8s-nolabs"
      title="Cordoned or tainted, so the scheduler places no lab pods on this node."
    >
      no labs
    </span>
  ) : null;

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
