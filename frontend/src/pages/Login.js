import React, { useState } from 'react';
import { API_BASE_URL } from '../config';
import kubendtLogo from '../assets/images/kubendt-logo.svg';
import { ReactComponent as LockIcon } from '../assets/images/icons/lock.svg';
import './Login.css';

const Shell = ({ title, lead, children }) => (
  <div className="login-wrapper">
    <div className="login-card">
      <div className="login-brand">
        <img src={kubendtLogo} alt="" className="login-logo" />
        <div className="login-brand-text">
          <span className="login-brand-name">KubeNDT</span>
          <span className="login-brand-sub">Network Digital Twin</span>
        </div>
      </div>
      <h1 className="login-title">{title}</h1>
      {lead && <p className="login-lead">{lead}</p>}
      {children}
    </div>
    <a className="login-site" href="https://kubendt.org" target="_blank" rel="noopener noreferrer">
      kubendt.org
    </a>
  </div>
);

const Field = ({ id, label, children }) => (
  <div className="login-field">
    <label className="login-label" htmlFor={id}>
      {label}
    </label>
    {children}
  </div>
);

// The sign-in screen: an account and its password. The account is admin
// when left empty, as it was before there were accounts. On success it calls
// onSuccess so AuthGate re-checks and renders the app, or asks for a new
// password first.
export default function Login({ onSuccess }) {
  const [username, setUsername] = useState('');
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
        body: JSON.stringify({ username: username.trim(), password }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        if (res.status === 429) throw new Error('Too many attempts. Try again in a minute.');
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
    <Shell title="Sign in">
      <form className="login-form" onSubmit={submit}>
        <Field id="login-username" label="Account">
          <input
            id="login-username"
            type="text"
            className="login-input"
            placeholder="user"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoFocus
          />
        </Field>
        <Field id="login-password" label="Password">
          <input
            id="login-password"
            type="password"
            className="login-input"
            placeholder="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        {error && (
          <div className="login-error" role="alert">
            {error}
          </div>
        )}
        <button type="submit" className="login-btn" disabled={busy || !password}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </Shell>
  );
}

// An account with a password to change sets it here before anything else:
// the one it signed in with, the new one twice.
export function SetPassword({ username, onDone, onLogout }) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [again, setAgain] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const mismatch = again !== '' && next !== again;

  const submit = async (e) => {
    e.preventDefault();
    if (mismatch) return;
    setBusy(true);
    setError('');
    try {
      const res = await fetch(`${API_BASE_URL}/auth/password`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ current_password: current, new_password: next }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || 'Could not change the password');
      }
      onDone?.();
    } catch (err) {
      setError(err.message || 'Could not change the password');
    } finally {
      setBusy(false);
    }
  };

  return (
    <Shell
      title="Set a new password"
      lead={
        <>
          <LockIcon className="app-icon login-lead-icon" aria-hidden="true" />
          <strong>{username}</strong> has to set a new password.
        </>
      }
    >
      <form className="login-form" onSubmit={submit}>
        <Field id="pw-current" label="Current password">
          <input
            id="pw-current"
            type="password"
            className="login-input"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            autoFocus
          />
        </Field>
        <Field id="pw-new" label="New password">
          <input
            id="pw-new"
            type="password"
            className="login-input"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />
        </Field>
        <Field id="pw-again" label="New password, again">
          <input
            id="pw-again"
            type="password"
            className={`login-input${mismatch ? ' login-input-bad' : ''}`}
            autoComplete="new-password"
            value={again}
            onChange={(e) => setAgain(e.target.value)}
          />
        </Field>
        {mismatch && <div className="login-note">The two do not match.</div>}
        {error && (
          <div className="login-error" role="alert">
            {error}
          </div>
        )}
        <button
          type="submit"
          className="login-btn"
          disabled={busy || !current || !next || next !== again}
        >
          {busy ? 'Saving…' : 'Save and continue'}
        </button>
        <button type="button" className="login-link" onClick={onLogout}>
          Sign in as someone else
        </button>
      </form>
    </Shell>
  );
}
