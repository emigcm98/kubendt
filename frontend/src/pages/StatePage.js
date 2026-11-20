import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { API_BASE_URL } from '../config';
import '../components/NamespaceNavbar.css';
import './StatePage.css';
import kubendtLogo from '../assets/images/kubendt-logo.svg';
import { ReactComponent as HomeIcon } from '../assets/images/icons/home.svg';
import { ReactComponent as ClusterIcon } from '../assets/images/icons/cluster.svg';
import { ReactComponent as TopologyIcon } from '../assets/images/icons/topology.svg';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as CopyIcon } from '../assets/images/icons/copy.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';

// What each role is to KubeNDT, in the words of the table.
const ROLE_LABELS = {
  namespace: 'Namespace',
  node: 'Node',
  pod: 'Pod',
  topology: 'Links',
  env: 'Environment',
  file: 'File',
  folder: 'Folder',
  mount: 'Deleted file, mounted',
  history: 'History',
  layout: 'Layout',
  'layout-attic': 'Layout kept',
  lock: 'Lock',
  'iface-counts': 'Interface counts',
  user: 'User',
  token: 'API token',
  'session-key': 'Session key',
  other: 'Other',
};

const ROLE_ORDER = Object.keys(ROLE_LABELS);

export const roleLabel = (role) => ROLE_LABELS[role] || role;

export const formatSize = (b) => {
  if (!(b > 0)) return '0 B';
  if (b >= 1e6) return `${(b / 1e6).toFixed(1)} MB`;
  if (b >= 1e3) return `${(b / 1e3).toFixed(1)} kB`;
  return `${b} B`;
};

