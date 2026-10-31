import React, { useState, useEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { useNavigate } from 'react-router-dom';
import './PodInfoPanel.css';
import AlertModal from './AlertModal';
import InterfaceContextMenu from './InterfaceContextMenu';
import usePanelClose from './usePanelClose';
import { API_BASE_URL } from '../config';
import { useCounters } from './counters';
import { copyToClipboard } from './podpanel/clipboard';
import SummaryTab from './podpanel/SummaryTab';
import StateTab from './podpanel/StateTab';
import ConfigTab from './podpanel/ConfigTab';
import LinksTab from './podpanel/LinksTab';
import VarsTab from './podpanel/VarsTab';
import pcIcon from '../assets/images/nodes/host.svg';
import routerIcon from '../assets/images/nodes/router.svg';
import switchIcon from '../assets/images/nodes/switch.svg';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as LogsIcon } from '../assets/images/icons/logs.svg';
import { ReactComponent as CopyIcon } from '../assets/images/icons/copy.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';

const POD_INFO_TAB_KEY_PREFIX = 'kubendt.podInfoPanel.activeTab.';
const TABS = [
  ['summary', 'Summary', 'Summary information about the pod'],
  ['state', 'State', 'Live state of the node: routes, neighbours, protocol tables'],
  ['config', 'Config', 'What the node has been configured with, and its driver'],
  ['links', 'Links', 'Network links and interfaces'],
  ['vars', 'Files/Vars', 'Environment variables and mounts'],
];
const VALID_TABS = new Set(TABS.map(([id]) => id));

