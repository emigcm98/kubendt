import React from 'react';
import { ReactComponent as WarningIcon } from '../../assets/images/icons/warning.svg';
import { ReactComponent as LockIcon } from '../../assets/images/icons/lock.svg';

const VarsTab = ({ pod, onOpenFile }) => (
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
                className={`pod-vars-key pod-vars-file${mount.missing ? ' pod-vars-file-missing' : ''}`}
                title={
                  mount.missing
                    ? `${mount.file} is no longer present in the file manager`
                    : mount.file
                }
                onClick={mount.missing ? undefined : () => onOpenFile(mount.file)}
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
);

export default VarsTab;
