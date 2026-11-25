import React, { useEffect, useState } from 'react';
import { Navigate } from 'react-router-dom';
import AppHeader from '../components/AppHeader';
import K8sNodeInfoPanel from '../components/K8sNodeInfoPanel';
import { useAuth } from '../auth/AuthContext';
import { API_BASE_URL } from '../config';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import { ReactComponent as LoadingIcon } from '../assets/images/icons/loading.svg';
import { ReactComponent as ErrorIcon } from '../assets/images/icons/error.svg';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as ClusterIcon } from '../assets/images/icons/cluster.svg';
import { ReactComponent as ChevronIcon } from '../assets/images/icons/chevron-right.svg';
import { Meter, meshnetDot, vcpu, gib, fixed } from '../components/K8sBits';
import '../components/Dialog.css';
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
  const [kubeMode, setKubeMode] = useState('');
  const [clusterId, setClusterId] = useState('');
  // The context waiting for its cluster's admin password, '' when none.
  const [askingFor, setAskingFor] = useState('');
  const [password, setPassword] = useState('');
  const [switchError, setSwitchError] = useState('');
  const [selectedNodeName, setSelectedNodeName] = useState(null);
  const isOwner = !auth.enabled || auth.role === 'owner';

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
      setKubeMode(data.mode || '');
      setClusterId(data.cluster_id || '');
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

  // A context on the same cluster only changes the credentials. One on
  // another cluster is another KubeNDT, so it takes its admin's password and
  // the page starts over there, with nothing kept from this one.
  const switchContext = async (context, pw) => {
    setSwitchingContext(true);
    setKubeInfoError('');
    setSwitchError('');
    try {
      const res = await fetch(`${API_BASE_URL}/kube/context`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(pw ? { context, password: pw } : { context }),
      });
      const data = await res.json().catch(() => ({}));
      // The label said the same cluster, the server found another one.
      if (!pw && data.code === 'PASSWORD_REQUIRED') {
        setPassword('');
        setAskingFor(context);
        return;
      }
      if (!res.ok) throw new Error(data.error || `The switch failed (${res.status})`);
      if (data.cluster_changed) {
        Object.keys(sessionStorage)
          .filter((k) => k.startsWith('kubendt.'))
          .forEach((k) => sessionStorage.removeItem(k));
        window.location.assign('/');
        return;
      }
      await fetchKubeConfigInfo();
      await fetchClusterStatus();
    } catch (err) {
      if (pw) setSwitchError(err.message);
      else setKubeInfoError(err.message);
    } finally {
      setSwitchingContext(false);
    }
  };

  // Esc closes the password dialog, unless a switch is under way.
  useEffect(() => {
    if (!askingFor) return undefined;
    const onKey = (e) => {
      if (e.key === 'Escape' && !switchingContext) setAskingFor('');
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [askingFor, switchingContext]);

  const handleSwitchContext = () => {
    if (!selectedContext || selectedContext === currentContext) return;
    const target = contextClusterIds[selectedContext];
    if (target && target === clusterId) {
      switchContext(selectedContext);
      return;
    }
    setPassword('');
    setSwitchError('');
    setAskingFor(selectedContext);
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
    <div className="cl-page">
      <AppHeader
        title={{
          icon: ClusterIcon,
          label: 'Cluster',
          hint: 'The nodes, the kubeconfig and the context',
        }}
      />
      {kubeConfigured === false && (
        <div className="cl-gate" role="alert">
          <WarningIcon className="app-icon" aria-hidden="true" />
          <span>
            <strong>No kubeconfig loaded.</strong> KubeNDT needs one to talk to a cluster, mount it
            and restart.
          </span>
        </div>
      )}
      <div className="cl-main">
        <section className="cl-card cl-nodes">
          <div className="cl-card-head">
            <h2 className="cl-card-title">Nodes</h2>
            {clusterStatus?.nodes && (
              <span
                className={`k8s-pill k8s-pill-${clusterStatus.ready === clusterStatus.total ? 'ok' : 'warn'}`}
              >
                Ready {clusterStatus.ready}/{clusterStatus.total}
              </span>
            )}
            {meshnetBadge && (
              <span
                className={`k8s-pill k8s-pill-${meshnetBadge.cls === 'ok' ? 'ok' : meshnetBadge.cls === 'unknown' ? 'warn' : 'bad'}`}
                title={meshnetBadge.title}
              >
                {meshnetBadge.label}
              </span>
            )}
            <span className="cl-space" />
            <button
              type="button"
              className="cl-icon-btn"
              onClick={fetchClusterStatus}
              disabled={clusterLoading || kubeConfigured === false}
              title="Refresh the nodes"
            >
              <RefreshIcon className="app-icon" />
            </button>
          </div>

          {initialClusterLoading ? (
            <div className="cl-empty">
              <LoadingIcon className="app-icon" aria-hidden="true" /> Loading the cluster…
            </div>
          ) : clusterStatus && clusterStatus.nodes && clusterStatus.nodes.length > 0 ? (
            <>
              {clusterAggregates && (
                <div className="cl-stats">
                  <div>
                    <small>Nodes</small>
                    <b>{clusterAggregates.total}</b>
                  </div>
                  <div>
                    <small>Avg CPU</small>
                    <b>
                      {clusterAggregates.metricsAvailable && clusterAggregates.cpuAvg !== null
                        ? `${clusterAggregates.cpuAvg.toFixed(1)}%`
                        : '?'}
                    </b>
                  </div>
                  <div>
                    <small>Avg RAM</small>
                    <b>
                      {clusterAggregates.metricsAvailable && clusterAggregates.memAvg !== null
                        ? `${clusterAggregates.memAvg.toFixed(1)}%`
                        : '?'}
                    </b>
                  </div>
                </div>
              )}
              {clusterAggregates && !clusterAggregates.metricsAvailable && (
                <p
                  className="cl-note"
                  title="metrics-server is not reachable or no node reports metrics. Install it in the cluster to see live CPU and RAM."
                >
                  No metrics-server, so no usage figures.
                </p>
              )}
              <div className="cl-scroll">
                <div className="cl-grid">
                  {clusterStatus.nodes.map((node) => {
                    const mesh = nodeMeshnetChip(node.meshnet);
                    const metrics = node.cpu_usage !== 'N/A' || node.memory_usage !== 'N/A';
                    return (
                      <button
                        type="button"
                        key={node.name}
                        className="cl-node"
                        onClick={() => setSelectedNodeName(node.name)}
                        title={`${node.name}, ${node.status}`}
                      >
                        <span className="cl-node-top">
                          <b>{node.name}</b>
                          {node.roles.slice(0, 1).map((role) => (
                            <span key={role} className="k8s-role">
                              {role}
                            </span>
                          ))}
                          {node.status !== 'Ready' && (
                            <span className="k8s-pill k8s-pill-bad">{node.status}</span>
                          )}
                          <span className="cl-space" />
                          {mesh && (
                            <span className="cl-node-mesh" title={mesh.title}>
                              <span className={`k8s-dot k8s-dot-${meshnetDot(node.meshnet)}`} />
                              Meshnet
                            </span>
                          )}
                          <ChevronIcon className="app-icon cl-node-go" aria-hidden="true" />
                        </span>
                        {metrics ? (
                          <>
                            <span className="cl-node-row">
                              <span>CPU</span>
                              <Meter value={node.cpu_percentage} />
                              <span>
                                {fixed(vcpu(node.cpu_usage), 2)} /{' '}
                                {fixed(vcpu(node.cpu_capacity), 0)} vCPU
                              </span>
                            </span>
                            <span className="cl-node-row">
                              <span>RAM</span>
                              <Meter value={node.memory_percentage} kind="ram" />
                              <span>
                                {fixed(gib(node.memory_usage), 1)} /{' '}
                                {fixed(gib(node.memory_capacity), 1)} GiB
                              </span>
                            </span>
                          </>
                        ) : (
                          <span className="cl-node-row cl-node-plain">
                            {node.cpu_capacity} CPU, {node.memory_capacity} RAM
                          </span>
                        )}
                        <span className="cl-node-foot">
                          kubelet {formatKubeletVersion(node.kubelet_version)}
                        </span>
                      </button>
                    );
                  })}
                </div>
              </div>
            </>
          ) : kubeConfigured === false ? (
            <div className="cl-empty">Mount a kubeconfig and restart to see the nodes.</div>
          ) : (
            <div className="cl-empty cl-empty-bad">
              <ErrorIcon className="app-icon" aria-hidden="true" /> Could not read the cluster.
            </div>
          )}
        </section>

        <section className="cl-card cl-context">
          <div className="cl-card-head">
            <h2 className="cl-card-title">Kubectl context</h2>
            <span className="cl-space" />
            <button
              type="button"
              className="cl-icon-btn"
              onClick={fetchKubeConfigInfo}
              disabled={kubeInfoLoading}
              title="Refresh the kubeconfig"
            >
              <RefreshIcon className="app-icon" />
            </button>
          </div>
          {kubeInfoLoading ? (
            <div className="cl-empty">Reading the kubeconfig…</div>
          ) : kubeConfigured === false ? (
            <p className="cl-hint">
              No kubeconfig. Mount one, at <code>KUBECONFIG</code> or <code>~/.kube/config</code>,
              and restart.
            </p>
          ) : (
            <>
              <div className="cl-kv">
                <span>Reached with</span>
                <b>{kubeMode === 'in-cluster' ? 'Its ServiceAccount' : 'A kubeconfig'}</b>
              </div>
              {kubeMode !== 'in-cluster' && (
                <>
                  <div className="cl-kv">
                    <span>Kubeconfig</span>
                    <b className="cl-mono" title={kubeconfigPath}>
                      {kubeconfigPath || 'Not available'}
                    </b>
                  </div>
                  <div className="cl-kv">
                    <span>Context</span>
                    <b>{currentContext || 'Not set'}</b>
                  </div>
                </>
              )}
              <div className="cl-kv">
                <span>Cluster ID</span>
                <b className="cl-mono" title={clusterId || 'Cluster unreachable or not resolved'}>
                  {clusterId || 'n/a'}
                </b>
              </div>

              {kubeMode === 'in-cluster' ? (
                <p className="cl-hint">
                  This KubeNDT runs in its cluster and serves that cluster only.
                </p>
              ) : (
                <>
                  <label className="cl-label" htmlFor="cl-context">
                    Switch context
                  </label>
                  <div className="cl-inline">
                    <select
                      id="cl-context"
                      className="cl-input"
                      value={selectedContext}
                      onChange={(e) => setSelectedContext(e.target.value)}
                      disabled={!isOwner}
                    >
                      {kubeContexts.length === 0 && <option value="">No contexts found</option>}
                      {kubeContexts.map((ctx) => {
                        const cid = contextClusterIds[ctx];
                        const where = !cid
                          ? 'unreachable'
                          : cid === clusterId
                            ? 'this cluster'
                            : `another cluster, ${cid.slice(0, 8)}`;
                        return (
                          <option key={ctx} value={ctx}>
                            {`${ctx} · ${where}`}
                          </option>
                        );
                      })}
                    </select>
                    <button
                      type="button"
                      className="cl-btn"
                      onClick={handleSwitchContext}
                      disabled={
                        !isOwner ||
                        switchingContext ||
                        !selectedContext ||
                        selectedContext === currentContext ||
                        !contextClusterIds[selectedContext]
                      }
                      title={isOwner ? undefined : 'Only the owner switches the cluster'}
                    >
                      {switchingContext && !askingFor ? 'Switching…' : 'Switch'}
                    </button>
                  </div>
                  <p className="cl-hint">
                    Another cluster is another KubeNDT, with its own accounts and labs. To add a
                    context, edit the kubeconfig and refresh, or restart a compose install.
                  </p>
                </>
              )}

              {kubeInfoError && <div className="cl-error">{kubeInfoError}</div>}
            </>
          )}
        </section>
      </div>
      {askingFor && (
        <div className="dlg-overlay" onClick={() => !switchingContext && setAskingFor('')}>
          <form
            className="dlg"
            role="dialog"
            aria-label="Switch to another cluster"
            onClick={(e) => e.stopPropagation()}
            onSubmit={(e) => {
              e.preventDefault();
              if (password) switchContext(askingFor, password);
            }}
          >
            <div className="dlg-head">
              <ClusterIcon className="app-icon" aria-hidden="true" />
              <h2 className="dlg-title">Switch to another cluster</h2>
            </div>
            <div className="dlg-body">
              <p className="dlg-text">
                <strong>{askingFor}</strong> is another cluster, so another KubeNDT with its own
                accounts, settings and labs. Everyone signed in here is signed out.
              </p>
              <label className="dlg-label" htmlFor="cl-switch-password">
                Its admin password
              </label>
              <input
                id="cl-switch-password"
                className="dlg-input"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={switchingContext}
                autoFocus
              />
              <p className="dlg-hint">
                Where KubeNDT has never run, type yours, it becomes its admin.
              </p>
              {switchError && <p className="cl-error">{switchError}</p>}
            </div>
            <div className="dlg-foot">
              <button
                type="button"
                className="dlg-btn dlg-btn-quiet"
                onClick={() => setAskingFor('')}
                disabled={switchingContext}
              >
                Cancel
              </button>
              <button
                type="submit"
                className="dlg-btn dlg-btn-primary"
                disabled={switchingContext || !password}
              >
                {switchingContext ? 'Switching…' : 'Switch and sign in'}
              </button>
            </div>
          </form>
        </div>
      )}
      {selectedNodeName && (
        <K8sNodeInfoPanel nodeName={selectedNodeName} onClose={() => setSelectedNodeName(null)} />
      )}
    </div>
  );
};

export default ClusterPage;
