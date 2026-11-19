import React from 'react';
import { groupHistoryByCapability } from './history';
import { ReactComponent as RefreshIcon } from '../../assets/images/icons/refresh.svg';
import { ReactComponent as TrashIcon } from '../../assets/images/icons/trash.svg';
import { ReactComponent as EyeIcon } from '../../assets/images/icons/eye.svg';
import { ReactComponent as BookIcon } from '../../assets/images/icons/book.svg';

// What the node has been configured with: its history grouped by the
// capability of each action, with a link to the driver's page at the bottom.
const ConfigTab = ({
  driverDisplay,
  driverName,
  driverCaps,
  driverHistory,
  loadingDriverHistory,
  errorDriverHistory,
  deletingHistoryId,
  clearingPodHistory,
  onReloadHistory,
  onDeleteEntry,
  onDeleteEntries,
  onDeletePodHistory,
  onShowEntry,
  formatHistoryDate,
}) => {
  const groups = groupHistoryByCapability(driverHistory, driverCaps);

  return (
    <div className="config-tab">
      <div className="driver-header driver-header-actions">
        <button
          className="driver-nav-btn"
          title="Reload the history"
          onClick={onReloadHistory}
          disabled={loadingDriverHistory}
        >
          <RefreshIcon className="app-icon" />
        </button>
        <span className="driver-title">{driverDisplay}</span>
        <button
          className="driver-history-clear-pod-btn"
          onClick={onDeletePodHistory}
          disabled={clearingPodHistory || driverHistory.length === 0}
          title="Delete every operation of this pod"
        >
          {clearingPodHistory ? '…' : <TrashIcon className="app-icon" />}
        </button>
      </div>

      <div className="config-body">
        {loadingDriverHistory && <p className="links-loading">Loading history...</p>}
        {errorDriverHistory && <p className="links-error">{errorDriverHistory}</p>}
        {!loadingDriverHistory && !errorDriverHistory && driverHistory.length === 0 && (
          <p className="no-interfaces">No operations applied</p>
        )}
        {!loadingDriverHistory &&
          !errorDriverHistory &&
          groups.map((group) => (
            <div className="config-group" key={group.id}>
              <div className="config-group-head">
                <span className="config-group-label" title={group.label}>
                  {group.id === 'other' ? 'Other' : group.id}
                </span>
                <span className="state-count">{group.entries.length}</span>
                <button
                  className="config-group-clear"
                  title={`Delete the ${group.entries.length} operations of ${group.label}`}
                  onClick={() =>
                    onDeleteEntries(
                      group.entries.map((e) => e.id),
                      group.label
                    )
                  }
                  disabled={clearingPodHistory}
                >
                  clear
                </button>
              </div>
              {group.entries.map((entry) => (
                <div className="driver-history-item" key={entry.id}>
                  <div className="driver-history-row">
                    <div className="driver-history-action">
                      {entry.action_type || entry?.action?.type || 'unknown'}
                    </div>
                    <div className="driver-history-actions">
                      <button
                        className="driver-history-info-btn"
                        title="Show action details"
                        onClick={() => onShowEntry(entry)}
                      >
                        <EyeIcon className="app-icon" />
                      </button>
                      <button
                        className="driver-history-delete-btn"
                        title={`Delete operation #${entry.id}`}
                        onClick={() => onDeleteEntry(entry.id)}
                        disabled={deletingHistoryId === entry.id || clearingPodHistory}
                      >
                        {deletingHistoryId === entry.id ? '…' : <TrashIcon className="app-icon" />}
                      </button>
                    </div>
                  </div>
                  <div className="driver-history-date">{formatHistoryDate(entry.executed_at)}</div>
                </div>
              ))}
            </div>
          ))}

        <button
          type="button"
          className="config-reference-link"
          onClick={() =>
            window.open(`/drivers/${encodeURIComponent(driverName || '')}`, '_blank', 'noopener')
          }
          title="The driver's page: its capabilities, actions, reads, images and shells"
        >
          <BookIcon className="app-icon" aria-hidden="true" />
          Driver reference
        </button>
      </div>
    </div>
  );
};

export default ConfigTab;
