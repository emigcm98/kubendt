import React from 'react';
import GraphMenu, { DIVIDER } from './GraphMenu';
import { ReactComponent as PcapIcon } from '../assets/images/icons/pcap.svg';
import { ReactComponent as InfoIcon } from '../assets/images/icons/info.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as SlidersIcon } from '../assets/images/icons/sliders.svg';
import { ReactComponent as GaugeIcon } from '../assets/images/icons/gauge.svg';

// Right-click menu of a link. Capture, traffic control and the throughput
// test are groups with one entry per end, so the menu stays at six rows.
const EdgeContextMenu = ({
  x,
  y,
  edge,
  sourceLabel,
  targetLabel,
  onClose,
  onOpenInfoPanel,
  onDelete,
  onCaptureSource,
  onCaptureTarget,
  onTCSource,
  onTCTarget,
  onTCLink,
  onIperfSourceToTarget,
  onIperfTargetToSource,
}) => {
  if (!edge) return null;
  const a = `${sourceLabel || edge.source}:${edge.data?.localIntf || '?'}`;
  const b = `${targetLabel || edge.target}:${edge.data?.peerIntf || '?'}`;
  const na = sourceLabel || edge.source;
  const nb = targetLabel || edge.target;

  const items = [
    onOpenInfoPanel && { label: 'Open info panel', icon: <InfoIcon />, onClick: onOpenInfoPanel },
    DIVIDER,
    {
      label: 'Capture',
      icon: <PcapIcon />,
      wideIcon: true,
      children: [
        onCaptureSource && { label: `on ${a}`, alone: `Capture on ${a}`, onClick: onCaptureSource },
        onCaptureTarget && { label: `on ${b}`, alone: `Capture on ${b}`, onClick: onCaptureTarget },
      ],
    },
    {
      label: 'Traffic control',
      icon: <SlidersIcon />,
      children: [
        onTCSource && { label: `on ${a}`, alone: `Traffic control on ${a}`, onClick: onTCSource },
        onTCTarget && { label: `on ${b}`, alone: `Traffic control on ${b}`, onClick: onTCTarget },
        onTCLink && {
          label: 'on both ends',
          alone: 'Traffic control on both ends',
          onClick: onTCLink,
        },
      ],
    },
    {
      label: 'Throughput test',
      icon: <GaugeIcon />,
      children: [
        onIperfSourceToTarget && {
          label: `${na} to ${nb}`,
          alone: `Throughput test ${na} to ${nb}`,
          onClick: onIperfSourceToTarget,
        },
        onIperfTargetToSource && {
          label: `${nb} to ${na}`,
          alone: `Throughput test ${nb} to ${na}`,
          onClick: onIperfTargetToSource,
        },
      ],
    },
    DIVIDER,
    onDelete && { label: 'Delete link', icon: <TrashIcon />, onClick: onDelete, danger: true },
  ];

  return <GraphMenu x={x} y={y} title={`${a} ↔ ${b}`} items={items} onClose={onClose} />;
};

export default EdgeContextMenu;
