import React from 'react';
import './PodInfoPanel.css';
import './ExternalNodeInfoPanel.css';
import usePanelClose from './usePanelClose';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';

const ExternalNodeInfoPanel = ({
  node,
  onClosePanel,
  onDeleteExternal,
  closeSignal,
  isBusy = false,
}) => {
  const { isClosing, requestClose, handleAnimationEnd } = usePanelClose(onClosePanel, closeSignal);
  if (!node) return null;

  const hostInterfaces = Array.isArray(node.hostInterfaces) ? node.hostInterfaces : [];
  const connectedNodes = Array.isArray(node.connectedNodes) ? node.connectedNodes : [];
  const connectedWorkers = Array.isArray(node.connectedWorkers) ? node.connectedWorkers : [];
  const connectedLinks = Array.isArray(node.connectedLinks) ? node.connectedLinks : [];

  const interfaceText = hostInterfaces.length > 0 ? hostInterfaces.join(', ') : 'host uplink';
  const workerText = connectedWorkers.length > 0 ? connectedWorkers.join(', ') : 'unknown worker';
  const connectedCount = connectedNodes.length;

  return (
    <div className="panel-wrapper">
      <div
        className={`pod-info-panel external-info-panel${isClosing ? ' is-closing' : ''}`}
        onAnimationEnd={handleAnimationEnd}
      >
        {onDeleteExternal && (
          <button
            className="delete-btn"
            onClick={() => onDeleteExternal(node)}
            title={
              isBusy ? 'Operation in progress…' : 'Delete external network (remove all links to it)'
            }
            disabled={isBusy}
          >
            <TrashIcon className="app-icon" />
          </button>
        )}
        <button className="close-btn" onClick={requestClose} title="Close panel">
          <CloseIcon className="app-icon" />
        </button>

        <h3>{node.baseName || node.name}</h3>

        <div className="pod-body external-info-body">
          <p className="external-description">
            External L2 uplink to the host network. Devices in this segment reach outside through
            it.
          </p>

          <hr />

          <div className="pod-line">
            <span className="pod-label">Type:</span>
            <span className="pod-value">External uplink</span>
          </div>
          <div className="pod-line">
            <span className="pod-label">Host intf:</span>
            <span className="pod-value">{interfaceText}</span>
          </div>
          <div className="pod-line">
            <span className="pod-label">Worker node:</span>
            <span className="pod-value">{workerText}</span>
          </div>
          <div className="pod-line">
            <span className="pod-label">Connected:</span>
            <span className="pod-value">
              {connectedCount} device{connectedCount === 1 ? '' : 's'}
            </span>
          </div>

          {connectedNodes.length > 0 && (
            <div className="external-connected-list">
              {connectedNodes.map((name) => (
                <span key={name} className="external-connected-chip">
                  {name}
                </span>
              ))}
            </div>
          )}

          {connectedLinks.length > 0 && (
            <>
              <hr />
              <div className="external-notes-title">Mapped links</div>
              <div className="external-links-list">
                {connectedLinks.map((link) => (
                  <div key={link} className="external-link-row">
                    {link}
                  </div>
                ))}
              </div>
            </>
          )}

          <hr />
          <p className="external-hint">
            Informational node. Shell and restart are not available for external endpoints.
          </p>
        </div>
      </div>
    </div>
  );
};

export default ExternalNodeInfoPanel;
