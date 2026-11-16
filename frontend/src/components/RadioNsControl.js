// RadioNsControl.js: start or stop the radio emulation of a namespace generated from a radio zone.
import React, { useEffect, useState } from 'react';
import { API_BASE_URL } from '../config';

const RADIO = `${API_BASE_URL.replace(/\/+$/, '')}/radio`;

const RadioNsControl = ({ namespace }) => {
  const [zone, setZone] = useState(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let stopped = false;
    const load = async () => {
      try {
        const res = await fetch(`${RADIO}/namespaces/${encodeURIComponent(namespace)}`);
        const data = res.ok ? await res.json() : null;
        if (!stopped) setZone(data);
      } catch (e) {
        if (!stopped) setZone(null);
      }
    };
    load();
    const t = setInterval(load, 3000);
    return () => {
      stopped = true;
      clearInterval(t);
    };
  }, [namespace]);

  if (!zone || !zone.namespace || zone.namespace.status !== 'ready') return null;
  const emu = zone.emulation || {};
  const act = async (action) => {
    setBusy(true);
    try {
      const res = await fetch(`${RADIO}/zones/${zone.id}/emulation/${action}`, { method: 'POST' });
      const data = await res.json().catch(() => null);
      if (res.ok) setZone(data);
      else window.alert((data && data.error) || 'The radio service refused the request.');
    } finally {
      setBusy(false);
    }
  };
  const title = `Radio zone ${zone.name}: SUMO moves the UEs, Sionna RT gives each radio link, KubeNDT applies it`;
  return emu.alive ? (
    <button className="navbar-button navbar-button-danger" disabled={busy} onClick={() => act('stop')} title={title}>
      ■ Stop radio{emu.state === 'running' ? ` · t=${Math.round(emu.t)} s` : ' · starting'}
    </button>
  ) : (
    <button className="navbar-button" disabled={busy || zone.namespace.stale} onClick={() => act('start')} title={zone.namespace.stale ? 'The zone changed: regenerate the namespace from Radio area' : title}>
      ▶ Start radio
    </button>
  );
};

export default RadioNsControl;
