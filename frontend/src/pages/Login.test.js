import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import Login from './Login';

describe('Login', () => {
  afterEach(() => jest.restoreAllMocks());

  test('submits the password and calls onSuccess on success', async () => {
    const onSuccess = jest.fn();
    jest
      .spyOn(global, 'fetch')
      .mockResolvedValue({ ok: true, json: async () => ({ authenticated: true }) });

    render(<Login onSuccess={onSuccess} />);
    fireEvent.change(screen.getByPlaceholderText(/admin password/i), {
      target: { value: 'secret' },
    });
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
    expect(global.fetch).toHaveBeenCalledWith(
      expect.stringContaining('/auth/login'),
      expect.objectContaining({ method: 'POST' })
    );
  });

  test('shows an error and does not call onSuccess on invalid credentials', async () => {
    const onSuccess = jest.fn();
    jest.spyOn(global, 'fetch').mockResolvedValue({
      ok: false,
      status: 401,
      json: async () => ({ error: 'Invalid credentials' }),
    });

    render(<Login onSuccess={onSuccess} />);
    fireEvent.change(screen.getByPlaceholderText(/admin password/i), {
      target: { value: 'bad' },
    });
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }));

    expect(await screen.findByText(/invalid credentials/i)).toBeInTheDocument();
    expect(onSuccess).not.toHaveBeenCalled();
  });
});
