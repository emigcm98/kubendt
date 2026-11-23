import React, { useCallback, useEffect, useState } from 'react';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { API_BASE_URL } from '../config';
import './ApiTokensModal.css';
import './ShareNamespaceModal.css';

const LEVELS = ['viewer', 'operator', 'editor'];
const LEVEL_HELP = {
  viewer: 'sees the lab',
  operator: 'works on the pods',
  editor: 'changes the lab',
};

// The owner of a namespace naming its guests and their level, or handing
// it to someone else. The accounts are typed, the API knows whether they
// exist. Read only, it just tells a guest who else is in.
export default function ShareNamespaceModal({ namespace, onClose, onChanged, readOnly = false }) {
  const [owner, setOwner] = useState('');
  const [guests, setGuests] = useState({});
  const [newGuest, setNewGuest] = useState('');
  const [newLevel, setNewLevel] = useState('viewer');
  const [newOwner, setNewOwner] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [loaded, setLoaded] = useState(false);

  const load = useCallback(async () => {
    setError('');
    try {
      const res = await fetch(`${API_BASE_URL}/namespaces/access/${encodeURIComponent(namespace)}`);
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error(data?.error || `Error ${res.status}`);
      setOwner(data.owner || '');
      setGuests(data.guests || {});
      setLoaded(true);
    } catch (e) {
      setError(e.message || 'Could not read who has access');
    }
  }, [namespace]);

  useEffect(() => {
    load();
  }, [load]);

  const save = async (nextOwner, nextGuests) => {
    setBusy(true);
    setError('');
    setSaved(false);
    try {
      const res = await fetch(
        `${API_BASE_URL}/namespaces/access/${encodeURIComponent(namespace)}`,
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ owner: nextOwner, guests: nextGuests }),
        }
      );
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error(data?.error || `Error ${res.status}`);
      setOwner(data.owner || nextOwner);
      setGuests(data.guests || nextGuests);
      setSaved(true);
      onChanged?.();
      return true;
    } catch (e) {
      setError(e.message || 'Could not save');
      return false;
    } finally {
      setBusy(false);
    }
  };

  const addGuest = async (e) => {
    e.preventDefault();
    const name = newGuest.trim();
    if (!name) return;
    if (await save(owner, { ...guests, [name]: newLevel })) setNewGuest('');
  };

  const setLevel = (name, level) => save(owner, { ...guests, [name]: level });

  const removeGuest = (name) => {
    const next = { ...guests };
    delete next[name];
    return save(owner, next);
  };

  const handOver = async (e) => {
    e.preventDefault();
    const name = newOwner.trim();
    if (!name || name === owner) return;
    const stay = owner ? ' You stay as an editor.' : '';
    if (!window.confirm(`Hand ${namespace} over to ${name}?${stay}`)) return;
    // A namespace from before the accounts has no owner to keep as a guest.
    const next = owner ? { ...guests, [owner]: 'editor' } : { ...guests };
    delete next[name];
    if (await save(name, next)) setNewOwner('');
  };

  const names = Object.keys(guests).sort();

  return (
    <>
      <div className="tokens-backdrop" onClick={onClose} />
      <div
        className="tokens-modal share-modal"
        role="dialog"
        aria-label={readOnly ? 'Who has access' : 'Share namespace'}
      >
        <div className="tokens-header">
          <h2>
            {readOnly ? 'Who is in' : 'Share'} <code>{namespace}</code>
          </h2>
          <button className="tokens-close" onClick={onClose} title="Close">
            <CloseIcon className="app-icon" />
          </button>
        </div>

        <div className="share-owner">
          <span className="share-label">Owner</span>
          <strong>{owner || 'nobody, the admins'}</strong>
        </div>

        <div className="share-label">Guests</div>
        <div className="share-list">
          {names.length === 0 && (
            <div className="tokens-empty">Nobody else sees this namespace.</div>
          )}
          {names.map((name) => (
            <div key={name} className="share-row">
              <span className="share-name">{name}</span>
              {readOnly ? (
                <span className="share-level" title={LEVEL_HELP[guests[name]]}>
                  {guests[name]}
                </span>
              ) : (
                <>
                  <select
                    className="tokens-expiry"
                    value={guests[name]}
                    onChange={(e) => setLevel(name, e.target.value)}
                    disabled={busy}
                    title={LEVEL_HELP[guests[name]]}
                  >
                    {LEVELS.map((l) => (
                      <option key={l} value={l}>
                        {l}
                      </option>
                    ))}
                  </select>
                  <button
                    type="button"
                    className="tokens-revoke"
                    onClick={() => removeGuest(name)}
                    disabled={busy}
                    title="Remove"
                  >
                    <TrashIcon className="app-icon" aria-hidden="true" />
                  </button>
                </>
              )}
            </div>
          ))}
        </div>

        {readOnly ? (
          <p className="tokens-hint share-help">
            Viewer sees, operator works on the pods, editor changes the lab.
          </p>
        ) : (
          <>
            <form className="tokens-create" onSubmit={addGuest}>
              <input
                className="tokens-input"
                placeholder="Account to invite"
                value={newGuest}
                onChange={(e) => setNewGuest(e.target.value)}
              />
              <select
                className="tokens-expiry"
                value={newLevel}
                onChange={(e) => setNewLevel(e.target.value)}
                title={LEVEL_HELP[newLevel]}
              >
                {LEVELS.map((l) => (
                  <option key={l} value={l}>
                    {l}
                  </option>
                ))}
              </select>
              <button
                type="submit"
                className="tokens-create-btn"
                disabled={busy || !loaded || !newGuest.trim()}
              >
                Invite
              </button>
            </form>
            <p className="tokens-hint share-help">
              Viewer sees, operator works on the pods, editor changes the lab.
            </p>

            <form className="tokens-create share-handover" onSubmit={handOver}>
              <input
                className="tokens-input"
                placeholder="Hand over to"
                value={newOwner}
                onChange={(e) => setNewOwner(e.target.value)}
              />
              <button
                type="submit"
                className="tokens-create-btn tokens-create-btn-quiet"
                disabled={busy || !loaded || !newOwner.trim() || newOwner.trim() === owner}
              >
                Hand over
              </button>
            </form>
          </>
        )}

        {error && <div className="tokens-error">{error}</div>}
        {saved && !error && <div className="share-saved">Saved.</div>}
      </div>
    </>
  );
}
