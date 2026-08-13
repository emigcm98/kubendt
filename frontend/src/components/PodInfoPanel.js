import React, { useState, useEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { useNavigate } from 'react-router-dom';
import './PodInfoPanel.css';
import AlertModal from './AlertModal';
import InterfaceContextMenu from './InterfaceContextMenu';
import usePanelClose from './usePanelClose';
import { API_BASE_URL } from '../config';
import pcIcon from '../assets/images/nodes/host.svg';
import routerIcon from '../assets/images/nodes/router.svg';
import switchIcon from '../assets/images/nodes/switch.svg';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as FileIcon } from '../assets/images/icons/file.svg';
import { ReactComponent as EyeIcon } from '../assets/images/icons/eye.svg';
import { ReactComponent as LockIcon } from '../assets/images/icons/lock.svg';
import { ReactComponent as CopyIcon } from '../assets/images/icons/copy.svg';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { ReactComponent as SlidersIcon } from '../assets/images/icons/sliders.svg';

const POD_INFO_TAB_KEY_PREFIX = 'kubendt.podInfoPanel.activeTab.';
const VALID_TABS = new Set(['summary', 'driver', 'links', 'vars']);

// Copy text to the clipboard with a graceful fallback. navigator.clipboard
// is only available in secure contexts (HTTPS or localhost), so production
// deployments served over plain HTTP must fall back to the legacy
// execCommand('copy') trick via a hidden textarea.
const copyToClipboard = async (text) => {
  if (text == null) return false;
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // fall through to the legacy path
    }
  }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'absolute';
    ta.style.left = '-9999px';
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
};

