import React, { useState, useEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { useNavigate } from 'react-router-dom';
import './PodInfoPanel.css';
import AlertModal from './AlertModal';
import { API_BASE_URL } from '../config';
import pcIcon from '../assets/images/nodes/host.svg';
import routerIcon from '../assets/images/nodes/router.svg';
import switchIcon from '../assets/images/nodes/switch.svg';

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
  isBusy = false,
}) => {
  const navigate = useNavigate();

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
  const [qdiscData, setQdiscData] = useState(null);
  const [loadingQdisc, setLoadingQdisc] = useState(false);
  const [qdiscError, setQdiscError] = useState(null);
  const [feedback, setFeedback] = useState(null); // {type:'ok'|'error', text} banner after apply/remove
  const [isDraft, setIsDraft] = useState(false); // qdisc created in the UI but not yet applied
  const [newQdiscType, setNewQdiscType] = useState('netem'); // selector when no qdisc

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

  // --- Defaults ---
  const defaultNetem = {
    qdisc: 'netem',
    delay: '0ms',
    jitter: '0ms',
    loss: '0%',
    duplicate: '0%',
    corrupt: '0%',
    limit: 1000,
  };

  const defaultTBF = {
    qdisc: 'tbf',
    rate: '1mbit',
    burst: '32Kb',
    latency: '400ms',
  };

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

  const fetchQdisc = async (iface) => {
    const hasTC = driverCaps?.some((c) => c.id === 'TCCapable');
    if (!hasTC) return;
    setLoadingQdisc(true);
    setQdiscError(null);
    try {
      const res = await fetch(`${API_BASE_URL}/pods/tc/${namespace}/${pod.name}/${iface}`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      setQdiscData(data.tcparams || null);
      setIsDraft(false);
    } catch (err) {
      console.error('❌ Error fetching qdisc:', err);
      setQdiscError('Error fetching qdisc');
    } finally {
      setLoadingQdisc(false);
    }
  };

  // Auto-dismiss the feedback banner.
  useEffect(() => {
    if (!feedback) return;
    const t = setTimeout(() => setFeedback(null), 4000);
    return () => clearTimeout(t);
  }, [feedback]);

  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') onClosePanel();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClosePanel]);

  // --- Refrescar qdisc al cambiar interfaz / pod / ns
  useEffect(() => {
    if (selectedInterface) fetchQdisc(selectedInterface);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedInterface, namespace, pod.name]);

  // --- Create Qdisc (according to selected type) ---
  const handleCreateQdisc = () => {
    setQdiscData(newQdiscType === 'netem' ? defaultNetem : defaultTBF);
    setIsDraft(true);
    setFeedback(null);
  };

  // --- Cancel a draft qdisc (return to the no-qdisc view) ---
  const handleCancelDraft = () => {
    setQdiscData(null);
    setIsDraft(false);
    setFeedback(null);
  };

  // --- Front-end validations according to qdisc ---
  const validateNetem = (qd) => {
    const errors = [];
    const delayVal = parseInt(qd.delay) || 0;
    const jitterVal = parseInt(qd.jitter) || 0;
    const lossVal = parseFloat(qd.loss) || 0;
    const dupVal = parseFloat(qd.duplicate) || 0;
    const corruptVal = parseFloat(qd.corrupt) || 0;
    const limitVal = parseInt(qd.limit) || 0;

    if (delayVal < 0 || delayVal > 60000) errors.push('Delay must be between 0 and 60000 ms');
    if (jitterVal < 0 || jitterVal > 10000) errors.push('Jitter must be between 0 and 10000 ms');
    if (lossVal < 0 || lossVal > 100) errors.push('Loss must be between 0% and 100%');
    if (dupVal < 0 || dupVal > 100) errors.push('Duplicate must be between 0% and 100%');
    if (corruptVal < 0 || corruptVal > 100) errors.push('Corrupt must be between 0% and 100%');
    if (limitVal < 100 || limitVal > 10000) errors.push('Limit must be between 100 and 10000');

    // If there's jitter but no delay, error
    if (qd.jitter && parseInt(qd.jitter) > 0 && (!qd.delay || parseInt(qd.delay) === 0)) {
      errors.push('Jitter requires a non-zero Delay');
    }
    return errors;
  };

  const validateTBF = (qd) => {
    const errors = [];
    if (!qd.rate || qd.rate.trim() === '') {
      errors.push('Rate is required (e.g., 10mbit)');
    }
    // Latency 1–5000 ms
    const lat = parseInt(qd.latency) || 0;
    if (lat < 1 || lat > 5000) errors.push('Latency must be between 1 and 5000 ms');
    // Burst as text (we allow any suffix, backend validates more)
    if (!qd.burst || qd.burst.trim() === '') {
      errors.push('Burst is required (e.g., 32Kb)');
    }
    return errors;
  };

  // --- Apply ---
  const handleApplyQdisc = async () => {
    if (!selectedInterface || !qdiscData) return;

    // Validaciones por tipo
    let errors = [];
    if (qdiscData.qdisc === 'netem') {
      errors = validateNetem(qdiscData);
    } else if (qdiscData.qdisc === 'tbf') {
      errors = validateTBF(qdiscData);
    } else {
      errors = ['Unsupported qdisc type'];
    }

    if (errors.length > 0) {
      setFeedback({ type: 'error', text: errors.join('. ') });
      return;
    }

    // Send only the editable fields. The fetched qdisc carries read-only
    // fields (e.g. handle, parent) that the backend rejects with strict JSON.
    const cleanParams =
      qdiscData.qdisc === 'netem'
        ? {
            qdisc: 'netem',
            delay: qdiscData.delay,
            jitter: qdiscData.jitter,
            loss: qdiscData.loss,
            duplicate: qdiscData.duplicate,
            corrupt: qdiscData.corrupt,
            limit: qdiscData.limit,
          }
        : {
            qdisc: 'tbf',
            rate: qdiscData.rate,
            burst: qdiscData.burst,
            latency: qdiscData.latency,
          };

    const payload = {
      targets: [
        {
          pod: pod.name,
          actions: [
            {
              type: 'add_qdisc',
              iface: selectedInterface,
              tcparams: cleanParams,
            },
          ],
        },
      ],
    };

    try {
      const res = await fetch(`${API_BASE_URL}/network/configure/${namespace}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });

      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }

      setIsDraft(false);
      setFeedback({ type: 'ok', text: `${qdiscData.qdisc} applied to ${selectedInterface}` });
      fetchQdisc(selectedInterface);
    } catch (err) {
      console.error('❌ Error applying qdisc:', err);
      setFeedback({ type: 'error', text: `Could not apply qdisc: ${err.message}` });
    }
  };

  // --- Delete ---
  const handleDeleteQdisc = async () => {
    if (!selectedInterface) return;
    const payload = {
      targets: [
        {
          pod: pod.name,
          actions: [
            {
              type: 'del_qdisc',
              iface: selectedInterface,
            },
          ],
        },
      ],
    };
    try {
      const res = await fetch(`${API_BASE_URL}/network/configure/${namespace}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      // Limpia UI a estado "no qdisc"
      setQdiscData(null);
      setIsDraft(false);
      setNewQdiscType('netem');
      setFeedback({ type: 'ok', text: `Traffic shaping removed from ${selectedInterface}` });
    } catch (err) {
      console.error('❌ Error deleting qdisc:', err);
      setFeedback({ type: 'error', text: `Could not remove qdisc: ${err.message}` });
    }
  };

  // --- Render: No Qdisc (selector tipo + create) ---
  const renderNoQdisc = () => (
    <div className="qdisc-empty">
      <p className="qdisc-empty-text">
        This interface forwards traffic without shaping (<code>noqueue</code>).
      </p>
      <div className="qdisc-add">
        <select
          id="qdisc-type"
          className="qdisc-select"
          value={newQdiscType}
          onChange={(e) => setNewQdiscType(e.target.value)}
        >
          <option value="netem">netem</option>
          <option value="tbf">tbf</option>
        </select>
        <button className="qdisc-btn primary" onClick={handleCreateQdisc}>
          Add qdisc
        </button>
      </div>
      <p className="qdisc-hint">
        <strong>netem</strong> emulates delay, jitter and packet loss. <strong>tbf</strong> limits
        bandwidth.
      </p>
    </div>
  );

  // --- Render: Form according to qdisc ---
  const renderQdiscForm = () => {
    if (!qdiscData) return renderNoQdisc();

    switch (qdiscData.qdisc) {
      case 'netem':
        return (
          <div className="qdisc-config">
            <p className="qdisc-desc">
              Emulates link impairments: latency, jitter and packet loss, duplication or corruption.
            </p>

            <label>
              Delay (ms):
              <input
                type="number"
                min="0"
                max="60000"
                value={parseInt(qdiscData.delay) || 0}
                onChange={(e) => setQdiscData({ ...qdiscData, delay: e.target.value + 'ms' })}
              />
            </label>

            <label>
              Jitter (ms):
              <input
                type="number"
                min="0"
                max="10000"
                value={parseInt(qdiscData.jitter) || 0}
                onChange={(e) => setQdiscData({ ...qdiscData, jitter: e.target.value + 'ms' })}
              />
            </label>

            <label>
              Loss (%):
              <input
                type="number"
                min="0"
                max="100"
                step="0.1"
                value={parseFloat(qdiscData.loss) || 0}
                onChange={(e) => setQdiscData({ ...qdiscData, loss: e.target.value + '%' })}
              />
            </label>

            <label>
              Duplicate (%):
              <input
                type="number"
                min="0"
                max="100"
                step="0.1"
                value={parseFloat(qdiscData.duplicate) || 0}
                onChange={(e) => setQdiscData({ ...qdiscData, duplicate: e.target.value + '%' })}
              />
            </label>

            <label>
              Corrupt (%):
              <input
                type="number"
                min="0"
                max="100"
                step="0.1"
                value={parseFloat(qdiscData.corrupt) || 0}
                onChange={(e) => setQdiscData({ ...qdiscData, corrupt: e.target.value + '%' })}
              />
            </label>

            <label>
              Limit:
              <input
                type="number"
                min="100"
                max="10000"
                value={qdiscData.limit || 0}
                onChange={(e) => setQdiscData({ ...qdiscData, limit: parseInt(e.target.value) })}
              />
            </label>

            <div className="qdisc-actions">
              {isDraft && (
                <button className="qdisc-btn ghost" onClick={handleCancelDraft}>
                  Cancel
                </button>
              )}
              <button className="qdisc-btn primary" onClick={handleApplyQdisc}>
                {isDraft ? 'Apply' : 'Update'}
              </button>
              {!isDraft && (
                <button className="qdisc-btn danger" onClick={handleDeleteQdisc}>
                  Remove
                </button>
              )}
              <button
                className="qdisc-btn ghost"
                title="Reload current settings"
                onClick={() => fetchQdisc(selectedInterface)}
              >
                Refresh
              </button>
            </div>
          </div>
        );

      case 'tbf':
        return (
          <div className="qdisc-config">
            <p className="qdisc-desc">
              Token Bucket Filter: caps the egress bandwidth of this interface.
            </p>

            <label>
              Rate (Mbit):
              <input
                type="number"
                min="1"
                max="100000"
                value={parseInt(qdiscData.rate) || 0}
                onChange={(e) => setQdiscData({ ...qdiscData, rate: e.target.value + 'Mbit' })}
              />
            </label>

            <label>
              Burst (kbit):
              <input
                type="number"
                min="1"
                max="100000"
                value={parseInt(qdiscData.burst) || 0}
                onChange={(e) => setQdiscData({ ...qdiscData, burst: e.target.value + 'kb' })}
              />
            </label>

            <label>
              Latency (ms):
              <input
                type="number"
                min="1"
                max="5000"
                value={parseInt(qdiscData.latency) || 0}
                onChange={(e) => setQdiscData({ ...qdiscData, latency: e.target.value + 'ms' })}
              />
            </label>

            <div className="qdisc-actions">
              {isDraft && (
                <button className="qdisc-btn ghost" onClick={handleCancelDraft}>
                  Cancel
                </button>
              )}
              <button className="qdisc-btn primary" onClick={handleApplyQdisc}>
                {isDraft ? 'Apply' : 'Update'}
              </button>
              {!isDraft && (
                <button className="qdisc-btn danger" onClick={handleDeleteQdisc}>
                  Remove
                </button>
              )}
              <button
                className="qdisc-btn ghost"
                title="Reload current settings"
                onClick={() => fetchQdisc(selectedInterface)}
              >
                Refresh
              </button>
            </div>
          </div>
        );

      default:
        return renderNoQdisc();
    }
  };

  if (!pod) return null;

  return (
    <div className="panel-wrapper">
      {showInteractiveShell && <div className="panel-overlay" />}
      <div className="pod-info-panel">
        <button
          className="restart-btn"
          onClick={handleRestart}
          title={isBusy ? 'Operation in progress…' : 'Restart pod (Reboot)'}
          disabled={restartElapsed !== null || isBusy}
        >
          {restartElapsed !== null ? (
            <span className="restart-counter">{restartElapsed}s</span>
          ) : (
            '🔄'
          )}
        </button>
        {onDeletePod && (
          <button
            className="delete-btn"
            onClick={() => onDeletePod(pod)}
            title={isBusy ? 'Operation in progress…' : 'Delete pod (remove from topology)'}
            disabled={isBusy}
          >
            🗑️
          </button>
        )}
        <button className="close-btn" onClick={onClosePanel} title="Close panel">
          ✖
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
                        📄
                      </button>
                    ) : (
                      <button
                        className="driver-history-clear-pod-btn"
                        onClick={deletePodDriverHistory}
                        disabled={clearingPodHistory}
                        title="Delete all operations for this pod"
                      >
                        {clearingPodHistory ? '…' : '🗑'}
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
                                        👁
                                      </button>
                                      <button
                                        className="driver-history-delete-btn"
                                        title={`Delete operation #${entry.id}`}
                                        onClick={() => deleteDriverHistoryEntry(entry.id)}
                                        disabled={
                                          deletingHistoryId === entry.id || clearingPodHistory
                                        }
                                      >
                                        {deletingHistoryId === entry.id ? '…' : '🗑'}
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
                                  <p className="cap-desc">{driverExecutor || 'kubectl'}</p>
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
                        >
                          <div className="interface-name">
                            {intf.interface}
                            {intf.guestInterface && (
                              <span className="interface-guest-name"> → {intf.guestInterface}</span>
                            )}
                            {intf.state && (
                              <span
                                className={`interface-state-dot ${intf.state === 'up' ? 'state-up' : 'state-down'}`}
                                title={intf.state}
                              />
                            )}
                          </div>
                          <div className="interface-detail">IPv4: {intf.ipv4}</div>
                          <div className="interface-detail">MAC: {intf.mac}</div>
                        </div>
                      ))
                    )}
                  </div>

                  {/* Lower half: details (qdisc or message if no TCCapable) */}
                  <div className="interface-details">
                    {(() => {
                      const hasTC = driverCaps?.some((c) => c.id === 'TCCapable');

                      if (!selectedInterface) {
                        return (
                          <p className="no-selection">Select an interface to view more details</p>
                        );
                      }
                      if (loadingDriverCaps) {
                        return <p className="links-loading">Checking driver capabilities...</p>;
                      }
                      if (errorDriverCaps) {
                        return <p className="links-error">{errorDriverCaps}</p>;
                      }
                      if (!hasTC) {
                        return (
                          <p className="no-interfaces">
                            Unable to manage TC on this pod. Capability <strong>TCCapable</strong>{' '}
                            is not available for driver <strong>{driverDisplay}</strong>.
                          </p>
                        );
                      }
                      // Has TCCapable → qdisc UI
                      return (
                        <div className="details-scroll">
                          <div className="qdisc-panel">
                            <div className="qdisc-head">
                              <span className="qdisc-head-title">Traffic control</span>
                              <span
                                className={`qdisc-status ${qdiscData ? (isDraft ? 'draft' : 'on') : 'off'}`}
                              >
                                {qdiscData
                                  ? isDraft
                                    ? `${qdiscData.qdisc} · draft`
                                    : `${qdiscData.qdisc} · active`
                                  : 'No shaping'}
                              </span>
                            </div>
                            {feedback && (
                              <div className={`qdisc-feedback ${feedback.type}`}>
                                {feedback.type === 'ok' ? '✓ ' : '⚠ '}
                                {feedback.text}
                              </div>
                            )}
                            {loadingQdisc && <p className="qdisc-loading">Loading qdisc…</p>}
                            {qdiscError && <p className="error-text">{qdiscError}</p>}
                            {!loadingQdisc && renderQdiscForm()}
                          </div>
                        </div>
                      );
                    })()}
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
                        className="pod-vars-row"
                        title={`${mount.file} -> ${mount.mountTo}`}
                      >
                        <span
                          className="pod-vars-key pod-vars-file"
                          title={mount.file}
                          onClick={() => handleOpenFile(mount.file)}
                        >
                          {mount.sensitive && (
                            <span
                              className="sensitive-lock"
                              title="Sensitive file (Kubernetes Secret)"
                              aria-label="Sensitive"
                            >
                              🔒
                            </span>
                          )}
                          {mount.file}
                        </span>
                        <span className="pod-vars-arrow">→</span>
                        <span className="pod-vars-value" title={mount.mountTo}>
                          {mount.mountTo}
                        </span>
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
                    ✖
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
                            📋
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
