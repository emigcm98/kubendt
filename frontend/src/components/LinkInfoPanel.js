import React, { useEffect, useState } from 'react';
import './PodInfoPanel.css';
import './LinkInfoPanel.css';
import usePanelClose from './usePanelClose';
import { API_BASE_URL } from '../config';
import { ReactComponent as PcapIcon } from '../assets/images/icons/pcap.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as LinkIcon } from '../assets/images/icons/link.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import pcIcon from '../assets/images/nodes/host.svg';
import routerIcon from '../assets/images/nodes/router.svg';
import switchIcon from '../assets/images/nodes/switch.svg';
import externalIcon from '../assets/images/nodes/switch_gray.svg';

const ICONS = {
  host: pcIcon,
  router: routerIcon,
  switch: switchIcon,
  external: externalIcon,
};

const formatPodLabel = (nodeData) => {
  if (!nodeData) return '—';
  return nodeData.label || nodeData.name || '—';
};

const sameSubnet = (cidrA, cidrB) => {
  if (!cidrA || !cidrB) return null;
  const stripA = String(cidrA).split('/')[0];
  const stripB = String(cidrB).split('/')[0];
  if (!stripA || !stripB) return null;
  const partsA = stripA.split('.');
  const partsB = stripB.split('.');
  if (partsA.length !== 4 || partsB.length !== 4) return null;
  // Use /24 heuristic for display purposes only
  return partsA.slice(0, 3).join('.') === partsB.slice(0, 3).join('.');
};

