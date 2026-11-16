// RadioPage.js: choose the area a radio scenario emulates, place its sites and build it.
//
// The map is OpenStreetMap through Leaflet, vendored under public/vendor/leaflet
// and loaded on demand, so the dashboard gains no build dependency. What the
// user sees is what gets built: the scene's buildings and roads come from the
// same OpenStreetMap data, fetched by the radio service behind /radio.
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { API_BASE_URL } from '../config';
import gnbIcon from '../assets/images/nodes/gnb.svg';
import ueIcon from '../assets/images/nodes/ue.svg';
import './RadioPage.css';

const RADIO = `${API_BASE_URL.replace(/\/+$/, '')}/radio`;
const LEAFLET = `${process.env.PUBLIC_URL || ''}/vendor/leaflet`;
const DEFAULT_SITE = { height_m: null, azimuths: [0, 120, 240], downtilt_deg: 9, power_dbm: 44 };
// one colour per UE for its trail and its planned route
const UE_COLOURS = ['#e53935', '#8e24aa', '#3949ab', '#00897b', '#f4511e', '#6d4c41', '#d81b60', '#1e88e5', '#7cb342', '#fb8c00', '#5e35b1', '#00acc1'];

const STATUS_LABEL = {
  created: 'created',
  fetching: 'downloading map',
  fetched: 'map ready',
  building: 'building',
  ready: 'ready',
  error: 'error',
};

let leafletPromise = null;
const loadLeaflet = () => {
  if (window.L) return Promise.resolve(window.L);
  if (!leafletPromise) {
    leafletPromise = new Promise((resolve, reject) => {
      const css = document.createElement('link');
      css.rel = 'stylesheet';
      css.href = `${LEAFLET}/leaflet.css`;
      document.head.appendChild(css);
      const js = document.createElement('script');
      js.src = `${LEAFLET}/leaflet.js`;
      js.onload = () => resolve(window.L);
      js.onerror = () => reject(new Error('Could not load the map library.'));
      document.head.appendChild(js);
    });
  }
  return leafletPromise;
};

const api = async (path, options = {}) => {
  const res = await fetch(`${RADIO}${path}`, {
    ...options,
    headers: options.body ? { 'Content-Type': 'application/json' } : undefined,
  });
  const text = await res.text();
  let data = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch (e) {
    data = null;
  }
  if (!res.ok) {
    const error = new Error((data && data.error) || `Request failed (${res.status})`);
    error.data = data;
    throw error;
  }
  return data;
};

const metres = (bbox) => {
  const [s, w, n, e] = bbox;
  const ky = 111319.49;
  return [Math.round((e - w) * ky * Math.cos(((s + n) / 2) * (Math.PI / 180))), Math.round((n - s) * ky)];
};

// name,lat,lon[,height_m[,azimuths]]: azimuths separated by spaces or semicolons; a header line is skipped.
const parseCsv = (text) => {
  const out = [];
  text.split(/\r?\n/).forEach((line, i) => {
    const cols = line.split(/[,\t]/).map((c) => c.trim());
    if (cols.length < 3) return;
    const lat = parseFloat(cols[1]);
    const lon = parseFloat(cols[2]);
    if (Number.isNaN(lat) || Number.isNaN(lon)) return;
    const az = (cols[4] || '')
      .split(/[\s;]+/)
      .map(parseFloat)
      .filter((v) => !Number.isNaN(v));
    out.push({
      ...DEFAULT_SITE,
      name: cols[0] || `csv-${i}`,
      lat,
      lon,
      height_m: cols[3] ? parseFloat(cols[3]) || null : null,
      azimuths: az.length ? az : [0, 120, 240],
      source: 'CSV',
    });
  });
  return out;
};

