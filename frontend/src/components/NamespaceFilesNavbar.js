// components/NamespaceFilesNavbar.js
import React from 'react';
import { useNavigate } from 'react-router-dom';
import './NamespaceNavbar.css'; // reuse styles

function NamespaceFilesNavbar({ namespace }) {
  const navigate = useNavigate();

  return (
    <div className="navbar-container">
      <div className="navbar-left">
        <button className="navbar-button" onClick={() => navigate(`/`)}>
          ← Home
        </button>
        <button
          className="navbar-button"
          onClick={() => {
            sessionStorage.setItem(`kubendt.restoreCache.${namespace}`, 'true');
            navigate(`/${namespace}`);
          }}
        >
          ← Go back to Namespace
        </button>
      </div>

      <div className="navbar-center">
        <div className="navbar-label">Namespace</div>
        <div className="navbar-namespace">{namespace}</div>
      </div>

      <div className="navbar-right">
        <a
          className="navbar-brand"
          href="https://github.com/emigcm98/kubendt"
          target="_blank"
          rel="noopener noreferrer"
          title="View KubeNDT on GitHub"
        >
          KubeNDT
        </a>
      </div>
    </div>
  );
}

export default NamespaceFilesNavbar;
