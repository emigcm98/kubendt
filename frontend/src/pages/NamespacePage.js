import React, { useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import Modal from 'react-modal';
import NetworkGraph from '../components/NetworkGraph';
import NamespaceNavbar from '../components/NamespaceNavbar';
import ErrorPage from './ErrorPage';
import { ReactComponent as SuccessIcon } from '../assets/images/icons/success.svg';
import { ReactComponent as ErrorIcon } from '../assets/images/icons/error.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { API_BASE_URL } from '../config';
import './NamespacePage.css';

Modal.setAppElement('#root');

function NamespacePage() {
  const { namespace } = useParams();
  const navigate = useNavigate();
  const [fetchError, setFetchError] = useState(null);
  const [isImporting, setIsImporting] = useState(false);
  const [showDeleteModal, setShowDeleteModal] = useState(false);
  const [deleteResult, setDeleteResult] = useState({ success: false, message: '', action: '' });
  const [refreshTrigger, setRefreshTrigger] = useState(0);

  const handleDeleteNamespace = async (ns, clearPositions = false, deleteFiles = false) => {
    if (clearPositions) {
      // Clear local session cache immediately; DB cleanup is handled by the backend.
      sessionStorage.removeItem(`kubendt.networkGraph.${ns}`);
      sessionStorage.removeItem(`kubendt.restoreCache.${ns}`);
    }
    try {
      const params = new URLSearchParams();
      if (clearPositions) params.set('deletePositions', 'true');
      if (deleteFiles) params.set('deleteFiles', 'true');
      const queryString = params.toString() ? `?${params.toString()}` : '';

      const res = await fetch(`${API_BASE_URL}/namespaces/${ns}${queryString}`, {
        method: 'DELETE',
      });

      const payload = await res.json().catch(() => null);
      const errorMessage = payload?.error || 'Could not delete namespace';
      const successMessage = payload?.message || `Namespace '${ns}' deleted successfully`;

      if (res.ok) {
        setDeleteResult({ success: true, message: successMessage, action: 'namespace_delete' });
        setShowDeleteModal(true);
      } else {
        setDeleteResult({ success: false, message: errorMessage, action: 'namespace_delete' });
        setShowDeleteModal(true);
      }
    } catch (err) {
      console.error('Error deleting namespace:', err);
      setDeleteResult({
        success: false,
        message: 'Error deleting namespace',
        action: 'namespace_delete',
      });
      setShowDeleteModal(true);
    }
  };

  const handleDeleteNamespaceHistory = async (ns) => {
    try {
      const res = await fetch(`${API_BASE_URL}/drivers/history/namespace/${ns}`, {
        method: 'DELETE',
      });

      const payload = await res.json().catch(() => null);
      const errorMessage = payload?.error || 'Could not delete namespace history';

      if (!res.ok) {
        throw new Error(errorMessage);
      }

      setDeleteResult({
        success: true,
        message: `Namespace history '${ns}' deleted successfully`,
        action: 'namespace_history_delete',
      });
      setShowDeleteModal(true);
      setRefreshTrigger((prev) => prev + 1);
    } catch (err) {
      console.error('Error deleting namespace history:', err);
      setDeleteResult({
        success: false,
        message: err.message || 'Error deleting namespace history',
        action: 'namespace_history_delete',
      });
      setShowDeleteModal(true);
    }
  };

  const handleCloseDeleteModal = () => {
    setShowDeleteModal(false);
    if (deleteResult.success && deleteResult.action === 'namespace_delete') {
      navigate('/');
    }
  };

  if (fetchError) {
    return <ErrorPage statusCode={fetchError.status || 500} rawMessage={fetchError.message} />;
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
      <NamespaceNavbar
        namespace={namespace}
        onDelete={handleDeleteNamespace}
        onDeleteHistory={handleDeleteNamespaceHistory}
        onRefresh={() => {
          setFetchError(null);
          setRefreshTrigger((prev) => prev + 1);
        }}
        disabled={isImporting}
      />
      <NetworkGraph
        namespace={namespace}
        onError={setFetchError}
        onImportingChange={setIsImporting}
        refreshTrigger={refreshTrigger}
      />

      {/* Deletion result modal */}
      <Modal
        isOpen={showDeleteModal}
        onRequestClose={handleCloseDeleteModal}
        className="delete-result-modal"
        overlayClassName="delete-result-overlay"
      >
        <div className="modal-header">
          <h2>
            {deleteResult.success ? (
              <>
                <SuccessIcon className="app-icon" /> Operation completed
              </>
            ) : (
              <>
                <ErrorIcon className="app-icon" /> Operation failed
              </>
            )}
          </h2>
          <button className="modal-close-btn" onClick={handleCloseDeleteModal}>
            <CloseIcon className="app-icon" />
          </button>
        </div>

        <div className="modal-body">
          <p>{deleteResult.message}</p>
        </div>

        <div className="modal-footer">
          <button
            className={`modal-btn modal-btn-confirm ${deleteResult.success ? 'success' : 'error'}`}
            onClick={handleCloseDeleteModal}
          >
            OK
          </button>
        </div>
      </Modal>
    </div>
  );
}

export default NamespacePage;
