import React, { useState } from 'react';
import { createPortal } from 'react-dom';
import { groupHistoryByCapability } from './history';
import { ReactComponent as RefreshIcon } from '../../assets/images/icons/refresh.svg';
import { ReactComponent as TrashIcon } from '../../assets/images/icons/trash.svg';
import { ReactComponent as EyeIcon } from '../../assets/images/icons/eye.svg';
import { ReactComponent as ChevronDown } from '../../assets/images/icons/chevron-down.svg';
import { ReactComponent as ChevronRight } from '../../assets/images/icons/chevron-right.svg';

// What the node has been configured with: its history grouped by the
// capability of each action, with the driver's reference (executor, naming
// rules, capabilities and their methods) folded at the bottom.
const ConfigTab = ({
  driverDisplay,
  driverCaps,
  driverExecutor,
  driverInterfaceConstraints,
  driverKnownImages,
  loadingDriverCaps,
  errorDriverCaps,
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
  const [referenceOpen, setReferenceOpen] = useState(false);
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

        <div className={`config-reference${referenceOpen ? ' open' : ''}`}>
          <div className="config-reference-toggle" onClick={() => setReferenceOpen((v) => !v)}>
            {referenceOpen ? (
              <ChevronDown className="app-icon state-chevron" />
            ) : (
              <ChevronRight className="app-icon state-chevron" />
            )}
            <span>Driver reference</span>
          </div>
          {referenceOpen && (
            <DriverReference
              key={driverDisplay}
              driverCaps={driverCaps}
              driverExecutor={driverExecutor}
              driverInterfaceConstraints={driverInterfaceConstraints}
              driverKnownImages={driverKnownImages}
              loading={loadingDriverCaps}
              error={errorDriverCaps}
            />
          )}
        </div>
      </div>
    </div>
  );
};

// The capabilities of the driver with their methods, as GET /drivers
// publishes them; a method shows its parameters on hover.
const DriverReference = ({
  driverCaps,
  driverExecutor,
  driverInterfaceConstraints,
  driverKnownImages,
  loading,
  error,
}) => {
  const [selectedId, setSelectedId] = useState(null);
  const [popover, setPopover] = useState(null);
  const selected = driverCaps.find((c) => c.id === (selectedId || driverCaps[0]?.id));
  const methods = selected?.methods || [];

  if (loading) return <p className="links-loading">Loading capabilities...</p>;
  if (error) return <p className="links-error">{error}</p>;

  return (
    <div className="config-reference-body">
      <div className="config-reference-facts">
        <div>
          <span className="pod-label">Executor:</span>{' '}
          <code className="cap-code">{driverExecutor || 'kubectl'}</code>
        </div>
        <div>
          <span className="pod-label">Interface naming:</span>{' '}
          {driverInterfaceConstraints ? (
            <>
              <code className="cap-code">
                {driverInterfaceConstraints.patternHuman || driverInterfaceConstraints.pattern}
              </code>
              {Array.isArray(driverInterfaceConstraints.reserved) &&
                driverInterfaceConstraints.reserved.length > 0 && (
                  <>
                    {' '}
                    reserved{' '}
                    <code className="cap-code">
                      {driverInterfaceConstraints.reserved.join(', ')}
                    </code>
                  </>
                )}
            </>
          ) : (
            <span>Linux default</span>
          )}
        </div>
        {driverKnownImages?.length > 0 && (
          <div>
            <span className="pod-label">Images tried:</span>{' '}
            {driverKnownImages.map((img) => (
              <code className="cap-code config-reference-image" key={img}>
                {img}
              </code>
            ))}
          </div>
        )}
      </div>
      <div className="config-reference-caps">
        {driverCaps
          .filter((cap) => !cap.universal)
          .map((cap) => (
            <button
              key={cap.id}
              className={`config-cap-chip${selected?.id === cap.id ? ' active' : ''}`}
              onClick={() => setSelectedId(cap.id)}
              title={cap.description}
            >
              {cap.label || cap.id}
            </button>
          ))}
      </div>
      {driverCaps.some((cap) => cap.universal) && (
        <div className="config-reference-caps config-reference-universal">
          <span
            className="config-reference-universal-label"
            title="Run on any node, outside the driver"
          >
            Universal
          </span>
          {driverCaps
            .filter((cap) => cap.universal)
            .map((cap) => (
              <button
                key={cap.id}
                className={`config-cap-chip universal${selected?.id === cap.id ? ' active' : ''}`}
                onClick={() => setSelectedId(cap.id)}
                title={cap.description}
              >
                {cap.label || cap.id}
              </button>
            ))}
        </div>
      )}
      {selected && (
        <div className="config-reference-detail">
          {selected.description && <p className="cap-desc">{selected.description}</p>}
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
                    setPopover({
                      methodName: method.name,
                      top: rect.top + rect.height / 2,
                      left: rect.right + 12,
                      params: method.params || [],
                    });
                  }}
                  onMouseLeave={() => setPopover(null)}
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
        </div>
      )}
      {popover &&
        createPortal(
          <div
            className="method-popover"
            style={{
              position: 'fixed',
              top: popover.top,
              left: popover.left,
              transform: 'translateY(-50%)',
              zIndex: 5000,
            }}
          >
            <div className="popover-content">
              <div className="popover-header">
                <strong>{popover.methodName}</strong>
              </div>
              {popover.params.length > 0 ? (
                <div className="popover-params">
                  <div className="params-label">Parameters:</div>
                  {popover.params.map((param, pidx) => (
                    <div key={pidx} className="param-item">
                      <span className="param-name">{param.name}</span>
                      <span className="param-type">
                        {param.type}
                        {param.required === false ? ', optional' : ''}
                      </span>
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
    </div>
  );
};

export default ConfigTab;
