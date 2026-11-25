import React, { useCallback, useEffect, useState } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import AppHeader from '../components/AppHeader';
import { QuotaCard } from '../components/HomeSide';
import { useAuth } from '../auth/AuthContext';
import { API_BASE_URL } from '../config';
import { ReactComponent as KeyIcon } from '../assets/images/icons/key.svg';
import { ReactComponent as CopyIcon } from '../assets/images/icons/copy.svg';
import { ReactComponent as PowerIcon } from '../assets/images/icons/power.svg';
import { ReactComponent as ChevronIcon } from '../assets/images/icons/chevron-right.svg';
import { ReactComponent as UserIcon } from '../assets/images/icons/user.svg';
import './ProfilePage.css';

const when = (secs) => (secs ? new Date(secs * 1000).toLocaleDateString() : '');

const copyText = async (text) => {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  // Plain HTTP has no clipboard API, so a hidden textarea does it.
  const ta = document.createElement('textarea');
  ta.value = text;
  document.body.appendChild(ta);
  ta.select();
  document.execCommand('copy');
  document.body.removeChild(ta);
};

// One's own password: the current one, the new one twice. The other sessions
// end; this one goes on with a fresh cookie.
const PasswordCard = () => {
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
    setDone(false);
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
      setCurrent('');
      setNext('');
      setAgain('');
    } catch (err) {
      setError(err.message || 'Could not change the password');
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="me-card" id="password">
      <h3 className="me-card-title">Password</h3>
      <form className="me-form" onSubmit={submit}>
        <input
          type="password"
          placeholder="Current password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
        <input
          type="password"
          placeholder="New password"
          autoComplete="new-password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
        <input
          type="password"
          placeholder="New password, again"
          autoComplete="new-password"
          value={again}
          onChange={(e) => setAgain(e.target.value)}
        />
        {mismatch && <p className="me-error">The two do not match.</p>}
        {error && <p className="me-error">{error}</p>}
        {done && <p className="me-ok">Changed. Your other sessions are signed out.</p>}
        <button
          type="submit"
          className="me-btn"
          disabled={busy || !current || !next || next !== again}
        >
          {busy ? 'Saving…' : 'Change password'}
        </button>
      </form>
    </section>
  );
};

