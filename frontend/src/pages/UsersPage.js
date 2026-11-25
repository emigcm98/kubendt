import React, { useCallback, useEffect, useMemo, useState } from 'react';
import AppHeader from '../components/AppHeader';
import { API_BASE_URL } from '../config';
import { useAuth } from '../auth/AuthContext';
import '../components/NamespaceNavbar.css';
import './UsersPage.css';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as CopyIcon } from '../assets/images/icons/copy.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as UsersIcon } from '../assets/images/icons/users.svg';

const ROLES = ['user', 'admin'];
const LEVELS = ['viewer', 'operator', 'editor'];
const fmtDate = (secs) => (secs ? new Date(secs * 1000).toLocaleDateString() : '');
const quotaText = (n) => (n > 0 ? String(n) : 'default');

const standingOf = (u) =>
  u.disabled ? 'disabled' : u.must_change_password ? 'password to change' : 'active';

const copy = async (text) => {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
};

const COLUMNS = [
  { key: 'username', label: 'Account' },
  { key: 'role', label: 'Role' },
  { key: 'standing', label: 'Standing' },
  { key: 'namespaces', label: 'Namespaces' },
  { key: 'quota', label: 'Quota' },
  { key: 'created_at', label: 'Since' },
];

const cellValue = (u, key) => {
  switch (key) {
    case 'standing':
      return standingOf(u);
    case 'namespaces':
      return u.role === 'user' ? (u.can_create_namespaces ? 'may create' : 'shared only') : 'all';
    case 'quota':
      return u.role === 'user'
        ? `${quotaText(u.quota_namespaces)} / ${quotaText(u.quota_pods)} / ${quotaText(u.quota_pods_per_namespace)}`
        : 'none';
    default:
      return u[key] ?? '';
  }
};

