import React from 'react';
import GraphMenu, { DIVIDER } from './GraphMenu';
import { ReactComponent as TraceIcon } from '../assets/images/icons/trace-icon.svg';
import { ReactComponent as SlidersIcon } from '../assets/images/icons/sliders.svg';
import { ReactComponent as TerminalIcon } from '../assets/images/icons/terminal.svg';
import { ReactComponent as WrenchIcon } from '../assets/images/icons/wrench.svg';
import { ReactComponent as GaugeIcon } from '../assets/images/icons/gauge.svg';
import { ReactComponent as LogsIcon } from '../assets/images/icons/logs.svg';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as InfoIcon } from '../assets/images/icons/info.svg';

// Right-click menu of a node. The shells and the tools are groups, so the
// menu stays at six rows however many a node supports.
const ContextMenu = ({
  x,
  y,
  node,
  onClose,
  onOpenShell,
  onOpenLogs,
  onRestartPod,
  onOpenInfoPanel,
  onStartTrace,
  onStartIperf,
  onStartTC,
  onDelete,
}) => {
  if (!node) return null;
  const info = node.fullInfo || {};
  const isPod = node.type !== 'external';
  const hasSerial = Boolean(
    info.runtime === 'qemu' || info.serialShell || info.shellMode === 'serial'
  );
  const isFRR = info.driver === 'FRRRouterDriver';

  const items = [
    onOpenInfoPanel && { label: 'Open info panel', icon: <InfoIcon />, onClick: onOpenInfoPanel },
    isPod && onOpenLogs && { label: 'View logs', icon: <LogsIcon />, onClick: onOpenLogs },
    DIVIDER,
    isPod &&
      onOpenShell && {
        label: 'Shell',
        icon: <TerminalIcon />,
        children: [
          { label: 'sh', alone: 'Open pod shell (sh)', onClick: () => onOpenShell('sh') },
          hasSerial && {
            label: 'serial (attach)',
            alone: 'Open serial shell (attach)',
            onClick: () => onOpenShell('serial'),
          },
          isFRR && {
            label: 'vtysh',
            alone: 'Open vtysh console',
            onClick: () => onOpenShell('vtysh'),
          },
        ],
      },
    isPod && {
      label: 'Tools',
      icon: <WrenchIcon />,
      children: [
        onStartTrace &&
          info.l3capable && {
            label: 'Traceroute from here',
            icon: <TraceIcon />,
            onClick: onStartTrace,
          },
        // A guest VM has no iperf3 reachable from the pod yet; see PLAN, tool runner.
        onStartIperf &&
          info.l3capable && {
            label: 'Throughput test from here',
            icon: <GaugeIcon />,
            onClick: onStartIperf,
            disabled: info.runtime === 'qemu',
            note: `${info.driver || 'This driver'} does not support it on a guest VM yet`,
          },
        onStartTC && { label: 'Traffic control', icon: <SlidersIcon />, onClick: onStartTC },
      ],
    },
    DIVIDER,
    isPod && onRestartPod && { label: 'Restart pod', icon: <RefreshIcon />, onClick: onRestartPod },
    onDelete && {
      label: isPod ? 'Delete pod' : 'Delete external network',
      icon: <TrashIcon />,
      onClick: onDelete,
      danger: true,
    },
  ];

  return <GraphMenu x={x} y={y} title={node.label} items={items} onClose={onClose} />;
};

export default ContextMenu;
