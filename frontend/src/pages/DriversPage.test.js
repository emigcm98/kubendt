import { render, screen, fireEvent } from '@testing-library/react';
import DriversPage from './DriversPage';

// CRA's jest cannot resolve react-router 7's package exports, so the hooks
// are stubbed: the route param and the navigation the page asks for.
const mockNavigate = jest.fn();
let mockParams = {};
jest.mock('react-router-dom', () => ({
  useNavigate: () => mockNavigate,
  useParams: () => mockParams,
}));

const drivers = [
  {
    name: 'LinuxRouterDriver',
    type: 'router',
    executor: 'kubectl',
    isDefault: true,
    tools: ['trace'],
    knownImages: [],
    shells: [{ mode: 'sh', label: 'pod shell (sh)' }],
    aliases: [],
    capabilities: [
      {
        id: 'L3Capable',
        label: 'Level 3: IP addressing',
        description: 'Addresses and routes.',
        methods: [
          {
            name: 'set_ip',
            label: 'Set an address',
            params: [{ name: 'iface', type: 'iface', required: true }],
          },
        ],
        inspections: [
          {
            id: 'routes',
            label: 'Routing table',
            columns: [{ name: 'dst', label: 'Destination' }],
          },
        ],
      },
    ],
  },
  {
    name: 'FRRRouterDriver',
    type: 'router',
    executor: 'kubectl',
    isDefault: false,
    tools: [],
    knownImages: ['quay.io/frrouting/frr:10.7.1'],
    shells: [{ mode: 'vtysh', label: 'vtysh console' }],
    aliases: ['OldFRRDriver'],
    capabilities: [
      {
        id: 'BGPCapable',
        label: 'BGP Routing',
        description: '',
        methods: [
          {
            name: 'bgp_set_router',
            label: 'Run BGP in an AS',
            params: [],
            example: { params: { as: 65001 }, commands: ['vtysh -c "router bgp 65001"'] },
          },
        ],
        inspections: [],
      },
      {
        id: 'custom',
        label: 'Custom',
        description: '',
        methods: [],
        inspections: [],
        universal: true,
      },
    ],
  },
];

describe('DriversPage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
    jest.spyOn(global, 'fetch').mockResolvedValue({ ok: true, json: async () => ({ drivers }) });
  });
  afterEach(() => jest.restoreAllMocks());

  test('opens on the driver of the route and lists what it offers', async () => {
    mockParams = { driver: 'FRRRouterDriver' };
    render(<DriversPage />);
    expect(await screen.findByRole('heading', { name: 'FRRRouterDriver' })).toBeInTheDocument();
    expect(screen.getByText('bgp_set_router')).toBeInTheDocument();
    expect(screen.getByText('vtysh console')).toBeInTheDocument();
    expect(screen.getByText(/also answers to OldFRRDriver/)).toBeInTheDocument();
    expect(screen.getByText('On every node')).toBeInTheDocument();
  });

  test('the commands of an action unfold on demand', async () => {
    mockParams = { driver: 'FRRRouterDriver' };
    render(<DriversPage />);
    await screen.findByRole('heading', { name: 'FRRRouterDriver' });
    expect(screen.queryByText(/router bgp 65001/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Show commands' }));
    expect(screen.getByText(/router bgp 65001/)).toBeInTheDocument();
    expect(screen.getByText(/with as=65001/)).toBeInTheDocument();
  });

  test('without a driver in the route it opens the first one, and a click navigates', async () => {
    mockParams = {};
    render(<DriversPage />);
    expect(await screen.findByRole('heading', { name: 'LinuxRouterDriver' })).toBeInTheDocument();
    expect(screen.getByText('Routing table')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /FRRRouterDriver/ }));
    expect(mockNavigate).toHaveBeenCalledWith('/drivers/FRRRouterDriver');
  });

  test('says when the route names no driver', async () => {
    mockParams = { driver: 'NoSuchDriver' };
    render(<DriversPage />);
    expect(await screen.findByText(/No driver named/)).toBeInTheDocument();
  });
});
