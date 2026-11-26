import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { API_BASE_URL } from '../config';
import { useNavigate } from 'react-router-dom';
import BrandMark from './BrandMark';
import './NamespaceNavbar.css';
import AlertModal from './AlertModal';
import DeleteExtrasCheckboxes from './DeleteExtrasCheckboxes';
import ShareNamespaceModal from './ShareNamespaceModal';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as FolderIcon } from '../assets/images/icons/folder.svg';
import { ReactComponent as ClusterIcon } from '../assets/images/icons/cluster.svg';
import { ReactComponent as TopologyIcon } from '../assets/images/icons/topology.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as ClockIcon } from '../assets/images/icons/clock.svg';
import { ReactComponent as ChevronDownIcon } from '../assets/images/icons/chevron-down.svg';
import { ReactComponent as UsersIcon } from '../assets/images/icons/users.svg';
import { ReactComponent as EyeIcon } from '../assets/images/icons/eye.svg';
import ThemeToggle from './ThemeToggle';

const plural = (type) => (type === 'switch' ? 'switches' : `${type}s`);

function NamespaceNavbar({
  namespace,
  onDelete,
  onDeleteHistory,
  level,
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

  // The namespace menu: whose it is, who is in, sharing and the deletions,
  // each by the account's level. No level means auth is off, so everything.
  // It closes on a click elsewhere or Esc.
  const isOwner = !level || level === 'owner';
  const canEdit = isOwner || level === 'editor';
  const [menuOpen, setMenuOpen] = useState(false);
  const [shareOpen, setShareOpen] = useState(false);
  const [access, setAccess] = useState(null);
  const menuRef = useRef(null);

  // Who owns and who is in, read when the menu opens and after a change.
  const loadAccess = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE_URL}/namespaces/access/${encodeURIComponent(namespace)}`);
      const data = await res.json().catch(() => null);
      setAccess(res.ok && data ? data : false);
    } catch {
      setAccess(false);
    }
  }, [namespace]);
  useEffect(() => {
    if (menuOpen) loadAccess();
  }, [menuOpen, loadAccess]);
  useEffect(() => {
    if (!menuOpen) return undefined;
    const onDown = (e) => {
      if (!menuRef.current?.contains(e.target)) setMenuOpen(false);
    };
    const onKey = (e) => {
      if (e.key === 'Escape') setMenuOpen(false);
    };
    document.addEventListener('pointerdown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [menuOpen]);

  const handleDelete = () => {
    setMenuOpen(false);
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
          <BrandMark />
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
          <ThemeToggle className="navbar-button" />
          <button
            className="navbar-button"
            onClick={() => {
              sessionStorage.setItem(`kubendt.restoreCache.${namespace}`, 'true');
              navigate(`/${namespace}/files`);
            }}
            title="The files of this namespace, to mount into its pods"
          >
            <FolderIcon className="app-icon" aria-hidden="true" />
            <span className="navbar-button-label">File Manager</span>
          </button>
          {isOwner && (
            <button
              className="navbar-button"
              onClick={() => {
                sessionStorage.setItem(`kubendt.restoreCache.${namespace}`, 'true');
                navigate(`/${namespace}/state`);
              }}
              title="Every Kubernetes object of this namespace and what it is for KubeNDT"
            >
              <ClusterIcon className="app-icon" aria-hidden="true" />
              <span className="navbar-button-label">State</span>
            </button>
          )}
          <div className="navbar-menu-anchor" ref={menuRef}>
            <button
              className={`navbar-button${menuOpen ? ' navbar-button-open' : ''}`}
              onClick={() => setMenuOpen((v) => !v)}
              aria-haspopup="menu"
              aria-expanded={menuOpen}
              title="Owner, guests and actions"
            >
              <TopologyIcon className="app-icon" aria-hidden="true" />
              <span className="navbar-button-label">Namespace</span>
              <ChevronDownIcon className="app-icon navbar-caret" aria-hidden="true" />
            </button>
            {menuOpen && (
              <div className="navbar-menu" role="menu">
                <div className="navbar-menu-info">
                  <div>
                    <span className="navbar-menu-key">Owner</span>
                    <strong>
                      {access === null
                        ? '…'
                        : access === false
                          ? '?'
                          : access.owner || 'the admins'}
                    </strong>
                  </div>
                  <div>
                    <span className="navbar-menu-key">Guests</span>
                    <strong>
                      {access
                        ? Object.keys(access.guests || {}).length
                        : access === null
                          ? '…'
                          : '?'}
                    </strong>
                  </div>
                  {level && (
                    <div>
                      <span className="navbar-menu-key">You</span>
                      <strong>{level}</strong>
                    </div>
                  )}
                </div>
                <div className="navbar-menu-sep" />
                <button
                  type="button"
                  className="navbar-menu-item"
                  role="menuitem"
                  onClick={() => {
                    setMenuOpen(false);
                    setShareOpen(true);
                  }}
                >
                  {isOwner ? (
                    <UsersIcon className="app-icon" aria-hidden="true" />
                  ) : (
                    <EyeIcon className="app-icon" aria-hidden="true" />
                  )}
                  {isOwner ? 'Share or hand over' : 'Who is in'}
                </button>
                {canEdit && (
                  <button
                    type="button"
                    className="navbar-menu-item"
                    role="menuitem"
                    disabled={disabled || !onDeleteHistory}
                    onClick={() => {
                      setMenuOpen(false);
                      setShowDeleteHistoryConfirmModal(true);
                    }}
                  >
                    <ClockIcon className="app-icon" aria-hidden="true" />
                    Delete history
                    <span className="navbar-menu-sub">the topology stays</span>
                  </button>
                )}
                {isOwner && (
                  <button
                    type="button"
                    className="navbar-menu-item navbar-menu-item-danger"
                    role="menuitem"
                    disabled={disabled}
                    onClick={handleDelete}
                  >
                    <TrashIcon className="app-icon" aria-hidden="true" />
                    Delete namespace
                  </button>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      {shareOpen && (
        <ShareNamespaceModal
          namespace={namespace}
          readOnly={!isOwner}
          onClose={() => setShareOpen(false)}
          onChanged={loadAccess}
        />
      )}

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
