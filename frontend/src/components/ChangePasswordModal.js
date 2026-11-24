import React, { useState } from 'react';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { API_BASE_URL } from '../config';
import './ApiTokensModal.css';

// An account changing its own password: the current one, the new one twice.
// Its other sessions end; this one goes on with a fresh cookie.
export default function ChangePasswordModal({ onClose }) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [again, setAgain] = useState('');
  const [error, setError] = useState('');
  const [done, setDone] = useState(false);
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
      setDone(true);
    } catch (err) {
      setError(err.message || 'Could not change the password');
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="tokens-backdrop" onClick={onClose} />
      <div className="tokens-modal tokens-modal-narrow" role="dialog" aria-label="Change password">
        <div className="tokens-header">
          <h2>Change password</h2>
          <button className="tokens-close" onClick={onClose} title="Close">
            <CloseIcon className="app-icon" />
          </button>
        </div>
        {done ? (
          <>
            <p className="tokens-hint">
              Password changed. Your other sessions have been signed out.
            </p>
            <button type="button" className="tokens-create-btn" onClick={onClose}>
              Close
            </button>
          </>
        ) : (
          <form className="tokens-create tokens-create-column" onSubmit={submit}>
            <input
              className="tokens-input"
              type="password"
              placeholder="Current password"
              autoComplete="current-password"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
              autoFocus
            />
            <input
              className="tokens-input"
              type="password"
              placeholder="New password"
              autoComplete="new-password"
              value={next}
              onChange={(e) => setNext(e.target.value)}
            />
            <input
              className="tokens-input"
              type="password"
              placeholder="New password, again"
              autoComplete="new-password"
              value={again}
              onChange={(e) => setAgain(e.target.value)}
            />
            {mismatch && <div className="tokens-error">The two do not match.</div>}
            {error && <div className="tokens-error">{error}</div>}
            <button
              type="submit"
              className="tokens-create-btn"
              disabled={busy || !current || !next || next !== again}
            >
              {busy ? 'Saving…' : 'Change password'}
            </button>
          </form>
        )}
      </div>
    </>
  );
}
