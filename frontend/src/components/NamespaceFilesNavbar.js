// components/NamespaceFilesNavbar.js
import React from 'react';
import { useNavigate } from 'react-router-dom';
import './NamespaceNavbar.css'; // reuse styles
import { ReactComponent as HomeIcon } from '../assets/images/icons/home.svg';
import { ReactComponent as TopologyIcon } from '../assets/images/icons/topology.svg';
import kubendtLogo from '../assets/images/kubendt-logo.svg';

function NamespaceFilesNavbar({ namespace }) {
  const navigate = useNavigate();

  return (
    <div className="navbar-container">
      <div className="navbar-left">
        <button className="navbar-button" onClick={() => navigate(`/`)}>
          <HomeIcon className="app-icon" aria-hidden="true" />
          Home
        </button>
        <button
          className="navbar-button navbar-button-graph"
          onClick={() => {
            sessionStorage.setItem(`kubendt.restoreCache.${namespace}`, 'true');
            navigate(`/${namespace}`);
          }}
        >
          <TopologyIcon className="app-icon" aria-hidden="true" />
          Go to graph
        </button>
      </div>

      <div className="navbar-center navbar-center-static" title={`Namespace: ${namespace}`}>
        <TopologyIcon className="navbar-ns-icon" aria-hidden="true" />
        <div className="navbar-center-text">
          <span className="navbar-label">Namespace</span>
          <span className="navbar-namespace">{namespace}</span>
        </div>
      </div>

      <div className="navbar-right">
        <a
          className="navbar-brand"
          href="https://github.com/emigcm98/kubendt"
          target="_blank"
          rel="noopener noreferrer"
          title="View KubeNDT on GitHub"
        >
          <img src={kubendtLogo} alt="KubeNDT" className="navbar-brand-logo" />
        </a>
      </div>
    </div>
  );
}

export default NamespaceFilesNavbar;
