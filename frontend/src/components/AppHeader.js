import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import ProfileMenu from './ProfileMenu';
import { useAuth } from '../auth/AuthContext';
import { API_BASE_URL, SWAGGER_UI_URL } from '../config';
import kubendtLogo from '../assets/images/kubendt-logo.svg';
import { ReactComponent as BookIcon } from '../assets/images/icons/book.svg';
import { ReactComponent as BoxIcon } from '../assets/images/icons/box.svg';
import { ReactComponent as ClusterIcon } from '../assets/images/icons/cluster.svg';
import './AppHeader.css';

import { ReactComponent as GlobeIcon } from '../assets/images/icons/globe.svg';
import GithubMark from './GithubMark';

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

// The bar on top of the pages that are not a namespace's: the brand and the
// project's link on the left, the tool's pages and the account on the right.
const AppHeader = () => {
  const navigate = useNavigate();
  const auth = useAuth();
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
    <header className="app-header">
      <button type="button" className="app-header-brand" onClick={() => navigate('/')}>
        <span className="app-header-logo">
          <img src={kubendtLogo} alt="" />
        </span>
        <span className="app-header-name">
          <span className="app-header-title">
            KubeNDT
            {version?.version && (
              <span className="app-header-version" title={versionTitle || undefined}>
                {isDev ? 'dev' : `v${version.version}`}
              </span>
            )}
          </span>
          <span className="app-header-tagline">
            A Kubernetes-native platform for Network Digital Twins
          </span>
        </span>
      </button>
      <span className="app-header-links">
        <a
          className="app-header-github"
          href="https://github.com/emigcm98/kubendt"
          target="_blank"
          rel="noopener noreferrer"
          title="KubeNDT on GitHub"
        >
          <GithubMark />
          GitHub
        </a>
        <a
          className="app-header-github"
          href="https://kubendt.org"
          target="_blank"
          rel="noopener noreferrer"
          title="The project's site"
        >
          <GlobeIcon className="app-icon" aria-hidden="true" />
          kubendt.org
        </a>
      </span>
      <span className="app-header-space" />
      <nav className="app-header-nav">
        <button
          type="button"
          className="app-header-btn"
          onClick={() => navigate('/drivers')}
          title="Every driver and what its nodes can do"
        >
          <BoxIcon className="app-icon" aria-hidden="true" />
          Drivers
        </button>
        {auth.isAdmin && (
          <button
            type="button"
            className="app-header-btn"
            onClick={() => navigate('/state')}
            title="Every Kubernetes object KubeNDT keeps in this cluster"
          >
            <ClusterIcon className="app-icon" aria-hidden="true" />
            State
          </button>
        )}
        <button
          type="button"
          className="app-header-btn"
          onClick={() => window.open(SWAGGER_UI_URL, '_blank', 'noopener,noreferrer')}
          title="The REST API, in Swagger"
        >
          <BookIcon className="app-icon" aria-hidden="true" />
          API
        </button>
        {auth.enabled && <ProfileMenu />}
      </nav>
    </header>
  );
};

export default AppHeader;
