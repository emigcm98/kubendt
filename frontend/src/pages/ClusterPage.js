import React, { useEffect, useRef, useState } from 'react';
import { Navigate } from 'react-router-dom';
import AppHeader from '../components/AppHeader';
import K8sNodeInfoPanel from '../components/K8sNodeInfoPanel';
import { useAuth } from '../auth/AuthContext';
import { API_BASE_URL } from '../config';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import { ReactComponent as LoadingIcon } from '../assets/images/icons/loading.svg';
import { ReactComponent as ErrorIcon } from '../assets/images/icons/error.svg';
import { ReactComponent as FolderIcon } from '../assets/images/icons/folder.svg';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import './ClusterPage.css';

// The admins' page for the cluster under KubeNDT: its nodes and their use,
// and the kubeconfig and context the backend talks to it with.
const ClusterPage = () => {
  const auth = useAuth();
  const [clusterStatus, setClusterStatus] = useState(null);
  const [clusterLoading, setClusterLoading] = useState(false);
  const [initialClusterLoading, setInitialClusterLoading] = useState(true);
  const [kubeInfoLoading, setKubeInfoLoading] = useState(false);
  const [kubeInfoError, setKubeInfoError] = useState('');
  const [kubeconfigPath, setKubeconfigPath] = useState('');
  const [kubeContexts, setKubeContexts] = useState([]);
  const [contextClusterIds, setContextClusterIds] = useState({});
  const [currentContext, setCurrentContext] = useState('');
  const [selectedContext, setSelectedContext] = useState('');
  const [switchingContext, setSwitchingContext] = useState(false);
  const [kubeConfigured, setKubeConfigured] = useState(null);
  const [loadingKubeConfig, setLoadingKubeConfig] = useState(false);
  const [kubeconfigDragging, setKubeconfigDragging] = useState(false);
  const [selectedNodeName, setSelectedNodeName] = useState(null);
  const kubeconfigFileInputRef = useRef(null);

  const fetchClusterStatus = async () => {
    try {
      setClusterLoading(true);
      const res = await fetch(`${API_BASE_URL}/cluster/status`);
      if (res.status === 503) {
        setKubeConfigured(false);
        setClusterStatus(null);
        setInitialClusterLoading(false);
        return;
      }
      const data = await res.json();
      setClusterStatus(data);
      setKubeConfigured(true);
      setInitialClusterLoading(false); // Disable initial loading after first fetch
    } catch (err) {
      console.error('Error fetching cluster status:', err);
      setInitialClusterLoading(false); // Also disable in case of error
    } finally {
      setClusterLoading(false);
    }
  };

  const fetchKubeConfigInfo = async () => {
    setKubeInfoLoading(true);
    setKubeInfoError('');
    try {
      const res = await fetch(`${API_BASE_URL}/kube/config`);
      if (!res.ok) {
        const text = await res.text();
        throw new Error(text || 'Failed to load kubeconfig info');
      }
      const data = await res.json();
      setKubeConfigured(!!data.configured);
      setKubeconfigPath(data.path || '');
      setKubeContexts(Array.isArray(data.contexts) ? data.contexts : []);
      setContextClusterIds(data.context_cluster_ids || {});
      setCurrentContext(data.current_context || '');
      setSelectedContext(data.current_context || '');
    } catch (err) {
      setKubeInfoError(err.message || 'Failed to load kubeconfig info');
    } finally {
      setKubeInfoLoading(false);
    }
  };

  const handleSwitchContext = async () => {
    if (!selectedContext || selectedContext === currentContext) return;
    setSwitchingContext(true);
    setKubeInfoError('');
    try {
      const res = await fetch(`${API_BASE_URL}/kube/context`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ context: selectedContext }),
      });
      if (!res.ok) {
        const text = await res.text();
        throw new Error(text || 'Failed to switch context');
      }
      setCurrentContext(selectedContext);
      // The new context is a different cluster, so its status is read again.
      await fetchClusterStatus();
    } catch (err) {
      setKubeInfoError(err.message || 'Failed to switch context');
    } finally {
      setSwitchingContext(false);
    }
  };

  const handleLoadKubeConfigFile = async (e) => {
    const file = e.target.files?.[0];
    if (!file) return;

    setLoadingKubeConfig(true);
    setKubeInfoError('');
    try {
      const formData = new FormData();
      formData.append('file', file);

      const res = await fetch(`${API_BASE_URL}/kube/config`, {
        method: 'POST',
        body: formData,
      });
      if (!res.ok) {
        const errorData = await res.json().catch(() => ({ error: 'Failed to load kubeconfig' }));
        throw new Error(errorData.error || 'Failed to load kubeconfig');
      }
      const data = await res.json();
      setKubeConfigured(!!data.configured);
      setKubeconfigPath(data.path || '');
      setKubeContexts(Array.isArray(data.contexts) ? data.contexts : []);
      setContextClusterIds(data.context_cluster_ids || {});
      setCurrentContext(data.current_context || '');
      setSelectedContext(data.current_context || '');
      await fetchClusterStatus();
      e.target.value = ''; // Reset file input
    } catch (err) {
      setKubeInfoError(err.message || 'Failed to load kubeconfig');
    } finally {
      setLoadingKubeConfig(false);
    }
  };

  useEffect(() => {
    if (auth.enabled && !auth.isAdmin) return undefined;
    fetchClusterStatus();
    fetchKubeConfigInfo();
    const interval = setInterval(fetchClusterStatus, 30000);
    return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Aggregated cluster metrics, backend already computes the weighted
  // averages (sum(usage)/sum(capacity) on raw millicores/bytes) so we
  // simply forward them here. metricsAvailable is true iff at least one
  // node reports usage from metrics-server; otherwise the percentages
  // are meaningless and we surface a clear "unavailable" message.
  const clusterAggregates = (() => {
    if (!clusterStatus || !clusterStatus.nodes || clusterStatus.nodes.length === 0) return null;
    const metricsAvailable = clusterStatus.nodes.some((n) => n.cpu_usage && n.cpu_usage !== 'N/A');
    return {
      ready: clusterStatus.ready,
      total: clusterStatus.total,
      metricsAvailable,
      cpuAvg:
        metricsAvailable && typeof clusterStatus.avg_cpu_percentage === 'number'
          ? clusterStatus.avg_cpu_percentage
          : null,
      memAvg:
        metricsAvailable && typeof clusterStatus.avg_memory_percentage === 'number'
          ? clusterStatus.avg_memory_percentage
          : null,
    };
  })();

  // Meshnet CNI health, shown as a pill next to the node count. The Topology
  // CRD ships with meshnet, so the DaemonSet is the real signal: without it the
  // topology deploys but links never get wired. "unknown" stays neutral so a
  // kubeconfig that can't list DaemonSets doesn't look like a missing install.
  const meshnetBadge = (() => {
    const m = clusterStatus?.meshnet;
    if (!m || !m.state) return null;
    switch (m.state) {
      case 'ok':
        return {
          cls: 'ok',
          label: `Meshnet ${m.ready}/${m.desired}`,
          title: 'Meshnet CNI is running on every node.',
        };
      case 'degraded':
        return {
          cls: 'degraded',
          label: `Meshnet ${m.ready}/${m.desired}`,
          title:
            'Meshnet is installed but the dataplane is not ready on every node. New topology links may not be wired until it recovers.',
        };
      case 'missing':
        return {
          cls: 'missing',
          label: 'Meshnet not found',
          title:
            'No meshnet DaemonSet found. Topologies will deploy but their links will not be wired. Install the Meshnet CNI in this cluster.',
        };
      default:
        return {
          cls: 'unknown',
          label: 'Meshnet unknown',
          title:
            m.message || 'Could not determine meshnet status (no permission to list DaemonSets).',
        };
    }
  })();

  // Per-node meshnet chip for the node cards. Null hides it (older backends).
  const nodeMeshnetChip = (state) => {
    switch (state) {
      case 'running':
        return { cls: 'ok', title: 'Meshnet is running on this node.' };
      case 'not-running':
        return {
          cls: 'down',
          title: 'No meshnet pod on this node. Links landing here will not be wired.',
        };
      case 'unknown':
        return { cls: 'unknown', title: 'Could not check meshnet on this node.' };
      default:
        return null;
    }
  };

  // Render the "v" prefix only if the backend didn't already include one.
  const formatKubeletVersion = (v) =>
    !v ? '' : v.startsWith('v') || v.startsWith('V') ? v : `v${v}`;

  if (auth.enabled && !auth.isAdmin) return <Navigate to="/" replace />;

  return (
    <div className="cluster-page">
      <AppHeader />
      {kubeConfigured === false && (
        <div className="kubeconfig-gate-banner" role="alert">
          <WarningIcon className="kubeconfig-gate-icon" aria-hidden="true" />
          <div className="kubeconfig-gate-text">
            <strong>No kubeconfig loaded.</strong> KubeNDT needs one to talk to a cluster. Load it
            in the <em>Kubectl context</em> panel.
          </div>
        </div>
      )}
      <div className="cluster-page-body">
        {/* Cluster Status Panel */}
        <div className="cluster-status-card">
          <div className="cluster-card-header">
            <h2 className="cluster-card-title">
              Cluster status{' '}
              {clusterStatus && (
                <span className="cluster-status-badge">
                  Ready {clusterStatus.ready}/{clusterStatus.total}
                </span>
              )}
              {meshnetBadge && (
                <span
                  className={`meshnet-badge meshnet-${meshnetBadge.cls}`}
                  title={meshnetBadge.title}
                >
                  <span className="meshnet-dot" aria-hidden="true" />
                  {meshnetBadge.label}
                </span>
              )}
            </h2>
            <div className="cluster-refresh-controls">
              <button
                className="refresh-btn"
                onClick={fetchClusterStatus}
                disabled={clusterLoading || kubeConfigured === false}
                title="Refresh cluster status"
              >
                <RefreshIcon style={{ width: 15, height: 15 }} />
              </button>
            </div>
          </div>
          {initialClusterLoading ? (
            <div className="cluster-loading">
              <LoadingIcon className="app-icon" aria-hidden="true" /> Loading cluster information...
            </div>
          ) : clusterStatus && clusterStatus.nodes && clusterStatus.nodes.length > 0 ? (
            <>
              {clusterAggregates && (
                <div className="cluster-summary-strip">
                  <div className="cluster-summary-item">
                    <span className="cluster-summary-label">Ready</span>
                    <span className="cluster-summary-value">
                      {clusterAggregates.ready}/{clusterAggregates.total}
                    </span>
                  </div>
                  {clusterAggregates.metricsAvailable ? (
                    <>
                      <div className="cluster-summary-divider" />
                      <div className="cluster-summary-item">
                        <span className="cluster-summary-label">Avg CPU</span>
                        <span className="cluster-summary-value">
                          {clusterAggregates.cpuAvg !== null
                            ? `${clusterAggregates.cpuAvg.toFixed(1)}%`
                            : '—'}
                        </span>
                      </div>
                      <div className="cluster-summary-divider" />
                      <div className="cluster-summary-item">
                        <span className="cluster-summary-label">Avg RAM</span>
                        <span className="cluster-summary-value">
                          {clusterAggregates.memAvg !== null
                            ? `${clusterAggregates.memAvg.toFixed(1)}%`
                            : '—'}
                        </span>
                      </div>
                    </>
                  ) : (
                    <>
                      <div className="cluster-summary-divider" />
                      <div
                        className="cluster-summary-warning"
                        title="metrics-server is not reachable or no node is reporting metrics. Install metrics-server in the cluster to see live CPU and RAM."
                      >
                        <WarningIcon className="app-icon" aria-hidden="true" /> metrics-server
                        unavailable. CPU / RAM not reported
                      </div>
                    </>
                  )}
                </div>
              )}
              <div className="nodes-grid">
                {clusterStatus.nodes.map((node) => (
                  <div
                    key={node.name}
                    className={`node-card ${node.status.toLowerCase()}`}
                    role="button"
                    tabIndex={0}
                    onClick={() => setSelectedNodeName(node.name)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        setSelectedNodeName(node.name);
                      }
                    }}
                    title={`Open details for ${node.name}`}
                  >
                    <div className="node-header">
                      <div className="node-name" title={node.name}>
                        {node.name}
                      </div>
                    </div>
                    {/* Meta strip: role, status and meshnet on one row, so
                    the name gets the full width above it. */}
                    <div className="node-meta">
                      {node.roles.map((role) => (
                        <span key={role} className="role-badge">
                          {role}
                        </span>
                      ))}
                      <div className={`node-status-indicator ${node.status.toLowerCase()}`}>
                        {node.status}
                      </div>
                      {(() => {
                        const chip = nodeMeshnetChip(node.meshnet);
                        return chip ? (
                          <span
                            className={`node-meshnet node-meshnet-${chip.cls}`}
                            title={chip.title}
                          >
                            <span className="node-meshnet-dot" aria-hidden="true" />
                            meshnet
                          </span>
                        ) : null;
                      })()}
                    </div>
                    <div className="node-metrics">
                      <div className="metric-row">
                        <span className="metric-label">CPU:</span>
                        <div className="metric-bar-container">
                          <div
                            className="metric-bar cpu"
                            style={{ width: `${Math.min(node.cpu_percentage, 100)}%` }}
                          ></div>
                          <span className="metric-value">
                            {node.cpu_usage !== 'N/A'
                              ? `${node.cpu_percentage.toFixed(1)}%`
                              : 'N/A'}
                          </span>
                        </div>
                      </div>
                      <div className="metric-row">
                        <span className="metric-label">RAM:</span>
                        <div className="metric-bar-container">
                          <div
                            className="metric-bar memory"
                            style={{ width: `${Math.min(node.memory_percentage, 100)}%` }}
                          ></div>
                          <span className="metric-value">
                            {node.memory_usage !== 'N/A'
                              ? `${node.memory_percentage.toFixed(1)}%`
                              : 'N/A'}
                          </span>
                        </div>
                      </div>
                      <div className="node-capacity">
                        Resources: {node.cpu_capacity} CPU, {node.memory_capacity} RAM
                      </div>
                    </div>
                    <div className="node-version">{formatKubeletVersion(node.kubelet_version)}</div>
                  </div>
                ))}
              </div>
            </>
          ) : kubeConfigured === false ? (
            <div className="cluster-error">Load a kubeconfig to see cluster status</div>
          ) : (
            <div className="cluster-error">
              <ErrorIcon className="app-icon" aria-hidden="true" /> Failed to fetch cluster
              information
            </div>
          )}
        </div>

        {/* Kubeconfig Panel, lives in the left column under the cluster status */}
        <div className="kubeconfig-card">
          <div className="kubeconfig-header">
            <h2 className="kubeconfig-title">Kubectl context</h2>
            <button
              className="kubeconfig-refresh"
              onClick={fetchKubeConfigInfo}
              disabled={kubeInfoLoading}
              title="Refresh kubeconfig info"
            >
              <RefreshIcon style={{ width: 14, height: 14 }} />
            </button>
          </div>
          {kubeInfoLoading ? (
            <div className="kubeconfig-loading">Loading kubeconfig info...</div>
          ) : (
            <>
              <div className="kubeconfig-row">
                <span className="kubeconfig-label">Config path</span>
                <span className="kubeconfig-value" title={kubeconfigPath}>
                  {kubeconfigPath || 'Not available'}
                </span>
              </div>
              <div className="kubeconfig-row">
                <span className="kubeconfig-label">Current context</span>
                <span className="kubeconfig-value">{currentContext || 'Not set'}</span>
              </div>
              <div className="kubeconfig-row">
                <span className="kubeconfig-label">Cluster ID</span>
                <span
                  className="kubeconfig-value"
                  title={contextClusterIds[currentContext] || 'Cluster unreachable or not resolved'}
                >
                  {contextClusterIds[currentContext] || 'n/a'}
                </span>
              </div>
              <div className="kubeconfig-controls">
                <select
                  className="kubeconfig-select"
                  value={selectedContext}
                  onChange={(e) => setSelectedContext(e.target.value)}
                >
                  {kubeContexts.length === 0 && <option value="">No contexts found</option>}
                  {kubeContexts.map((ctx) => {
                    const cid = contextClusterIds[ctx];
                    const shortId = cid ? cid.slice(0, 8) : 'unreachable';
                    return (
                      <option key={ctx} value={ctx}>
                        {`${ctx} — ${shortId}`}
                      </option>
                    );
                  })}
                </select>
                <button
                  className="kubeconfig-apply"
                  onClick={handleSwitchContext}
                  disabled={
                    switchingContext || !selectedContext || selectedContext === currentContext
                  }
                >
                  {switchingContext ? 'Switching...' : 'Apply'}
                </button>
              </div>

              <input
                ref={kubeconfigFileInputRef}
                type="file"
                onChange={handleLoadKubeConfigFile}
                disabled={loadingKubeConfig}
                style={{ display: 'none' }}
                accept=".config,.yaml,*"
              />
              <div
                role="button"
                tabIndex={0}
                className={`kubeconfig-drop-zone${kubeconfigDragging ? ' is-dragging' : ''}${loadingKubeConfig ? ' is-busy' : ''}`}
                onClick={() => !loadingKubeConfig && kubeconfigFileInputRef.current?.click()}
                onKeyDown={(e) => {
                  if ((e.key === 'Enter' || e.key === ' ') && !loadingKubeConfig) {
                    e.preventDefault();
                    kubeconfigFileInputRef.current?.click();
                  }
                }}
                onDragOver={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  if (!loadingKubeConfig) setKubeconfigDragging(true);
                }}
                onDragLeave={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  setKubeconfigDragging(false);
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  setKubeconfigDragging(false);
                  if (loadingKubeConfig) return;
                  const file = e.dataTransfer.files?.[0];
                  if (!file) return;
                  handleLoadKubeConfigFile({ target: { files: [file], value: '' } });
                }}
                aria-busy={loadingKubeConfig || undefined}
              >
                <FolderIcon className="kubeconfig-drop-icon" aria-hidden="true" />
                <span className="kubeconfig-drop-text">
                  {loadingKubeConfig
                    ? 'Loading kubeconfig…'
                    : 'Drop kubeconfig here or click to select'}
                </span>
              </div>

              {kubeInfoError && <div className="kubeconfig-error">{kubeInfoError}</div>}
            </>
          )}
        </div>
      </div>
      {selectedNodeName && (
        <K8sNodeInfoPanel nodeName={selectedNodeName} onClose={() => setSelectedNodeName(null)} />
      )}
    </div>
  );
};

export default ClusterPage;
