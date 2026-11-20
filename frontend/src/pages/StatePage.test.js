import { render, screen, fireEvent } from '@testing-library/react';
import StatePage, { formatAge, formatSize } from './StatePage';

// CRA's jest cannot resolve react-router 7's package exports, so the hooks
// are stubbed: the route param and the navigation the page asks for.
const mockNavigate = jest.fn();
let mockParams = {};
jest.mock('react-router-dom', () => ({
  useNavigate: () => mockNavigate,
  useParams: () => mockParams,
}));

const cluster = {
  namespaces: [
    {
      name: 'lab',
      role: 'lab',
      objects: [
        {
          kind: 'Namespace',
          name: 'lab',
          role: 'namespace',
          detail: 'lab',
          size: 400,
          resource_version: '1',
        },
        {
          kind: 'StatefulSet',
          name: 'r1',
          namespace: 'lab',
          role: 'node',
          node: 'r1',
          detail: 'router FRRRouterDriver, 1 replica',
          size: 2100,
          created_at: '2026-11-20T07:00:00Z',
          resource_version: '2',
          labels: { 'kubendt/type': 'router' },
        },
        {
          kind: 'ConfigMap',
          name: 'kubendt-internal-history-r1-0',
          namespace: 'lab',
          role: 'history',
          node: 'r1',
          detail: 'r1-0, 2 operations',
          size: 900,
          resource_version: '3',
        },
      ],
    },
    {
      name: 'kubendt-system',
      role: 'system',
      objects: [
        {
          kind: 'Namespace',
          name: 'kubendt-system',
          role: 'namespace',
          detail: "the platform's own",
          size: 300,
          resource_version: '4',
        },
        {
          kind: 'Secret',
          name: 'kubendt-user-admin',
          namespace: 'kubendt-system',
          role: 'user',
          detail: 'admin, password hash',
          size: 500,
          resource_version: '5',
        },
      ],
    },
  ],
};

const objectDoc = {
  namespace: 'lab',
  kind: 'statefulset',
  name: 'r1',
  object: { kind: 'StatefulSet' },
  yaml: 'apiVersion: apps/v1\nkind: StatefulSet\nmetadata:\n  name: r1\n',
};

describe('StatePage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
    jest.spyOn(global, 'fetch').mockImplementation(async (url) => {
      if (String(url).endsWith('/state')) return { ok: true, json: async () => cluster };
      if (String(url).endsWith('/state/lab')) {
        return {
          ok: true,
          json: async () => ({
            namespace: 'lab',
            role: 'lab',
            objects: cluster.namespaces[0].objects,
          }),
        };
      }
      return { ok: true, json: async () => objectDoc };
    });
  });

  afterEach(() => {
    global.fetch.mockRestore();
  });

  test('the cluster view lists every namespace and what each object is', async () => {
    mockParams = {};
    render(<StatePage />);
    expect(await screen.findByText('kubendt-internal-history-r1-0')).toBeInTheDocument();
    expect(screen.getByText('5 objects')).toBeInTheDocument();
    expect(screen.getByText('router FRRRouterDriver, 1 replica')).toBeInTheDocument();
    expect(screen.getAllByText('History').length).toBeGreaterThan(0);
    expect(screen.getByText('system')).toBeInTheDocument();

    // Picking the system namespace narrows the table.
    fireEvent.click(screen.getByRole('button', { name: /kubendt-system/ }));
    expect(screen.getByText('2 objects')).toBeInTheDocument();
    expect(screen.queryByText('r1')).not.toBeInTheDocument();
    expect(screen.getByText('admin, password hash')).toBeInTheDocument();
  });

  test('a row opens the object as YAML', async () => {
    mockParams = {};
    render(<StatePage />);
    fireEvent.click(await screen.findByText('router FRRRouterDriver, 1 replica'));
    expect(await screen.findByText(/kind: StatefulSet/)).toBeInTheDocument();
    expect(global.fetch).toHaveBeenCalledWith(
      expect.stringMatching(/\/state\/lab\/statefulset\/r1$/)
    );
    expect(screen.getByText('kubendt/type=router')).toBeInTheDocument();
  });

  test('the namespace view reads one namespace and filters by search', async () => {
    mockParams = { namespace: 'lab' };
    render(<StatePage />);
    expect(await screen.findByText('State of lab')).toBeInTheDocument();
    expect(global.fetch).toHaveBeenCalledWith(expect.stringMatching(/\/state\/lab$/));
    expect(await screen.findByText('3 objects')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Filter the objects'), { target: { value: 'history' } });
    expect(screen.getByText('1 object')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Topology' }));
    expect(mockNavigate).toHaveBeenCalledWith('/lab');
  });

  test('sizes and ages read as people do', () => {
    expect(formatSize(0)).toBe('0 B');
    expect(formatSize(900)).toBe('900 B');
    expect(formatSize(2100)).toBe('2.1 kB');
    expect(formatSize(1500000)).toBe('1.5 MB');
    const now = Date.parse('2026-11-20T10:00:00Z');
    expect(formatAge('2026-11-20T09:59:30Z', now)).toBe('30s');
    expect(formatAge('2026-11-20T08:00:00Z', now)).toBe('2h');
    expect(formatAge('2026-11-17T10:00:00Z', now)).toBe('3d');
    expect(formatAge('', now)).toBe('');
  });
});
