import { render, screen } from '@testing-library/react';
import {
  HealthPill,
  MeshnetFlavour,
  MeshnetLink,
  NodeRole,
  NoLabs,
  clusterHealth,
  meshnetFlavour,
  meshnetName,
  nodeKind,
} from './K8sBits';

const fork = {
  state: 'ok',
  flavour: 'fork',
  image: 'ghcr.io/emigcm98/meshnet:v1.0.0',
  version: '1.0.0',
  support: 'supported',
  url: 'https://github.com/emigcm98/meshnet-cni/tree/v1.0.0',
  images: [{ image: 'ghcr.io/emigcm98/meshnet:v1.0.0', nodes: 3 }],
};

describe('meshnetFlavour', () => {
  test('nothing without a flavour, as an older backend answers', () => {
    expect(meshnetFlavour({ state: 'ok', ready: 3, desired: 3 })).toBeNull();
    expect(meshnetFlavour(undefined)).toBeNull();
  });

  test('a release of the fork, by its version', () => {
    const f = meshnetFlavour(fork);
    expect(f.text).toBe('KubeNDT Meshnet 1.0.0');
    expect(f.warn).toBe(false);
    expect(f.title).toContain('ghcr.io/emigcm98/meshnet:v1.0.0');
    expect(f.title).toContain('Supported');
  });

  test('a development build, by its commit, with no alarm', () => {
    const f = meshnetFlavour({
      ...fork,
      version: undefined,
      commit: '30ab7f0',
      support: 'development',
    });
    expect(f.text).toBe('KubeNDT Meshnet · dev 30ab7f0');
    expect(f.warn).toBe(false);
    expect(f.notes).toEqual([]);
  });

  test('stock and an unknown one by their name alone', () => {
    expect(meshnetFlavour({ flavour: 'stock', support: 'supported' }).text).toBe(
      'Meshnet (networkop)'
    );
    const unknown = meshnetFlavour({ flavour: 'not recognised', support: 'unsupported' });
    expect(unknown.text).toBe('Meshnet (not recognised)');
    expect(unknown.url).toBeNull();
    expect(unknown.warn).toBe(true);
  });

  test('a version out of range and a mismatch are notes', () => {
    const old = meshnetFlavour({ ...fork, version: '0.9.0', support: 'too-old' });
    expect(old.notes).toEqual(['KubeNDT Meshnet 0.9.0: Older than this KubeNDT works with.']);
    const upgrade = meshnetFlavour({
      ...fork,
      images: [
        { image: 'a', nodes: 2 },
        { image: 'b', nodes: 1 },
      ],
      warnings: ['The nodes run different Meshnet images, as in the middle of an upgrade.'],
    });
    expect(upgrade.warn).toBe(true);
    expect(upgrade.title).toContain('a on 2 nodes');
    expect(upgrade.title).toContain('b on 1 node');
  });
});

describe('meshnetName', () => {
  test('the fork is KubeNDT Meshnet, by release or by commit', () => {
    expect(meshnetName({ flavour: 'fork', version: '1.2.3' })).toBe('KubeNDT Meshnet 1.2.3');
    expect(meshnetName({ flavour: 'fork', version: '1.0.0-rc.1' })).toBe(
      'KubeNDT Meshnet 1.0.0-rc.1'
    );
    expect(meshnetName({ flavour: 'fork', commit: 'abc1234' })).toBe(
      'KubeNDT Meshnet · dev abc1234'
    );
    expect(meshnetName({ flavour: 'fork' })).toBe('KubeNDT Meshnet · dev');
  });
});

describe('the Meshnet link and the node role', () => {
  test('a known Meshnet links to its source in a new tab', () => {
    render(<MeshnetFlavour meshnet={fork} />);
    expect(screen.getByText('CNI')).toBeInTheDocument();
    const link = screen.getByRole('link', { name: 'KubeNDT Meshnet 1.0.0' });
    expect(link).toHaveAttribute('href', fork.url);
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
  });

  test('an unknown one is text, not a link', () => {
    render(<MeshnetFlavour meshnet={{ flavour: 'not recognised', support: 'unsupported' }} />);
    expect(screen.queryByRole('link')).toBeNull();
    expect(screen.getByText('Meshnet (not recognised)')).toBeInTheDocument();
  });

  test('the control plane reads apart from a worker, and a node without labs says so', () => {
    const { rerender } = render(
      <>
        <NodeRole node={{ roles: ['control-plane'] }} />
        <NoLabs node={{ schedulable: false }} />
      </>
    );
    expect(screen.getByText('control plane')).toHaveClass('k8s-role-plane');
    expect(screen.getByText('no labs')).toBeInTheDocument();
    rerender(
      <>
        <NodeRole node={{ roles: ['worker'] }} />
        <NoLabs node={{ schedulable: true }} />
      </>
    );
    expect(screen.getByText('worker')).not.toHaveClass('k8s-role-plane');
    expect(screen.queryByText('no labs')).toBeNull();
    // An older backend sends no field: nothing is claimed.
    rerender(<NoLabs node={{}} />);
    expect(screen.queryByText('no labs')).toBeNull();
  });
});

