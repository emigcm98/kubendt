import React, { useState } from 'react';
import { MiniMap } from 'reactflow';
import { ReactComponent as MinusIcon } from '../assets/images/icons/minus.svg';
import { ReactComponent as MaximizeIcon } from '../assets/images/icons/maximize.svg';
import './MiniMapOverlay.css';

// Whether the minimap is hidden is a per-browser convenience, so it lives in
// localStorage; a browser that refuses storage just starts with it shown.
const STORAGE_KEY = 'kubendt.minimap';
const readHidden = () => {
  try {
    return localStorage.getItem(STORAGE_KEY) === 'hidden';
  } catch (e) {
    return false;
  }
};
const storeHidden = (hidden) => {
  try {
    localStorage.setItem(STORAGE_KEY, hidden ? 'hidden' : 'shown');
  } catch (e) {
    /* storage unavailable, the choice lasts the session */
  }
};

const MiniMapOverlay = ({ nodes, selectedNodeId }) => {
  const [hidden, setHidden] = useState(readHidden);
  const toggle = () => {
    setHidden((h) => {
      storeHidden(!h);
      return !h;
    });
  };

  const getColor = (node) => {
    const type = node.data?.type || node.type;
    switch (type) {
      case 'router':
        return '#3b82f6';
      case 'switch':
        return '#6b7280';
      case 'host':
        return '#10b981';
      case 'external':
        return '#8b5cf6';
      default:
        return '#facc15';
    }
  };

  const getClassName = (node) => {
    const type = node.data?.type || node.type;
    const base =
      type === 'router'
        ? 'minimap-node router'
        : type === 'switch'
          ? 'minimap-node switch'
          : type === 'external'
            ? 'minimap-node external'
            : 'minimap-node host';
    return node.id === selectedNodeId ? `${base} selected` : base;
  };

  if (hidden) {
    return (
      <button
        type="button"
        className="minimap-toggle minimap-toggle-collapsed"
        title="Show the minimap"
        aria-label="Show the minimap"
        onClick={toggle}
      >
        <MaximizeIcon className="app-icon" />
      </button>
    );
  }

  return (
    <div className="minimap-container">
      <button
        type="button"
        className="minimap-toggle"
        title="Hide the minimap"
        aria-label="Hide the minimap"
        onClick={toggle}
      >
        <MinusIcon className="app-icon" />
      </button>
      <MiniMap
        nodes={nodes}
        nodeColor={getColor}
        nodeStrokeColor="#111"
        nodeStrokeWidth={1.5}
        nodeClassName={getClassName}
        zoomable
        pannable
        style={{
          width: 300,
          height: 220,
          margin: 0,
        }}
      />
    </div>
  );
};

export default MiniMapOverlay;
