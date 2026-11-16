// RadioZoneCard.js: the radio area in use on the home page, with the way into its map.
import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { API_BASE_URL } from '../config';
import gnbIcon from '../assets/images/nodes/gnb.svg';
import './RadioZoneCard.css';

const RADIO = `${API_BASE_URL.replace(/\/+$/, '')}/radio`;

const RadioZoneCard = () => {
  const navigate = useNavigate();
  const [zones, setZones] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    let stopped = false;
    const load = async () => {
      try {
        const res = await fetch(`${RADIO}/zones`);
        const data = await res.json().catch(() => null);
        if (stopped) return;
        if (!res.ok) {
          setError((data && data.error) || `The radio service answered ${res.status}.`);
          setZones(null);
        } else {
          setError(null);
          setZones(data || []);
        }
      } catch (e) {
        if (!stopped) setError('The radio service does not answer.');
      }
    };
    load();
    const t = setInterval(load, 10000);
    return () => {
      stopped = true;
      clearInterval(t);
    };
  }, []);

  const active = zones && zones.find((z) => z.active);
  const radio = active && active.stats && active.stats.radio;

  return (
    <div className="cluster-status-card radio-zone-card">
      <div className="cluster-card-header">
        <h2 className="cluster-card-title">
          <img src={gnbIcon} alt="" className="radio-zone-card-icon" />
          Radio area
        </h2>
        <button className="radio-zone-card-btn" onClick={() => navigate('/radio')} title="Open the map: pick an area, place the sites, build the radio scenario">
          {active ? 'Change area' : 'Select area'}
        </button>
      </div>
      {error && <div className="radio-zone-card-note radio-zone-card-error">{error}</div>}
      {!error && zones === null && <div className="radio-zone-card-note">Loading…</div>}
      {!error && zones && !active && (
        <div className="radio-zone-card-note">
          No area in use. {zones.length ? `${zones.length} saved ${zones.length === 1 ? 'area' : 'areas'}: open the map to build one and mark it for emulation.` : 'Pick any place on the map, put the sites where you want them and build its radio scenario.'}
        </div>
      )}
      {active && (
        <div className="radio-zone-card-body" onClick={() => navigate('/radio')} role="button" tabIndex={0}>
          <div className="radio-zone-card-name">{active.name}</div>
          <div className="radio-zone-card-stats">
            <span>
              <b>
                {active.size_m[0]} × {active.size_m[1]} m
              </b>
              area
            </span>
            <span>
              <b>{active.sites.length}</b>
              sites
            </span>
            {radio && (
              <span>
                <b>{radio.sectors}</b>
                sectors
              </span>
            )}
            {radio && (
              <span>
                <b>{Math.round(radio.served_fraction_of_streets * 100)}%</b>
                streets served
              </span>
            )}
            <span>
              <b>{(active.ues || []).length}</b>
              UEs
            </span>
          </div>
          {active.namespace && active.namespace.status === 'ready' && (
            <div className="radio-zone-card-ns">
              Namespace{' '}
              <a
                href={`/${active.namespace.name}`}
                onClick={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  navigate(`/${active.namespace.name}`);
                }}
              >
                {active.namespace.name}
              </a>
              {active.emulation && active.emulation.alive ? ' · emulation running' : ' · emulation stopped'}
            </div>
          )}
          {!active.namespace && <div className="radio-zone-card-ns">No namespace yet: create it from the map.</div>}
        </div>
      )}
    </div>
  );
};

export default RadioZoneCard;
