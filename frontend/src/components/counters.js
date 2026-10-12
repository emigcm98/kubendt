import React, { useEffect, useState } from 'react';
import { API_BASE_URL } from '../config';
import './counters.css';

// Interface counters read on demand from GET /pods/counters: a read when the
// view opens and one every 5 s while it stays open, nothing otherwise. Two
// reads give the rates the panels show.
const COUNTERS_PERIOD_MS = 5000;

// useCounters keeps the last two reads of a pod while active. last is null
// before the first read and false once a read failed, which stops polling.
export function useCounters(namespace, podName, active) {
  const [samples, setSamples] = useState({ prev: null, last: null });

  useEffect(() => {
    if (!active || !namespace || !podName) {
      setSamples({ prev: null, last: null });
      return undefined;
    }
    let stopped = false;
    let timer = null;
    const read = async () => {
      try {
        const res = await fetch(`${API_BASE_URL}/pods/counters/${namespace}/${podName}`);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json();
        if (stopped) return;
        setSamples((s) => ({ prev: s.last || null, last: data }));
      } catch (err) {
        console.error(`❌ Error reading counters of ${podName}:`, err);
        if (!stopped) setSamples({ prev: null, last: false });
        return;
      }
      if (!stopped) timer = setTimeout(read, COUNTERS_PERIOD_MS);
    };
    setSamples({ prev: null, last: null });
    read();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [namespace, podName, active]);

  return samples;
}

// countersRates turns two reads into per-second rates for one interface.
export const countersRates = (prev, last, iface) => {
  const a = prev?.interfaces?.[iface];
  const b = last?.interfaces?.[iface];
  if (!a || !b) return null;
  const dt = (last.at - prev.at) / 1000;
  if (!(dt > 0)) return null;
  const per = (x, y) => Math.max(0, (y - x) / dt);
  return {
    rxBits: per(a.rx_bytes, b.rx_bytes) * 8,
    txBits: per(a.tx_bytes, b.tx_bytes) * 8,
    rxPackets: per(a.rx_packets, b.rx_packets),
    txPackets: per(a.tx_packets, b.tx_packets),
  };
};

export const formatBitRate = (bps) => {
  if (bps < 1000) return `${Math.round(bps)} bit/s`;
  if (bps < 1e6) return `${(bps / 1e3).toFixed(1)} kbit/s`;
  if (bps < 1e9) return `${(bps / 1e6).toFixed(1)} Mbit/s`;
  return `${(bps / 1e9).toFixed(2)} Gbit/s`;
};

export const formatCount = (n) => {
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}k`;
  return String(n);
};

// CountersRows shows one interface: a row per direction with its rate,
// packets per second, and the errors and drops counted so far.
export function CountersRows({ samples, iface }) {
  const { prev, last } = samples;
  if (last === false) return <div className="counters-note">Counters unavailable</div>;
  const counters = last?.interfaces?.[iface];
  if (last && !counters) return <div className="counters-note">No counters for {iface}</div>;
  const rates = countersRates(prev, last, iface);
  const row = (label, bits, pkts, errs, drops) => (
    <React.Fragment key={label}>
      <span className="counters-key">{label}</span>
      <span className="counters-val">{rates ? formatBitRate(bits) : '…'}</span>
      <span className="counters-val">{rates ? `${Math.round(pkts)} pkt/s` : ''}</span>
      <span className="counters-val counters-muted">
        {counters ? `${formatCount(errs)} err · ${formatCount(drops)} drop` : ''}
      </span>
    </React.Fragment>
  );
  return (
    <div
      className="counters-grid"
      title="Rates over the last 5 s, errors and drops since the interface came up"
    >
      {row('rx', rates?.rxBits, rates?.rxPackets, counters?.rx_errors, counters?.rx_dropped)}
      {row('tx', rates?.txBits, rates?.txPackets, counters?.tx_errors, counters?.tx_dropped)}
    </div>
  );
}
