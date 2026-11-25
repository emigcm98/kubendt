import React from 'react';
import { useNavigate } from 'react-router-dom';
import ProfileMenu from './ProfileMenu';
import { useAuth } from '../auth/AuthContext';
import { SWAGGER_UI_URL } from '../config';
import { ReactComponent as BookIcon } from '../assets/images/icons/book.svg';
import { ReactComponent as BoxIcon } from '../assets/images/icons/box.svg';
import { ReactComponent as ClusterIcon } from '../assets/images/icons/cluster.svg';
import { ReactComponent as GlobeIcon } from '../assets/images/icons/globe.svg';
import BrandMark from './BrandMark';
import GithubMark from './GithubMark';
import './AppHeader.css';

// The bar on top of the pages that are not a namespace's. On the left the
// brand, then the project's links on the Home or the page's title elsewhere;
// on the right the page's own buttons, the tool's pages and the account.
// `current` marks the tool page one is on.
const AppHeader = ({ links = false, title = null, actions = null, current = '' }) => {
  const navigate = useNavigate();
  const auth = useAuth();
  return (
    <header className={`app-header${title ? ' app-header-titled' : ''}`}>
      <BrandMark />
      {links && (
        <span className="app-header-aside app-header-links">
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
      )}
      {title && (
        <span className="app-header-aside">
          <span className="app-header-page" title={title.hint || undefined}>
            {title.icon && <title.icon className="app-icon" aria-hidden="true" />}
            {title.label}
          </span>
        </span>
      )}
      <span className="app-header-space" />
      {actions && <span className="app-header-actions">{actions}</span>}
      <nav className="app-header-nav">
        <button
          type="button"
          className={`app-header-btn${current === 'drivers' ? ' app-header-btn-on' : ''}`}
          aria-current={current === 'drivers' ? 'page' : undefined}
          onClick={() => navigate('/drivers')}
          title="Every driver and what its nodes can do"
        >
          <BoxIcon className="app-icon" aria-hidden="true" />
          <span className="app-header-btn-label">Drivers</span>
        </button>
        {auth.isAdmin && (
          <button
            type="button"
            className={`app-header-btn${current === 'state' ? ' app-header-btn-on' : ''}`}
            aria-current={current === 'state' ? 'page' : undefined}
            onClick={() => navigate('/state')}
            title="Every Kubernetes object KubeNDT keeps in this cluster"
          >
            <ClusterIcon className="app-icon" aria-hidden="true" />
            <span className="app-header-btn-label">State</span>
          </button>
        )}
        <button
          type="button"
          className="app-header-btn"
          onClick={() => window.open(SWAGGER_UI_URL, '_blank', 'noopener,noreferrer')}
          title="The REST API, in Swagger"
        >
          <BookIcon className="app-icon" aria-hidden="true" />
          <span className="app-header-btn-label">API</span>
        </button>
        {auth.enabled && <ProfileMenu />}
      </nav>
    </header>
  );
};

export default AppHeader;
