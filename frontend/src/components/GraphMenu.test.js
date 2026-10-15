import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import ContextMenu from './ContextMenu';
import EdgeContextMenu from './EdgeContextMenu';

const noop = () => {};

const nodeMenu = (fullInfo, extra = {}) =>
  render(
    <ContextMenu
      x={10}
      y={10}
      node={{ label: 'router1-0', type: 'router', fullInfo }}
      onClose={noop}
      onOpenInfoPanel={noop}
      onOpenShell={noop}
      onOpenLogs={noop}
      onRestartPod={noop}
      onStartTrace={noop}
      onStartTC={noop}
      onDelete={noop}
      {...extra}
    />
  );

const labels = () =>
  Array.from(document.querySelectorAll('.graph-context-menu-item')).map((el) =>
    el.textContent.trim()
  );

test('an FRR router groups its shells and tools', () => {
  nodeMenu({ driver: 'FRRRouterDriver', l3capable: true });
  expect(labels()).toEqual([
    'Open info panel',
    'View logs',
    'Shell',
    'Tools',
    'Restart pod',
    'Delete pod',
  ]);
  fireEvent.mouseEnter(screen.getByText('Shell'));
  expect(labels()).toContain('vtysh');
  expect(labels()).toContain('sh');
  expect(labels()).not.toContain('serial (attach)');
  fireEvent.mouseEnter(screen.getByText('Tools'));
  expect(labels()).toContain('Traceroute from here');
  expect(labels()).toContain('Traffic control');
  expect(labels()).not.toContain('vtysh');
});

test('a group with one entry collapses into it', () => {
  nodeMenu({ driver: 'HostDriver', l3capable: false });
  expect(labels()).toEqual([
    'Open info panel',
    'View logs',
    'Open pod shell (sh)',
    'Traffic control',
    'Restart pod',
    'Delete pod',
  ]);
});

test('a submenu entry runs its action and closes the menu', () => {
  const onOpenShell = jest.fn();
  const onClose = jest.fn();
  nodeMenu({ driver: 'FRRRouterDriver', l3capable: true }, { onOpenShell, onClose });
  fireEvent.mouseEnter(screen.getByText('Shell'));
  fireEvent.click(screen.getByText('vtysh'));
  expect(onOpenShell).toHaveBeenCalledWith('vtysh');
  expect(onClose).toHaveBeenCalled();
});

test('an external node keeps only the panel and the delete entries', () => {
  render(
    <ContextMenu
      x={0}
      y={0}
      node={{ label: 'external', type: 'external', fullInfo: {} }}
      onClose={noop}
      onOpenInfoPanel={noop}
      onOpenShell={noop}
      onOpenLogs={noop}
      onRestartPod={noop}
      onStartTC={noop}
      onDelete={noop}
    />
  );
  expect(labels()).toEqual(['Open info panel', 'Delete external network']);
});

test('a link groups capture and traffic control per end', () => {
  render(
    <EdgeContextMenu
      x={0}
      y={0}
      edge={{
        source: 'router1-0',
        target: 'host-0',
        data: { localIntf: 'eth1', peerIntf: 'eth1' },
      }}
      sourceLabel="router1-0"
      targetLabel="host-0"
      onClose={noop}
      onOpenInfoPanel={noop}
      onDelete={noop}
      onCaptureSource={noop}
      onCaptureTarget={noop}
      onTCSource={noop}
      onTCTarget={noop}
      onTCLink={noop}
    />
  );
  expect(labels()).toEqual(['Open info panel', 'Capture', 'Traffic control', 'Delete link']);
  fireEvent.mouseEnter(screen.getByText('Traffic control'));
  expect(labels()).toContain('on router1-0:eth1');
  expect(labels()).toContain('on host-0:eth1');
  expect(labels()).toContain('on both ends');
});

test('a link to the external network collapses capture into its one end', () => {
  render(
    <EdgeContextMenu
      x={0}
      y={0}
      edge={{ source: 'router1-0', target: 'external', data: { localIntf: 'eth2', peerIntf: '?' } }}
      sourceLabel="router1-0"
      targetLabel="external"
      onClose={noop}
      onOpenInfoPanel={noop}
      onCaptureSource={noop}
      onTCSource={noop}
    />
  );
  expect(labels()).toEqual([
    'Open info panel',
    'Capture on router1-0:eth2',
    'Traffic control on router1-0:eth2',
  ]);
});