const PodInfoPanel = ({
  pod,
  namespace,
  onOpenInteractiveShell,
  showInteractiveShell,
  onClosePanel,
  onRestartPod,
  onDeletePod,
  closeSignal,
  loadingInterfaces,
  setLoadingInterfaces,
  onUpdateInterface,
  onOpenTrafficControl,
  isBusy = false,
}) => {
  const navigate = useNavigate();
  const { isClosing, requestClose, handleAnimationEnd } = usePanelClose(onClosePanel, closeSignal);

  const [activeTab, setActiveTabRaw] = useState(() => {
    try {
      const stored = sessionStorage.getItem(POD_INFO_TAB_KEY_PREFIX + (namespace || ''));
      return stored && VALID_TABS.has(stored) ? stored : 'summary';
    } catch {
      return 'summary';
    }
  });

  const setActiveTab = (tab) => {
    setActiveTabRaw(tab);
    try {
      sessionStorage.setItem(POD_INFO_TAB_KEY_PREFIX + (namespace || ''), tab);
    } catch {
      /* sessionStorage unavailable, ignore */
    }
  };
  const [interfaces, setInterfaces] = useState([]);
  const [selectedInterface, setSelectedInterface] = useState(null);
  const [loadingLinks, setLoadingLinks] = useState(false);
  const [errorLinks, setErrorLinks] = useState(null);

  // --- QDISC STATES ---
  // Read-only qdisc status for the Links tab badge; editing lives in TCPanel.
  const [qdiscData, setQdiscData] = useState(null);
  const [loadingQdisc, setLoadingQdisc] = useState(false);

  const [copyFloater, setCopyFloater] = useState(null);

  const handleCopyImage = (e) => {
    if (!pod.image) return;
    const { clientX, clientY } = e;
    copyToClipboard(pod.image).then((ok) => {
      if (!ok) return;
      setCopyFloater({ x: clientX, y: clientY, id: Date.now() });
      setTimeout(() => setCopyFloater(null), 1000);
    });
  };

  // --- DRIVER TAB STATES ---
  const [driverCaps, setDriverCaps] = useState([]);
  const [driverExecutor, setDriverExecutor] = useState('');
  const [driverInterfaceConstraints, setDriverInterfaceConstraints] = useState(null);
  const [loadingDriverCaps, setLoadingDriverCaps] = useState(false);
  const [errorDriverCaps, setErrorDriverCaps] = useState(null);
  const [selectedCapabilityId, setSelectedCapabilityId] = useState(null);
  const [methodPopover, setMethodPopover] = useState({
    visible: false,
    methodName: '',
    top: 0,
    left: 0,
    params: [],
  });
  const [driverViewMode, setDriverViewMode] = useState('capabilities');

  // --- METRICS ---
  const [podMetrics, setPodMetrics] = useState(null); // null = loading, false = unavailable, object = data
  const metricsIntervalRef = useRef(null);
  const metricsUnavailableRef = useRef(false); // true = stop polling permanently until pod changes

  // --- RESTART COUNTER ---
  const [restartElapsed, setRestartElapsed] = useState(null); // seconds since restart, null = idle
  const restartIntervalRef = useRef(null);

  const [driverHistory, setDriverHistory] = useState([]);
  const [loadingDriverHistory, setLoadingDriverHistory] = useState(false);
  const [errorDriverHistory, setErrorDriverHistory] = useState(null);
  const [selectedHistoryEntry, setSelectedHistoryEntry] = useState(null);
  const [deletingHistoryId, setDeletingHistoryId] = useState(null);
  const [clearingPodHistory, setClearingPodHistory] = useState(false);
  const [historyBtnTooltip, setHistoryBtnTooltip] = useState({ visible: false, top: 0, left: 0 });
  const [historyAlert, setHistoryAlert] = useState({
    isOpen: false,
    type: 'info',
    title: '',
    message: '',
    onConfirm: null,
    onCancel: null,
    confirmText: 'Accept',
    cancelText: 'Cancel',
  });

  const displayName = pod.replicaCount > 1 ? pod.name : pod.baseName;
  const runtimeLabel = pod?.runtime === 'qemu' ? 'qemu-based' : 'k8s linux host-based';
  const driverDisplay = pod?.driver || '—';

  const handleOpenFile = (fileName) => {
    if (!fileName) return;
    navigate(`/${namespace}/files?file=${encodeURIComponent(fileName)}`);
  };

  const handleRestart = () => {
    if (onRestartPod) {
      setRestartElapsed(0);
      if (restartIntervalRef.current) clearInterval(restartIntervalRef.current);
      restartIntervalRef.current = setInterval(() => {
        setRestartElapsed((prev) => prev + 1);
      }, 1000);
      onRestartPod(pod.name);
    }
  };

  const fetchPodMetrics = async () => {
    if (!pod?.name || !namespace) return;
    if (metricsUnavailableRef.current) return;
    try {
      const res = await fetch(`${API_BASE_URL}/pods/metrics/${namespace}/${pod.name}`);
      if (!res.ok) {
        // Non-2xx means unavailable (503 = no metrics-server, 404 = bad route)
        metricsUnavailableRef.current = true;
        setPodMetrics(false);
        if (metricsIntervalRef.current) clearInterval(metricsIntervalRef.current);
        return;
      }
      const data = await res.json();
      if (!data.available) {
        metricsUnavailableRef.current = true;
        setPodMetrics(false);
        if (metricsIntervalRef.current) clearInterval(metricsIntervalRef.current);
        return;
      }
      setPodMetrics(data);
    } catch (_) {
      // Network error: leave podMetrics as null, will retry
    }
  };

  // Effect: poll metrics every 10s when summary tab is active; reset unavailable flag when pod changes
  useEffect(() => {
    metricsUnavailableRef.current = false;
    setPodMetrics(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pod?.name, namespace]);

  useEffect(() => {
    if (activeTab !== 'summary') {
      if (metricsIntervalRef.current) clearInterval(metricsIntervalRef.current);
      return;
    }
    if (metricsUnavailableRef.current) return;
    fetchPodMetrics();
    metricsIntervalRef.current = setInterval(fetchPodMetrics, 10000);
    return () => clearInterval(metricsIntervalRef.current);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab, pod?.name, namespace]);

  // Effect: stop restart counter when pod comes back Running
  useEffect(() => {
    if (restartElapsed !== null && pod?.status === 'Running') {
      clearInterval(restartIntervalRef.current);
      restartIntervalRef.current = null;
      setRestartElapsed(null);
    }
  }, [pod?.status, restartElapsed]);

  const formatHistoryDate = (value) => {
    if (!value) return '—';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return String(value);
    return date.toLocaleString();
  };

  const formatActionValue = (value) => {
    if (value === null || value === undefined) return '—';
    if (Array.isArray(value)) return value.length ? value.join(', ') : '—';
    if (typeof value === 'object') return JSON.stringify(value);
    if (value === '') return '—';
    return String(value);
  };

  const extractActionDetails = (action = {}) => {
    return Object.entries(action)
      .filter(([key, value]) => {
        if (key === 'type') return false;
        if (value === null || value === undefined) return false;
        if (typeof value === 'string' && value.trim() === '') return false;
        if (Array.isArray(value) && value.length === 0) return false;
        return true;
      })
      .map(([key, value]) => ({
        key,
        value: formatActionValue(value),
      }));
  };

  const loadDriverCaps = async () => {
    if (!pod?.driver) {
      setDriverCaps([]);
      setDriverExecutor('');
      setDriverInterfaceConstraints(null);
      setSelectedCapabilityId(null);
      setErrorDriverCaps(null);
      return;
    }

    setLoadingDriverCaps(true);
    setErrorDriverCaps(null);

    try {
      const res = await fetch(`${API_BASE_URL}/drivers/${pod.driver}`);
      if (!res.ok) throw new Error(`Error ${res.status}`);
      const data = await res.json();
      setDriverExecutor(data?.executor || '');
      // Backend omits this field when the driver imposes no extra rules
      // beyond Linux kernel naming, so a null here is the normal case
      // for hosts and generic switches.
      setDriverInterfaceConstraints(data?.interfaceNameConstraints || null);

      const normalizedCaps = (data.capabilities || []).map((cap) => {
        const rawMethods = cap?.methods;
        let methods = [];

        if (Array.isArray(rawMethods)) {
          methods = rawMethods.map((method) => ({
            name: method?.name || '',
            label: method?.label || method?.name || '',
            params: Array.isArray(method?.params) ? method.params : [],
          }));
        } else if (rawMethods && typeof rawMethods === 'object') {
          methods = Object.entries(rawMethods).map(([name, label]) => ({
            name,
            label: String(label || name),
            params: [],
          }));
        }

        return {
          ...cap,
          methods,
        };
      });

      setDriverCaps(normalizedCaps);
      if (normalizedCaps.length > 0) {
        setSelectedCapabilityId((prev) => prev || normalizedCaps[0].id);
      } else {
        setSelectedCapabilityId(null);
      }
    } catch (err) {
      console.error('❌ Error loading driver capabilities:', err);
      setDriverCaps([]);
      setDriverExecutor('');
      setDriverInterfaceConstraints(null);
      setSelectedCapabilityId(null);
      setErrorDriverCaps('Error loading driver capabilities');
    } finally {
      setLoadingDriverCaps(false);
    }
  };

  const loadDriverHistory = async () => {
    if (!pod?.name || !namespace) {
      setDriverHistory([]);
      setErrorDriverHistory(null);
      return;
    }

    setLoadingDriverHistory(true);
    setErrorDriverHistory(null);

    try {
      const res = await fetch(
        `${API_BASE_URL}/drivers/history/${encodeURIComponent(namespace)}/${encodeURIComponent(pod.name)}`
      );
      if (!res.ok) throw new Error(`Error ${res.status}`);
      const data = await res.json();
      setDriverHistory(Array.isArray(data.operations) ? data.operations : []);
    } catch (err) {
      console.error('❌ Error loading driver operation history:', err);
      setDriverHistory([]);
      setErrorDriverHistory('Error loading driver history');
    } finally {
      setLoadingDriverHistory(false);
    }
  };

  const deleteDriverHistoryEntry = async (operationId) => {
    if (!operationId) return;

    setHistoryAlert({
      isOpen: true,
      type: 'confirm',
      title: 'Delete operation',
      message: `Delete operation #${operationId}?`,
      confirmText: 'Delete',
      cancelText: 'Cancel',
      onConfirm: async () => {
        setHistoryAlert((prev) => ({ ...prev, isOpen: false }));

        setDeletingHistoryId(operationId);
        try {
          const res = await fetch(`${API_BASE_URL}/drivers/history/${operationId}`, {
            method: 'DELETE',
          });

          if (!res.ok) {
            let message = `Error ${res.status}`;
            try {
              const data = await res.json();
              if (data?.error) message = data.error;
            } catch {
              // ignore response parsing errors
            }
            throw new Error(message);
          }

          setDriverHistory((prev) => prev.filter((entry) => entry.id !== operationId));
          setSelectedHistoryEntry((prev) => (prev?.id === operationId ? null : prev));
        } catch (err) {
          console.error('❌ Error deleting driver operation history entry:', err);
          setHistoryAlert({
            isOpen: true,
            type: 'error',
            title: 'Delete failed',
            message: `Error deleting operation #${operationId}: ${err.message}`,
            confirmText: 'Accept',
            cancelText: 'Cancel',
            onConfirm: () => setHistoryAlert((prev) => ({ ...prev, isOpen: false })),
            onCancel: null,
          });
        } finally {
          setDeletingHistoryId(null);
        }
      },
      onCancel: () => setHistoryAlert((prev) => ({ ...prev, isOpen: false })),
    });
  };

  const deletePodDriverHistory = async () => {
    if (!namespace || !pod?.name) return;
    setHistoryAlert({
      isOpen: true,
      type: 'confirm',
      title: 'Delete pod history',
      message: `Delete ALL driver operations for pod ${pod.name}?`,
      confirmText: 'Delete',
      cancelText: 'Cancel',
      onConfirm: async () => {
        setHistoryAlert((prev) => ({ ...prev, isOpen: false }));

        setClearingPodHistory(true);
        try {
          const res = await fetch(
            `${API_BASE_URL}/drivers/history/namespace/${encodeURIComponent(namespace)}/pod/${encodeURIComponent(pod.name)}`,
            {
              method: 'DELETE',
            }
          );

          if (!res.ok) {
            let message = `Error ${res.status}`;
            try {
              const data = await res.json();
              if (data?.error) message = data.error;
            } catch {
              // ignore response parsing errors
            }
            throw new Error(message);
          }

          setDriverHistory([]);
          setSelectedHistoryEntry(null);
          setErrorDriverHistory(null);
          loadDriverHistory();
        } catch (err) {
          console.error('❌ Error deleting pod driver history:', err);
          setHistoryAlert({
            isOpen: true,
            type: 'error',
            title: 'Delete failed',
            message: `Error deleting pod history: ${err.message}`,
            confirmText: 'Accept',
            cancelText: 'Cancel',
            onConfirm: () => setHistoryAlert((prev) => ({ ...prev, isOpen: false })),
            onCancel: null,
          });
        } finally {
          setClearingPodHistory(false);
        }
      },
      onCancel: () => setHistoryAlert((prev) => ({ ...prev, isOpen: false })),
    });
  };

  // Fetchdriver capabilities when switching to "driver" tab
  useEffect(() => {
    if (activeTab === 'driver') loadDriverCaps();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab, pod?.driver]);

  useEffect(() => {
    setDriverViewMode('capabilities');
    setDriverHistory([]);
    setLoadingDriverHistory(false);
    setErrorDriverHistory(null);
    setSelectedHistoryEntry(null);
  }, [namespace, pod?.name, pod?.driver]);

  // When opening Links, make sure to have capabilities before continuing
  useEffect(() => {
    if (activeTab !== 'links') return;
    if (driverCaps.length === 0 && !loadingDriverCaps && !errorDriverCaps) {
      // prefetch if not yet loaded
      loadDriverCaps();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab, pod?.driver]);

  // Fetchinterfaces when switching to "links" tab
  useEffect(() => {
    const fetchLinks = async () => {
      if (activeTab !== 'links') return;

      // Espera a tener el resultado de capabilities
      if (loadingDriverCaps) return;

      setLoadingLinks(true);
      setErrorLinks(null);
      try {
        const res = await fetch(`${API_BASE_URL}/pods/ips/${namespace}/${pod.name}`);
        if (!res.ok) throw new Error(`Error ${res.status}`);
        const data = await res.json();
        setInterfaces(data.interfaces || []);
      } catch (err) {
        setErrorLinks('Error loading interfaces');
        console.error('❌ Error fetching interfaces:', err);
      } finally {
        setLoadingLinks(false);
      }
    };
    fetchLinks();
  }, [activeTab, namespace, pod.name, driverCaps, loadingDriverCaps]);

  // Right-click an interface to enable/disable it, reusing the graph's context
  // menu. The menu is portaled to the body since the panel clips its overflow.
  const [ifaceMenu, setIfaceMenu] = useState(null);

  // Toggle through the same shared loading set and optimistic graph update the
  // graph edge labels use, so the spinner shows in both places and the graph
  // reflects the change immediately instead of waiting for the next refresh.
  const toggleInterfaceState = async (podName, iface, currentIsUp) => {
    const actionType = currentIsUp ? 'link_down' : 'link_up';
    const key = `${podName}:${iface}`;
    setLoadingInterfaces?.((prev) => new Set([...prev, key]));
    try {
      await fetch(`${API_BASE_URL}/network/configure/${namespace}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          targets: [
            {
              pod: podName,
              actions: [{ type: actionType, iface, options: { persist_history: false } }],
            },
          ],
        }),
      });
      const res = await fetch(
        `${API_BASE_URL}/pods/ips/${namespace}/${podName}?intf=${encodeURIComponent(iface)}`
      );
      if (res.ok) {
        const data = await res.json();
        const match = data.interfaces?.find((i) => i.interface === iface);
        if (match) {
          setInterfaces((prev) =>
            prev.map((it) => (it.interface === iface ? { ...it, state: match.state } : it))
          );
          onUpdateInterface?.(podName, iface, match.state === 'up');
        }
      }
    } catch (err) {
      console.error('Error toggling interface state:', err);
    } finally {
      setLoadingInterfaces?.((prev) => {
        const next = new Set(prev);
        next.delete(key);
        return next;
      });
    }
  };

  const fetchQdisc = async (iface) => {
    setLoadingQdisc(true);
    try {
      const res = await fetch(`${API_BASE_URL}/pods/tc/${namespace}/${pod.name}/${iface}`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      setQdiscData(data.tcparams || null);
    } catch (err) {
      console.error('❌ Error fetching qdisc:', err);
      setQdiscData(null);
    } finally {
      setLoadingQdisc(false);
    }
  };

  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') requestClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [requestClose]);

  // --- Refrescar qdisc al cambiar interfaz / pod / ns
  useEffect(() => {
    if (selectedInterface) fetchQdisc(selectedInterface);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedInterface, namespace, pod.name]);

  if (!pod) return null;

  return (
    <div className="panel-wrapper">
      {showInteractiveShell && <div className="panel-overlay" />}
      <div
        className={`pod-info-panel${isClosing ? ' is-closing' : ''}`}
        onAnimationEnd={handleAnimationEnd}
      >
        <button
          className="restart-btn"
          onClick={handleRestart}
          title={isBusy ? 'Operation in progress…' : 'Restart pod (Reboot)'}
          disabled={restartElapsed !== null || isBusy}
        >
          {restartElapsed !== null ? (
            <span className="restart-counter">{restartElapsed}s</span>
          ) : (
            <RefreshIcon className="app-icon" />
          )}
        </button>
        {onDeletePod && (
          <button
            className="delete-btn"
            onClick={() => onDeletePod(pod)}
            title={isBusy ? 'Operation in progress…' : 'Delete pod (remove from topology)'}
            disabled={isBusy}
          >
            <TrashIcon className="app-icon" />
          </button>
        )}
        <button className="close-btn" onClick={requestClose} title="Close panel">
          <CloseIcon className="app-icon" />
        </button>

        <h3>
          {pod.type === 'host' && <img src={pcIcon} alt="Host" className="pod-type-icon" />}
          {pod.type === 'router' && <img src={routerIcon} alt="Router" className="pod-type-icon" />}
          {pod.type === 'switch' && <img src={switchIcon} alt="Switch" className="pod-type-icon" />}
          {displayName}
        </h3>

        {/* Tabs */}
        <div className="pod-tabs">
          <div
            className={`pod-tab ${activeTab === 'summary' ? 'active' : ''}`}
            onClick={() => setActiveTab('summary')}
            title="Summary information about the pod"
          >
            Summary
          </div>
          <div
            className={`pod-tab ${activeTab === 'driver' ? 'active' : ''}`}
            onClick={() => setActiveTab('driver')}
            title="Driver capabilities and methods"
          >
            Driver
          </div>
          <div
            className={`pod-tab ${activeTab === 'links' ? 'active' : ''}`}
            onClick={() => setActiveTab('links')}
            title="Network links and interfaces"
          >
            Links
          </div>
          <div
            className={`pod-tab ${activeTab === 'vars' ? 'active' : ''}`}
            onClick={() => setActiveTab('vars')}
            title="Environment variables and mounts"
          >
            Files/Vars
          </div>
        </div>

        <div className="pod-body">
          {/* === SUMMARY === */}
          {activeTab === 'summary' && (
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
              <hr />
              <div className="pod-line">
                <span className="pod-label">K8s Node:</span>
                <span className="pod-value">{pod.node}</span>
              </div>
              <div className="pod-line">
                <span className="pod-label">Image:</span>
                <span
                  className="pod-value pod-value-image"
                  title={pod.image}
                  onClick={handleCopyImage}
                >
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
                <span
                  className={`pod-value podinfo-status podinfo-status-${pod.status.toLowerCase()}`}
                >
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
                <div className="pod-metrics-item">
                  <span className="pod-metrics-label">CPU</span>
                  {podMetrics === null ? (
                    <span className="pod-metrics-value pod-metrics-loading">…</span>
                  ) : podMetrics === false ? (
                    <span className="pod-metrics-value pod-metrics-na">
                      n/a
                      <span className="pod-metrics-tooltip">
                        Error: metrics-server not available.
                      </span>
                    </span>
                  ) : (
                    <span className="pod-metrics-value">
                      {(podMetrics.cpu_milli / 1000).toFixed(2)} <em>vCPU</em>
                    </span>
                  )}
                </div>
                <div className="pod-metrics-item">
                  <span className="pod-metrics-label">RAM</span>
                  {podMetrics === null ? (
                    <span className="pod-metrics-value pod-metrics-loading">…</span>
                  ) : podMetrics === false ? (
                    <span className="pod-metrics-value pod-metrics-na">
                      n/a
                      <span className="pod-metrics-tooltip">
                        Error: metrics-server not available.
                      </span>
                    </span>
                  ) : (
                    <span className="pod-metrics-value">
                      {podMetrics.memory_mib} <em>MiB</em>
                    </span>
                  )}
                </div>
              </div>
            </>
          )}
          {/* === DRIVER === */}
          {activeTab === 'driver' && (
            <div className="driver-tab">
              {loadingDriverCaps && <p className="links-loading">Loading capabilities...</p>}
              {errorDriverCaps && <p className="links-error">{errorDriverCaps}</p>}

              {!loadingDriverCaps && !errorDriverCaps && (
                <>
                  <div className="driver-header driver-header-actions">
                    {driverViewMode === 'history' ? (
                      <button
                        className="driver-nav-btn"
                        title="Back"
                        onClick={() => setDriverViewMode('capabilities')}
                      >
                        ←
                      </button>
                    ) : (
                      <span className="driver-nav-spacer" />
                    )}

                    <span className="driver-title">{driverDisplay}</span>

                    {driverViewMode === 'capabilities' ? (
                      <button
                        className="driver-nav-btn"
                        onClick={() => {
                          setDriverViewMode('history');
                          loadDriverHistory();
                        }}
                        onMouseEnter={(e) => {
                          const rect = e.currentTarget.getBoundingClientRect();
                          setHistoryBtnTooltip({
                            visible: true,
                            top: rect.top - 6,
                            left: rect.left + rect.width / 2,
                          });
                        }}
                        onMouseLeave={() =>
                          setHistoryBtnTooltip({ visible: false, top: 0, left: 0 })
                        }
                      >
                        <FileIcon className="app-icon" />
                      </button>
                    ) : (
                      <button
                        className="driver-history-clear-pod-btn"
                        onClick={deletePodDriverHistory}
                        disabled={clearingPodHistory}
                        title="Delete all operations for this pod"
                      >
                        {clearingPodHistory ? '…' : <TrashIcon className="app-icon" />}
                      </button>
                    )}
                  </div>

                  {driverViewMode === 'history' ? (
                    <div className="driver-history-panel">
                      {loadingDriverHistory && <p className="links-loading">Loading history...</p>}
                      {errorDriverHistory && <p className="links-error">{errorDriverHistory}</p>}
                      {!loadingDriverHistory &&
                        !errorDriverHistory &&
                        (driverHistory.length === 0 ? (
                          <p className="no-interfaces">No operations found</p>
                        ) : (
                          <>
                            <div className="driver-history-list">
                              {driverHistory.map((entry) => (
                                <div className="driver-history-item" key={entry.id}>
                                  <div className="driver-history-row">
                                    <div className="driver-history-action">
                                      {entry.action_type || entry?.action?.type || 'unknown'}
                                    </div>
                                    <div className="driver-history-actions">
                                      <button
                                        className="driver-history-info-btn"
                                        title="Show action details"
                                        onClick={() => setSelectedHistoryEntry(entry)}
                                      >
                                        <EyeIcon className="app-icon" />
                                      </button>
                                      <button
                                        className="driver-history-delete-btn"
                                        title={`Delete operation #${entry.id}`}
                                        onClick={() => deleteDriverHistoryEntry(entry.id)}
                                        disabled={
                                          deletingHistoryId === entry.id || clearingPodHistory
                                        }
                                      >
                                        {deletingHistoryId === entry.id ? (
                                          '…'
                                        ) : (
                                          <TrashIcon className="app-icon" />
                                        )}
                                      </button>
                                    </div>
                                  </div>
                                  <div className="driver-history-date">
                                    {formatHistoryDate(entry.executed_at)}
                                  </div>
                                </div>
                              ))}
                            </div>
                          </>
                        ))}
                    </div>
                  ) : (
                    <div className="links-content">
                      <div className="interfaces-list">
                        {driverCaps.length === 0 ? (
                          <p className="no-interfaces">No capabilities found</p>
                        ) : (
                          driverCaps.map((cap) => (
                            <div
                              key={cap.id}
                              className={`interface-item ${selectedCapabilityId === cap.id ? 'selected' : ''}`}
                              onClick={() => setSelectedCapabilityId(cap.id)}
                              title={cap.description}
                            >
                              <div className="interface-name">
                                <strong>{cap.id}</strong>
                              </div>
                              <div className="interface-detail">{cap.label}</div>
                            </div>
                          ))
                        )}
                      </div>

                      <div className="interface-details">
                        {selectedCapabilityId ? (
                          <div
                            className="details-scroll"
                            style={{ textAlign: 'left', width: '100%' }}
                          >
                            {(() => {
                              const selected = driverCaps.find(
                                (c) => c.id === selectedCapabilityId
                              );
                              const methods = selected?.methods || [];

                              return (
                                <>
                                  <h5>Executor</h5>
                                  <p className="cap-desc">
                                    <code className="cap-code">{driverExecutor || 'kubectl'}</code>
                                  </p>
                                  <h5>Interface naming</h5>
                                  {driverInterfaceConstraints ? (
                                    <>
                                      <p className="cap-desc">
                                        Pattern:{' '}
                                        <code className="cap-code">
                                          {driverInterfaceConstraints.patternHuman ||
                                            driverInterfaceConstraints.pattern}
                                        </code>
                                      </p>
                                      {Array.isArray(driverInterfaceConstraints.reserved) &&
                                        driverInterfaceConstraints.reserved.length > 0 && (
                                          <p className="cap-desc">
                                            Reserved:{' '}
                                            <code className="cap-code">
                                              {driverInterfaceConstraints.reserved.join(', ')}
                                            </code>
                                          </p>
                                        )}
                                    </>
                                  ) : (
                                    <p className="cap-desc">
                                      Default Linux naming (any name up to 15 chars, no{' '}
                                      <code className="cap-code">/</code> or whitespace).
                                    </p>
                                  )}
                                  <h5>Description</h5>
                                  {selected?.description && (
                                    <p className="cap-desc">{selected.description}</p>
                                  )}
                                  <h5>Driver methods</h5>
                                  {methods.length === 0 ? (
                                    <p className="no-interfaces">No methods defined</p>
                                  ) : (
                                    <ul className="cap-methods-list">
                                      {methods.map((method, idx) => (
                                        <li
                                          key={`${method.name}-${idx}`}
                                          className="method-item"
                                          onMouseEnter={(e) => {
                                            const rect = e.currentTarget.getBoundingClientRect();
                                            setMethodPopover({
                                              visible: true,
                                              methodName: method.name,
                                              top: rect.top + rect.height / 2,
                                              left: rect.right + 12,
                                              params: method.params || [],
                                            });
                                          }}
                                          onMouseLeave={() => {
                                            setMethodPopover({
                                              visible: false,
                                              methodName: '',
                                              top: 0,
                                              left: 0,
                                              params: [],
                                            });
                                          }}
                                          style={{ position: 'relative' }}
                                        >
                                          <code className="method-code">{method.name}</code>
                                          <span className="method-signature">
                                            {method.label ? ` (${method.label})` : ''}
                                          </span>
                                        </li>
                                      ))}
                                    </ul>
                                  )}
                                </>
                              );
                            })()}
                          </div>
                        ) : (
                          <p className="no-selection">Select a capability to view more details</p>
                        )}
                      </div>
                    </div>
                  )}
                </>
              )}
            </div>
          )}
          {/* === LINKS === */}
          {activeTab === 'links' && (
            <div className="links-tab">
              {loadingLinks && <p className="links-loading">Loading interfaces...</p>}
              {errorLinks && <p className="links-error">{errorLinks}</p>}

              {!loadingLinks && !errorLinks && (
                <div className="links-content">
                  {/* Upper half: interfaces list */}
                  <div className="interfaces-list">
                    {interfaces.length === 0 ? (
                      <p className="no-interfaces">No interfaces found</p>
                    ) : (
                      interfaces.map((intf) => (
                        <div
                          key={intf.interface}
                          className={`interface-item ${selectedInterface === intf.interface ? 'selected' : ''}`}
                          onClick={() => setSelectedInterface(intf.interface)}
                          onContextMenu={(e) => {
                            e.preventDefault();
                            const status =
                              intf.state === 'up'
                                ? true
                                : intf.state === 'down'
                                  ? false
                                  : undefined;
                            setIfaceMenu({
                              x: e.clientX,
                              y: e.clientY,
                              iface: intf.interface,
                              status,
                            });
                          }}
                        >
                          <div className="interface-name">
                            {intf.interface}
                            {intf.guestInterface && (
                              <span className="interface-guest-name"> → {intf.guestInterface}</span>
                            )}
                            {loadingInterfaces?.has(`${pod.name}:${intf.interface}`) ? (
                              <span className="interface-state-spinner" title="applying…" />
                            ) : (
                              intf.state && (
                                <span
                                  className={`interface-state-dot ${intf.state === 'up' ? 'state-up' : 'state-down'}`}
                                  title={intf.state}
                                />
                              )
                            )}
                          </div>
                          <div className="interface-detail">IPv4: {intf.ipv4}</div>
                          <div className="interface-detail">MAC: {intf.mac}</div>
                        </div>
                      ))
                    )}
                  </div>

                  {/* Lower half: traffic-control status + open the editor panel */}
                  <div className="interface-details">
                    {!selectedInterface ? (
                      <p className="no-selection">Select an interface to view more details</p>
                    ) : (
                      <div className="tc-links-panel">
                        <div className="tc-links-head">
                          <span className="tc-links-title">Traffic control</span>
                          <span className={`tc-links-status ${qdiscData ? 'on' : 'off'}`}>
                            {loadingQdisc
                              ? 'checking…'
                              : qdiscData
                                ? `${qdiscData.qdisc} · active`
                                : 'No shaping'}
                          </span>
                        </div>
                        <button
                          className="tc-links-open"
                          onClick={() =>
                            onOpenTrafficControl &&
                            onOpenTrafficControl(pod.name, selectedInterface)
                          }
                        >
                          <SlidersIcon className="app-icon" />
                          Open traffic control
                        </button>
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}
          {/* === FILES/VARS === */}
          {activeTab === 'vars' && (
            <div className="pod-vars-tab">
              <div className="pod-vars-section">
                <div className="pod-vars-header">Environment Variables</div>
                <div className="pod-vars-list">
                  {Object.keys(pod.env || {}).length > 0 ? (
                    Object.entries(pod.env).map(([key, value]) => (
                      <div key={key} className="pod-vars-row" title={`${key}: ${String(value)}`}>
                        <span className="pod-vars-key" title={key}>
                          {key}
                        </span>
                        <span className="pod-vars-value" title={String(value)}>
                          {String(value)}
                        </span>
                      </div>
                    ))
                  ) : (
                    <div className="pod-vars-empty">No environment variables configured</div>
                  )}
                </div>
              </div>

              <div className="pod-vars-section">
                <div className="pod-vars-header">Mounted Files</div>
                <div className="pod-vars-list">
                  {(pod.mounts || []).length > 0 ? (
                    pod.mounts.map((mount, idx) => (
                      <div
                        key={`${mount.file}-${idx}`}
                        className="pod-vars-row pod-vars-mount-row"
                        title={`${mount.file} -> ${mount.mountTo}`}
                      >
                        <span
                          className={`pod-vars-key pod-vars-file${
                            mount.missing ? ' pod-vars-file-missing' : ''
                          }`}
                          title={
                            mount.missing
                              ? `${mount.file} is no longer present in the file manager`
                              : mount.file
                          }
                          onClick={mount.missing ? undefined : () => handleOpenFile(mount.file)}
                        >
                          {mount.missing && (
                            <span
                              className="mount-missing-warning"
                              title="File is no longer present in the file manager"
                              aria-label="Missing file"
                            >
                              <WarningIcon className="app-icon icon-warning" />
                            </span>
                          )}
                          {mount.sensitive && (
                            <span
                              className="sensitive-lock"
                              title="Sensitive file (Kubernetes Secret)"
                              aria-label="Sensitive"
                            >
                              <LockIcon className="app-icon" />
                            </span>
                          )}
                          {mount.file}
                        </span>
                        <div className="pod-vars-mount-target">
                          <span className="pod-vars-arrow">→</span>
                          <span className="pod-vars-value" title={mount.mountTo}>
                            {mount.mountTo}
                          </span>
                        </div>
                      </div>
                    ))
                  ) : (
                    <div className="pod-vars-empty">No mounted files</div>
                  )}
                </div>
              </div>
            </div>
          )}
        </div>

        {historyBtnTooltip.visible &&
          createPortal(
            <div
              className="driver-nav-portal-tooltip"
              style={{
                position: 'fixed',
                top: historyBtnTooltip.top,
                left: historyBtnTooltip.left,
                transform: 'translateX(-50%) translateY(-100%)',
                zIndex: 5000,
              }}
            >
              Show operation history
            </div>,
            document.body
          )}

        {methodPopover.visible &&
          createPortal(
            <div
              className="method-popover"
              style={{
                position: 'fixed',
                top: methodPopover.top,
                left: methodPopover.left,
                transform: 'translateY(-50%)',
                zIndex: 5000,
              }}
            >
              <div className="popover-content">
                <div className="popover-header">
                  <strong>{methodPopover.methodName}</strong>
                </div>
                {methodPopover.params && methodPopover.params.length > 0 ? (
                  <div className="popover-params">
                    <div className="params-label">Parameters:</div>
                    {methodPopover.params.map((param, pidx) => (
                      <div key={pidx} className="param-item">
                        <span className="param-name">{param.name}</span>
                        <span className="param-type">{param.type}</span>
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="popover-params">
                    <div className="params-label">No parameters</div>
                  </div>
                )}
              </div>
            </div>,
            document.body
          )}

        {selectedHistoryEntry &&
          createPortal(
            <div className="history-popup-overlay" onClick={() => setSelectedHistoryEntry(null)}>
              <div className="history-popup" onClick={(e) => e.stopPropagation()}>
                <div className="history-popup-header">
                  <strong>Driver action details</strong>
                  <button
                    className="history-popup-close"
                    onClick={() => setSelectedHistoryEntry(null)}
                  >
                    <CloseIcon className="app-icon" />
                  </button>
                </div>

                <div className="history-popup-body">
                  <div className="history-popup-line">
                    <span>ID:</span>
                    <b>{selectedHistoryEntry.id ?? '—'}</b>
                  </div>
                  <div className="history-popup-line">
                    <span>Action:</span>
                    <b>
                      {selectedHistoryEntry.action_type ||
                        selectedHistoryEntry?.action?.type ||
                        'unknown'}
                    </b>
                  </div>
                  <div className="history-popup-line">
                    <span>Date:</span>
                    <b>{formatHistoryDate(selectedHistoryEntry.executed_at)}</b>
                  </div>
                  <div className="history-popup-line">
                    <span>Pod:</span>
                    <b>{selectedHistoryEntry.pod_name || pod.name}</b>
                  </div>

                  <div className="history-popup-fields-title">Parameters</div>
                  {extractActionDetails(selectedHistoryEntry.action).length === 0 ? (
                    <p className="history-popup-empty">No parameters</p>
                  ) : (
                    <div className="history-popup-fields">
                      {extractActionDetails(selectedHistoryEntry.action).map((item) => (
                        <div key={item.key} className="history-popup-field-row">
                          <span className="history-popup-field-key">{item.key}</span>
                          <span className="history-popup-field-value">{item.value}</span>
                        </div>
                      ))}
                    </div>
                  )}

                  <div className="history-popup-fields-title">Equivalent commands</div>
                  {Array.isArray(selectedHistoryEntry.commands) &&
                  selectedHistoryEntry.commands.length > 0 ? (
                    <div className="history-popup-commands">
                      {selectedHistoryEntry.commands.map((cmd, index) => (
                        <div
                          key={`${selectedHistoryEntry.id}-cmd-${index}`}
                          className="history-popup-command-row"
                        >
                          <code className="history-popup-command">{cmd}</code>
                          <button
                            className="history-popup-copy-btn"
                            title="Copy command"
                            onClick={() => copyToClipboard(cmd)}
                          >
                            <CopyIcon className="app-icon" />
                          </button>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <p className="history-popup-empty">No equivalent command available</p>
                  )}
                </div>
              </div>
            </div>,
            document.body
          )}

        {ifaceMenu &&
          createPortal(
            <InterfaceContextMenu
              x={ifaceMenu.x}
              y={ifaceMenu.y}
              pod={pod.name}
              iface={ifaceMenu.iface}
              status={ifaceMenu.status}
              namespace={namespace}
              apiBase={API_BASE_URL}
              onClose={() => setIfaceMenu(null)}
              onToggle={() =>
                toggleInterfaceState(pod.name, ifaceMenu.iface, ifaceMenu.status === true)
              }
            />,
            document.body
          )}

        <AlertModal
          isOpen={historyAlert.isOpen}
          type={historyAlert.type}
          title={historyAlert.title}
          message={historyAlert.message}
          onConfirm={historyAlert.onConfirm}
          onCancel={historyAlert.onCancel}
          confirmText={historyAlert.confirmText}
          cancelText={historyAlert.cancelText}
        />
      </div>
    </div>
  );
};

export default PodInfoPanel;
