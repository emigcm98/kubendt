import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import UsersPage from './UsersPage';
import { AuthContext } from '../auth/AuthContext';

// CRA's jest cannot resolve react-router 7's package exports, so the hooks
// are stubbed.
const mockNavigate = jest.fn();
jest.mock('react-router-dom', () => ({
  useNavigate: () => mockNavigate,
}));

const users = [
  { username: 'admin', role: 'owner', disabled: false, created_at: 1700000000 },
  {
    username: 'alice',
    role: 'user',
    disabled: false,
    must_change_password: true,
    can_create_namespaces: true,
    quota_namespaces: 2,
    quota_pods: 0,
    quota_pods_per_namespace: 5,
    created_at: 1700000000,
  },
];

const asOwner = (ui) =>
  render(
    <AuthContext.Provider
      value={{ enabled: true, identity: 'admin', role: 'owner', isAdmin: true }}
    >
      {ui}
    </AuthContext.Provider>
  );

describe('UsersPage', () => {
  beforeEach(() => {
    jest.spyOn(global, 'fetch').mockImplementation(async (url, init = {}) => {
      if (String(url).endsWith('/users') && (!init.method || init.method === 'GET')) {
        return { ok: true, json: async () => ({ users }) };
      }
      if (String(url).endsWith('/users/alice/namespaces')) {
        return {
          ok: true,
          json: async () => ({
            username: 'alice',
            namespaces: [
              { namespace: 'lab', owner: 'alice', level: 'owner' },
              { namespace: 'shared', owner: 'bob', level: 'editor' },
            ],
          }),
        };
      }
      if (String(url).endsWith('/namespaces/')) {
        return {
          ok: true,
          json: async () => ({
            namespaces: [{ name: 'lab' }, { name: 'shared' }, { name: 'other' }],
          }),
        };
      }
      if (String(url).endsWith('/users') && init.method === 'POST') {
        return {
          ok: true,
          status: 201,
          json: async () => ({
            user: { username: 'bob', role: 'user' },
            password: 'generated-one',
          }),
        };
      }
      return { ok: true, json: async () => ({}) };
    });
    Object.assign(navigator, { clipboard: { writeText: jest.fn().mockResolvedValue() } });
  });

  afterEach(() => {
    global.fetch.mockRestore();
  });

  test('lists the accounts with their role, standing and quota', async () => {
    asOwner(<UsersPage />);
    expect(await screen.findByText('alice')).toBeInTheDocument();
    expect(screen.getByText('owner')).toBeInTheDocument();
    expect(screen.getByText('password to change')).toBeInTheDocument();
    expect(screen.getByText('may create')).toBeInTheDocument();
    expect(screen.getByText('2 / default / 5')).toBeInTheDocument();
  });

  test('creates an account and shows the generated password once', async () => {
    asOwner(<UsersPage />);
    await screen.findByText('alice');
    fireEvent.change(screen.getByLabelText('Account'), { target: { value: 'bob' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create account' }));
    expect(await screen.findByText('generated-one')).toBeInTheDocument();
    const post = global.fetch.mock.calls.find(([, init]) => init?.method === 'POST');
    expect(JSON.parse(post[1].body)).toMatchObject({ username: 'bob', role: 'user' });
  });

  test('a picked account opens with its namespaces and settings', async () => {
    asOwner(<UsersPage />);
    fireEvent.click(await screen.findByText('alice'));
    expect(screen.getAllByLabelText('Role')[1]).toHaveValue('user');
    expect(screen.getByRole('button', { name: 'Delete account' })).toBeInTheDocument();
    expect(await screen.findByText('shared')).toBeInTheDocument();
    expect(screen.getByLabelText('Level in shared')).toHaveValue('editor');
    expect(screen.getByLabelText('Namespace to add')).toBeInTheDocument();
  });

  test('the table filters and sorts', async () => {
    asOwner(<UsersPage />);
    await screen.findByText('alice');
    fireEvent.change(screen.getByLabelText('Filter the accounts'), { target: { value: 'owner' } });
    expect(screen.getByText('1 of 2')).toBeInTheDocument();
    expect(screen.queryByText('alice')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Filter the accounts'), { target: { value: '' } });
    // Sorted by account already, so one click turns it around.
    fireEvent.click(screen.getByRole('button', { name: /^Account/ }));
    const names = screen
      .getAllByRole('row')
      .slice(1)
      .map((r) => r.querySelector('.users-name').textContent);
    expect(names[0]).toMatch(/^alice/);
  });
});
