import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import Login, { SetPassword } from './Login';

describe('Login', () => {
  afterEach(() => jest.restoreAllMocks());

  test('submits the account and the password and calls onSuccess on success', async () => {
    const onSuccess = jest.fn();
    jest
      .spyOn(global, 'fetch')
      .mockResolvedValue({ ok: true, json: async () => ({ authenticated: true }) });

    render(<Login onSuccess={onSuccess} />);
    fireEvent.change(screen.getByLabelText('Account'), { target: { value: 'alice' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'secret' } });
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
    const [url, init] = global.fetch.mock.calls[0];
    expect(url).toContain('/auth/login');
    expect(JSON.parse(init.body)).toEqual({ username: 'alice', password: 'secret' });
  });

  test('an empty account is sent as such, the server takes it as admin', async () => {
    jest
      .spyOn(global, 'fetch')
      .mockResolvedValue({ ok: true, json: async () => ({ authenticated: true }) });
    render(<Login onSuccess={jest.fn()} />);
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'secret' } });
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => expect(global.fetch).toHaveBeenCalled());
    expect(JSON.parse(global.fetch.mock.calls[0][1].body).username).toBe('');
  });

  test('shows an error and does not call onSuccess on invalid credentials', async () => {
    const onSuccess = jest.fn();
    jest.spyOn(global, 'fetch').mockResolvedValue({
      ok: false,
      status: 401,
      json: async () => ({ error: 'Invalid credentials' }),
    });

    render(<Login onSuccess={onSuccess} />);
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'bad' } });
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }));

    expect(await screen.findByText(/invalid credentials/i)).toBeInTheDocument();
    expect(onSuccess).not.toHaveBeenCalled();
  });
});

describe('SetPassword', () => {
  afterEach(() => jest.restoreAllMocks());

  test('asks the current password and the new one twice, then goes on', async () => {
    const onDone = jest.fn();
    jest.spyOn(global, 'fetch').mockResolvedValue({ ok: true, json: async () => ({}) });
    render(<SetPassword username="alice" onDone={onDone} onLogout={jest.fn()} />);
    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'old' } });
    fireEvent.change(screen.getByLabelText('New password'), { target: { value: 'new-one' } });
    fireEvent.change(screen.getByLabelText('New password, again'), {
      target: { value: 'new-two' },
    });
    expect(screen.getByText('The two do not match.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /save and continue/i })).toBeDisabled();
    fireEvent.change(screen.getByLabelText('New password, again'), {
      target: { value: 'new-one' },
    });
    fireEvent.click(screen.getByRole('button', { name: /save and continue/i }));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    const [url, init] = global.fetch.mock.calls[0];
    expect(url).toContain('/auth/password');
    expect(init.method).toBe('PUT');
    expect(JSON.parse(init.body)).toEqual({ current_password: 'old', new_password: 'new-one' });
  });
});
