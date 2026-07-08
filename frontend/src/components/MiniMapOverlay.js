import React from 'react';
import { MiniMap } from 'reactflow';
import './MiniMapOverlay.css';

const MiniMapOverlay = ({ nodes, selectedNodeId }) => {
  const getColor = (node) => {
    const type = node.data?.type || node.type;
    switch (type) {
      case 'router':
        return '#3b82f6';
      case 'switch':
        return '#6b7280';
      case 'host':
        return '#10b981';
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
          : 'minimap-node host';
    return node.id === selectedNodeId ? `${base} selected` : base;
  };

  return (
    <div className="minimap-container">
      <MiniMap
        nodes={nodes}
        nodeColor={getColor}
        nodeStrokeColor="#111"
        nodeStrokeWidth={1.5}
        nodeClassName={getClassName}
        zoomable
        pannable
        style={{
          width: 280,
          height: 200,
        }}
      />
    </div>
  );
};

export default MiniMapOverlay;
