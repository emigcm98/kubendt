import React from 'react';
import { Handle } from 'reactflow';
import pcIcon from '../assets/images/nodes/host.svg';
import routerIcon from '../assets/images/nodes/router.svg';
import switchIcon from '../assets/images/nodes/switch.svg';
import externalIcon from '../assets/images/nodes/switch_gray.svg';
import internetIcon from '../assets/images/nodes/internet.svg';
import './CustomNode.css';

export const NODE_SIZE = 64;

const CustomNode = ({ data }) => {
  const imageSrc =
    data.type === 'router'
      ? routerIcon
      : data.type === 'switch'
        ? switchIcon
        : data.type === 'external'
          ? externalIcon
          : pcIcon;

  return (
    <div
      className={`custom-node${data.selected ? ' node-selected' : ''}${data.deleting ? ' node-deleting' : ''}${data.creating ? ' node-creating' : ''}${data.restarting ? ' node-restarting' : ''}${data.restartSuccess ? ' node-restart-success' : ''}`}
      style={{ width: NODE_SIZE, height: NODE_SIZE, position: 'relative' }}
    >
      <div
        className="label"
        style={{
          fontSize: NODE_SIZE * 0.13,
          padding: `${NODE_SIZE * 0.03}px ${NODE_SIZE * 0.06}px`,
          transform: `translate(-50%, ${data.type === 'host' ? '150%' : '100%'})`,
        }}
      >
        {data.label}
      </div>

      <img src={imageSrc} alt={data.type} className="node-icon" />
      {data.type !== 'external' && (
        <div
          className={`status-dot ${
            data.status === 'Running'
              ? 'status-running'
              : data.status === 'Pending'
                ? 'status-pending'
                : data.status === 'running-not-ready' || data.status === 'container-not-ready'
                  ? 'status-notready'
                  : 'status-error'
          }`}
          title={data.status || 'unknown'}
          style={{
            width: `${NODE_SIZE / 6}px`,
            height: `${NODE_SIZE / 6}px`,
            bottom: `${data.type === 'switch' ? NODE_SIZE / 5 : NODE_SIZE / 7}px`,
            right: `${data.type === 'switch' ? NODE_SIZE / 8 : NODE_SIZE / 10}px`,
          }}
        />
      )}

      {/* Icono de internet si aplica */}
      {data.internet && (
        <img
          src={internetIcon}
          alt="internet"
          title={`Internet gateway: NAT in ${data.internet}`}
          style={{
            position: 'absolute',
            width: `${NODE_SIZE / 3}px`,
            height: `${NODE_SIZE / 3}px`,
            top: '0px',
            right: '0px',
            zIndex: 50,
          }}
        />
      )}

      {/* Restart spinner */}
      {data.restarting && (
        <div
          className="restart-spinner"
          title="Restarting..."
          style={{
            width: `${NODE_SIZE / 6}px`,
            height: `${NODE_SIZE / 6}px`,
            top: `${-NODE_SIZE / 18}px`,
            right: `${-NODE_SIZE / 18}px`,
          }}
        />
      )}

      {/* Invisible center handles */}
      <Handle
        id="center"
        type="source"
        style={{
          top: NODE_SIZE / 2 - 2,
          left: NODE_SIZE / 2 - 2,
          width: 4,
          height: 4,
          background: 'transparent',
          border: 'none',
          position: 'absolute',
          pointerEvents: 'none',
        }}
      />
      <Handle
        id="center"
        type="target"
        style={{
          top: NODE_SIZE / 2 - 2,
          left: NODE_SIZE / 2 - 2,
          width: 4,
          height: 4,
          background: 'transparent',
          border: 'none',
          position: 'absolute',
          pointerEvents: 'none',
        }}
      />
    </div>
  );
};

export default CustomNode;
