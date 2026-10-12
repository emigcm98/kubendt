import React, { useEffect, useMemo, useState } from 'react';
import { API_BASE_URL } from '../config';
import { useNavigate } from 'react-router-dom';
import './NamespaceNavbar.css';
import AlertModal from './AlertModal';
import DeleteExtrasCheckboxes from './DeleteExtrasCheckboxes';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as HomeIcon } from '../assets/images/icons/home.svg';
import { ReactComponent as FolderIcon } from '../assets/images/icons/folder.svg';
import { ReactComponent as TopologyIcon } from '../assets/images/icons/topology.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as ClockIcon } from '../assets/images/icons/clock.svg';
import kubendtLogo from '../assets/images/kubendt-logo.svg';

const plural = (type) => (type === 'switch' ? 'switches' : `${type}s`);

function NamespaceNavbar({
  namespace,
  onDelete,
  onDeleteHistory,
  onRefresh,
  disabled = false,
  stats = null,
}) {
  const navigate = useNavigate();

  // What the namespace uses, from metrics-server through the backend: read
  // every 15 s, its own resolution, and dropped for good when it answers
  // unavailable. null is loading, false is unavailable.
  const [nsMetrics, setNsMetrics] = useState(null);
  useEffect(() => {
    let stopped = false;
    let timer = null;
    const read = async () => {
      try {
        const res = await fetch(`${API_BASE_URL}/namespaces/metrics/${namespace}`);
        const data = await res.json();
        if (stopped) return;
        if (!res.ok || !data.available) {
          setNsMetrics(false);
          return;
        }
        setNsMetrics(data);
        timer = setTimeout(read, 15000);
      } catch {
        if (!stopped) setNsMetrics(false);
      }
    };
    setNsMetrics(null);
    read();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [namespace]);

  // The totals split by node type, for the hover text on the right column.
  const breakdown = useMemo(() => {
    if (!nsMetrics || !stats?.types) return '';
    const byType = {};
    for (const p of nsMetrics.pods || []) {
      const type = stats.types[p.pod] || 'other';
      const acc = byType[type] || { n: 0, cpu: 0, mib: 0 };
      acc.n += 1;
      acc.cpu += p.cpu_milli || 0;
      acc.mib += p.memory_mib || 0;
      byType[type] = acc;
    }
    return Object.entries(byType)
      .map(
        ([type, v]) =>
          `${v.n} ${v.n === 1 ? type : plural(type)}: ${(v.cpu / 1000).toFixed(2)} vCPU, ${v.mib} MiB`
      )
      .join('\n');
  }, [nsMetrics, stats]);
  const [showDeleteConfirmModal, setShowDeleteConfirmModal] = useState(false);
  const [showDeleteHistoryConfirmModal, setShowDeleteHistoryConfirmModal] = useState(false);
  const [deletePositionsChecked, setDeletePositionsChecked] = useState(false);
  const [deleteFilesChecked, setDeleteFilesChecked] = useState(false);

  const handleDelete = () => {
    if (disabled) return;
    setDeletePositionsChecked(false);
    setDeleteFilesChecked(false);
    setShowDeleteConfirmModal(true);
  };

  const confirmDelete = () => {
    setShowDeleteConfirmModal(false);
    onDelete(namespace, deletePositionsChecked, deleteFilesChecked);
  };

  return (
    <>
      <div className="navbar-container">
        <div className="navbar-left">
          <button className="navbar-button" onClick={() => navigate('/')}>
            <HomeIcon className="app-icon" aria-hidden="true" />
            Home
          </button>
          <button
            className="navbar-button navbar-button-danger"
            onClick={handleDelete}
            disabled={disabled}
          >
            <TrashIcon className="app-icon" aria-hidden="true" />
            Delete namespace
          </button>
          <button
            className="navbar-button navbar-button-namespace-history"
            onClick={() => {
              if (disabled) return;
              if (!onDeleteHistory) return;
              setShowDeleteHistoryConfirmModal(true);
            }}
            disabled={disabled}
          >
            <ClockIcon className="app-icon" aria-hidden="true" />
            Delete history
          </button>
        </div>

        <div className="navbar-center-group">
          <div className="navbar-stat navbar-stat-left" title="Pods and links in this topology">
            {stats && stats.pods > 0 && (
              <>
                <span>
                  {stats.pods} <em>{stats.pods === 1 ? 'pod' : 'pods'}</em>
                </span>
                <span>
                  {stats.links} <em>{stats.links === 1 ? 'link' : 'links'}</em>
                </span>
              </>
            )}
          </div>
          <div className="navbar-center" title={`Namespace: ${namespace}`}>
            <TopologyIcon className="navbar-ns-icon" aria-hidden="true" />
            <div className="navbar-center-text">
              <span className="navbar-label">Namespace</span>
              <span className="navbar-namespace">{namespace}</span>
            </div>
            <button
              className="navbar-refresh-btn"
              onClick={() => !disabled && onRefresh && onRefresh()}
              disabled={disabled}
              title="Refresh topology"
            >
              <RefreshIcon style={{ width: 14, height: 14 }} />
            </button>
          </div>
          <div
            className="navbar-stat navbar-stat-right"
            title={breakdown || 'CPU and memory used by this namespace'}
          >
            {nsMetrics && nsMetrics.pod_count > 0 && (
              <>
                <span>
                  {(nsMetrics.total_cpu_milli / 1000).toFixed(2)} <em>vCPU</em>
                </span>
                <span>
                  {nsMetrics.total_memory_mib} <em>MiB</em>
                </span>
              </>
            )}
          </div>
        </div>

        <div className="navbar-right">
          <button
            className="navbar-button navbar-button-files"
            onClick={() => {
              sessionStorage.setItem(`kubendt.restoreCache.${namespace}`, 'true');
              navigate(`/${namespace}/files`);
            }}
          >
            <FolderIcon className="app-icon" aria-hidden="true" />
            File Manager
          </button>
          <a
            className="navbar-brand"
            href="https://github.com/emigcm98/kubendt"
            target="_blank"
            rel="noopener noreferrer"
            title="View KubeNDT on GitHub"
          >
            <img src={kubendtLogo} alt="KubeNDT" className="navbar-brand-logo" />
          </a>
        </div>
      </div>

      <AlertModal
        isOpen={showDeleteConfirmModal}
        type="warning"
        danger
        title="Delete namespace"
        message={
          <>
            Delete the namespace <strong>'{namespace}'</strong>?
          </>
        }
        confirmText="Delete namespace"
        cancelText="Cancel"
        onConfirm={confirmDelete}
        onCancel={() => setShowDeleteConfirmModal(false)}
        extraContent={
          <DeleteExtrasCheckboxes
            positionsChecked={deletePositionsChecked}
            onPositionsChange={setDeletePositionsChecked}
            filesChecked={deleteFilesChecked}
            onFilesChange={setDeleteFilesChecked}
          />
        }
      />

      <AlertModal
        isOpen={showDeleteHistoryConfirmModal}
        type="warning"
        danger
        title="Delete history"
        message={
          <>
            Delete all driver operations in namespace <strong>'{namespace}'</strong>? Only the
            operation history is cleared, the topology stays.
          </>
        }
        confirmText="Delete history"
        cancelText="Cancel"
        onConfirm={() => {
          setShowDeleteHistoryConfirmModal(false);
          onDeleteHistory(namespace);
        }}
        onCancel={() => setShowDeleteHistoryConfirmModal(false)}
      />
    </>
  );
}

export default NamespaceNavbar;
