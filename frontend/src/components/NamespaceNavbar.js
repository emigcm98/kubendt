import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import Modal from 'react-modal';
import './NamespaceNavbar.css';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as HomeIcon } from '../assets/images/icons/home.svg';
import { ReactComponent as FolderIcon } from '../assets/images/icons/folder.svg';
import { ReactComponent as TopologyIcon } from '../assets/images/icons/topology.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as ClockIcon } from '../assets/images/icons/clock.svg';
import kubendtLogo from '../assets/images/kubendt-logo.svg';

Modal.setAppElement('#root');

function NamespaceNavbar({ namespace, onDelete, onDeleteHistory, onRefresh, disabled = false }) {
  const navigate = useNavigate();
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

      {/* Deletion confirmation modal */}
      <Modal
        isOpen={showDeleteConfirmModal}
        onRequestClose={() => setShowDeleteConfirmModal(false)}
        className="delete-confirm-modal"
        overlayClassName="delete-confirm-overlay"
      >
        <div className="modal-header">
          <h2>
            <WarningIcon className="app-icon" /> Confirm Namespace Deletion
          </h2>
          <button className="modal-close-btn" onClick={() => setShowDeleteConfirmModal(false)}>
            <CloseIcon className="app-icon" />
          </button>
        </div>

        <div className="modal-body">
          <p>
            Are you sure you want to delete the namespace <strong>'{namespace}'</strong>?
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
            onClick={() => setShowDeleteConfirmModal(false)}
          >
            Cancel
          </button>
          <button className="modal-btn modal-btn-confirm modal-btn-danger" onClick={confirmDelete}>
            Delete namespace
          </button>
        </div>
      </Modal>

      <Modal
        isOpen={showDeleteHistoryConfirmModal}
        onRequestClose={() => setShowDeleteHistoryConfirmModal(false)}
        className="delete-confirm-modal"
        overlayClassName="delete-confirm-overlay"
      >
        <div className="modal-header">
          <h2>
            <WarningIcon className="app-icon" /> Confirm History Deletion
          </h2>
          <button
            className="modal-close-btn"
            onClick={() => setShowDeleteHistoryConfirmModal(false)}
          >
            <CloseIcon className="app-icon" />
          </button>
        </div>

        <div className="modal-body">
          <p>
            Are you sure you want to delete ALL driver operations in namespace{' '}
            <strong>'{namespace}'</strong>?
          </p>
          <p style={{ color: '#9b7410', fontWeight: 600, marginTop: '1rem' }}>
            This will only clear operation history.
          </p>
        </div>

        <div className="modal-footer">
          <button
            className="modal-btn modal-btn-cancel"
            onClick={() => setShowDeleteHistoryConfirmModal(false)}
          >
            Cancel
          </button>
          <button
            className="modal-btn modal-btn-confirm"
            onClick={() => {
              setShowDeleteHistoryConfirmModal(false);
              onDeleteHistory(namespace);
            }}
          >
            Delete namespace history
          </button>
        </div>
      </Modal>
    </>
  );
}

export default NamespaceNavbar;