// The accounts: who there is, what role and standing each has, where each
// is and what it may own; a new one on the left, the one picked on the
// right. Admins and the owner only, and what each may do to whom is the
// API's rule, told here.
const UsersPage = () => {
  const auth = useAuth();
  const [users, setUsers] = useState(null);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState('');
  const [shown, setShown] = useState(null); // {username, password} shown once
  const [copied, setCopied] = useState(false);
  const [query, setQuery] = useState('');
  const [sort, setSort] = useState({ key: 'username', dir: 1 });

  const load = useCallback(async () => {
    setError('');
    try {
      const res = await fetch(`${API_BASE_URL}/users`);
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error(data?.error || `Error ${res.status}`);
      setUsers(Array.isArray(data.users) ? data.users : []);
    } catch (e) {
      setError(e.message || 'Could not load the accounts');
      setUsers([]);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const rows = useMemo(() => {
    if (!users) return [];
    const q = query.trim().toLowerCase();
    const list = users.filter(
      (u) =>
        !q ||
        COLUMNS.some((c) => String(cellValue(u, c.key)).toLowerCase().includes(q)) ||
        fmtDate(u.created_at).includes(q)
    );
    const sorted = [...list].sort((a, b) => {
      const va = cellValue(a, sort.key);
      const vb = cellValue(b, sort.key);
      const cmp = typeof va === 'number' ? va - vb : String(va).localeCompare(String(vb));
      return cmp * sort.dir || a.username.localeCompare(b.username);
    });
    return sorted;
  }, [users, query, sort]);

  const current = useMemo(
    () => (users || []).find((u) => u.username === selected) || null,
    [users, selected]
  );
  const isOwner = auth.role === 'owner';

  const showPassword = async (username, password) => {
    setShown({ username, password });
    setCopied(await copy(password));
  };

  const sortBy = (key) => setSort((s) => ({ key, dir: s.key === key ? -s.dir : 1 }));

  return (
    <div className="users-root">
      <AppHeader
        title={{ icon: UsersIcon, label: 'Accounts', hint: 'The accounts of this KubeNDT' }}
        actions={
          <button
            type="button"
            className="app-header-btn"
            onClick={load}
            title="Read the accounts again"
          >
            <RefreshIcon className="app-icon" aria-hidden="true" />
            Refresh
          </button>
        }
      />

      <div className="users-page">
        <aside className="users-side">
          <NewAccount isOwner={isOwner} onCreated={load} onPassword={showPassword} />
        </aside>

        <main className="users-main">
          <div className="users-toolbar">
            <input
              type="search"
              className="users-input users-search"
              placeholder="Filter accounts"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              aria-label="Filter the accounts"
            />
            <span className="users-muted">
              {users === null ? 'Loading…' : `${rows.length} of ${users.length}`}
            </span>
          </div>
          {error && <p className="users-note users-error">{error}</p>}
          {users && users.length > 0 && (
            <table className="users-table">
              <thead>
                <tr>
                  {COLUMNS.map((c) => (
                    <th key={c.key}>
                      <button
                        type="button"
                        className={`users-sort${sort.key === c.key ? ' active' : ''}`}
                        onClick={() => sortBy(c.key)}
                        title={`Sort by ${c.label.toLowerCase()}`}
                      >
                        {c.label}
                        {sort.key === c.key && (
                          <span aria-hidden="true">{sort.dir > 0 ? ' ↑' : ' ↓'}</span>
                        )}
                      </button>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((u) => (
                  <tr
                    key={u.username}
                    className={`users-row${selected === u.username ? ' active' : ''}`}
                    onClick={() => setSelected(selected === u.username ? '' : u.username)}
                  >
                    <td className="users-name">
                      {u.username}
                      {u.username === auth.identity && <span className="users-you">you</span>}
                    </td>
                    <td>
                      <span className={`users-tag users-tag-${u.role}`}>{u.role}</span>
                    </td>
                    <td>
                      <span className={`users-tag users-tag-${standingOf(u).replace(/ /g, '-')}`}>
                        {standingOf(u)}
                      </span>
                    </td>
                    <td className="users-muted">{cellValue(u, 'namespaces')}</td>
                    <td
                      className="users-muted users-quota-cell"
                      title="namespaces / pods in total / pods per namespace"
                    >
                      {cellValue(u, 'quota')}
                    </td>
                    <td className="users-muted">{fmtDate(u.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {users && users.length > 0 && (
            <p className="users-note users-legend">
              Quota is namespaces, pods in total and pods per namespace.
            </p>
          )}
        </main>

        {current && (
          <AccountDetail
            key={current.username}
            user={current}
            isOwner={isOwner}
            self={current.username === auth.identity}
            others={(users || []).filter((u) => u.username !== current.username)}
            onChanged={load}
            onClose={() => setSelected('')}
            onPassword={showPassword}
          />
        )}
      </div>

      {shown && (
        <div className="users-once" role="alert">
          <div>
            Password of <strong>{shown.username}</strong>, shown once
          </div>
          <code>{shown.password}</code>
          <div className="users-once-actions">
            <button
              type="button"
              className="users-btn"
              onClick={async () => setCopied(await copy(shown.password))}
            >
              <CopyIcon className="app-icon" aria-hidden="true" />
              {copied ? 'Copied' : 'Copy'}
            </button>
            <button type="button" className="users-btn" onClick={() => setShown(null)}>
              <CloseIcon className="app-icon" aria-hidden="true" />
            </button>
          </div>
        </div>
      )}
    </div>
  );
};

const QuotaFields = ({ form, set }) => (
  <>
    <div className="users-group-title users-group-title-sub">Quota, 0 is the default</div>
    <QuotaInput
      label="Namespaces owned"
      value={form.quota_namespaces}
      onChange={(v) => set('quota_namespaces', v)}
    />
    <QuotaInput
      label="Pods in total"
      value={form.quota_pods}
      onChange={(v) => set('quota_pods', v)}
    />
    <QuotaInput
      label="Pods per namespace"
      value={form.quota_pods_per_namespace}
      onChange={(v) => set('quota_pods_per_namespace', v)}
    />
  </>
);

const QuotaInput = ({ label, value, onChange }) => (
  <label className="users-quota-item">
    <span>{label}</span>
    <input
      className="users-input users-input-num"
      type="number"
      min="0"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  </label>
);

const NewAccount = ({ isOwner, onCreated, onPassword }) => {
  const [form, setForm] = useState({
    username: '',
    password: '',
    role: 'user',
    can_create_namespaces: false,
    quota_namespaces: 0,
    quota_pods: 0,
    quota_pods_per_namespace: 0,
  });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const set = (k, v) => setForm((f) => ({ ...f, [k]: v }));

  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      const res = await fetch(`${API_BASE_URL}/users`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          ...form,
          username: form.username.trim(),
          quota_namespaces: Number(form.quota_namespaces) || 0,
          quota_pods: Number(form.quota_pods) || 0,
          quota_pods_per_namespace: Number(form.quota_pods_per_namespace) || 0,
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || `Error ${res.status}`);
      if (data.password) onPassword(data.user?.username || form.username.trim(), data.password);
      setForm((f) => ({ ...f, username: '', password: '' }));
      onCreated();
    } catch (err) {
      setError(err.message || 'Could not create the account');
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="users-form" onSubmit={submit}>
      <div className="users-group-title">New account</div>
      <label className="users-label" htmlFor="new-username">
        Account
      </label>
      <input
        id="new-username"
        className="users-input"
        placeholder="lowercase, digits, dashes"
        value={form.username}
        onChange={(e) => set('username', e.target.value)}
      />
      <label className="users-label" htmlFor="new-password">
        Password
      </label>
      <input
        id="new-password"
        className="users-input"
        type="password"
        placeholder="leave empty to generate one"
        autoComplete="new-password"
        value={form.password}
        onChange={(e) => set('password', e.target.value)}
      />
      <label className="users-label" htmlFor="new-role">
        Role
      </label>
      <select
        id="new-role"
        className="users-input"
        value={form.role}
        onChange={(e) => set('role', e.target.value)}
        disabled={!isOwner}
        title={isOwner ? '' : 'Only the owner creates admins'}
      >
        {ROLES.map((r) => (
          <option key={r} value={r}>
            {r}
          </option>
        ))}
      </select>
      {form.role === 'user' && (
        <>
          <label className="users-check">
            <input
              type="checkbox"
              checked={form.can_create_namespaces}
              onChange={(e) => set('can_create_namespaces', e.target.checked)}
            />
            May create namespaces
          </label>
          <QuotaFields form={form} set={set} />
        </>
      )}
      {error && <div className="users-error-box">{error}</div>}
      <button
        type="submit"
        className="users-btn users-btn-primary"
        disabled={busy || !form.username.trim()}
      >
        {busy ? 'Creating…' : 'Create account'}
      </button>
      <p className="users-note">
        It sets a new password at first sign-in. Access is given per namespace.
      </p>
    </form>
  );
};

const AccountDetail = ({ user, isOwner, self, others, onChanged, onClose, onPassword }) => {
  const [form, setForm] = useState({
    role: user.role,
    disabled: user.disabled,
    can_create_namespaces: user.can_create_namespaces,
    quota_namespaces: user.quota_namespaces,
    quota_pods: user.quota_pods,
    quota_pods_per_namespace: user.quota_pods_per_namespace,
  });
  const [reassign, setReassign] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const set = (k, v) => setForm((f) => ({ ...f, [k]: v }));
  const owner = user.role === 'owner';
  // The owner is nobody's to change; an admin changes plain users only.
  const frozen = owner || (!isOwner && user.role === 'admin');

  const call = async (method, path, body) => {
    setBusy(true);
    setError('');
    try {
      const res = await fetch(`${API_BASE_URL}${path}`, {
        method,
        headers: body ? { 'Content-Type': 'application/json' } : undefined,
        body: body ? JSON.stringify(body) : undefined,
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || `Error ${res.status}`);
      return data;
    } catch (err) {
      setError(err.message || 'The change failed');
      return null;
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    const patch = {
      disabled: form.disabled,
      can_create_namespaces: form.can_create_namespaces,
      quota_namespaces: Number(form.quota_namespaces) || 0,
      quota_pods: Number(form.quota_pods) || 0,
      quota_pods_per_namespace: Number(form.quota_pods_per_namespace) || 0,
    };
    if (isOwner && form.role !== user.role) patch.role = form.role;
    if (await call('PUT', `/users/${encodeURIComponent(user.username)}`, patch)) onChanged();
  };

  const reset = async () => {
    const data = await call('POST', `/users/${encodeURIComponent(user.username)}/password`, {});
    if (data?.password) onPassword(user.username, data.password);
    if (data) onChanged();
  };

  const remove = async () => {
    if (!window.confirm(`Delete the account ${user.username}?`)) return;
    const q = reassign ? `?reassign_to=${encodeURIComponent(reassign)}` : '';
    if (await call('DELETE', `/users/${encodeURIComponent(user.username)}${q}`)) {
      onClose();
      onChanged();
    }
  };

  return (
    <section className="users-detail" aria-label="Account">
      <header className="users-detail-head">
        <div>
          <strong>{user.username}</strong>
          <span className={`users-tag users-tag-${user.role}`}>{user.role}</span>
          {self && <span className="users-you">you</span>}
        </div>
        <button type="button" className="users-btn" onClick={onClose} title="Close">
          <CloseIcon className="app-icon" aria-hidden="true" />
        </button>
      </header>

      {owner && <p className="users-note">The owner account cannot be changed.</p>}
      {!owner && !isOwner && user.role === 'admin' && (
        <p className="users-note">Only the owner changes admins.</p>
      )}

      {user.role === 'user' && <AccountNamespaces username={user.username} busy={busy} />}

      {!frozen && (
        <>
          <div className="users-group-title users-group-title-sub">Account</div>
          {isOwner && (
            <>
              <label className="users-label" htmlFor="detail-role">
                Role
              </label>
              <select
                id="detail-role"
                className="users-input"
                value={form.role}
                onChange={(e) => set('role', e.target.value)}
              >
                {ROLES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
            </>
          )}
          <label className="users-check">
            <input
              type="checkbox"
              checked={form.disabled}
              onChange={(e) => set('disabled', e.target.checked)}
              disabled={self}
            />
            Disabled
          </label>
          {form.role === 'user' && (
            <>
              <label className="users-check">
                <input
                  type="checkbox"
                  checked={form.can_create_namespaces}
                  onChange={(e) => set('can_create_namespaces', e.target.checked)}
                />
                May create namespaces
              </label>
              <QuotaFields form={form} set={set} />
            </>
          )}
          <button
            type="button"
            className="users-btn users-btn-primary"
            onClick={save}
            disabled={busy}
          >
            Save
          </button>

          <div className="users-group-title users-group-title-sub">Password</div>
          <button type="button" className="users-btn" onClick={reset} disabled={busy}>
            Reset password
          </button>

          {!self && (
            <>
              <div className="users-group-title users-group-title-sub">Delete</div>
              <label className="users-label" htmlFor="detail-reassign">
                Namespaces go to
              </label>
              <select
                id="detail-reassign"
                className="users-input"
                value={reassign}
                onChange={(e) => setReassign(e.target.value)}
              >
                <option value="">the admins</option>
                {others.map((u) => (
                  <option key={u.username} value={u.username}>
                    {u.username}
                  </option>
                ))}
              </select>
              <button
                type="button"
                className="users-btn users-btn-danger"
                onClick={remove}
                disabled={busy}
              >
                Delete account
              </button>
            </>
          )}
        </>
      )}
      {error && <div className="users-error-box">{error}</div>}
    </section>
  );
};

// Where a plain account is and as what: its own namespaces and the ones it
// is a guest of, each level changed here, a namespace added or taken away.
// An admin holds the owner level everywhere, so the access API answers it.
const AccountNamespaces = ({ username }) => {
  const [mine, setMine] = useState(null);
  const [all, setAll] = useState([]);
  const [add, setAdd] = useState({ namespace: '', level: 'viewer' });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setError('');
    try {
      const [a, b] = await Promise.all([
        fetch(`${API_BASE_URL}/users/${encodeURIComponent(username)}/namespaces`),
        fetch(`${API_BASE_URL}/namespaces/`),
      ]);
      const da = await a.json().catch(() => null);
      const db = await b.json().catch(() => null);
      if (!a.ok) throw new Error(da?.error || `Error ${a.status}`);
      setMine(Array.isArray(da?.namespaces) ? da.namespaces : []);
      setAll(Array.isArray(db?.namespaces) ? db.namespaces.map((n) => n.name) : []);
    } catch (e) {
      setError(e.message || 'Could not read the namespaces');
      setMine([]);
    }
  }, [username]);

  useEffect(() => {
    load();
  }, [load]);

  // The guest list of a namespace is replaced whole, so it is read first.
  const setGuest = async (namespace, level) => {
    setBusy(true);
    setError('');
    try {
      const res = await fetch(`${API_BASE_URL}/namespaces/access/${encodeURIComponent(namespace)}`);
      const access = await res.json().catch(() => null);
      if (!res.ok) throw new Error(access?.error || `Error ${res.status}`);
      if (!access.owner) {
        throw new Error(`${namespace} has no owner yet. Hand it over from its card first.`);
      }
      const guests = { ...(access.guests || {}) };
      if (level) guests[username] = level;
      else delete guests[username];
      const put = await fetch(
        `${API_BASE_URL}/namespaces/access/${encodeURIComponent(namespace)}`,
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ owner: access.owner, guests }),
        }
      );
      const data = await put.json().catch(() => null);
      if (!put.ok) throw new Error(data?.error || `Error ${put.status}`);
      await load();
    } catch (e) {
      setError(e.message || 'The change failed');
    } finally {
      setBusy(false);
    }
  };

  const candidates = all.filter((n) => !(mine || []).some((m) => m.namespace === n));

  return (
    <>
      <div className="users-group-title users-group-title-sub">Namespaces</div>
      {mine === null && <p className="users-note">Loading…</p>}
      {mine && mine.length === 0 && <p className="users-note">No namespaces yet.</p>}
      {mine && mine.length > 0 && (
        <div className="users-ns-list">
          {mine.map((m) => (
            <div key={m.namespace} className="users-ns-row">
              <span className="users-ns-name">{m.namespace}</span>
              {m.level === 'owner' ? (
                <span className="users-tag users-tag-owner-ns">owner</span>
              ) : (
                <>
                  <select
                    className="users-input users-input-level"
                    value={m.level}
                    onChange={(e) => setGuest(m.namespace, e.target.value)}
                    disabled={busy}
                    aria-label={`Level in ${m.namespace}`}
                  >
                    {LEVELS.map((l) => (
                      <option key={l} value={l}>
                        {l}
                      </option>
                    ))}
                  </select>
                  <button
                    type="button"
                    className="users-btn users-btn-icon"
                    onClick={() => setGuest(m.namespace, '')}
                    disabled={busy}
                    title={`Take ${username} out of ${m.namespace}`}
                  >
                    <TrashIcon className="app-icon" aria-hidden="true" />
                  </button>
                </>
              )}
            </div>
          ))}
        </div>
      )}
      {candidates.length > 0 && (
        <div className="users-ns-add">
          <select
            className="users-input"
            value={add.namespace}
            onChange={(e) => setAdd((a) => ({ ...a, namespace: e.target.value }))}
            aria-label="Namespace to add"
          >
            <option value="">Add to a namespace…</option>
            {candidates.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
          <select
            className="users-input users-input-level"
            value={add.level}
            onChange={(e) => setAdd((a) => ({ ...a, level: e.target.value }))}
            aria-label="Level to give"
          >
            {LEVELS.map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </select>
          <button
            type="button"
            className="users-btn"
            disabled={busy || !add.namespace}
            onClick={async () => {
              await setGuest(add.namespace, add.level);
              setAdd({ namespace: '', level: 'viewer' });
            }}
          >
            Add
          </button>
        </div>
      )}
      <p className="users-note users-legend">
        Viewer sees, operator works on the pods, editor changes the lab.
      </p>
      {error && <div className="users-error-box">{error}</div>}
    </>
  );
};

export default UsersPage;
