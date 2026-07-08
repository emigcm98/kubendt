import React, { useState } from 'react';
import { API_BASE_URL } from '../config';
import kubendtLogo from '../assets/images/kubendt-logo.svg';
import './Login.css';

// Password login screen. On success it calls onSuccess so AuthGate re-checks
// and renders the app.
export default function Login({ onSuccess }) {
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      const res = await fetch(`${API_BASE_URL}/auth/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || 'Login failed');
      }
      onSuccess?.();
    } catch (err) {
      setError(err.message || 'Login failed');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login-wrapper">
      <form className="login-card" onSubmit={submit}>
        <img src={kubendtLogo} alt="KubeNDT" className="login-logo" />
        <h1 className="login-title">KubeNDT</h1>
        <p className="login-subtitle">Sign in to continue</p>
        <div className="login-field">
          <label className="login-label">User</label>
          <div className="login-user">
            <span className="login-user-avatar">A</span>
            admin
          </div>
        </div>
        <div className="login-field">
          <label className="login-label" htmlFor="login-password">
            Password
          </label>
          <input
            id="login-password"
            type="password"
            className="login-input"
            placeholder="Admin password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoFocus
          />
        </div>
        {error && <div className="login-error">{error}</div>}
        <button type="submit" className="login-btn" disabled={busy || !password}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  );
}
