import React, { useEffect, useState } from 'react';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { API_BASE_URL } from '../config';
import './K8sNodeInfoPanel.css';

// Format byte counts into a compact human-readable string ("4.32 GiB").
const formatBytes = (bytes) => {
  if (!bytes || bytes <= 0) return '0';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let n = bytes;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(n >= 100 ? 0 : n >= 10 ? 1 : 2)} ${units[i]}`;
};

// Render "423m → 0.42 cores" style strings for CPU.
const formatCpu = (milli) => {
  if (!milli || milli <= 0) return '0';
  if (milli >= 1000) return `${(milli / 1000).toFixed(2)} cores`;
  return `${milli}m`;
};

const sortEntries = (obj) => Object.entries(obj || {}).sort(([a], [b]) => a.localeCompare(b));

// Maps the per-node meshnet state to a chip. Null hides it (older backends
// that don't report the field).
const meshnetChip = (state) => {
  switch (state) {
    case 'running':
      return { cls: 'ok', label: 'Meshnet', title: 'A ready meshnet pod is running on this node.' };
    case 'not-running':
      return {
        cls: 'down',
        label: 'Meshnet down',
        title: 'No ready meshnet pod on this node. Links landing here will not be wired.',
      };
    case 'unknown':
      return {
        cls: 'unknown',
        label: 'Meshnet ?',
        title: 'Could not check meshnet on this node (no permission to list pods).',
      };
    default:
      return null;
  }
};

const Section = ({ title, children, count }) => (
  <section className="kni-section">
    <h3 className="kni-section-title">
      {title}
      {typeof count === 'number' && <span className="kni-section-count">{count}</span>}
    </h3>
    <div className="kni-section-body">{children}</div>
  </section>
);

const KV = ({ k, v, mono }) => (
  <div className="kni-kv">
    <span className="kni-kv-key">{k}</span>
    <span
      className={`kni-kv-value${mono ? ' kni-mono' : ''}`}
      title={typeof v === 'string' ? v : undefined}
    >
      {v ?? <span className="kni-na">—</span>}
    </span>
  </div>
);

const K8sNodeInfoPanel = ({ nodeName, onClose }) => {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  useEffect(() => {
    if (!nodeName) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetch(`${API_BASE_URL}/cluster/nodes/${encodeURIComponent(nodeName)}`)
      .then(async (res) => {
        if (!res.ok) {
          const payload = await res.json().catch(() => null);
          throw new Error(payload?.error || `HTTP ${res.status}`);
        }
        return res.json();
      })
      .then((d) => {
        if (!cancelled) setData(d);
      })
      .catch((e) => {
        if (!cancelled) setError(e.message || String(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [nodeName]);

  // Close on Escape, standard drawer behaviour.
  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') onClose?.();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  if (!nodeName) return null;

  const status = data?.status || 'Unknown';
  const statusClass = status === 'Ready' ? 'ready' : status === 'NotReady' ? 'notready' : 'unknown';

  return (
    <>
      <div className="kni-backdrop" onClick={onClose} />
      <aside className="kni-drawer" role="dialog" aria-label={`Node info: ${nodeName}`}>
        <header className="kni-header">
          <div className="kni-header-main">
            <div className="kni-header-eyebrow">Kubernetes node</div>
            <h2 className="kni-header-title">{nodeName}</h2>
            <div className="kni-header-meta">
              {data?.roles?.map((r) => (
                <span key={r} className="kni-chip">
                  {r}
                </span>
              ))}
              <span className={`kni-status kni-status-${statusClass}`}>
                <span className="kni-status-dot" />
                {status}
              </span>
              {(() => {
                const chip = meshnetChip(data?.meshnet);
                return chip ? (
                  <span className={`kni-meshnet kni-meshnet-${chip.cls}`} title={chip.title}>
                    <span className="kni-status-dot" />
                    {chip.label}
                  </span>
                ) : null;
              })()}
            </div>
          </div>
          <button className="kni-close" onClick={onClose} title="Close (Esc)">
            <CloseIcon className="app-icon" />
          </button>
        </header>

        <div className="kni-body">
          {loading && (
            <div className="kni-loading">
              <div className="kni-spinner" />
              <span>Loading node info…</span>
            </div>
          )}

          {!loading && error && (
            <div className="kni-error">
              <WarningIcon className="app-icon" /> Could not load node info: {error}
            </div>
          )}

          {!loading && !error && data && (
            <>
              {/* Live usage on top, single line of two bars */}
              <Section title="Live usage">
                <div className="kni-usage-row">
                  <div className="kni-usage-cell">
                    <div className="kni-usage-label">
                      <span>CPU</span>
                      <span className="kni-mono">
                        {data.cpu_milli_usage > 0
                          ? `${formatCpu(data.cpu_milli_usage)} / ${formatCpu(data.capacity?.cpu_milli)} (${data.cpu_percentage.toFixed(1)}%)`
                          : 'metrics unavailable'}
                      </span>
                    </div>
                    <div className="kni-usage-bar">
                      <div
                        className="kni-usage-bar-fill kni-bar-cpu"
                        style={{ '--kni-fill': `${Math.min(data.cpu_percentage || 0, 100)}%` }}
                      />
                    </div>
                  </div>
                  <div className="kni-usage-cell">
                    <div className="kni-usage-label">
                      <span>RAM</span>
                      <span className="kni-mono">
                        {data.memory_bytes_usage > 0
                          ? `${formatBytes(data.memory_bytes_usage)} / ${formatBytes(data.capacity?.memory_bytes)} (${data.memory_percentage.toFixed(1)}%)`
                          : 'metrics unavailable'}
                      </span>
                    </div>
                    <div className="kni-usage-bar">
                      <div
                        className="kni-usage-bar-fill kni-bar-mem"
                        style={{ '--kni-fill': `${Math.min(data.memory_percentage || 0, 100)}%` }}
                      />
                    </div>
                  </div>
                </div>
              </Section>

              <Section title="Network">
                <KV k="Pod CIDR" v={data.pod_cidr} mono />
                {data.pod_cidrs?.length > 1 && (
                  <KV k="Pod CIDRs (extra)" v={data.pod_cidrs.slice(1).join(', ')} mono />
                )}
                {data.addresses?.map((a) => (
                  <KV key={`${a.type}-${a.address}`} k={a.type} v={a.address} mono />
                ))}
              </Section>

              <Section title="OS & runtime">
                <KV k="OS image" v={data.os_info?.os_image} />
                <KV k="Kernel" v={data.os_info?.kernel_version} mono />
                <KV k="Architecture" v={data.os_info?.architecture} mono />
                <KV k="Container runtime" v={data.versions?.container_runtime_version} mono />
                <KV k="Kubelet" v={data.versions?.kubelet_version} mono />
                <KV k="Created" v={data.creation_timestamp} mono />
              </Section>

              <Section title="Capacity vs allocatable">
                <div className="kni-cap-row">
                  <div className="kni-cap-cell">
                    <span className="kni-cap-label">CPU</span>
                    <span className="kni-mono">
                      {formatCpu(data.allocatable?.cpu_milli)} /{' '}
                      {formatCpu(data.capacity?.cpu_milli)}
                    </span>
                  </div>
                  <div className="kni-cap-cell">
                    <span className="kni-cap-label">RAM</span>
                    <span className="kni-mono">
                      {formatBytes(data.allocatable?.memory_bytes)} /{' '}
                      {formatBytes(data.capacity?.memory_bytes)}
                    </span>
                  </div>
                  <div className="kni-cap-cell">
                    <span className="kni-cap-label">Pods</span>
                    <span className="kni-mono">
                      {data.allocatable?.pods} / {data.capacity?.pods}
                    </span>
                  </div>
                  <div className="kni-cap-cell">
                    <span className="kni-cap-label">Ephemeral</span>
                    <span className="kni-mono">
                      {formatBytes(data.allocatable?.storage_ephemeral_bytes)} /{' '}
                      {formatBytes(data.capacity?.storage_ephemeral_bytes)}
                    </span>
                  </div>
                </div>
              </Section>

              <Section title="Conditions" count={data.conditions?.length}>
                <div className="kni-conditions">
                  {data.conditions?.map((c) => {
                    // For "Ready", True is good. For pressure/unavailable, False is good.
                    const isGood = c.type === 'Ready' ? c.status === 'True' : c.status === 'False';
                    return (
                      <div
                        key={c.type}
                        className={`kni-condition kni-condition-${isGood ? 'good' : 'bad'}`}
                        title={c.message || c.reason || ''}
                      >
                        <span className="kni-condition-dot" />
                        <span className="kni-condition-type">{c.type}</span>
                        <span className="kni-condition-status">{c.status}</span>
                      </div>
                    );
                  })}
                </div>
              </Section>

              {data.taints?.length > 0 && (
                <Section title="Taints" count={data.taints.length}>
                  {data.taints.map((t, i) => (
                    <div key={`${t.key}-${i}`} className="kni-taint kni-mono">
                      <span className="kni-taint-key">{t.key}</span>
                      {t.value && <span className="kni-taint-eq">={t.value}</span>}
                      <span className="kni-taint-effect">:{t.effect}</span>
                    </div>
                  ))}
                </Section>
              )}

              <Section title="Labels" count={Object.keys(data.labels || {}).length}>
                <div className="kni-tags">
                  {sortEntries(data.labels).map(([k, v]) => (
                    <span key={k} className="kni-tag kni-mono" title={`${k}=${v}`}>
                      <span className="kni-tag-key">{k}</span>
                      {v && <span className="kni-tag-value">={v}</span>}
                    </span>
                  ))}
                </div>
              </Section>

              {Object.keys(data.annotations || {}).length > 0 && (
                <Section title="Annotations" count={Object.keys(data.annotations).length}>
                  <details className="kni-collapse">
                    <summary>Show / hide</summary>
                    <div className="kni-tags">
                      {sortEntries(data.annotations).map(([k, v]) => (
                        <div key={k} className="kni-kv kni-kv-multiline">
                          <span className="kni-kv-key kni-mono">{k}</span>
                          <span className="kni-kv-value kni-mono kni-pre">{v}</span>
                        </div>
                      ))}
                    </div>
                  </details>
                </Section>
              )}
            </>
          )}
        </div>
      </aside>
    </>
  );
};

export default K8sNodeInfoPanel;
