import React from 'react';
import './LoadingOverlay.css';

// Centered loading badge over the graph canvas. Variants:
// import (yellow), config (blue), modify (green), clear (red).
const LoadingOverlay = ({ variant = 'info', icon, message, withBackdrop = true }) => (
  <>
    {withBackdrop && <div className="loading-overlay-backdrop" />}
    <div className={`loading-overlay loading-overlay-${variant}`}>
      {icon && <span className="loading-overlay-icon">{icon}</span>}
      <span className="loading-overlay-message">{message}</span>
    </div>
  </>
);

export default LoadingOverlay;
