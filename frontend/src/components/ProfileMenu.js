import React, { useEffect, useRef, useState } from 'react';
import { useAuth } from '../auth/AuthContext';
import './ProfileMenu.css';

// Header account menu: click the user chip to reveal identity/roles plus the
// API tokens and logout actions.
export default function ProfileMenu({ onOpenTokens }) {
  const auth = useAuth();
  const [open, setOpen] = useState(false);
  const ref = useRef(null);

  useEffect(() => {
    if (!open) return undefined;
    const onDocClick = (e) => {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false);
    };
    const onKey = (e) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDocClick);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDocClick);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const identity = auth.identity || 'user';
  const initial = identity.charAt(0).toUpperCase();
  const roles = auth.roles && auth.roles.length ? auth.roles.join(', ') : '—';

  return (
    <div className="profile-menu" ref={ref}>
      <button
        className="profile-chip"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="true"
        aria-expanded={open}
        title="Account"
      >
        <span className="profile-avatar">{initial}</span>
        <span className="profile-name">{identity}</span>
        <span className={`profile-caret${open ? ' open' : ''}`}>▾</span>
      </button>

      {open && (
        <div className="profile-dropdown" role="menu">
          <div className="profile-info">
            <span className="profile-avatar profile-avatar-lg">{initial}</span>
            <div className="profile-info-text">
              <span className="profile-info-name">{identity}</span>
              <span className="profile-info-roles">Roles: {roles}</span>
            </div>
          </div>
          <div className="profile-sep" />
          <button
            className="profile-item"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              onOpenTokens?.();
            }}
          >
            <span className="profile-item-icon" aria-hidden="true">
              🔑
            </span>
            API tokens
          </button>
          <button
            className="profile-item profile-item-danger"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              auth.logout();
            }}
          >
            <span className="profile-item-icon" aria-hidden="true">
              ⏻
            </span>
            Logout
          </button>
        </div>
      )}
    </div>
  );
}