export const formatAge = (iso, now = Date.now()) => {
  if (!iso) return '';
  const s = Math.max(0, Math.floor((now - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
};

// Every object KubeNDT creates or uses, in one namespace or across the
// cluster, each told for what it is. Read-only: kubectl get from the UI.
const StatePage = () => {
  const navigate = useNavigate();
  const { namespace } = useParams();
  const [namespaces, setNamespaces] = useState(null);
  const [error, setError] = useState('');
  const [picked, setPicked] = useState('');
  const [role, setRole] = useState('');
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState(null);
  const [detail, setDetail] = useState(null);
  const [detailError, setDetailError] = useState('');
  const [copied, setCopied] = useState(false);

  const load = useCallback(async () => {
    setError('');
    try {
      const res = await fetch(
        namespace ? `${API_BASE_URL}/state/${namespace}` : `${API_BASE_URL}/state`
      );
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error(data?.error || `Error ${res.status}`);
      if (namespace) {
        setNamespaces([{ name: namespace, role: data.role, objects: data.objects || [] }]);
      } else {
        setNamespaces(Array.isArray(data.namespaces) ? data.namespaces : []);
      }
    } catch (e) {
      setError(e.message || 'Could not read the state');
      setNamespaces([]);
    }
  }, [namespace]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    if (!selected) {
      setDetail(null);
      setDetailError('');
      return undefined;
    }
    let stopped = false;
    setDetail(null);
    setDetailError('');
    setCopied(false);
    (async () => {
      try {
        const res = await fetch(
          `${API_BASE_URL}/state/${encodeURIComponent(selected.namespace || selected.name)}/${selected.kind.toLowerCase()}/${encodeURIComponent(selected.name)}`
        );
        const data = await res.json().catch(() => null);
        if (!res.ok) throw new Error(data?.error || `Error ${res.status}`);
        if (!stopped) setDetail(data);
      } catch (e) {
        if (!stopped) setDetailError(e.message || 'Could not read the object');
      }
    })();
    return () => {
      stopped = true;
    };
  }, [selected]);

  // The rows on show: one namespace or all, one role or all, the search on
  // name, node and detail.
  const rows = useMemo(() => {
    if (!namespaces) return [];
    const q = query.trim().toLowerCase();
    const out = [];
    for (const ns of namespaces) {
      if (picked && ns.name !== picked) continue;
      for (const o of ns.objects || []) {
        if (role && o.role !== role) continue;
        if (
          q &&
          ![o.name, o.node, o.detail, o.kind].some((v) => (v || '').toLowerCase().includes(q))
        )
          continue;
        out.push({ ...o, namespace: o.namespace || ns.name });
      }
    }
    return out;
  }, [namespaces, picked, role, query]);

  const roles = useMemo(() => {
    if (!namespaces) return [];
    const counts = {};
    for (const ns of namespaces) {
      if (picked && ns.name !== picked) continue;
      for (const o of ns.objects || []) counts[o.role] = (counts[o.role] || 0) + 1;
    }
    return Object.keys(counts)
      .sort((a, b) => {
        const ia = ROLE_ORDER.indexOf(a);
        const ib = ROLE_ORDER.indexOf(b);
        return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib);
      })
      .map((r) => ({ role: r, count: counts[r] }));
  }, [namespaces, picked]);

  const total = useMemo(
    () => (namespaces || []).reduce((n, ns) => n + (ns.objects || []).length, 0),
    [namespaces]
  );

  const copyYaml = async () => {
    if (!detail?.yaml) return;
    try {
      await navigator.clipboard.writeText(detail.yaml);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };

  const title = namespace ? `State of ${namespace}` : 'Cluster state';

  return (
    <div className="state-root">
      <div className="navbar-container">
        <div className="navbar-left">
          <button
            type="button"
            className="navbar-brand"
            onClick={() => navigate('/')}
            title="KubeNDT"
          >
            <img src={kubendtLogo} alt="KubeNDT" className="navbar-brand-logo" />
          </button>
          <button
            className="navbar-button"
            onClick={() => navigate('/')}
            title="Back to the namespace list"
          >
            <HomeIcon className="app-icon" aria-hidden="true" />
            Home
          </button>
          {namespace && (
            <button
              className="navbar-button"
              onClick={() => navigate(`/${namespace}`)}
              title="Back to the topology of this namespace"
            >
              <TopologyIcon className="app-icon" aria-hidden="true" />
              Topology
            </button>
          )}
        </div>
        <div
          className="navbar-center navbar-center-static"
          title="Every Kubernetes object KubeNDT creates or uses"
        >
          <ClusterIcon className="navbar-ns-icon" aria-hidden="true" />
          <div className="navbar-center-text">
            <span className="navbar-namespace">{title}</span>
          </div>
        </div>
        <div className="navbar-right">
          {namespace && (
            <button
              className="navbar-button"
              onClick={() => navigate('/state')}
              title="The same view over every namespace"
            >
              <ClusterIcon className="app-icon" aria-hidden="true" />
              Whole cluster
            </button>
          )}
          <button className="navbar-button" onClick={load} title="Read the cluster again">
            <RefreshIcon className="app-icon" aria-hidden="true" />
            Refresh
          </button>
        </div>
      </div>

      <div className="state-page">
        <aside className="state-list">
          {!namespace && (
            <div className="state-group">
              <div className="state-group-title">Namespaces</div>
              <button
                type="button"
                className={`state-item${picked === '' ? ' active' : ''}`}
                onClick={() => setPicked('')}
              >
                <span className="state-item-name">All</span>
                <span className="state-count">{total}</span>
              </button>
              {(namespaces || []).map((ns) => (
                <button
                  key={ns.name}
                  type="button"
                  className={`state-item${picked === ns.name ? ' active' : ''}`}
                  onClick={() => setPicked(ns.name)}
                  title={ns.role === 'system' ? "The platform's own namespace" : 'A lab'}
                >
                  <span className="state-item-name">{ns.name}</span>
                  {ns.role === 'system' && <span className="state-tag">system</span>}
                  <span className="state-count">{(ns.objects || []).length}</span>
                </button>
              ))}
            </div>
          )}
          <div className="state-group">
            <div className="state-group-title">What they are</div>
            <button
              type="button"
              className={`state-item${role === '' ? ' active' : ''}`}
              onClick={() => setRole('')}
            >
              <span className="state-item-name">Everything</span>
            </button>
            {roles.map((r) => (
              <button
                key={r.role}
                type="button"
                className={`state-item${role === r.role ? ' active' : ''}`}
                onClick={() => setRole(role === r.role ? '' : r.role)}
              >
                <span className="state-item-name">{roleLabel(r.role)}</span>
                <span className="state-count">{r.count}</span>
              </button>
            ))}
          </div>
        </aside>

        <main className="state-main">
          <div className="state-toolbar">
            <input
              type="search"
              className="state-search"
              placeholder="Filter by name, node or detail"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              aria-label="Filter the objects"
            />
            <span className="state-toolbar-count">
              {namespaces === null
                ? 'Loading…'
                : `${rows.length} object${rows.length === 1 ? '' : 's'}`}
            </span>
          </div>
          {error && <p className="state-note state-error">{error}</p>}
          {namespaces && namespaces.length === 0 && !error && (
            <p className="state-note">No namespace of KubeNDT in this cluster.</p>
          )}
          {rows.length > 0 && (
            <table className="state-table">
              <thead>
                <tr>
                  <th>Kind</th>
                  <th>Name</th>
                  {!namespace && !picked && <th>Namespace</th>}
                  <th>For KubeNDT</th>
                  <th>Node</th>
                  <th>Detail</th>
                  <th className="state-num">Size</th>
                  <th className="state-num">Age</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((o) => {
                  const key = `${o.namespace}/${o.kind}/${o.name}`;
                  const active =
                    selected &&
                    selected.namespace === o.namespace &&
                    selected.kind === o.kind &&
                    selected.name === o.name;
                  return (
                    <tr
                      key={key}
                      className={`state-row${active ? ' active' : ''}`}
                      onClick={() => setSelected(active ? null : o)}
                      title={`resourceVersion ${o.resource_version}`}
                    >
                      <td className="state-kind">{o.kind}</td>
                      <td className="state-name">{o.name}</td>
                      {!namespace && !picked && <td className="state-muted">{o.namespace}</td>}
                      <td>
                        <span className={`state-tag state-tag-${o.role}`}>{roleLabel(o.role)}</span>
                      </td>
                      <td className="state-muted">{o.node || ''}</td>
                      <td className="state-detail-cell">{o.detail || ''}</td>
                      <td className="state-num state-muted">{formatSize(o.size)}</td>
                      <td className="state-num state-muted">{formatAge(o.created_at)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </main>

        {selected && (
          <section className="state-detail" aria-label="Object">
            <header className="state-detail-head">
              <div className="state-detail-title">
                <span className="state-kind">{selected.kind}</span>
                <strong>{selected.name}</strong>
                <span className="state-muted">{selected.namespace}</span>
              </div>
              <div className="state-detail-actions">
                <button
                  type="button"
                  className="state-detail-btn"
                  onClick={copyYaml}
                  disabled={!detail?.yaml}
                  title="Copy the YAML"
                >
                  <CopyIcon className="app-icon" aria-hidden="true" />
                  {copied ? 'Copied' : 'Copy'}
                </button>
                <button
                  type="button"
                  className="state-detail-btn"
                  onClick={() => setSelected(null)}
                  title="Close"
                >
                  <CloseIcon className="app-icon" aria-hidden="true" />
                </button>
              </div>
            </header>
            {selected.labels && Object.keys(selected.labels).length > 0 && (
              <div className="state-labels">
                {Object.entries(selected.labels).map(([k, v]) => (
                  <code key={k}>
                    {k}={v}
                  </code>
                ))}
              </div>
            )}
            {detailError && <p className="state-note state-error">{detailError}</p>}
            {!detail && !detailError && <p className="state-note">Loading…</p>}
            {detail?.yaml && <pre className="state-yaml">{detail.yaml}</pre>}
          </section>
        )}
      </div>
    </div>
  );
};

export default StatePage;
