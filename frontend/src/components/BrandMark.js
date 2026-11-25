import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { API_BASE_URL } from '../config';
import kubendtLogo from '../assets/images/kubendt-logo.svg';
import './BrandMark.css';

// The version never changes while the app runs, so it is read once.
let versionPromise = null;
const readVersion = () => {
  if (!versionPromise) {
    versionPromise = fetch(`${API_BASE_URL}/version`)
      .then((res) => (res.ok ? res.json() : null))
      .catch(() => null);
  }
  return versionPromise;
};

// The brand on every blue bar: the logo, the name, the version and what
// KubeNDT is. A click on it goes to the main page.
const BrandMark = () => {
  const navigate = useNavigate();
  const [version, setVersion] = useState(null);

  useEffect(() => {
    let alive = true;
    readVersion().then((v) => alive && setVersion(v));
    return () => {
      alive = false;
    };
  }, []);

  const isDev = version?.version === 'dev';
  const versionTitle = isDev
    ? 'local build'
    : [version?.commit, version?.build_date].filter((v) => v && v !== 'unknown').join(' · ');

  return (
    <button
      type="button"
      className="brand-mark"
      onClick={() => navigate('/')}
      title="Go to the main page"
    >
      <span className="brand-mark-logo">
        <img src={kubendtLogo} alt="" />
      </span>
      <span className="brand-mark-name">
        <span className="brand-mark-title">
          KubeNDT
          {version?.version && (
            <span className="brand-mark-version" title={versionTitle || undefined}>
              {isDev ? 'dev' : `v${version.version}`}
            </span>
          )}
        </span>
        <span className="brand-mark-tagline">
          A Kubernetes-native platform for Network Digital Twins
        </span>
      </span>
    </button>
  );
};

export default BrandMark;
