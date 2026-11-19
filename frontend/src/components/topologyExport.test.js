import { topologyFile } from './topologyExport';

const data = {
  nodes: [{ name: 'r1' }],
  links: [
    { node: 'r1', localIntf: 'eth1', peerNode: 'sw', peerIntf: 'eth1', uid: 7 },
    { node: 'r1', localIntf: 'eth2', peerNode: 'external', peerIntf: 'eth0', uid: 8 },
  ],
  layout: { positions: { 'r1-0': { x: 1, y: 2 } }, annotations: [] },
  placement: 'pack',
};

test('the file leaves the link uids out and keeps everything else', () => {
  const file = topologyFile(data);
  expect(file.links.map((l) => l.uid)).toEqual([undefined, undefined]);
  expect(file.links[0].peerNode).toBe('sw');
  expect(file.layout).toEqual(data.layout);
  expect(file.placement).toBe('pack');
  expect(data.links[0].uid).toBe(7);
});

test('the uids stay when asked', () => {
  expect(topologyFile(data, { keepUids: true })).toBe(data);
});

test('a response without links is returned as it is', () => {
  expect(topologyFile({ nodes: [] })).toEqual({ nodes: [] });
});