const RadioPage = () => {
  const navigate = useNavigate();
  const mapEl = useRef(null);
  const map = useRef(null);
  const layers = useRef({});
  const modeRef = useRef('idle');
  const sitesRef = useRef([]);

  const [ready, setReady] = useState(false);
  const [zones, setZones] = useState([]);
  const [zone, setZone] = useState(null);
  const [draft, setDraft] = useState(null); // [s, w, n, e] being drawn
  const [name, setName] = useState('');
  const [sites, setSites] = useState([]);
  const [selected, setSelected] = useState(-1);
  const [dirty, setDirty] = useState(false);
  const [mode, setModeState] = useState('idle'); // idle | draw | site
  const [message, setMessage] = useState(null); // { kind: 'error' | 'info', text }
  const [search, setSearch] = useState('');
  const [opacity, setOpacity] = useState(0.75);
  const [autoCount, setAutoCount] = useState(3);
  const [serviceError, setServiceError] = useState(null);
  // the UEs of the zone, its namespace, its emulation
  const [ues, setUes] = useState([]);
  const uesRef = useRef([]);
  const zoneRef = useRef(null);
  const ueDefaults = useRef({ kind: 'car', mode: 'mobile' });
  const addUeRef = useRef(null);
  const [uesDirty, setUesDirty] = useState(false);
  const [ueKind, setUeKind] = useState('car');
  const [ueMode, setUeMode] = useState('mobile');
  const [autoCars, setAutoCars] = useState(3);
  const [autoPersons, setAutoPersons] = useState(3);
  const [nsName, setNsName] = useState('');
  const [working, setWorking] = useState(null);
  // the UE whose route is being drawn: clicks on the map add its destinations, in order
  const [routeEdit, setRouteEditState] = useState(-1);
  const routeEditRef = useRef(-1);
  const setRouteEdit = (i) => {
    routeEditRef.current = i;
    setRouteEditState(i);
  };

  const setMode = (m) => {
    modeRef.current = m;
    setModeState(m);
    if (map.current) {
      map.current.getContainer().style.cursor = m === 'idle' ? '' : 'crosshair';
      if (m === 'draw') map.current.dragging.disable();
      else map.current.dragging.enable();
      // quick clicks while placing points must not zoom the map
      if (m === 'idle') map.current.doubleClickZoom.enable();
      else map.current.doubleClickZoom.disable();
    }
  };
  const updateSites = (next, markDirty = true) => {
    sitesRef.current = next;
    setSites(next);
    if (markDirty) setDirty(true);
  };
  const fail = (e) => setMessage({ kind: 'error', text: e.message || String(e) });

  const refresh = useCallback(async () => {
    try {
      const list = await api('/zones');
      setZones(list);
      setServiceError(null);
      setZone((cur) => (cur ? list.find((z) => z.id === cur.id) || null : cur));
    } catch (e) {
      setServiceError(e.message);
    }
  }, []);

  // ---- map
  useEffect(() => {
    let cancelled = false;
    loadLeaflet()
      .then((L) => {
        if (cancelled || map.current) return;
        const m = L.map(mapEl.current, { center: [40.3325, -3.7655], zoom: 15, zoomControl: true });
        L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
          maxZoom: 19,
          attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
        }).addTo(m);
        layers.current.sites = L.layerGroup().addTo(m);
        layers.current.ues = L.layerGroup().addTo(m);
        let start = null;
        m.on('mousedown', (ev) => {
          if (modeRef.current === 'draw') start = ev.latlng;
        });
        m.on('mousemove', (ev) => {
          if (modeRef.current !== 'draw' || !start) return;
          const b = L.latLngBounds(start, ev.latlng);
          setDraft([b.getSouth(), b.getWest(), b.getNorth(), b.getEast()]);
        });
        m.on('mouseup', () => {
          if (modeRef.current === 'draw' && start) {
            start = null;
            setMode('idle');
          }
        });
        m.on('click', (ev) => {
          if (modeRef.current === 'ue') {
            addUeRef.current(ev.latlng);
            return;
          }
          if (modeRef.current !== 'site') return;
          const next = [
            ...sitesRef.current,
            { ...DEFAULT_SITE, name: `site-${sitesRef.current.length}`, lat: ev.latlng.lat, lon: ev.latlng.lng, source: 'manual' },
          ];
          updateSites(next);
          setSelected(next.length - 1);
        });
        map.current = m;
        // the container gets its size after the first layout: without this the first tiles do not load
        setTimeout(() => m.invalidateSize(), 60);
        setReady(true);
      })
      .catch(fail);
    return () => {
      cancelled = true;
      if (map.current) {
        map.current.remove();
        map.current = null;
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 1500);
    return () => clearInterval(t);
  }, [refresh]);

  // ---- the area rectangle: the zone's, or the one being drawn
  const box = draft || (zone && zone.bbox) || null;
  useEffect(() => {
    const L = window.L;
    if (!ready || !L) return;
    if (layers.current.box) layers.current.box.remove();
    layers.current.box = null;
    if (box) {
      layers.current.box = L.rectangle(
        [
          [box[0], box[1]],
          [box[2], box[3]],
        ],
        { color: draft ? '#f59e0b' : '#1976d2', weight: 2, fillOpacity: 0.04, dashArray: draft ? '6 6' : null, interactive: false }
      ).addTo(map.current);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, JSON.stringify(box), !!draft]);

  // ---- coverage overlay
  const coverageKey = zone && zone.has_coverage ? `${zone.id}:${zone.built}` : '';
  useEffect(() => {
    const L = window.L;
    if (!ready || !L) return;
    if (layers.current.cov) layers.current.cov.remove();
    layers.current.cov = null;
    if (coverageKey && zone.stats && zone.stats.radio) {
      layers.current.cov = L.imageOverlay(`${RADIO}/zones/${zone.id}/coverage.png?v=${encodeURIComponent(zone.built)}`, zone.stats.radio.coverage_bounds, {
        opacity,
        interactive: false,
      }).addTo(map.current);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, coverageKey]);
  useEffect(() => {
    if (layers.current.cov) layers.current.cov.setOpacity(opacity);
  }, [opacity]);

  // ---- site markers with their sectors
  useEffect(() => {
    const L = window.L;
    if (!ready || !L) return;
    const group = layers.current.sites;
    group.clearLayers();
    sites.forEach((s, i) => {
      (s.azimuths || []).forEach((az) => {
        const r = 0.0009;
        const a = (az * Math.PI) / 180;
        const tip = [s.lat + r * Math.cos(a), s.lon + (r * Math.sin(a)) / Math.cos((s.lat * Math.PI) / 180)];
        L.polyline([[s.lat, s.lon], tip], { color: i === selected ? '#e53935' : '#37474f', weight: 4, opacity: 0.85, interactive: false }).addTo(group);
      });
      const marker = L.marker([s.lat, s.lon], {
        draggable: true,
        title: s.name,
        icon: L.icon({ iconUrl: gnbIcon, iconSize: [40, 40], iconAnchor: [20, 30], className: i === selected ? 'radio-site radio-site-selected' : 'radio-site' }),
      }).addTo(group);
      marker.on('click', () => setSelected(i));
      marker.on('dragend', (ev) => {
        const p = ev.target.getLatLng();
        updateSites(sitesRef.current.map((x, k) => (k === i ? { ...x, lat: p.lat, lon: p.lng } : x)));
      });
      marker.bindTooltip(s.name, { permanent: true, direction: 'bottom', offset: [0, 8], className: 'radio-site-label' });
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, sites, selected]);

  // ---- actions
  const openZone = (z) => {
    setDraft(null);
    setZone(z);
    updateSites((z.sites || []).map((s) => ({ ...s })), false);
    uesRef.current = (z.ues || []).map((u) => ({ ...u }));
    setUes(uesRef.current);
    setUesDirty(false);
    setNsName('');
    setDirty(false);
    setSelected(-1);
    setMessage(null);
    if (map.current) {
      map.current.fitBounds(
        [
          [z.bbox[0], z.bbox[1]],
          [z.bbox[2], z.bbox[3]],
        ],
        { padding: [20, 20] }
      );
    }
  };

  const doSearch = async (ev) => {
    ev.preventDefault();
    if (!search.trim()) return;
    try {
      const res = await fetch(`https://nominatim.openstreetmap.org/search?format=json&limit=1&q=${encodeURIComponent(search)}`);
      const hits = await res.json();
      if (!hits.length) throw new Error('Place not found.');
      const b = hits[0].boundingbox.map(parseFloat);
      map.current.fitBounds(
        [
          [b[0], b[2]],
          [b[1], b[3]],
        ],
        { maxZoom: 16 }
      );
    } catch (e) {
      fail(e);
    }
  };

  const createZone = async () => {
    try {
      const z = await api('/zones', { method: 'POST', body: JSON.stringify({ name: name.trim() || 'zone', bbox: draft }) });
      setName('');
      await refresh();
      openZone(z);
    } catch (e) {
      fail(e);
    }
  };

  const saveSites = async () => {
    const z = await api(`/zones/${zone.id}/sites`, { method: 'PUT', body: JSON.stringify({ sites: sitesRef.current }) });
    setDirty(false);
    setZone(z);
    return z;
  };

  const addMany = (list, label) => {
    if (!list.length) {
      setMessage({ kind: 'info', text: `${label}: none found in this area.` });
      return;
    }
    const inside = list.filter((s) => s.lat >= zone.bbox[0] && s.lat <= zone.bbox[2] && s.lon >= zone.bbox[1] && s.lon <= zone.bbox[3]);
    updateSites([...sitesRef.current, ...inside]);
    setMessage({ kind: 'info', text: `${label}: ${inside.length} added${inside.length < list.length ? `, ${list.length - inside.length} outside the area skipped` : ''}.` });
  };

  const importOsm = async () => {
    try {
      addMany(await api(`/zones/${zone.id}/sites/osm`), 'Masts mapped in OpenStreetMap');
    } catch (e) {
      fail(e);
    }
  };
  const autoPlace = async () => {
    try {
      updateSites(await api(`/zones/${zone.id}/sites/auto`, { method: 'POST', body: JSON.stringify({ count: autoCount }) }));
      setSelected(-1);
    } catch (e) {
      fail(e);
    }
  };
  const importCsv = (ev) => {
    const file = ev.target.files[0];
    ev.target.value = '';
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => addMany(parseCsv(String(reader.result)), 'CSV');
    reader.readAsText(file);
  };

  const buildZone = async () => {
    try {
      setMessage(null);
      await saveSites();
      setZone(await api(`/zones/${zone.id}/build`, { method: 'POST' }));
    } catch (e) {
      fail(e);
    }
  };
  const activate = async () => {
    try {
      setZone(await api(`/zones/${zone.id}/activate`, { method: 'POST' }));
      refresh();
    } catch (e) {
      fail(e);
    }
  };
  const removeZone = async () => {
    if (!window.confirm(`Delete the zone "${zone.name}" and everything built for it?`)) return;
    try {
      await api(`/zones/${zone.id}`, { method: 'DELETE' });
      setZone(null);
      updateSites([], false);
      refresh();
    } catch (e) {
      fail(e);
    }
  };

  // ---- UEs: cars and persons, on the streets only
  const updateUes = (next, markDirty = true) => {
    uesRef.current = next;
    setUes(next);
    if (markDirty) setUesDirty(true);
  };
  useEffect(() => {
    zoneRef.current = zone;
  }, [zone]);
  useEffect(() => {
    ueDefaults.current = { kind: ueKind, mode: ueMode };
  }, [ueKind, ueMode]);
  const snapUe = async (lat, lon, kind) =>
    api(`/zones/${zoneRef.current.id}/ues/snap`, { method: 'POST', body: JSON.stringify({ lat, lon, kind }) });
  addUeRef.current = async (latlng) => {
    try {
      const editing = routeEditRef.current;
      if (editing >= 0 && uesRef.current[editing]) {
        // a destination of the route being drawn, after the ones already there
        const p = await snapUe(latlng.lat, latlng.lng, uesRef.current[editing].kind);
        updateUes(uesRef.current.map((x, k) => (k === editing ? { ...x, waypoints: [...(x.waypoints || []), { lat: p.lat, lon: p.lon }] } : x)));
        return;
      }
      const { kind, mode } = ueDefaults.current;
      const p = await snapUe(latlng.lat, latlng.lng, kind);
      const ue = { kind, mode, lat: p.lat, lon: p.lon, source: 'manual' };
      if (mode === 'route') {
        ue.waypoints = [];
        ue.at_end = 'stop';
      }
      updateUes([...uesRef.current, ue]);
      if (mode === 'route') setRouteEdit(uesRef.current.length - 1);
    } catch (e) {
      fail(e);
    }
  };
  const saveUes = async () => {
    const z = await api(`/zones/${zone.id}/ues`, { method: 'PUT', body: JSON.stringify({ ues: uesRef.current }) });
    updateUes((z.ues || []).map((u) => ({ ...u })), false);
    setUesDirty(false);
    setZone(z);
    return z;
  };
  const run = async (label, fn) => {
    try {
      setMessage(null);
      setWorking(label);
      await fn();
    } catch (e) {
      fail(e);
    } finally {
      setWorking(null);
    }
  };
  const autoUes = () =>
    run('placing', async () => {
      updateUes(await api(`/zones/${zone.id}/ues/auto`, { method: 'POST', body: JSON.stringify({ cars: autoCars, persons: autoPersons }) }));
    });
  const createNamespace = (regenerate) =>
    run('namespace', async () => {
      if (regenerate && !window.confirm(`Regenerate the namespace "${zone.namespace.name}"? It is created again from scratch: nodes and links added by hand are lost. Export the topology first if you need them.`)) return;
      if (uesDirty) await saveUes();
      setZone(await api(`/zones/${zone.id}/namespace`, { method: 'POST', body: JSON.stringify({ name: regenerate ? zone.namespace.name : nsName.trim() || `radio-${zone.id}`.slice(0, 40) }) }));
    });
  const deleteNamespace = () =>
    run('namespace', async () => {
      if (!window.confirm(`Delete the namespace "${zone.namespace.name}" and everything in it?`)) return;
      setZone(await api(`/zones/${zone.id}/namespace`, { method: 'DELETE' }));
    });
  const emulation = (action) =>
    run('emulation', async () => {
      const call = (body) => api(`/zones/${zone.id}/emulation/${action}`, { method: 'POST', body: JSON.stringify(body) });
      try {
        // moving UEs or redrawing routes needs no new namespace: save them and go
        if (action === 'start' && uesDirty) await saveUes();
        setZone(await call({}));
      } catch (e) {
        // one emulation at a time: offer to move it here
        if (e.data && e.data.code === 'EMULATION_BUSY' && e.data.zone !== zone.id) {
          if (!window.confirm(`An emulation is running in the zone "${e.data.zone_name}". Only one runs at a time. Stop it and start this one?`)) return;
          setZone(await call({ takeover: true }));
        } else {
          throw e;
        }
      }
    });

  const emu = (zone && zone.emulation) || {};
  const live = emu.state === 'running' && Array.isArray(emu.ues);
  const liveKey = live ? `${emu.step}` : '';
  // ---- UE markers: where they are now while the emulation runs, where they start otherwise
  useEffect(() => {
    const L = window.L;
    if (!ready || !L) return;
    const group = layers.current.ues;
    group.clearLayers();
    const shown = live ? emu.ues : ues;
    shown.forEach((u, i) => {
      const cls = ['radio-ue', `radio-ue-${u.kind}`, u.mode === 'fixed' ? 'radio-ue-fixed' : '', live && u.outage ? 'radio-ue-outage' : ''].join(' ');
      const marker = L.marker([u.lat, u.lon], {
        draggable: !live,
        title: `ue-${i} · ${u.kind} · ${u.mode}`,
        zIndexOffset: 500,
        icon: L.divIcon({ className: cls, html: `<img src="${ueIcon}" alt=""/><b>${u.kind === 'car' ? '🚗' : '🚶'}</b><i>ue-${i}</i>`, iconSize: [34, 34], iconAnchor: [17, 17] }),
      }).addTo(group);
      if (live) {
        const colour = UE_COLOURS[i % UE_COLOURS.length];
        // where it has been (solid), where it is heading (dashed, ending at its destination)
        if (u.trail && u.trail.length > 1) L.polyline(u.trail, { color: colour, weight: 4, opacity: 0.85, interactive: false }).addTo(group);
        if (u.route && u.route.length > 1) {
          L.polyline(u.route, { color: colour, weight: 2.5, opacity: 0.8, dashArray: '4 7', interactive: false }).addTo(group);
          L.circleMarker(u.route[u.route.length - 1], { radius: 5, color: colour, weight: 2, fillColor: '#ffffff', fillOpacity: 1, interactive: false }).addTo(group);
        }
        const s = sitesRef.current[u.gnb];
        if (s && !u.outage) L.polyline([[u.lat, u.lon], [s.lat, s.lon]], { color: '#1976d2', weight: 1, opacity: 0.45, interactive: false }).addTo(group);
      } else {
        marker.on('dragend', async (ev) => {
          const p = ev.target.getLatLng();
          try {
            const sn = await snapUe(p.lat, p.lng, u.kind);
            updateUes(uesRef.current.map((x, k) => (k === i ? { ...x, lat: sn.lat, lon: sn.lon } : x)));
          } catch (e) {
            fail(e);
            updateUes([...uesRef.current], false);
          }
        });
      }
    });
    // the routes the user drew: start, then the numbered destinations in order
    ues.forEach((u, i) => {
      if (u.mode !== 'route' || !(u.waypoints || []).length) return;
      const colour = UE_COLOURS[i % UE_COLOURS.length];
      const pts = [[u.lat, u.lon], ...u.waypoints.map((w) => [w.lat, w.lon])];
      if (u.at_end === 'loop') pts.push([u.lat, u.lon]);
      if (!live) L.polyline(pts, { color: colour, weight: 2, opacity: 0.7, dashArray: '2 6', interactive: false }).addTo(group);
      u.waypoints.forEach((w, j) => {
        const m = L.marker([w.lat, w.lon], {
          draggable: !live,
          title: `ue-${i}: destination ${j + 1}`,
          zIndexOffset: 400,
          icon: L.divIcon({ className: 'radio-waypoint', html: `<span style="border-color:${colour};color:${colour}">${j + 1}</span>`, iconSize: [20, 20], iconAnchor: [10, 10] }),
        }).addTo(group);
        if (!live) {
          m.on('dragend', async (ev) => {
            const p = ev.target.getLatLng();
            try {
              const sn = await snapUe(p.lat, p.lng, u.kind);
              updateUes(uesRef.current.map((x, k) => (k === i ? { ...x, waypoints: x.waypoints.map((y, q) => (q === j ? { lat: sn.lat, lon: sn.lon } : y)) } : x)));
            } catch (e) {
              fail(e);
              updateUes([...uesRef.current], false);
            }
          });
        }
      });
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, ues, liveKey, live]);

  const editSite = (patch) => updateSites(sitesRef.current.map((x, k) => (k === selected ? { ...x, ...patch } : x)));
  const sel = selected >= 0 ? sites[selected] : null;
  const busy = zone && (zone.busy || zone.status === 'fetching' || zone.status === 'building');
  const stats = (zone && zone.stats) || {};
  const size = box ? metres(box) : null;

  return (
    <div className="radio-page">
      <aside className="radio-side">
        <div className="radio-head">
          <button className="radio-back" onClick={() => navigate('/')} title="Back to namespaces">
            ←
          </button>
          <div>
            <h1>Radio zones</h1>
            <p>Pick any area, place the sites and build its radio scenario.</p>
          </div>
        </div>

        {serviceError && <div className="radio-msg radio-msg-error">{serviceError}</div>}
        {message && (
          <div className={`radio-msg radio-msg-${message.kind}`} onClick={() => setMessage(null)}>
            {message.text}
          </div>
        )}

        <section>
          <h2>1 · Area</h2>
          <form className="radio-row" onSubmit={doSearch}>
            <input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search a place (city, campus, street)" />
            <button type="submit">Go</button>
          </form>
          <div className="radio-row">
            <button className={mode === 'draw' ? 'radio-on' : ''} onClick={() => setMode(mode === 'draw' ? 'idle' : 'draw')}>
              {mode === 'draw' ? 'Drag on the map…' : 'Draw a new area'}
            </button>
            {draft && (
              <button
                onClick={() => {
                  setDraft(null);
                  setMode('idle');
                }}
              >
                Cancel
              </button>
            )}
          </div>
          {size && (
            <div className="radio-note">
              {draft ? 'New area' : 'Area'}: {size[0]} × {size[1]} m{draft && (size[0] > 3000 || size[1] > 3000) ? ' (over the 3000 m limit)' : ''}
            </div>
          )}
          {draft && mode === 'idle' && (
            <div className="radio-row">
              <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Name of the zone" />
              <button className="radio-primary" onClick={createZone}>
                Create
              </button>
            </div>
          )}
          <ul className="radio-zones">
            {zones.map((z) => (
              <li key={z.id} className={zone && zone.id === z.id ? 'radio-zone-on' : ''} onClick={() => openZone(z)}>
                <span>{z.name}</span>
                <span className={`radio-status radio-status-${z.status}`}>
                  {z.emulation && z.emulation.alive ? <b className="radio-emulating">emulating · </b> : null}
                  {z.active ? 'in use · ' : ''}
                  {STATUS_LABEL[z.status] || z.status}
                </span>
              </li>
            ))}
            {!zones.length && !serviceError && <li className="radio-empty">No zones yet. Draw an area to create the first one.</li>}
          </ul>
        </section>

        {zone && (
          <section>
            <h2>2 · Sites ({sites.length})</h2>
            {stats.buildings !== undefined && (
              <div className="radio-note">
                {stats.buildings} buildings ({stats.buildings_with_height} with a mapped height), {stats.highway_ways} road segments.
              </div>
            )}
            <div className="radio-row">
              <button className={mode === 'site' ? 'radio-on' : ''} onClick={() => setMode(mode === 'site' ? 'idle' : 'site')}>
                {mode === 'site' ? 'Click on the map…' : 'Add on the map'}
              </button>
              <button onClick={importOsm} title="Telephony masts and antennas mapped in OpenStreetMap inside the area">
                Real masts (OSM)
              </button>
            </div>
            <div className="radio-row">
              <label className="radio-file">
                Import CSV
                <input type="file" accept=".csv,.txt" onChange={importCsv} />
              </label>
              <button onClick={autoPlace}>Auto-place</button>
              <input className="radio-num" type="number" min="1" max="20" value={autoCount} onChange={(e) => setAutoCount(parseInt(e.target.value, 10) || 1)} />
            </div>
            <div className="radio-note">CSV columns: name, lat, lon, height_m, azimuths (separated by spaces). Drag a mast to move it.</div>
            <ul className="radio-sites">
              {sites.map((s, i) => (
                <li key={i} className={i === selected ? 'radio-zone-on' : ''} onClick={() => setSelected(i)}>
                  <span>{s.name}</span>
                  <span className="radio-dim">
                    {s.azimuths.length} sectors · {s.height_m ? `${s.height_m} m` : 'rooftop'}
                  </span>
                </li>
              ))}
            </ul>
            {sel && (
              <div className="radio-editor">
                <label>
                  Name
                  <input value={sel.name} onChange={(e) => editSite({ name: e.target.value })} />
                </label>
                <label>
                  Height above ground (m)
                  <input
                    value={sel.height_m ?? ''}
                    placeholder="empty: highest rooftop nearby + mast"
                    onChange={(e) => editSite({ height_m: e.target.value === '' ? null : parseFloat(e.target.value) || null })}
                  />
                </label>
                <label>
                  Sector azimuths (degrees from north)
                  <input
                    defaultValue={sel.azimuths.join(' ')}
                    key={`${selected}-${sel.azimuths.length}`}
                    onBlur={(e) => {
                      const az = e.target.value
                        .split(/[\s,;]+/)
                        .map(parseFloat)
                        .filter((v) => !Number.isNaN(v));
                      if (az.length) editSite({ azimuths: az });
                    }}
                  />
                </label>
                <div className="radio-row">
                  <label>
                    Downtilt (°)
                    <input type="number" value={sel.downtilt_deg} onChange={(e) => editSite({ downtilt_deg: parseFloat(e.target.value) || 0 })} />
                  </label>
                  <label>
                    Power (dBm)
                    <input type="number" value={sel.power_dbm} onChange={(e) => editSite({ power_dbm: parseFloat(e.target.value) || 0 })} />
                  </label>
                </div>
                <div className="radio-dim">
                  {sel.lat.toFixed(6)}, {sel.lon.toFixed(6)} · {sel.source || 'manual'}
                  {sel.placement ? ` · ${sel.placement}` : ''}
                </div>
                <button
                  className="radio-danger"
                  onClick={() => {
                    updateSites(sitesRef.current.filter((_, k) => k !== selected));
                    setSelected(-1);
                  }}
                >
                  Remove this site
                </button>
              </div>
            )}
          </section>
        )}

        {zone && (
          <section>
            <h2>3 · Build</h2>
            <div className="radio-row">
              <button className="radio-primary" disabled={busy || !sites.length || zone.status === 'created'} onClick={buildZone}>
                {zone.status === 'ready' && !dirty ? 'Rebuild' : 'Build radio scenario'}
              </button>
              <button disabled={zone.status !== 'ready' || dirty || zone.active} onClick={activate}>
                {zone.active ? 'In use' : 'Use for emulation'}
              </button>
            </div>
            {busy && (
              <div className="radio-progress">
                <span className="radio-spinner" /> {zone.step || STATUS_LABEL[zone.status]}
              </div>
            )}
            {zone.status === 'error' && <div className="radio-msg radio-msg-error">{zone.error}</div>}
            {dirty && zone.status === 'ready' && <div className="radio-note">The sites changed: rebuild to update the coverage.</div>}
            {zone.status === 'ready' && stats.radio && (
              <div className="radio-stats">
                <div>
                  <b>{Math.round(stats.radio.served_fraction_of_streets * 100)}%</b>
                  <small>of the streets served</small>
                </div>
                <div>
                  <b>{stats.radio.sectors}</b>
                  <small>sectors</small>
                </div>
                <div>
                  <b>{stats.roads ? stats.roads.road_km : '–'} km</b>
                  <small>of roads</small>
                </div>
                <div>
                  <b>{stats.build_s} s</b>
                  <small>to build</small>
                </div>
              </div>
            )}
            {zone.has_coverage && (
              <div className="radio-legend">
                <div className="radio-row">
                  <span>Coverage</span>
                  <input type="range" min="0" max="1" step="0.05" value={opacity} onChange={(e) => setOpacity(parseFloat(e.target.value))} />
                </div>
                <div className="radio-gradient" />
                <div className="radio-row radio-dim">
                  <span>{stats.radio.legend_db[0]} dB</span>
                  <span>path gain of the best sector</span>
                  <span>{stats.radio.legend_db[1]} dB</span>
                </div>
              </div>
            )}
            <button className="radio-danger" disabled={busy} onClick={removeZone}>
              Delete zone
            </button>
          </section>
        )}
        {zone && zone.status === 'ready' && (
          <section>
            <h2>4 · UEs ({ues.length})</h2>
            <div className="radio-row">
              <select value={ueKind} onChange={(e) => setUeKind(e.target.value)} title="What the next UE is">
                <option value="car">🚗 Car</option>
                <option value="person">🚶 Person</option>
              </select>
              <select value={ueMode} onChange={(e) => setUeMode(e.target.value)} title="Mobile: moves along the streets from where you put it. Fixed: stays there.">
                <option value="mobile">Mobile</option>
                <option value="fixed">Fixed</option>
                <option value="route">Route</option>
              </select>
              <button
                className={mode === 'ue' ? 'radio-on' : ''}
                disabled={live}
                onClick={() => {
                  setRouteEdit(-1);
                  setMode(mode === 'ue' ? 'idle' : 'ue');
                }}
              >
                {mode !== 'ue' ? 'Add on the map' : routeEdit >= 0 ? 'Finish route' : ueMode === 'route' ? 'Click the start…' : 'Click on the map…'}
              </button>
            </div>
            <div className="radio-row">
              <span className="radio-dim">🚗</span>
              <input className="radio-num" type="number" min="0" max="24" value={autoCars} onChange={(e) => setAutoCars(parseInt(e.target.value, 10) || 0)} />
              <span className="radio-dim">🚶</span>
              <input className="radio-num" type="number" min="0" max="24" value={autoPersons} onChange={(e) => setAutoPersons(parseInt(e.target.value, 10) || 0)} />
              <button disabled={live || working === 'placing'} onClick={autoUes} title="Replace the list with this many cars and persons spread over the streets">
                Auto-place
              </button>
            </div>
            {routeEdit >= 0 && ues[routeEdit] && (
              <div className="radio-msg radio-msg-info">
                Route of ue-{routeEdit}: click its destinations on the map, in the order it visits them ({(ues[routeEdit].waypoints || []).length} so far). Then press Finish route.
              </div>
            )}
            <div className="radio-note">
              UEs go on the streets only: a car snaps to the nearest carriageway, a person to the nearest pavement or footpath. Drag one to move it. A route UE goes from its start
              through its numbered destinations in order; at the end it stops there, or loops back to the start and repeats.
            </div>
            <ul className="radio-sites">
              {ues.map((u, i) => (
                <li key={i}>
                  <span>
                    {u.kind === 'car' ? '🚗' : '🚶'} ue-{i}
                  </span>
                  <span className="radio-ue-actions">
                    {u.mode === 'route' ? (
                      <>
                        <span className="radio-dim">route · {(u.waypoints || []).length}</span>
                        <button disabled={live} onClick={() => updateUes(uesRef.current.map((x, k) => (k === i ? { ...x, at_end: x.at_end === 'loop' ? 'stop' : 'loop' } : x)))} title="At the last destination: stop there, or loop back to the start and repeat">
                          {u.at_end === 'loop' ? 'loop' : 'stop'}
                        </button>
                        <button
                          disabled={live}
                          className={routeEdit === i ? 'radio-on' : ''}
                          onClick={() => {
                            if (routeEdit === i) {
                              setRouteEdit(-1);
                              setMode('idle');
                            } else {
                              setRouteEdit(i);
                              setMode('ue');
                            }
                          }}
                          title="Add destinations to this route by clicking on the map"
                        >
                          {routeEdit === i ? 'done' : '+ point'}
                        </button>
                        <button disabled={live || !(u.waypoints || []).length} onClick={() => updateUes(uesRef.current.map((x, k) => (k === i ? { ...x, waypoints: x.waypoints.slice(0, -1) } : x)))} title="Remove the last destination">
                          ↶
                        </button>
                      </>
                    ) : (
                      <button disabled={live} onClick={() => updateUes(uesRef.current.map((x, k) => (k === i ? { ...x, mode: x.mode === 'fixed' ? 'mobile' : 'fixed' } : x)))} title="Switch between mobile and fixed">
                        {u.mode}
                      </button>
                    )}
                    <button
                      disabled={live}
                      onClick={() => {
                        setRouteEdit(-1);
                        updateUes(uesRef.current.filter((_, k) => k !== i));
                      }}
                      title="Remove this UE"
                    >
                      ×
                    </button>
                  </span>
                </li>
              ))}
            </ul>
            <div className="radio-row">
              <button className={uesDirty ? 'radio-primary' : ''} disabled={!uesDirty || live} onClick={() => run('ues', saveUes)}>
                {uesDirty ? 'Save UEs' : 'UEs saved'}
              </button>
            </div>
          </section>
        )}

        {zone && zone.status === 'ready' && (
          <section>
            <h2>5 · Namespace and emulation</h2>
            {!zone.namespace && (
              <>
                <div className="radio-note">
                  Creates a KubeNDT namespace with the 5G system of this zone: an Open5GS core, one gNB per site, one UE per terminal, and a data network with a first server
                  to hang your own from.
                </div>
                <div className="radio-row">
                  <input value={nsName} onChange={(e) => setNsName(e.target.value)} placeholder={`radio-${zone.id}`.slice(0, 40)} />
                  <button className="radio-primary" disabled={!ues.length || working === 'namespace'} onClick={() => createNamespace(false)}>
                    Create namespace
                  </button>
                </div>
                {!ues.length && <div className="radio-note">Add at least one UE first.</div>}
              </>
            )}
            {zone.namespace && zone.namespace.status === 'deploying' && (
              <div className="radio-progress">
                <span className="radio-spinner" /> {zone.namespace.name}: {zone.namespace.step}
              </div>
            )}
            {zone.namespace && zone.namespace.status === 'error' && (
              <>
                <div className="radio-msg radio-msg-error">{zone.namespace.error}</div>
                <div className="radio-row">
                  <button className="radio-primary" onClick={() => createNamespace(true)}>
                    Try again
                  </button>
                  <button className="radio-danger" onClick={deleteNamespace}>
                    Delete namespace
                  </button>
                </div>
              </>
            )}
            {zone.namespace && zone.namespace.status === 'ready' && (
              <>
                <div className="radio-ns">
                  <div>
                    <b>{zone.namespace.name}</b>
                    <small>
                      {zone.namespace.pods} pods · {zone.namespace.gnbs} gNB · {zone.namespace.sessions}/{zone.namespace.ues} UEs registered
                    </small>
                  </div>
                  <button className="radio-primary" onClick={() => navigate(`/${zone.namespace.name}`)}>
                    Open namespace
                  </button>
                </div>
                <div className="radio-note">
                  Servers hang from the switch <code>dn-0</code>: network {zone.namespace.data_network}, gateway 10.45.0.1. <code>server-0</code> is {zone.namespace.server}.
                </div>
                {zone.namespace.stale && <div className="radio-msg radio-msg-info">The number of sites or of UEs changed since this namespace was created. Regenerate it to apply it.</div>}
                <div className="radio-row">
                  <button disabled={emu.alive || working === 'namespace'} className={zone.namespace.stale ? 'radio-primary' : ''} onClick={() => createNamespace(true)}>
                    Regenerate
                  </button>
                  <button className="radio-danger" disabled={emu.alive || working === 'namespace'} onClick={deleteNamespace}>
                    Delete namespace
                  </button>
                </div>
                <div className="radio-row radio-emu-row">
                  {emu.alive ? (
                    <button className="radio-stop" disabled={working === 'emulation'} onClick={() => emulation('stop')}>
                      ■ Stop emulation
                    </button>
                  ) : (
                    <button className="radio-primary" disabled={zone.namespace.stale || working === 'emulation'} onClick={() => emulation('start')}>
                      ▶ Start emulation
                    </button>
                  )}
                  <span className="radio-dim">{emu.alive ? (live ? `t = ${emu.t} s · ${emu.realtime_factor}× real time · ${emu.step_ms.total} ms/step` : emu.step || 'starting…') : emu.state === 'error' ? '' : 'stopped'}</span>
                </div>
                {emu.state === 'error' && <div className="radio-msg radio-msg-error">{emu.error}</div>}
                {message && message.kind === 'error' && <div className="radio-msg radio-msg-error">{message.text}</div>}
                {!emu.alive && emu.state !== 'error' && (
                  <div className="radio-msg radio-msg-info">
                    The emulation is stopped: the UEs stand at their start points and the radio links are not shaped. Start it to see them move.
                  </div>
                )}
                {live && (
                  <div className="radio-note">
                    On the map, each UE draws where it has been (solid line) and where it is heading (dashed line to its destination). Mobile UEs pick a new destination when
                    they arrive; a person moves at about 1.2 m/s, so zoom in to see it advance.
                  </div>
                )}
                {live && (
                  <>
                    <div className="radio-stats">
                      <div>
                        <b>{emu.a3.inter_site}</b>
                        <small>gNB changes</small>
                      </div>
                      <div>
                        <b>{emu.a3.rlf}</b>
                        <small>radio link failures</small>
                      </div>
                      <div>
                        <b>
                          {emu.ues.filter((u) => u.session).length}/{emu.ues.length}
                        </b>
                        <small>PDU sessions up</small>
                      </div>
                      <div>
                        <b>
                          {emu.ues.filter((u) => u.reaches_server).length}/{emu.ues.length}
                        </b>
                        <small>reach the server</small>
                      </div>
                    </div>
                    <table className="radio-table">
                      <thead>
                        <tr>
                          <th>UE</th>
                          <th>m/s</th>
                          <th>gNB</th>
                          <th>SINR</th>
                          <th>DL</th>
                          <th>UL</th>
                          <th>ping</th>
                        </tr>
                      </thead>
                      <tbody>
                        {emu.ues.map((u) => (
                          <tr key={u.name} className={u.outage ? 'radio-row-out' : ''}>
                            <td>
                              <span className="radio-swatch" style={{ background: UE_COLOURS[emu.ues.indexOf(u) % UE_COLOURS.length] }} />
                              {u.kind === 'car' ? '🚗' : '🚶'} {u.name}
                            </td>
                            <td>{u.done ? 'arrived' : u.mode === 'fixed' ? 'fixed' : u.speed}</td>
                            <td>
                              {u.gnb}.{u.sector}
                            </td>
                            <td>{u.sinr}</td>
                            <td>{u.outage ? 'cut' : u.dl}</td>
                            <td>{u.outage ? '–' : u.ul}</td>
                            <td>{u.reaches_server === null ? '…' : u.reaches_server ? '✓' : '✗'}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <div className="radio-note">Rates in Mbit/s as applied to the radio link in KubeNDT; ping is a real one from the UE to server-0 through its 5G session.</div>
                  </>
                )}
              </>
            )}
          </section>
        )}
      </aside>
      <div className="radio-map" ref={mapEl} />
    </div>
  );
};

export default RadioPage;
