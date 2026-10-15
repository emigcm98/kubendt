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
// menu stays at six rows however many a node supports. What a node offers
// comes from the pod listing, the menu knows no driver.
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
  // The shells a node offers come from its driver through the pod listing.
  const shells =
    info.shells && info.shells.length ? info.shells : [{ mode: 'sh', label: 'pod shell (sh)' }];
  // The tools a node offers come from its driver through the pod listing; a
  // missing one shows dimmed with the reason, not hidden.
  const tools = info.tools || [];
  const toolEntry = (tool, entry) => ({
    ...entry,
    disabled: !tools.includes(tool),
    note: `${info.driver || 'This driver'} does not provide it on this node`,
  });

  const items = [
    onOpenInfoPanel && { label: 'Open info panel', icon: <InfoIcon />, onClick: onOpenInfoPanel },
    isPod && onOpenLogs && { label: 'View logs', icon: <LogsIcon />, onClick: onOpenLogs },
    DIVIDER,
    isPod &&
      onOpenShell && {
        label: 'Shell',
        icon: <TerminalIcon />,
        children: shells.map((sh) => ({
          label: sh.label,
          alone: `Open ${sh.label}`,
          onClick: () => onOpenShell(sh.mode),
        })),
      },
    isPod && {
      label: 'Tools',
      icon: <WrenchIcon />,
      children: [
        onStartTrace &&
          info.l3capable &&
          toolEntry('trace', {
            label: 'Traceroute from here',
            icon: <TraceIcon />,
            onClick: onStartTrace,
          }),
        onStartIperf &&
          info.l3capable &&
          toolEntry('iperf', {
            label: 'Throughput test from here',
            icon: <GaugeIcon />,
            onClick: onStartIperf,
          }),
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
