import React, { useCallback, useEffect, useState } from 'react';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { ReactComponent as UsersIcon } from '../assets/images/icons/users.svg';
import { API_BASE_URL } from '../config';
import AlertModal from './AlertModal';
import './Dialog.css';
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
  const [handoverTo, setHandoverTo] = useState('');
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

  // Esc closes the dialog, unless the handover confirmation is open on top.
  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape' && !handoverTo) onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [handoverTo, onClose]);

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

  const askHandOver = (e) => {
    e.preventDefault();
    const name = newOwner.trim();
    if (!name || name === owner) return;
    setHandoverTo(name);
  };

  const handOver = async () => {
    const name = handoverTo;
    setHandoverTo('');
    // A namespace from before the accounts has no owner to keep as a guest.
    const next = owner ? { ...guests, [owner]: 'editor' } : { ...guests };
    delete next[name];
    if (await save(name, next)) setNewOwner('');
  };

  const names = Object.keys(guests).sort();

  return (
    <>
      <div className="dlg-overlay" onClick={onClose}>
        <div
          className="dlg share-modal"
          role="dialog"
          aria-label={readOnly ? 'Who has access' : 'Share namespace'}
          onClick={(e) => e.stopPropagation()}
        >
          <div className="dlg-head">
            <UsersIcon className="app-icon" aria-hidden="true" />
            <h2 className="dlg-title">
              {readOnly ? 'Who is in' : 'Share'} <code className="share-ns">{namespace}</code>
            </h2>
            <button className="dlg-close" onClick={onClose} title="Close">
              <CloseIcon className="app-icon" />
            </button>
          </div>

          <div className="dlg-body">
            <div className="share-owner">
              <span className="dlg-label">Owner</span>
              <strong>{owner || 'nobody, the admins'}</strong>
            </div>

            <div className="dlg-label">Guests</div>
            <div className="share-list">
              {names.length === 0 && (
                <div className="share-empty">Nobody else sees this namespace.</div>
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
                        className="dlg-input share-level-select"
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
                        className="share-remove"
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

            {!readOnly && (
              <form className="dlg-inline share-invite" onSubmit={addGuest}>
                <input
                  className="dlg-input"
                  placeholder="Account to invite"
                  value={newGuest}
                  onChange={(e) => setNewGuest(e.target.value)}
                />
                <select
                  className="dlg-input"
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
                  className="dlg-btn dlg-btn-primary"
                  disabled={busy || !loaded || !newGuest.trim()}
                >
                  Invite
                </button>
              </form>
            )}
            <p className="dlg-hint">
              Viewer sees, operator works on the pods, editor changes the lab.
            </p>

            {!readOnly && (
              <>
                <div className="dlg-label">Hand over</div>
                <form className="dlg-inline" onSubmit={askHandOver}>
                  <input
                    className="dlg-input"
                    placeholder="New owner"
                    value={newOwner}
                    onChange={(e) => setNewOwner(e.target.value)}
                  />
                  <button
                    type="submit"
                    className="dlg-btn dlg-btn-quiet"
                    disabled={busy || !loaded || !newOwner.trim() || newOwner.trim() === owner}
                  >
                    Hand over
                  </button>
                </form>
              </>
            )}

            {error && <div className="share-error">{error}</div>}
            {saved && !error && <div className="share-saved">Saved.</div>}
          </div>

          <div className="dlg-foot">
            <button className="dlg-btn dlg-btn-quiet" onClick={onClose}>
              Close
            </button>
          </div>
        </div>
      </div>

      <AlertModal
        isOpen={!!handoverTo}
        type="confirm"
        title="Hand over"
        message={
          <>
            Hand <strong>{namespace}</strong> over to <strong>{handoverTo}</strong>?
            {owner ? ` ${owner} stays as an editor.` : ''}
          </>
        }
        confirmText="Hand over"
        onConfirm={handOver}
        onCancel={() => setHandoverTo('')}
      />
    </>
  );
}
