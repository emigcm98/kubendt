// components/NamespaceFilesNavbar.js
import React from 'react';
import { useNavigate } from 'react-router-dom';
import BrandMark from './BrandMark';
import './NamespaceNavbar.css'; // reuse styles
import { ReactComponent as TopologyIcon } from '../assets/images/icons/topology.svg';
import ThemeToggle from './ThemeToggle';

function NamespaceFilesNavbar({ namespace }) {
  const navigate = useNavigate();

  return (
    <div className="navbar-container">
      <div className="navbar-left">
        <BrandMark />
      </div>

      <div className="navbar-center navbar-center-static" title={`Namespace: ${namespace}`}>
        <TopologyIcon className="navbar-ns-icon" aria-hidden="true" />
        <div className="navbar-center-text">
          <span className="navbar-namespace">{namespace}</span>
        </div>
      </div>

      <div className="navbar-right">
        <ThemeToggle className="navbar-button" />
        <button
          className="navbar-button"
          onClick={() => {
            sessionStorage.setItem(`kubendt.restoreCache.${namespace}`, 'true');
            navigate(`/${namespace}`);
          }}
          title="Back to the graph of this namespace"
        >
          <TopologyIcon className="app-icon" aria-hidden="true" />
          <span className="navbar-button-label">Graph</span>
        </button>
      </div>
    </div>
  );
}

export default NamespaceFilesNavbar;
