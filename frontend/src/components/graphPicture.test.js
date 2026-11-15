import { paintGraph } from './graphPicture';

// jsdom has no canvas: the painter falls back to its own text estimate.
beforeAll(() => {
  HTMLCanvasElement.prototype.getContext = () => null;
});

const icons = {
  host: '<svg viewBox="0 0 10 10"><rect width="10" height="10"/></svg>',
  switch: '<svg viewBox="0 0 10 10"><circle r="5"/></svg>',
};

const nodes = [
  {
    id: 'h-0',
    type: 'custom',
    position: { x: 100, y: 100 },
    data: { type: 'host', label: 'h-0', status: 'Running' },
  },
  {
    id: 'sw-0',
    type: 'custom',
    position: { x: 300, y: 100 },
    data: { type: 'switch', label: 'sw-0', status: 'Running' },
  },
  {
    id: 'ann:a',
    type: 'annotation',
    position: { x: 0, y: 0 },
    style: { width: 500, height: 300 },
    data: { kind: 'rect', text: 'Site', color: 'blue', z: 0 },
  },
  {
    id: 'ann:n',
    type: 'annotation',
    position: { x: 10, y: 400 },
    style: { width: 200, height: 40 },
    data: { kind: 'note', text: 'a note' },
  },
  {
    id: 'gone',
    type: 'custom',
    hidden: true,
    position: { x: 2000, y: 2000 },
    data: { type: 'host', label: 'gone' },
  },
];
const edges = [
  {
    id: 'e',
    source: 'h-0',
    target: 'sw-0',
    data: { localIntf: 'eth1', peerIntf: 'eth2', linkName: 'N3' },
  },
];

test('the picture frames nodes, labels and annotations with the margin', () => {
  const p = paintGraph({
    nodes,
    edges,
    margin: 20,
    icons,
    interfaceStatus: (pod, iface) => iface === 'eth1',
  });
  // the rectangle spans 0..500 sideways plus a pixel for its stroke on each
  // side, the note at y=400 reaches lowest
  expect(p.width).toBe(542);
  expect(p.height).toBeGreaterThan(400 + 40);
  expect(p.svg).toContain('<line');
  expect(p.svg).toContain('>eth1<');
  expect(p.svg).toContain('>eth2<');
  expect(p.svg).toContain('>N3<');
  expect(p.svg).toContain('>Site<');
  expect(p.svg).toContain('>a note<');
  expect(p.svg).toContain('>h-0<');
  expect(p.svg).not.toContain('>gone<');
  expect(p.svg).toContain('<rect width="100%" height="100%" fill="#ffffff"/>');
  // the icons go inline as vectors
  expect(p.svg).toContain('<circle r="5"/>');
});

test('a transparent picture has no background and nothing paints nothing', () => {
  const t = paintGraph({ nodes, edges, margin: 20, icons, transparent: true });
  expect(t.svg).not.toContain('width="100%"');
  expect(paintGraph({ nodes: [], edges: [] })).toBeNull();
});

test('a hidden node leaves the frame', () => {
  const only = paintGraph({ nodes: nodes.slice(0, 2), edges, margin: 0, icons });
  const withGone = paintGraph({ nodes: [...nodes.slice(0, 2), nodes[4]], edges, margin: 0, icons });
  expect(withGone.width).toBe(only.width);
});