const LinkInfoPanel = ({
  link,
  nodes = [],
  namespace,
  onClosePanel,
  onDeleteLink,
  onStartCapture,
  closeSignal,
  isBusy = false,
}) => {
  const [localInfo, setLocalInfo] = useState(null);
  const [peerInfo, setPeerInfo] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const { isClosing, requestClose, handleAnimationEnd } = usePanelClose(onClosePanel, closeSignal);

  const sourceNode = nodes.find((n) => n.id === link?.source)?.data;
  const targetNode = nodes.find((n) => n.id === link?.target)?.data;

  const localIntf = link?.data?.localIntf;
  const peerIntf = link?.data?.peerIntf;
  const sourceIsExternal = sourceNode?.type === 'external';
  const targetIsExternal = targetNode?.type === 'external';

  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') requestClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [requestClose]);

  useEffect(() => {
    let aborted = false;
    const fetchInterface = async (podName, ifaceName) => {
      if (!podName || !ifaceName || !namespace) return null;
      try {
        const res = await fetch(
          `${API_BASE_URL}/pods/ips/${namespace}/${podName}?intf=${encodeURIComponent(ifaceName)}`
        );
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json();
        const match = (data.interfaces || []).find((i) => i.interface === ifaceName);
        return match || null;
      } catch (err) {
        console.error(`❌ Error fetching ${podName}:${ifaceName}:`, err);
        throw err;
      }
    };

    const load = async () => {
      setLoading(true);
      setError(null);
      setLocalInfo(null);
      setPeerInfo(null);

      try {
        const [a, b] = await Promise.all([
          sourceIsExternal ? Promise.resolve(null) : fetchInterface(link.source, localIntf),
          targetIsExternal ? Promise.resolve(null) : fetchInterface(link.target, peerIntf),
        ]);
        if (aborted) return;
        setLocalInfo(a);
        setPeerInfo(b);
      } catch (err) {
        if (!aborted) setError('Error loading link details');
      } finally {
        if (!aborted) setLoading(false);
      }
    };

    if (link) load();
    return () => {
      aborted = true;
    };
  }, [link, namespace, localIntf, peerIntf, sourceIsExternal, targetIsExternal]);

  if (!link) return null;

  const linkType = (() => {
    const a = sourceNode?.type || '?';
    const b = targetNode?.type || '?';
    const ordered = [a, b].sort();
    return `${ordered[0]} ↔ ${ordered[1]}`;
  })();

  const subnetMatch = sameSubnet(localInfo?.ipv4, peerInfo?.ipv4);

  // "pod:iface ↔ pod:iface" shown as a body attribute (too long for the title).
  const endpointLabel = (nodeData, iface) => {
    const base = formatPodLabel(nodeData);
    return iface ? `${base}:${iface}` : base;
  };
  const linkPath = `${endpointLabel(sourceNode, localIntf)} ↔ ${endpointLabel(targetNode, peerIntf)}`;

  const renderEndpoint = (side, podName, nodeData, ifaceName, info, isExternal) => {
    const iconKey = nodeData?.type || 'host';
    const icon = ICONS[iconKey] || pcIcon;
    const stateKnown = info?.state === 'up' || info?.state === 'down';
    const isUp = info?.state === 'up';
    const canCapture = !isExternal && onStartCapture && podName && ifaceName;

    return (
      <div className={`link-endpoint link-endpoint-${side}`}>
        <div className="link-endpoint-header">
          <img src={icon} alt={iconKey} className="link-endpoint-icon" />
          <div className="link-endpoint-titles">
            <div className="link-endpoint-pod" title={podName}>
              {formatPodLabel(nodeData)}
            </div>
            <div className="link-endpoint-iface">
              <span className="link-endpoint-iface-name">{ifaceName || '—'}</span>
              {stateKnown && (
                <span className={`link-state-badge ${isUp ? 'badge-up' : 'badge-down'}`}>
                  {isUp ? 'UP' : 'DOWN'}
                </span>
              )}
            </div>
          </div>
        </div>

        {isExternal ? (
          <div className="link-endpoint-detail link-endpoint-external-hint">
            External uplink (host interface, no pod info)
          </div>
        ) : loading ? (
          <div className="link-endpoint-detail link-endpoint-loading">…</div>
        ) : (
          <>
            {info?.guestInterface && (
              <div className="link-endpoint-detail">
                <span className="link-detail-key">guest</span>
                <span className="link-detail-val link-detail-italic">{info.guestInterface}</span>
              </div>
            )}
            <div className="link-endpoint-detail">
              <span className="link-detail-key">IPv4</span>
              <span className="link-detail-val" title={info?.ipv4 || ''}>
                {info?.ipv4 || '—'}
              </span>
            </div>
            <div className="link-endpoint-detail">
              <span className="link-detail-key">MAC</span>
              <span className="link-detail-val link-detail-mono" title={info?.mac || ''}>
                {info?.mac || '—'}
              </span>
            </div>
          </>
        )}

        {canCapture && (
          <button
            className="link-capture-btn"
            onClick={() => onStartCapture(podName, ifaceName)}
            title={`Capture packets on ${podName}:${ifaceName}`}
          >
            <PcapIcon /> Capture
          </button>
        )}
      </div>
    );
  };

  return (
    <div className="panel-wrapper">
      <div
        className={`pod-info-panel link-info-panel${isClosing ? ' is-closing' : ''}`}
        onAnimationEnd={handleAnimationEnd}
      >
        {onDeleteLink && (
          <button
            className="delete-btn link-delete-btn"
            onClick={() => onDeleteLink(link)}
            title={isBusy ? 'Operation in progress…' : 'Delete link (remove from topology)'}
            disabled={isBusy}
          >
            <TrashIcon className="app-icon" />
          </button>
        )}
        <button className="close-btn" onClick={requestClose} title="Close panel">
          <CloseIcon className="app-icon" />
        </button>

        <h3 className="link-info-title">
          <LinkIcon className="link-info-title-icon" />
          {link?.data?.linkName ? link.data.linkName : 'Link'}
        </h3>

        <div className="pod-body link-info-body">
          <div className="pod-line">
            <span className="pod-label">Path:</span>
            <span className="pod-value" title={linkPath}>
              {linkPath}
            </span>
          </div>
          <div className="pod-line">
            <span className="pod-label">Type:</span>
            <span className="pod-value">{linkType}</span>
          </div>
          {link.data?.uid !== undefined && link.data?.uid !== null && (
            <div className="pod-line">
              <span className="pod-label">UID:</span>
              <span className="pod-value">{String(link.data.uid)}</span>
            </div>
          )}
          {subnetMatch !== null && (
            <div className="pod-line">
              <span className="pod-label">Subnet:</span>
              <span
                className={`pod-value link-subnet ${subnetMatch ? 'subnet-ok' : 'subnet-diff'}`}
              >
                {subnetMatch ? 'same /24 ✓' : 'different /24 ✗'}
              </span>
            </div>
          )}

          {error && <p className="links-error">{error}</p>}

          <div className="link-endpoints">
            {renderEndpoint('a', link.source, sourceNode, localIntf, localInfo, sourceIsExternal)}
            <div className="link-endpoints-connector" />
            {renderEndpoint('b', link.target, targetNode, peerIntf, peerInfo, targetIsExternal)}
          </div>
        </div>
      </div>
    </div>
  );
};

export default LinkInfoPanel;
