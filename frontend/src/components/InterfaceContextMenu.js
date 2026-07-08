import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import './InterfaceContextMenu.css';

const InterfaceContextMenu = ({
  x,
  y,
  pod,
  iface,
  status,
  namespace,
  apiBase,
  onClose,
  onToggle,
}) => {
  const menuRef = useRef(null);
  const [ifaceInfo, setIfaceInfo] = useState(null); // { ipv4, mac, guestInterface }
  const [loadingInfo, setLoadingInfo] = useState(true);

  useEffect(() => {
    const handleClick = (e) => {
      if (menuRef.current && !menuRef.current.contains(e.target)) {
        onClose();
      }
    };
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [onClose]);

  useEffect(() => {
    if (!pod || !iface || !namespace || !apiBase) {
      setLoadingInfo(false);
      return;
    }
    setLoadingInfo(true);
    fetch(`${apiBase}/pods/ips/${namespace}/${pod}?intf=${encodeURIComponent(iface)}`)
      .then((r) => (r.ok ? r.json() : Promise.reject()))
      .then((data) => {
        const match = data.interfaces?.find((i) => i.interface === iface);
        setIfaceInfo(match || null);
      })
      .catch(() => setIfaceInfo(null))
      .finally(() => setLoadingInfo(false));
  }, [pod, iface, namespace, apiBase]);

  const [pos, setPos] = useState({ top: y, left: x });

  useLayoutEffect(() => {
    if (!menuRef.current) return;
    const rect = menuRef.current.getBoundingClientRect();
    const MARGIN = 8;
    let top = y;
    let left = x;
    if (left + rect.width > window.innerWidth - MARGIN)
      left = window.innerWidth - rect.width - MARGIN;
    if (top + rect.height > window.innerHeight - MARGIN)
      top = window.innerHeight - rect.height - MARGIN;
    if (left < MARGIN) left = MARGIN;
    if (top < MARGIN) top = MARGIN;
    setPos({ top, left });
  }, [x, y]);

  const stateKnown = status === true || status === false;
  const isUp = status === true;

  const handleToggle = () => {
    if (!stateKnown) return;
    onToggle();
    onClose();
  };

  return (
    <div
      ref={menuRef}
      className="intf-context-menu"
      style={{ position: 'fixed', top: `${pos.top}px`, left: `${pos.left}px` }}
    >
      {/* Header */}
      <div className="intf-context-header">
        <span className="intf-context-pod">{pod}</span>
        <span className="intf-context-title">
          <span
            className={`intf-context-dot ${stateKnown ? (isUp ? 'dot-up' : 'dot-down') : 'dot-unknown'}`}
          />
          {iface}
        </span>
        {stateKnown && (
          <span className={`intf-context-badge ${isUp ? 'badge-up' : 'badge-down'}`}>
            {isUp ? 'UP' : 'DOWN'}
          </span>
        )}
      </div>

      <div className="intf-context-divider" />

      {/* IP / MAC info */}
      <div className="intf-context-info">
        {loadingInfo ? (
          <span className="intf-context-info-loading">
            <span className="intf-info-spinner" /> fetching info…
          </span>
        ) : ifaceInfo ? (
          <>
            {ifaceInfo.guestInterface && (
              <div className="intf-info-row">
                <span className="intf-info-key">guest</span>
                <span className="intf-info-val intf-info-italic">{ifaceInfo.guestInterface}</span>
              </div>
            )}
            <div className="intf-info-row">
              <span className="intf-info-key">IPv4</span>
              <span className="intf-info-val">{ifaceInfo.ipv4 || '—'}</span>
            </div>
            <div className="intf-info-row">
              <span className="intf-info-key">MAC</span>
              <span className="intf-info-val intf-info-mac">{ifaceInfo.mac || '—'}</span>
            </div>
          </>
        ) : (
          <span className="intf-context-info-loading" style={{ opacity: 0.5 }}>
            no data
          </span>
        )}
      </div>

      <div className="intf-context-divider" />

      {/* Action */}
      {stateKnown ? (
        <div
          className={`intf-context-item ${isUp ? 'item-danger' : 'item-success'}`}
          onClick={handleToggle}
        >
          <span className="intf-context-item-icon">{isUp ? '⏻' : '⏼'}</span>
          <span className="intf-context-item-label">
            {isUp ? 'Disable interface' : 'Enable interface'}
          </span>
        </div>
      ) : (
        <div className="intf-context-item item-disabled">
          <span className="intf-context-item-icon">⚠</span>
          <span className="intf-context-item-label">State unavailable</span>
        </div>
      )}
    </div>
  );
};

export default InterfaceContextMenu;
