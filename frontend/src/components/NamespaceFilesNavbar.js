// components/NamespaceFilesNavbar.js
import React from 'react';
import { useNavigate } from 'react-router-dom';
import './NamespaceNavbar.css'; // reuse styles
import { ReactComponent as TopologyIcon } from '../assets/images/icons/topology.svg';
import { ReactComponent as HomeIcon } from '../assets/images/icons/home.svg';
import kubendtLogo from '../assets/images/kubendt-logo.svg';

function NamespaceFilesNavbar({ namespace }) {
  const navigate = useNavigate();

  return (
    <div className="navbar-container">
      <div className="navbar-left">
        <button
          type="button"
          className="navbar-brand"
          onClick={() => navigate('/')}
          title="KubeNDT"
        >
          <img src={kubendtLogo} alt="KubeNDT" className="navbar-brand-logo" />
        </button>
        <button
          className="navbar-button"
          onClick={() => navigate('/')}
          title="Back to the namespace list"
        >
          <HomeIcon className="app-icon" aria-hidden="true" />
          Home
        </button>
      </div>

      <div className="navbar-center navbar-center-static" title={`Namespace: ${namespace}`}>
        <TopologyIcon className="navbar-ns-icon" aria-hidden="true" />
        <div className="navbar-center-text">
          <span className="navbar-namespace">{namespace}</span>
        </div>
      </div>

      <div className="navbar-right">
        <button
          className="navbar-button"
          onClick={() => {
            sessionStorage.setItem(`kubendt.restoreCache.${namespace}`, 'true');
            navigate(`/${namespace}`);
          }}
          title="Back to the graph of this namespace"
        >
          <TopologyIcon className="app-icon" aria-hidden="true" />
          Graph
        </button>
      </div>
    </div>
  );
}

export default NamespaceFilesNavbar;
