import React, { useEffect, useState } from 'react';
import { Navigate } from 'react-router-dom';
import AppHeader from '../components/AppHeader';
import { useAuth } from '../auth/AuthContext';
import { API_BASE_URL } from '../config';
import { ReactComponent as SlidersIcon } from '../assets/images/icons/sliders.svg';
import './SettingsPage.css';

const GROUPS = [
  ['passwords', 'Passwords and sessions', 'For passwords chosen from now on.'],
  ['quotas', 'Default quotas', 'For accounts without their own. 0 is no limit.'],
  [
    'resources',
    "Pods' resources",
    'For containers without their own, in new namespaces. Empty is none.',
  ],
  ['labs', 'Labs', 'For labs deployed and captures started from now on.'],
];

const SOURCE = { saved: 'saved here', environment: 'environment', default: 'default' };

// How a setting is typed: a choice, a quantity, or a number.
const kindOf = (e) =>
  e.options?.length ? 'choice' : /_(CPU|MEMORY)_/.test(e.key) ? 'quantity' : 'number';

const Field = ({ entry, value, disabled, onChange }) => {
  const kind = kindOf(entry);
  if (kind === 'choice') {
    return (
      <select value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)}>
        {entry.options.map((o) => (
          <option key={o} value={o}>
            {o || 'left to the scheduler'}
          </option>
        ))}
      </select>
    );
  }
  return (
    <input
      type={kind === 'number' ? 'number' : 'text'}
      step="any"
      value={value}
      disabled={disabled}
      placeholder={kind === 'quantity' ? (/CPU/.test(entry.key) ? 'e.g. 250m' : 'e.g. 256Mi') : ''}
      onChange={(e) => onChange(e.target.value)}
    />
  );
};

const Group = ({ title, note, entries, editable, onSaved }) => {
  const [draft, setDraft] = useState({});
  const [errors, setErrors] = useState({});
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const changed = entries.filter((e) => draft[e.key] !== undefined && draft[e.key] !== e.value);

  const send = async (values) => {
    setBusy(true);
    setErrors({});
    setDone(false);
    try {
      const res = await fetch(`${API_BASE_URL}/settings`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ values }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        // Under its row the setting's name is already in sight.
        const said = data.error || 'Could not save the settings';
        const key = data.key || '';
        const shown = key && said.startsWith(`${key}: `) ? said.slice(key.length + 2) : said;
        setErrors({ [key]: shown.charAt(0).toUpperCase() + shown.slice(1) });
        return;
      }
      setDraft({});
      setDone(true);
      onSaved(data.settings || []);
    } catch (e) {
      setErrors({ '': e.message });
    } finally {
      setBusy(false);
    }
  };

  const save = () => send(Object.fromEntries(changed.map((e) => [e.key, draft[e.key]])));

  return (
    <section className="set-card">
      <h3>{title}</h3>
      <p className="set-note">{note}</p>
      {entries.map((e) => (
        <div className="set-row" key={e.key}>
          <div className="set-label">
            <b>{e.label}</b>
            {e.hint && <span>{e.hint}</span>}
            <code title="The environment variable">{e.key}</code>
          </div>
          <div className="set-control">
            <span className="set-input">
              <Field
                entry={e}
                value={draft[e.key] ?? e.value}
                disabled={!editable || busy}
                onChange={(v) => {
                  setDone(false);
                  setDraft((d) => ({ ...d, [e.key]: v }));
                }}
              />
              {e.unit && <em>{e.unit}</em>}
            </span>
            <span className="set-source">
              <span className={`set-badge set-badge-${e.source}`}>{SOURCE[e.source]}</span>
              {e.source === 'saved' && (
                <span className="set-env">
                  {e.env ? `environment ${e.env}` : `default ${e.default || 'none'}`}
                </span>
              )}
              {e.source === 'saved' && editable && (
                <button
                  type="button"
                  className="set-reset"
                  disabled={busy}
                  onClick={() => send({ [e.key]: null })}
                  title="Drop the saved value"
                >
                  Use {e.env ? 'environment' : 'default'}
                </button>
              )}
            </span>
            {errors[e.key] && <p className="set-error">{errors[e.key]}</p>}
          </div>
        </div>
      ))}
      {errors[''] && <p className="set-error">{errors['']}</p>}
      {editable && (
        <div className="set-actions">
          {done && <span className="set-done">Saved, in force now.</span>}
          <button
            type="button"
            className="set-btn-quiet"
            disabled={busy || changed.length === 0}
            onClick={() => {
              setDraft({});
              setErrors({});
            }}
          >
            Undo
          </button>
          <button
            type="button"
            className="set-btn"
            disabled={busy || changed.length === 0}
            onClick={save}
          >
            {busy ? 'Saving…' : 'Save'}
          </button>
        </div>
      )}
    </section>
  );
};

// The environment's settings the portal may change. The owner saves them,
// any admin reads them; a value saved here wins over the environment.
const SettingsPage = () => {
  const auth = useAuth();
  const [data, setData] = useState(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!auth.isAdmin) return;
    fetch(`${API_BASE_URL}/settings`)
      .then(async (res) => {
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(body.error || 'Could not read the settings');
        setData(body);
      })
      .catch((e) => setError(e.message));
  }, [auth.isAdmin]);

  if (!auth.isAdmin) return <Navigate to="/" replace />;

  const entries = data?.settings || [];
  return (
    <div className="settings-page">
      <AppHeader
        title={{
          icon: SlidersIcon,
          label: 'Settings',
          hint: 'What the owner may change from here',
        }}
      />
      <main className="set-main">
        <p className="set-intro">
          {data && !data.editable
            ? 'Only the owner changes these. Each value says where it comes from.'
            : 'Saved values override the environment and apply right away.'}
        </p>
        {error && <p className="set-error">{error}</p>}
        {!data && !error && <p className="set-intro">Reading the settings…</p>}
        {data && (
          <div className="set-grid">
            {GROUPS.map(([group, title, note]) => (
              <Group
                key={group}
                title={title}
                note={note}
                entries={entries.filter((e) => e.group === group)}
                editable={data.editable}
                onSaved={(settings) => setData((d) => ({ ...d, settings }))}
              />
            ))}
          </div>
        )}
      </main>
    </div>
  );
};

export default SettingsPage;
