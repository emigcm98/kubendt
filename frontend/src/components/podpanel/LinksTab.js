import React from 'react';
import { interfaceAddresses } from '../addresses';
import { CountersRows } from '../counters';
import { ReactComponent as SlidersIcon } from '../../assets/images/icons/sliders.svg';
import { ReactComponent as PcapIcon } from '../../assets/images/icons/pcap.svg';

// Interfaces of the pod and, for the picked one, its counters, shaping
// state and the two tools of a link end.
const LinksTab = ({
  pod,
  interfaces,
  loadingLinks,
  errorLinks,
  selectedInterface,
  onSelectInterface,
  onInterfaceContextMenu,
  qdiscByIface,
  loadingQdisc,
  loadingInterfaces,
  counters,
  onStartCapture,
  onOpenTrafficControl,
}) => (
  <div className="links-tab">
    {loadingLinks && <p className="links-loading">Loading interfaces...</p>}
    {errorLinks && <p className="links-error">{errorLinks}</p>}

    {!loadingLinks && !errorLinks && (
      <div className="links-content">
        <div className="interfaces-list">
          {interfaces.length === 0 ? (
            <p className="no-interfaces">No interfaces found</p>
          ) : (
            interfaces.map((intf) => (
              <div
                key={intf.interface}
                className={`interface-item ${selectedInterface === intf.interface ? 'selected' : ''}`}
                onClick={() => onSelectInterface(intf.interface)}
                onContextMenu={(e) => {
                  e.preventDefault();
                  const status =
                    intf.state === 'up' ? true : intf.state === 'down' ? false : undefined;
                  onInterfaceContextMenu({
                    x: e.clientX,
                    y: e.clientY,
                    iface: intf.interface,
                    status,
                  });
                }}
              >
                <div className="interface-name">
                  {intf.interface}
                  {intf.guestInterface && (
                    <span className="interface-guest-name"> → {intf.guestInterface}</span>
                  )}
                  {qdiscByIface[intf.interface]?.shaped && (
                    <span
                      className="interface-shaped"
                      title={`traffic control: ${qdiscByIface[intf.interface].qdisc}`}
                    >
                      tc
                    </span>
                  )}
                  {loadingInterfaces?.has(`${pod.name}:${intf.interface}`) ? (
                    <span className="interface-state-spinner" title="applying…" />
                  ) : (
                    intf.state && (
                      <span
                        className={`interface-state-dot ${intf.state === 'up' ? 'state-up' : 'state-down'}`}
                        title={intf.state}
                      />
                    )
                  )}
                </div>
                {(interfaceAddresses(intf).length ? interfaceAddresses(intf) : ['—']).map(
                  (addr) => (
                    <div className="interface-detail" key={addr}>
                      IP: {addr}
                    </div>
                  )
                )}
                <div className="interface-detail">MAC: {intf.mac}</div>
                {intf.mtu && <div className="interface-detail">MTU: {intf.mtu}</div>}
              </div>
            ))
          )}
        </div>

        <div className="interface-details">
          {!selectedInterface ? (
            <p className="no-selection">Select an interface to view more details</p>
          ) : (
            <div className="tc-links-panel">
              <div className="tc-links-head">
                <span className="tc-links-title">Traffic</span>
                <span className="counters-note">
                  {counters.last === null ? 'reading…' : 'every 5 s while open'}
                </span>
              </div>
              <CountersRows samples={counters} iface={selectedInterface} />
              <div className="tc-links-head">
                <span className="tc-links-title">Traffic control</span>
                <span
                  className={`tc-links-status ${qdiscByIface[selectedInterface]?.shaped ? 'on' : 'off'}`}
                >
                  {loadingQdisc
                    ? 'checking…'
                    : qdiscByIface[selectedInterface]?.shaped
                      ? `${qdiscByIface[selectedInterface].qdisc} · active`
                      : 'No shaping'}
                </span>
              </div>
              {/* Same two actions as an end of the link panel, same look. */}
              <div className="link-endpoint-actions">
                {onStartCapture && (
                  <button
                    className="link-capture-btn"
                    onClick={() => onStartCapture(pod.name, selectedInterface)}
                    title={`Capture packets on ${pod.name}:${selectedInterface}`}
                  >
                    <PcapIcon /> Capture
                  </button>
                )}
                {onOpenTrafficControl && (
                  <button
                    className="link-tc-btn"
                    onClick={() => onOpenTrafficControl(pod.name, selectedInterface)}
                    title={`Traffic control on ${pod.name}:${selectedInterface}`}
                  >
                    <SlidersIcon /> Traffic control
                  </button>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
    )}
  </div>
);

export default LinksTab;
