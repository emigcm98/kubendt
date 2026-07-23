import React, { useCallback, useEffect, useState } from 'react';
import { ReactComponent as KeyIcon } from '../assets/images/icons/key.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { API_BASE_URL } from '../config';
import './ApiTokensModal.css';

const fmt = (secs) => (secs ? new Date(secs * 1000).toLocaleString() : '—');

// Manage long-lived API tokens: create (value shown once), list and revoke.
export default function ApiTokensModal({ onClose }) {
  const [tokens, setTokens] = useState([]);
  const [name, setName] = useState('');
  const [expiresInDays, setExpiresInDays] = useState(0);
  const [newToken, setNewToken] = useState('');
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const copyToken = async () => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(newToken);
      } else {
        // Fallback for non-secure contexts (plain HTTP): use a temp textarea.
        const ta = document.createElement('textarea');
        ta.value = newToken;
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
      }
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable; ignore */
    }
  };

  const load = useCallback(async () => {
    setError('');
    try {
      const res = await fetch(`${API_BASE_URL}/auth/tokens`);
      if (!res.ok) throw new Error('Failed to load tokens');
      const data = await res.json();
      setTokens(Array.isArray(data.tokens) ? data.tokens : []);
    } catch (e) {
      setError(e.message || 'Failed to load tokens');
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const create = async (e) => {
    e.preventDefault();
    if (!name.trim()) return;
    setBusy(true);
    setError('');
    setNewToken('');
    try {
      const res = await fetch(`${API_BASE_URL}/auth/tokens`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: name.trim(), expires_in_days: Number(expiresInDays) }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to create token');
      setNewToken(data.token);
      setName('');
      setExpiresInDays(0);
      await load();
    } catch (err) {
      setError(err.message || 'Failed to create token');
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (id) => {
    setError('');
    try {
      const res = await fetch(`${API_BASE_URL}/auth/tokens/${id}`, { method: 'DELETE' });
      if (!res.ok && res.status !== 204) throw new Error('Failed to revoke token');
      await load();
    } catch (e) {
      setError(e.message || 'Failed to revoke token');
    }
  };

  return (
    <>
      <div className="tokens-backdrop" onClick={onClose} />
      <div className="tokens-modal" role="dialog" aria-label="API tokens">
        <div className="tokens-header">
          <h2>API tokens</h2>
          <button className="tokens-close" onClick={onClose} title="Close">
            <CloseIcon className="app-icon" />
          </button>
        </div>

        <p className="tokens-hint">
          Use tokens for scripts/CI: <code>Authorization: Bearer &lt;token&gt;</code>. They are
          shown only once.
        </p>

        <form className="tokens-create" onSubmit={create}>
          <input
            className="tokens-input"
            placeholder="Token name (e.g. ci-pipeline)"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <select
            className="tokens-expiry"
            value={expiresInDays}
            onChange={(e) => setExpiresInDays(Number(e.target.value))}
            title="Token expiry"
          >
            <option value={0}>Never</option>
            <option value={7}>7 days</option>
            <option value={30}>30 days</option>
            <option value={90}>90 days</option>
            <option value={365}>1 year</option>
          </select>
          <button type="submit" className="tokens-create-btn" disabled={busy || !name.trim()}>
            {busy ? 'Creating…' : 'Create'}
          </button>
        </form>

        {newToken && (
          <div className="tokens-new" role="alert">
            <div className="tokens-new-label">Copy it now. It won't be shown again:</div>
            <div className="tokens-new-row">
              <code className="tokens-new-value">{newToken}</code>
              <button type="button" className="tokens-copy" onClick={copyToken}>
                {copied ? 'Copied!' : 'Copy'}
              </button>
            </div>
          </div>
        )}

        {error && <div className="tokens-error">{error}</div>}

        <div className="tokens-list-title">Your tokens</div>
        <div className="tokens-list">
          {tokens.length === 0 && <div className="tokens-empty">No API tokens yet.</div>}
          {tokens.map((t) => (
            <div key={t.id} className="tokens-row">
              <span className="tokens-row-icon" aria-hidden="true">
                <KeyIcon className="tokens-row-icon-svg" />
              </span>
              <div className="tokens-row-main">
                <span className="tokens-row-name">{t.name}</span>
                <span className="tokens-row-meta">
                  Created {fmt(t.created_at)}
                  <span className="tokens-row-dot">·</span>
                  Last used {fmt(t.last_used_at)}
                  <span className="tokens-row-dot">·</span>
                  {t.expires_at ? `Expires ${fmt(t.expires_at)}` : 'Never expires'}
                </span>
              </div>
              <button className="tokens-revoke" onClick={() => revoke(t.id)} title="Revoke token">
                Revoke
              </button>
            </div>
          ))}
        </div>
      </div>
    </>
  );
}