// The node panel: a header with the pod's actions, five tabs, and the state
// the tabs share (the driver's capabilities, the history, the interfaces).
// Each tab's markup lives in ./podpanel.
const PodInfoPanel = ({
  pod,
  namespace,
  onOpenInteractiveShell,
  onOpenLogs,
  showInteractiveShell,
  onClosePanel,
  onRestartPod,
  onDeletePod,
  closeSignal,
  loadingInterfaces,
  setLoadingInterfaces,
  onUpdateInterface,
  onOpenTrafficControl,
  onStartCapture,
  isBusy = false,
}) => {
  const navigate = useNavigate();
  const { isClosing, requestClose, handleAnimationEnd } = usePanelClose(onClosePanel, closeSignal);

  const [activeTab, setActiveTabRaw] = useState(() => {
    try {
      const stored = sessionStorage.getItem(POD_INFO_TAB_KEY_PREFIX + (namespace || ''));
      // The driver tab became config.
      if (stored === 'driver') return 'config';
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

  // Read-only qdisc status for the Links tab badge; editing lives in TCPanel.
  const [qdiscByIface, setQdiscByIface] = useState({});
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

  // The driver's capabilities, read once per driver: the config tab's
  // reference and grouping, the state tab's section titles, the links tab.
  const [driverCaps, setDriverCaps] = useState([]);
  const [driverExecutor, setDriverExecutor] = useState('');
  const [driverInterfaceConstraints, setDriverInterfaceConstraints] = useState(null);
  const [driverKnownImages, setDriverKnownImages] = useState([]);
  const [loadingDriverCaps, setLoadingDriverCaps] = useState(false);
  const [errorDriverCaps, setErrorDriverCaps] = useState(null);

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
      setDriverKnownImages([]);
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
      setDriverKnownImages(Array.isArray(data?.knownImages) ? data.knownImages : []);

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
    } catch (err) {
      console.error('❌ Error loading driver capabilities:', err);
      setDriverCaps([]);
      setDriverExecutor('');
      setDriverInterfaceConstraints(null);
      setDriverKnownImages([]);
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

  const showHistoryError = (title, message) =>
    setHistoryAlert({
      isOpen: true,
      type: 'error',
      title,
      message,
      confirmText: 'Accept',
      cancelText: 'Cancel',
      onConfirm: () => setHistoryAlert((prev) => ({ ...prev, isOpen: false })),
      onCancel: null,
    });

  const deleteOne = async (operationId) => {
    const res = await fetch(`${API_BASE_URL}/drivers/history/${operationId}`, { method: 'DELETE' });
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
  };

  const deleteDriverHistoryEntry = (operationId) => {
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
          await deleteOne(operationId);
        } catch (err) {
          console.error('❌ Error deleting driver operation history entry:', err);
          showHistoryError(
            'Delete failed',
            `Error deleting operation #${operationId}: ${err.message}`
          );
        } finally {
          setDeletingHistoryId(null);
        }
      },
      onCancel: () => setHistoryAlert((prev) => ({ ...prev, isOpen: false })),
    });
  };

  // The operations of one capability, deleted one by one in order.
  const deleteDriverHistoryEntries = (ids, label) => {
    if (!ids?.length) return;
    setHistoryAlert({
      isOpen: true,
      type: 'confirm',
      title: `Delete ${label} operations`,
      message: `Delete the ${ids.length} ${label} operations of ${pod.name}?`,
      confirmText: 'Delete',
      cancelText: 'Cancel',
      onConfirm: async () => {
        setHistoryAlert((prev) => ({ ...prev, isOpen: false }));
        setClearingPodHistory(true);
        try {
          for (const id of ids) {
            // eslint-disable-next-line no-await-in-loop
            await deleteOne(id);
          }
        } catch (err) {
          console.error('❌ Error deleting driver operations:', err);
          showHistoryError('Delete failed', `Error deleting ${label} operations: ${err.message}`);
        } finally {
          setClearingPodHistory(false);
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
          showHistoryError('Delete failed', `Error deleting pod history: ${err.message}`);
        } finally {
          setClearingPodHistory(false);
        }
      },
      onCancel: () => setHistoryAlert((prev) => ({ ...prev, isOpen: false })),
    });
  };

  // The capabilities serve four tabs: read them once per node, the first
  // time one of them opens, and again when the node changes under an open
  // tab. A ref, since the reset below empties the state in the same commit.
  const capsFor = useRef('');
  useEffect(() => {
    const key = pod ? `${namespace}/${pod.name}` : '';
    if (!key || !['summary', 'config', 'state', 'links'].includes(activeTab)) return;
    if (capsFor.current === key) return;
    capsFor.current = key;
    loadDriverCaps();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab, namespace, pod?.name, pod?.driver]);

  useEffect(() => {
    setDriverCaps([]);
    setErrorDriverCaps(null);
    setDriverHistory([]);
    setLoadingDriverHistory(false);
    setErrorDriverHistory(null);
    setSelectedHistoryEntry(null);
  }, [namespace, pod?.name, pod?.driver]);

  // The history is read every time the config tab opens.
  useEffect(() => {
    if (activeTab === 'config') loadDriverHistory();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab, namespace, pod?.name]);

  // Fetch interfaces when switching to "links" tab
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

  // One exec reads the shaping state of every interface, so the list can badge
  // the shaped ones and the selected interface needs no read of its own.
  const fetchQdiscs = async () => {
    setLoadingQdisc(true);
    try {
      const res = await fetch(`${API_BASE_URL}/pods/tc/${namespace}/${pod.name}`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      setQdiscByIface(data.interfaces || {});
    } catch (err) {
      console.error('❌ Error fetching qdiscs:', err);
      setQdiscByIface({});
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

  // Refresh the shaping state when the pod changes or an interface is picked,
  // so the status is current after an apply in the traffic control panel.
  useEffect(() => {
    fetchQdiscs();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedInterface, namespace, pod.name]);

  // Counters of the picked interface, read while its detail is open.
  const counters = useCounters(namespace, pod?.name, activeTab === 'links' && !!selectedInterface);

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
        {onOpenLogs && (
          <button className="logs-btn" onClick={() => onOpenLogs(pod)} title="View the pod's log">
            <LogsIcon className="app-icon" />
          </button>
        )}
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

        <div className="pod-tabs">
          {TABS.map(([id, label, title]) => (
            <div
              key={id}
              className={`pod-tab ${activeTab === id ? 'active' : ''}`}
              onClick={() => setActiveTab(id)}
              title={title}
            >
              {label}
            </div>
          ))}
        </div>

        <div className="pod-body">
          {activeTab === 'summary' && (
            <SummaryTab
              pod={pod}
              runtimeLabel={runtimeLabel}
              driverDisplay={driverDisplay}
              driverCaps={driverCaps}
              driverKnownImages={driverKnownImages}
              podMetrics={podMetrics}
              onCopyImage={handleCopyImage}
              copyFloater={copyFloater}
            />
          )}
          {activeTab === 'state' && (
            <StateTab namespace={namespace} pod={pod} driverCaps={driverCaps} />
          )}
          {activeTab === 'config' && (
            <ConfigTab
              driverDisplay={driverDisplay}
              driverCaps={driverCaps}
              driverExecutor={driverExecutor}
              driverInterfaceConstraints={driverInterfaceConstraints}
              driverKnownImages={driverKnownImages}
              loadingDriverCaps={loadingDriverCaps}
              errorDriverCaps={errorDriverCaps}
              driverHistory={driverHistory}
              loadingDriverHistory={loadingDriverHistory}
              errorDriverHistory={errorDriverHistory}
              deletingHistoryId={deletingHistoryId}
              clearingPodHistory={clearingPodHistory}
              onReloadHistory={loadDriverHistory}
              onDeleteEntry={deleteDriverHistoryEntry}
              onDeleteEntries={deleteDriverHistoryEntries}
              onDeletePodHistory={deletePodDriverHistory}
              onShowEntry={setSelectedHistoryEntry}
              formatHistoryDate={formatHistoryDate}
            />
          )}
          {activeTab === 'links' && (
            <LinksTab
              pod={pod}
              interfaces={interfaces}
              loadingLinks={loadingLinks}
              errorLinks={errorLinks}
              selectedInterface={selectedInterface}
              onSelectInterface={setSelectedInterface}
              onInterfaceContextMenu={setIfaceMenu}
              qdiscByIface={qdiscByIface}
              loadingQdisc={loadingQdisc}
              loadingInterfaces={loadingInterfaces}
              counters={counters}
              onStartCapture={onStartCapture}
              onOpenTrafficControl={onOpenTrafficControl}
            />
          )}
          {activeTab === 'vars' && <VarsTab pod={pod} onOpenFile={handleOpenFile} />}
        </div>

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
