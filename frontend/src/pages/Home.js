import React, { useState, useEffect, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import Modal from 'react-modal';
import AlertModal from '../components/AlertModal';
import ErrorModal from '../components/ErrorModal';
import K8sNodeInfoPanel from '../components/K8sNodeInfoPanel';
import ApiTokensModal from '../components/ApiTokensModal';
import ProfileMenu from '../components/ProfileMenu';
import { useAuth } from '../auth/AuthContext';
import { API_BASE_URL, SWAGGER_UI_URL } from '../config';
import kubendtLogo from '../assets/images/kubendt-logo.svg';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import { ReactComponent as LoadingIcon } from '../assets/images/icons/loading.svg';
import { ReactComponent as ErrorIcon } from '../assets/images/icons/error.svg';
import { ReactComponent as FolderIcon } from '../assets/images/icons/folder.svg';
import { ReactComponent as ClockIcon } from '../assets/images/icons/clock.svg';
import { ReactComponent as SearchIcon } from '../assets/images/icons/search.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as PlusIcon } from '../assets/images/icons/plus.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as BookIcon } from '../assets/images/icons/book.svg';
import './Home.css';

Modal.setAppElement('#root');

const Home = () => {
  const [namespaces, setNamespaces] = useState([]);
  const [loading, setLoading] = useState(false);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [newNamespaceName, setNewNamespaceName] = useState('');
  const [creatingNamespace, setCreatingNamespace] = useState(false);
  const [errorMessage, setErrorMessage] = useState('');
  const [clusterStatus, setClusterStatus] = useState(null);
  const [clusterLoading, setClusterLoading] = useState(false);
  const [appVersion, setAppVersion] = useState(null);
  const [initialClusterLoading, setInitialClusterLoading] = useState(true);
  const [kubeInfoLoading, setKubeInfoLoading] = useState(false);
  const [kubeInfoError, setKubeInfoError] = useState('');
  const [kubeconfigPath, setKubeconfigPath] = useState('');
  const [kubeContexts, setKubeContexts] = useState([]);
  // Maps context name -> canonical cluster ID (kube-system UID). Unreachable
  // contexts are absent. Lets the user see which contexts share a cluster.
  const [contextClusterIds, setContextClusterIds] = useState({});
  const [currentContext, setCurrentContext] = useState('');
  const [selectedContext, setSelectedContext] = useState('');
  const [switchingContext, setSwitchingContext] = useState(false);
  // null = unknown yet, true/false = whether the backend has a usable kubeconfig.
  const [kubeConfigured, setKubeConfigured] = useState(null);
  const [loadingKubeConfig, setLoadingKubeConfig] = useState(false);
  const [namespaceWarningOpen, setNamespaceWarningOpen] = useState(false);
  const [nsNameValidation, setNsNameValidation] = useState(null);
  const [kubeconfigDragging, setKubeconfigDragging] = useState(false);
  const [namespaceSearch, setNamespaceSearch] = useState('');
  const [deleteTarget, setDeleteTarget] = useState(null); // ns object
  const [deletePositionsChecked, setDeletePositionsChecked] = useState(false);
  const [deleteFilesChecked, setDeleteFilesChecked] = useState(false);
  const [deletingNamespace, setDeletingNamespace] = useState(false);
  const [selectedNodeName, setSelectedNodeName] = useState(null);
  // Persisted sort preference for the namespace list.
  const [sortField, setSortField] = useState(() => {
    try {
      return localStorage.getItem('kubendt.home.nsSortField') || 'name';
    } catch {
      return 'name';
    }
  });
  const [sortDir, setSortDir] = useState(() => {
    try {
      return localStorage.getItem('kubendt.home.nsSortDir') || 'asc';
    } catch {
      return 'asc';
    }
  });
  const kubeconfigFileInputRef = useRef(null);
  const navigate = useNavigate();
  const auth = useAuth();
  const [tokensOpen, setTokensOpen] = useState(false);

  const handleSort = (field) => {
    let nextField = sortField;
    let nextDir = sortDir;
    if (field === sortField) {
      nextDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
      nextField = field;
      // Sensible defaults per field: A→Z for name, newest first for created.
      nextDir = field === 'created' ? 'desc' : 'asc';
    }
    setSortField(nextField);
    setSortDir(nextDir);
    try {
      localStorage.setItem('kubendt.home.nsSortField', nextField);
      localStorage.setItem('kubendt.home.nsSortDir', nextDir);
    } catch {
      /* localStorage unavailable, ignore */
    }
  };

  // Kubernetes RFC 1123 label rules: lowercase alphanumeric and '-',
  // must start and end with alphanumeric, max 63 chars.
  const K8S_NAMESPACE_REGEX = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
  const validateNamespaceName = (name) => {
    if (!name) return { ok: null, message: '' };
    if (name.length > 63) return { ok: false, message: 'Too long (max 63 characters).' };
    if (/[A-Z]/.test(name)) return { ok: false, message: 'Use lowercase letters only.' };
    if (/[^a-z0-9-]/.test(name))
      return { ok: false, message: "Only lowercase letters, numbers and '-' are allowed." };
    if (name.startsWith('-') || name.endsWith('-'))
      return { ok: false, message: 'Must start and end with a letter or number.' };
    if (!K8S_NAMESPACE_REGEX.test(name)) return { ok: false, message: 'Invalid name (RFC 1123).' };
    return { ok: true, message: '' };
  };

  // Per-rule live checks for the rule list shown in the modal.
  const namespaceRules = (raw) => {
    const name = (raw || '').trim();
    return [
      {
        id: 'len',
        label: 'Between 1 and 63 characters',
        ok: name.length >= 1 && name.length <= 63,
      },
      {
        id: 'chars',
        label: "Only lowercase letters, numbers and '-'",
        ok: /^[a-z0-9-]+$/.test(name),
      },
      {
        id: 'bounds',
        label: 'Starts and ends with a letter or number',
        ok: /^[a-z0-9].*[a-z0-9]$/.test(name) || /^[a-z0-9]$/.test(name),
      },
      { id: 'rfc', label: 'Matches Kubernetes RFC 1123 label', ok: K8S_NAMESPACE_REGEX.test(name) },
    ];
  };

  const handleOpenSwagger = () => {
    window.open(SWAGGER_UI_URL, '_blank', 'noopener,noreferrer');
  };

  const fetchNamespaces = async () => {
    try {
      setLoading(true);
      const res = await fetch(`${API_BASE_URL}/namespaces`);
      if (res.status === 503) {
        setKubeConfigured(false);
        setNamespaces([]);
        return;
      }
      const data = await res.json();
      // Show active and terminating
      const filtered = data.namespaces.filter(
        (ns) => ns.status === 'Active' || ns.status === 'Terminating'
      );
      setNamespaces(filtered);
    } catch (err) {
      console.error('Error fetching namespaces:', err);
    } finally {
      setLoading(false);
    }
  };

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

  // Running build version, shown as a badge next to the title. Best-effort:
  // if it fails the header just renders without the badge. It never changes
  // while the app runs, so we fetch it once at mount and don't poll.
  const fetchAppVersion = async () => {
    try {
      const res = await fetch(`${API_BASE_URL}/version`);
      if (!res.ok) return;
      setAppVersion(await res.json());
    } catch {
      // Keep the header intact if the version endpoint is unreachable.
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
      // The new context is a different cluster: refresh both the cluster status
      // and the namespace list, which are otherwise stale (showing the previous
      // cluster's namespaces until a manual reload).
      await Promise.all([fetchClusterStatus(), fetchNamespaces()]);
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
      await fetchNamespaces();
      e.target.value = ''; // Reset file input
    } catch (err) {
      setKubeInfoError(err.message || 'Failed to load kubeconfig');
    } finally {
      setLoadingKubeConfig(false);
    }
  };

  const handleNamespaceSelection = (ns) => {
    if (ns.status === 'Terminating') {
      setNamespaceWarningOpen(true);
      return;
    }

    navigate(`/${ns.name}`);
  };

  useEffect(() => {
    // Initial fetch, both cluster status and the namespace list so the
    // dashboard is fully populated when the user lands here.
    fetchClusterStatus();
    fetchKubeConfigInfo();
    fetchNamespaces();
    fetchAppVersion();

    // Refresh every 30 seconds
    const interval = setInterval(() => {
      fetchClusterStatus();
      fetchNamespaces();
    }, 30000);

    // Clean up intervals on unmount
    return () => {
      clearInterval(interval);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleCreateNamespace = async () => {
    const ns = newNamespaceName.trim();
    if (!ns) {
      setErrorMessage('Namespace name cannot be empty.');
      return;
    }

    setCreatingNamespace(true);

    try {
      const res = await fetch(`${API_BASE_URL}/namespaces/`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ namespace: ns }),
      });

      if (res.ok) {
        setIsModalOpen(false);
        setNewNamespaceName('');
        setTimeout(async () => {
          await fetchNamespaces();
          navigate(`/${ns}`);
        }, 100);
      } else {
        const data = await res.json();
        setErrorMessage(data.error || 'Error creating namespace');
      }
    } catch (err) {
      console.error('Error creating namespace:', err);
      setErrorMessage('Error creating namespace');
    } finally {
      setCreatingNamespace(false);
    }
  };

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

  const sortedNamespaces = [...namespaces].sort((a, b) => {
    let cmp;
    if (sortField === 'created') {
      // createdAt is "YYYY-MM-DD HH:MM:SS" so lexicographic = chronological.
      cmp = (a.createdAt || '').localeCompare(b.createdAt || '');
    } else {
      cmp = a.name.localeCompare(b.name);
    }
    return sortDir === 'asc' ? cmp : -cmp;
  });

  const filteredNamespaces = (() => {
    const q = namespaceSearch.trim().toLowerCase();
    if (!q) return sortedNamespaces;
    return sortedNamespaces.filter((ns) => ns.name.toLowerCase().includes(q));
  })();

  const handleRequestDeleteNamespace = (ns) => {
    setDeleteTarget(ns);
    setDeletePositionsChecked(false);
    setDeleteFilesChecked(false);
  };

  const handleConfirmDeleteNamespace = async () => {
    if (!deleteTarget) return;
    setDeletingNamespace(true);
    try {
      const params = new URLSearchParams();
      if (deletePositionsChecked) params.set('deletePositions', 'true');
      if (deleteFilesChecked) params.set('deleteFiles', 'true');
      const qs = params.toString() ? `?${params.toString()}` : '';
      const res = await fetch(`${API_BASE_URL}/namespaces/${deleteTarget.name}${qs}`, {
        method: 'DELETE',
      });
      if (!res.ok) {
        const data = await res.json().catch(() => null);
        setErrorMessage(data?.error || `Could not delete namespace '${deleteTarget.name}'`);
      }
      setDeleteTarget(null);
      // Give the API a moment to flip the namespace to Terminating, then refresh.
      setTimeout(fetchNamespaces, 250);
    } catch (err) {
      console.error('Error deleting namespace:', err);
      setErrorMessage(err.message || 'Error deleting namespace');
      setDeleteTarget(null);
    } finally {
      setDeletingNamespace(false);
    }
  };

  // "dev" for local builds, "vX.Y.Z" for releases. Commit and build date are
  // "unknown" on dev, so we drop them and just tag it as a local build.
  const renderVersionBadge = () => {
    if (!appVersion?.version) return null;
    const isDev = appVersion.version === 'dev';
    const meta = [appVersion.commit, appVersion.build_date].filter((v) => v && v !== 'unknown');
    const tooltip = isDev ? 'local build' : meta.join(' · ');
    return (
      <span
        className={`home-version-badge${isDev ? ' home-version-badge-dev' : ''}`}
        title={tooltip || undefined}
      >
        {isDev ? 'dev' : `v${appVersion.version}`}
      </span>
    );
  };

  return (
    <div className="home-wrapper">
      {/* Slim header, mirrors GitHub link on the left, Open API on the right */}
      <div className="home-header">
        <div className="home-header-left">
          <a
            className="home-header-icon-link"
            href="https://github.com/emigcm98/kubendt"
            target="_blank"
            rel="noopener noreferrer"
            title="View KubeNDT on GitHub"
          >
            <svg height="16" viewBox="0 0 16 16" width="16" fill="currentColor" aria-hidden="true">
              <path d="M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z"></path>
            </svg>
            <span>GitHub</span>
          </a>
        </div>
        <div className="home-header-brand">
          <img src={kubendtLogo} alt="KubeNDT" className="home-header-logo" />
          <div className="home-header-text">
            <div className="home-header-title-row">
              <h1 className="home-header-title">KubeNDT</h1>
              {renderVersionBadge()}
            </div>
            <span className="home-header-subtitle">
              Network Digital Twin platform in Kubernetes · by{' '}
              <a href="https://github.com/emigcm98" target="_blank" rel="noopener noreferrer">
                @emigcm98
              </a>
            </span>
            <a
              className="home-header-site"
              href="https://kubendt.org"
              target="_blank"
              rel="noopener noreferrer"
            >
              kubendt.org
            </a>
          </div>
        </div>
        <div className="home-header-right">
          <button className="open-api-btn" onClick={handleOpenSwagger}>
            <BookIcon className="app-icon" aria-hidden="true" />
            Open API
          </button>
          {auth.enabled && <ProfileMenu onOpenTokens={() => setTokensOpen(true)} />}
        </div>
      </div>

      {/* No kubeconfig loaded: nothing works until one is provided. */}
      {kubeConfigured === false && (
        <div className="kubeconfig-gate-banner" role="alert">
          <WarningIcon className="kubeconfig-gate-icon" aria-hidden="true" />
          <div className="kubeconfig-gate-text">
            <strong>No kubeconfig loaded.</strong> KubeNDT needs a kubeconfig to talk to a cluster.
            Load one in the <em>Kubectl context</em> panel below to get started.
          </div>
        </div>
      )}

      {/* Main Controls - 2 Column Layout */}
      <div className="main-layout">
        {/* Left Column: Cluster Status */}
        <div className="layout-column left-column">
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
                <LoadingIcon className="app-icon" aria-hidden="true" /> Loading cluster
                information...
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
                        <div className="node-header-badges">
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
                      </div>
                      <div className="node-roles">
                        {node.roles.map((role) => (
                          <span key={role} className="role-badge">
                            {role}
                          </span>
                        ))}
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
                      <div className="node-version">
                        {formatKubeletVersion(node.kubelet_version)}
                      </div>
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
                    title={
                      contextClusterIds[currentContext] || 'Cluster unreachable or not resolved'
                    }
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

        {/* Right Column: Namespaces */}
        <div className="layout-column right-column">
          {/* Namespace Management Panel */}
          <div className="namespace-card">
            <div className="namespace-card-header">
              <h2 className="namespace-card-title">
                Namespaces
                {namespaces.length > 0 && (
                  <span className="namespace-card-count">{namespaces.length}</span>
                )}
              </h2>
              <div className="namespace-card-actions">
                {namespaces.length > 1 && (
                  <div className="namespace-sort" role="group" aria-label="Sort namespaces">
                    <button
                      type="button"
                      className={`namespace-sort-btn${sortField === 'name' ? ' active' : ''}`}
                      onClick={() => handleSort('name')}
                      title={
                        sortField === 'name'
                          ? `Sort by name (${sortDir === 'asc' ? 'A → Z' : 'Z → A'}, click to flip)`
                          : 'Sort by name (A → Z)'
                      }
                      aria-pressed={sortField === 'name'}
                    >
                      <span className="namespace-sort-glyph">Aa</span>
                      <span className="namespace-sort-arrow">
                        {sortField === 'name' ? (sortDir === 'asc' ? '↓' : '↑') : '↕'}
                      </span>
                    </button>
                    <button
                      type="button"
                      className={`namespace-sort-btn${sortField === 'created' ? ' active' : ''}`}
                      onClick={() => handleSort('created')}
                      title={
                        sortField === 'created'
                          ? `Sort by creation (${sortDir === 'desc' ? 'newest first' : 'oldest first'}, click to flip)`
                          : 'Sort by creation date (newest first)'
                      }
                      aria-pressed={sortField === 'created'}
                    >
                      <ClockIcon className="namespace-sort-glyph app-icon" aria-hidden="true" />
                      <span className="namespace-sort-arrow">
                        {sortField === 'created' ? (sortDir === 'desc' ? '↓' : '↑') : '↕'}
                      </span>
                    </button>
                  </div>
                )}
                <button
                  className="namespace-refresh"
                  onClick={fetchNamespaces}
                  disabled={loading || kubeConfigured === false}
                  title="Refresh namespace list"
                >
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <polyline points="23 4 23 10 17 10" />
                    <polyline points="1 20 1 14 7 14" />
                    <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
                  </svg>
                </button>
              </div>
            </div>

            {namespaces.length > 0 && (
              <div className="namespace-search">
                <SearchIcon className="namespace-search-icon app-icon" aria-hidden="true" />
                <input
                  type="text"
                  value={namespaceSearch}
                  onChange={(e) => setNamespaceSearch(e.target.value)}
                  placeholder="Filter namespaces…"
                  aria-label="Filter namespaces"
                />
                {namespaceSearch && (
                  <button
                    type="button"
                    className="namespace-search-clear"
                    onClick={() => setNamespaceSearch('')}
                    title="Clear filter"
                  >
                    <CloseIcon className="app-icon" />
                  </button>
                )}
              </div>
            )}

            <div className="namespace-list">
              {loading && namespaces.length === 0 && (
                <div className="namespace-empty">Loading namespaces…</div>
              )}

              {!loading && namespaces.length === 0 && (
                <div className="namespace-empty">
                  No KubeNDT namespaces yet. Create your first one below.
                </div>
              )}

              {namespaces.length > 0 && filteredNamespaces.length === 0 && (
                <div className="namespace-empty">No namespaces match “{namespaceSearch}”</div>
              )}

              {filteredNamespaces.map((ns) => {
                const terminating = ns.status === 'Terminating';
                const hasTopology = ns.has_topology === true;
                return (
                  <div
                    key={ns.name}
                    className={`namespace-item${terminating ? ' namespace-item-terminating' : ''}`}
                  >
                    <button
                      type="button"
                      className="namespace-item-main"
                      onClick={() => handleNamespaceSelection(ns)}
                      title={terminating ? 'Namespace is terminating' : `Open namespace ${ns.name}`}
                    >
                      <span className="namespace-item-info">
                        <span className="namespace-item-name">{ns.name}</span>
                        {ns.createdAt && (
                          <span className="namespace-item-created">created: {ns.createdAt}</span>
                        )}
                      </span>
                      <span className="namespace-item-badges">
                        <span
                          className={`namespace-item-status namespace-item-status-${(ns.status || 'unknown').toLowerCase()}`}
                        >
                          <span className="namespace-item-status-dot" aria-hidden="true" />
                          {ns.status}
                        </span>
                        <span
                          className={`namespace-item-topology namespace-item-topology-${hasTopology ? 'yes' : 'no'}`}
                          title={
                            hasTopology
                              ? 'A KubeNDT topology is currently deployed'
                              : 'No KubeNDT topology deployed yet'
                          }
                        >
                          <span className="namespace-item-status-dot" aria-hidden="true" />
                          topology
                        </span>
                      </span>
                    </button>
                    <button
                      type="button"
                      className="namespace-item-delete"
                      onClick={(e) => {
                        e.stopPropagation();
                        handleRequestDeleteNamespace(ns);
                      }}
                      disabled={terminating}
                      title={terminating ? 'Already terminating' : `Delete namespace ${ns.name}`}
                      aria-label={`Delete namespace ${ns.name}`}
                    >
                      <TrashIcon className="app-icon" aria-hidden="true" />
                    </button>
                  </div>
                );
              })}
            </div>

            <button
              type="button"
              className="namespace-create-btn"
              disabled={kubeConfigured === false}
              onClick={() => {
                setNsNameValidation(null);
                setNewNamespaceName('');
                setIsModalOpen(true);
              }}
              title={
                kubeConfigured === false
                  ? 'Load a kubeconfig first'
                  : 'Create a new KubeNDT namespace'
              }
            >
              <span className="namespace-create-icon" aria-hidden="true">
                +
              </span>
              <span>Create KubeNDT namespace</span>
            </button>
          </div>
        </div>
      </div>

      {tokensOpen && <ApiTokensModal onClose={() => setTokensOpen(false)} />}

      {/* Modal to create namespace */}
      <Modal
        isOpen={isModalOpen}
        onRequestClose={() => !creatingNamespace && setIsModalOpen(false)}
        className="create-namespace-modal"
        overlayClassName="create-namespace-overlay"
        shouldCloseOnEsc={!creatingNamespace}
        shouldCloseOnOverlayClick={!creatingNamespace}
      >
        <div className="modal-header">
          <h2>
            <PlusIcon className="app-icon" aria-hidden="true" /> Create Namespace
          </h2>
          <button
            className="modal-close-btn"
            onClick={() => setIsModalOpen(false)}
            disabled={creatingNamespace}
          >
            <CloseIcon className="app-icon" />
          </button>
        </div>

        <div className="modal-body">
          <p className="ns-modal-intro">
            A Kubernetes namespace is a logical partition where KubeNDT will deploy your topology
            (pods, links, files…). Pick a name that follows the RFC 1123 conventions below.
          </p>

          <label htmlFor="ns-input" className="ns-input-label">
            Namespace name
          </label>
          <div className="ns-input-wrapper">
            <input
              id="ns-input"
              type="text"
              value={newNamespaceName}
              onChange={(e) => {
                const v = e.target.value;
                setNewNamespaceName(v);
                setNsNameValidation(validateNamespaceName(v.trim()));
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && nsNameValidation?.ok) handleCreateNamespace();
              }}
              placeholder="e.g., my-namespace"
              disabled={creatingNamespace}
              autoFocus
              aria-invalid={nsNameValidation?.ok === false || undefined}
              maxLength={63}
            />
            <span
              className={`ns-input-counter${newNamespaceName.length > 63 ? ' ns-input-counter-over' : ''}`}
              title="Character count (max 63)"
            >
              {newNamespaceName.length}/63
            </span>
          </div>

          <ul className="ns-rules" aria-label="Namespace name rules">
            {namespaceRules(newNamespaceName).map((r) => {
              const state = !newNamespaceName.trim() ? 'pending' : r.ok ? 'ok' : 'fail';
              return (
                <li key={r.id} className={`ns-rule ns-rule-${state}`}>
                  <span className="ns-rule-icon" aria-hidden="true">
                    {state === 'ok' ? '✓' : state === 'fail' ? '✕' : '•'}
                  </span>
                  <span className="ns-rule-text">{r.label}</span>
                </li>
              );
            })}
          </ul>

          {newNamespaceName.trim() &&
            nsNameValidation?.ok !== false &&
            namespaces.some((n) => n.name === newNamespaceName.trim()) && (
              <p className="ns-dup-warning" role="alert">
                <WarningIcon className="app-icon" aria-hidden="true" /> A namespace named “
                {newNamespaceName.trim()}” already exists in this cluster. Choose a different name
                or open the existing one.
              </p>
            )}
        </div>

        <div className="modal-footer">
          <button
            className="modal-btn modal-btn-cancel"
            onClick={() => setIsModalOpen(false)}
            disabled={creatingNamespace}
          >
            Cancel
          </button>
          <button
            className="modal-btn modal-btn-confirm"
            onClick={handleCreateNamespace}
            disabled={
              creatingNamespace ||
              !newNamespaceName.trim() ||
              nsNameValidation?.ok === false ||
              namespaces.some((n) => n.name === newNamespaceName.trim())
            }
            title={nsNameValidation?.ok === false ? nsNameValidation.message : undefined}
          >
            {creatingNamespace ? (
              <>
                <LoadingIcon className="app-icon" aria-hidden="true" /> Creating...
              </>
            ) : (
              '✓ Create'
            )}
          </button>
        </div>
      </Modal>

      <Modal
        isOpen={!!deleteTarget}
        onRequestClose={() => !deletingNamespace && setDeleteTarget(null)}
        className="delete-confirm-modal"
        overlayClassName="delete-confirm-overlay"
        shouldCloseOnEsc={!deletingNamespace}
        shouldCloseOnOverlayClick={!deletingNamespace}
      >
        <div className="modal-header">
          <h2>
            <WarningIcon className="app-icon" aria-hidden="true" /> Confirm Namespace Deletion
          </h2>
          <button
            className="modal-close-btn"
            onClick={() => setDeleteTarget(null)}
            disabled={deletingNamespace}
          >
            <CloseIcon className="app-icon" />
          </button>
        </div>

        <div className="modal-body">
          <p>
            Are you sure you want to delete the namespace <strong>'{deleteTarget?.name}'</strong>?
          </p>
          <p style={{ color: '#d32f2f', fontWeight: 600, marginTop: '1rem' }}>
            This action cannot be undone.
          </p>
          <div
            style={{
              marginTop: '12px',
              paddingTop: '10px',
              borderTop: '1px solid #eee',
              display: 'flex',
              flexDirection: 'column',
              gap: '8px',
            }}
          >
            <label
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
                fontSize: '0.88rem',
                color: '#444',
                cursor: 'pointer',
                userSelect: 'none',
              }}
            >
              <input
                type="checkbox"
                checked={deletePositionsChecked}
                onChange={(e) => setDeletePositionsChecked(e.target.checked)}
                style={{
                  width: '15px',
                  height: '15px',
                  accentColor: '#d32f2f',
                  cursor: 'pointer',
                  flexShrink: 0,
                }}
              />
              Also delete saved node positions
            </label>
            <label
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
                fontSize: '0.88rem',
                color: '#444',
                cursor: 'pointer',
                userSelect: 'none',
              }}
            >
              <input
                type="checkbox"
                checked={deleteFilesChecked}
                onChange={(e) => setDeleteFilesChecked(e.target.checked)}
                style={{
                  width: '15px',
                  height: '15px',
                  accentColor: '#d32f2f',
                  cursor: 'pointer',
                  flexShrink: 0,
                }}
              />
              Also delete namespace files (file manager)
            </label>
          </div>
        </div>

        <div className="modal-footer">
          <button
            className="modal-btn modal-btn-cancel"
            onClick={() => setDeleteTarget(null)}
            disabled={deletingNamespace}
          >
            Cancel
          </button>
          <button
            className="modal-btn modal-btn-confirm modal-btn-danger"
            onClick={handleConfirmDeleteNamespace}
            disabled={deletingNamespace}
          >
            {deletingNamespace ? (
              <>
                <LoadingIcon className="app-icon" aria-hidden="true" /> Deleting…
              </>
            ) : (
              <>
                <TrashIcon className="app-icon" aria-hidden="true" /> Delete namespace
              </>
            )}
          </button>
        </div>
      </Modal>

      <ErrorModal
        isOpen={errorMessage !== ''}
        message={errorMessage}
        onClose={() => setErrorMessage('')}
        title="Error"
      />

      <AlertModal
        isOpen={namespaceWarningOpen}
        type="warning"
        title="Namespace Terminating"
        message="This namespace is still terminating and cannot be opened yet. Wait until it disappears or becomes active again."
        onConfirm={() => setNamespaceWarningOpen(false)}
        onCancel={() => setNamespaceWarningOpen(false)}
        confirmText="OK"
        cancelText="Cancel"
      />

      {selectedNodeName && (
        <K8sNodeInfoPanel nodeName={selectedNodeName} onClose={() => setSelectedNodeName(null)} />
      )}
    </div>
  );
};

export default Home;
