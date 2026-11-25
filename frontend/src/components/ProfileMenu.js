import React, { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/AuthContext';
import './ProfileMenu.css';
import { ReactComponent as UserIcon } from '../assets/images/icons/user.svg';
import { ReactComponent as PowerIcon } from '../assets/images/icons/power.svg';
import { ReactComponent as UsersIcon } from '../assets/images/icons/users.svg';
import { ReactComponent as ClusterIcon } from '../assets/images/icons/cluster.svg';
import { ReactComponent as SlidersIcon } from '../assets/images/icons/sliders.svg';

// Header account menu: click the account chip to see who is signed in and
// their role, then one's profile, the accounts, the cluster and the
// settings for admins (the owner alone saves those), and sign out.
export default function ProfileMenu() {
  const auth = useAuth();
  const navigate = useNavigate();
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
  const role = auth.role || (auth.roles && auth.roles[0]) || '';

  const item = (label, Icon, onClick, extra = '', tag = '') => (
    <button
      className={`profile-item${extra}`}
      role="menuitem"
      onClick={() => {
        setOpen(false);
        onClick?.();
      }}
    >
      <span className="profile-item-icon" aria-hidden="true">
        <Icon className="app-icon" />
      </span>
      {label}
      {tag && <span className={`profile-item-tag profile-item-tag-${tag}`}>{tag}</span>}
    </button>
  );

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
              {role && <span className={`profile-role profile-role-${role}`}>{role}</span>}
            </div>
          </div>
          <div className="profile-sep" />
          {item('Profile', UserIcon, () => navigate('/profile'))}
          {auth.isAdmin && item('Accounts', UsersIcon, () => navigate('/users'), '', 'admin')}
          {auth.isAdmin && item('Cluster', ClusterIcon, () => navigate('/cluster'), '', 'admin')}
          {auth.isAdmin &&
            item(
              'Settings',
              SlidersIcon,
              () => navigate('/settings'),
              '',
              auth.role === 'owner' ? 'owner' : 'admin'
            )}
          <div className="profile-sep" />
          {item('Sign out', PowerIcon, () => auth.logout(), ' profile-item-danger')}
        </div>
      )}
    </div>
  );
}
