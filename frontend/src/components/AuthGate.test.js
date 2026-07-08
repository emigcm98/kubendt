import { render, screen } from '@testing-library/react';
import AuthGate from './AuthGate';

describe('AuthGate', () => {
  afterEach(() => jest.restoreAllMocks());

  test('shows the login screen when not authenticated', async () => {
    jest
      .spyOn(global, 'fetch')
      .mockResolvedValue({ ok: true, json: async () => ({ enabled: true, authenticated: false }) });

    render(
      <AuthGate>
        <div>secret app</div>
      </AuthGate>
    );

    expect(await screen.findByRole('button', { name: /sign in/i })).toBeInTheDocument();
    expect(screen.queryByText('secret app')).not.toBeInTheDocument();
  });

  test('renders the app when authenticated', async () => {
    jest.spyOn(global, 'fetch').mockResolvedValue({
      ok: true,
      json: async () => ({
        enabled: true,
        authenticated: true,
        identity: 'admin',
        roles: ['admin'],
      }),
    });

    render(
      <AuthGate>
        <div>secret app</div>
      </AuthGate>
    );

    expect(await screen.findByText('secret app')).toBeInTheDocument();
  });
});