describe('the pod of a node', () => {
  test('the link of a pod on an outdated image reads as a warning', () => {
    render(
      <MeshnetLink
        text={meshnetName({ flavour: 'fork', version: '1.0.0' })}
        url="https://example.org/x"
        warn
      />
    );
    expect(screen.getByRole('link')).toHaveClass('k8s-flavour-warn');
  });
});

const node = (name, status, meshnet, roles = ['worker']) => ({ name, status, meshnet, roles });
const cluster = (state, nodes, extra = {}) => ({ nodes, meshnet: { state, ...extra } });

describe('clusterHealth', () => {
  const ready = [
    node('cp', 'Ready', 'running', ['control-plane']),
    node('w1', 'Ready', 'running'),
    node('w2', 'Ready', 'running'),
  ];

  test('nothing without nodes', () => {
    expect(clusterHealth(undefined)).toBeNull();
    expect(clusterHealth({ nodes: [] })).toBeNull();
  });

  test('every node Ready with its Meshnet is one green count', () => {
    const h = clusterHealth(cluster('ok', ready));
    expect(h).toMatchObject({ cls: 'ok', text: '3/3 working', problems: [] });
  });

  test('a node without a ready pod is red and named', () => {
    const nodes = [ready[0], ready[1], node('w2', 'Ready', 'not-running')];
    const h = clusterHealth(cluster('degraded', nodes));
    expect(h.cls).toBe('bad');
    expect(h.text).toBe('2/3 working');
    expect(h.problems).toEqual(['No ready Meshnet pod on w2. Links landing there are not wired.']);
  });

  test('a node the DaemonSet does not target is no failure while it is fully ready', () => {
    const nodes = [node('cp', 'Ready', 'not-running', ['control-plane']), ready[1], ready[2]];
    expect(clusterHealth(cluster('ok', nodes))).toMatchObject({ cls: 'ok', text: '3/3 working' });
  });

  test('a node that is not Ready is a warning', () => {
    const nodes = [ready[0], ready[1], node('w2', 'NotReady', 'running')];
    const h = clusterHealth(cluster('ok', nodes));
    expect(h).toMatchObject({ cls: 'warn', text: '2/3 working' });
    expect(h.problems).toEqual(['w2 is NotReady.']);
  });

  test('no Meshnet at all is red and said in words', () => {
    const nodes = ready.map((n) => ({ ...n, meshnet: 'not-running' }));
    const h = clusterHealth(cluster('missing', nodes));
    expect(h).toMatchObject({ cls: 'bad', text: 'No Meshnet' });
    expect(h.problems[0]).toMatch(/^No Meshnet found/);
  });

  test('a Meshnet that could not be checked counts the Ready nodes and says so', () => {
    const nodes = ready.map((n) => ({ ...n, meshnet: 'unknown' }));
    const h = clusterHealth(cluster('unknown', nodes, { message: 'no permission' }));
    expect(h).toMatchObject({ cls: 'warn', text: '3/3 ready', problems: [] });
    expect(h.title).toContain('no permission');
  });

  test('the pill carries the count and the hover', () => {
    render(<HealthPill status={cluster('ok', ready)} />);
    const pill = screen.getByText('3/3 working');
    expect(pill).toHaveClass('k8s-pill-ok');
    expect(pill).toHaveAttribute('title', 'Every node is Ready and runs Meshnet.');
  });
});

describe('nodeKind', () => {
  test('a control plane or a master is the plane, anything else a worker', () => {
    expect(nodeKind({ roles: ['control-plane'] })).toBe('plane');
    expect(nodeKind({ roles: ['control-plane', 'master'] })).toBe('plane');
    expect(nodeKind({ roles: ['master'] })).toBe('plane');
    expect(nodeKind({ roles: ['worker'] })).toBe('worker');
    expect(nodeKind({})).toBe('worker');
  });
});