// One's API tokens, minted and revoked here. An admin may also list the
// tokens of the accounts it manages, but never mints for them.
const TokensCard = ({ admin }) => {
  const [all, setAll] = useState(false);
  const [tokens, setTokens] = useState(null);
  const [name, setName] = useState('');
  const [days, setDays] = useState(0);
  const [fresh, setFresh] = useState('');
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE_URL}/auth/tokens${all ? '?all=true' : ''}`);
      if (!res.ok) throw new Error('Could not read the tokens');
      const data = await res.json();
      setTokens(Array.isArray(data.tokens) ? data.tokens : []);
    } catch (e) {
      setError(e.message);
    }
  }, [all]);

  useEffect(() => {
    load();
  }, [load]);

  const create = async (e) => {
    e.preventDefault();
    if (!name.trim()) return;
    setBusy(true);
    setError('');
    setFresh('');
    try {
      const res = await fetch(`${API_BASE_URL}/auth/tokens`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: name.trim(), expires_in_days: Number(days) }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Could not create the token');
      setFresh(data.token);
      setName('');
      setDays(0);
      await load();
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (id) => {
    setError('');
    try {
      const res = await fetch(`${API_BASE_URL}/auth/tokens/${id}`, { method: 'DELETE' });
      if (!res.ok && res.status !== 204) throw new Error('Could not revoke the token');
      await load();
    } catch (e) {
      setError(e.message);
    }
  };

  const copy = async () => {
    try {
      await copyText(fresh);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* no clipboard */
    }
  };

  return (
    <section className="me-card me-card-grow" id="tokens">
      <h3 className="me-card-title">
        API tokens
        {tokens && <span className="me-count">{tokens.length}</span>}
        {admin && (
          <label className="me-toggle">
            <input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} />
            Also the accounts I manage
          </label>
        )}
      </h3>
      <p className="me-note">
        For scripts, sent as <code>Authorization: Bearer &lt;token&gt;</code>. A token acts as you.
      </p>
      <form className="me-token-new" onSubmit={create}>
        <input
          placeholder="Name, e.g. ci-pipeline"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <select value={days} onChange={(e) => setDays(Number(e.target.value))} title="Expires">
          <option value={0}>Never expires</option>
          <option value={7}>7 days</option>
          <option value={30}>30 days</option>
          <option value={90}>90 days</option>
          <option value={365}>1 year</option>
        </select>
        <button type="submit" className="me-btn" disabled={busy || !name.trim()}>
          {busy ? 'Creating…' : 'Create'}
        </button>
      </form>
      {fresh && (
        <div className="me-fresh" role="alert">
          <span>Copy it now, it is not shown again.</span>
          <div className="me-fresh-row">
            <code>{fresh}</code>
            <button type="button" className="me-btn-quiet" onClick={copy}>
              <CopyIcon className="app-icon" aria-hidden="true" /> {copied ? 'Copied' : 'Copy'}
            </button>
          </div>
        </div>
      )}
      {error && <p className="me-error">{error}</p>}
      <div className="me-scroll">
        {tokens && tokens.length === 0 && <p className="me-note">No tokens yet.</p>}
        {(tokens || []).map((t) => (
          <div key={t.id} className="me-token">
            <span className="me-token-icon" aria-hidden="true">
              <KeyIcon className="app-icon" />
            </span>
            <div className="me-token-main">
              <b>
                {t.name}
                {all && t.user && <span className="me-chip">{t.user}</span>}
              </b>
              <span>
                created {when(t.created_at)}
                {' · '}
                {t.last_used_at ? `used ${when(t.last_used_at)}` : 'never used'}
                {' · '}
                {t.expires_at ? `expires ${when(t.expires_at)}` : 'no expiry'}
              </span>
            </div>
            <button type="button" className="me-btn-danger" onClick={() => revoke(t.id)}>
              Revoke
            </button>
          </div>
        ))}
      </div>
    </section>
  );
};

const NamespacesCard = ({ me, onOpen }) => {
  const [list, setList] = useState(null);
  useEffect(() => {
    let alive = true;
    fetch(`${API_BASE_URL}/namespaces`)
      .then((res) => (res.ok ? res.json() : { namespaces: [] }))
      .then((data) => alive && setList(data.namespaces || []))
      .catch(() => alive && setList([]));
    return () => {
      alive = false;
    };
  }, []);
  // Only those one is in by name; an admin's owner level everywhere is not
  // the same as being in them.
  const mine = (list || []).filter((ns) => ns.owner === me || (ns.guests || {})[me]);
  return (
    <section className="me-card me-card-grow">
      <h3 className="me-card-title">
        Your namespaces <span className="me-count">{mine.length}</span>
      </h3>
      {list && mine.length === 0 && <p className="me-note">You are in none yet.</p>}
      <div className="me-scroll">
        {mine.map((ns) => {
          const level = ns.owner === me ? 'owner' : ns.guests[me];
          return (
            <button type="button" key={ns.name} className="me-ns" onClick={() => onOpen(ns.name)}>
              <span className="me-ns-name">
                <b title={ns.name}>{ns.name}</b>
                <span>
                  {ns.owner === me ? 'yours' : `owned by ${ns.owner || 'nobody'}`}
                  {ns.has_topology ? ` · ${ns.pods || 0} pods` : ' · no topology'}
                </span>
              </span>
              <span className={`me-level me-level-${level}`}>{level}</span>
              <ChevronIcon className="app-icon me-ns-go" aria-hidden="true" />
            </button>
          );
        })}
      </div>
    </section>
  );
};

// One's own page: who one is, the password, the API tokens, the quota and
// the namespaces one is in. It takes the place of the dialogs the account
// menu used to open.
const ProfilePage = () => {
  const auth = useAuth();
  const navigate = useNavigate();
  const { hash } = useLocation();
  const me = auth.identity || auth.username || '';

  useEffect(() => {
    if (!hash) return;
    document.getElementById(hash.slice(1))?.scrollIntoView({ block: 'start' });
  }, [hash]);

  if (!auth.enabled) return <Navigate to="/" replace />;

  const role = auth.role || '';
  return (
    <div className="profile-page">
      <AppHeader title={{ icon: UserIcon, label: 'Profile', hint: 'Your account' }} />
      <main className="me-main">
        <section className="me-band">
          <span className="me-avatar">{me.charAt(0).toUpperCase()}</span>
          <div className="me-who">
            <h1>{me}</h1>
            <div className="me-tags">
              {role && <span className={`me-role me-role-${role}`}>{role}</span>}
              {role === 'user' && (
                <span className="me-chip">
                  {auth.canCreateNamespaces
                    ? 'may create namespaces'
                    : 'works in shared namespaces'}
                </span>
              )}
            </div>
          </div>
          <button type="button" className="me-btn-quiet" onClick={() => auth.logout()}>
            <PowerIcon className="app-icon" aria-hidden="true" /> Sign out
          </button>
        </section>
        <div className="me-grid">
          <div className="me-col">
            <TokensCard admin={auth.isAdmin} />
          </div>
          <div className="me-col">
            <PasswordCard />
            {!auth.isAdmin && <QuotaCard namespaces={[]} me={me} />}
            <NamespacesCard me={me} onOpen={(ns) => navigate(`/${ns}`)} />
          </div>
        </div>
      </main>
    </div>
  );
};

export default ProfilePage;
