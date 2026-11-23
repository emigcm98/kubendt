import React, { useCallback, useEffect, useState } from 'react';
import { API_BASE_URL } from '../config';
import { AuthContext } from '../auth/AuthContext';
import Login, { SetPassword } from '../pages/Login';
import './AuthGate.css';

const isAdminRole = (role) => role === 'admin' || role === 'owner';

// AuthGate resolves the current auth state before rendering the app. When auth
// is enabled and there is no valid session it shows the login screen; an
// account with a password to change sees that screen first; when auth is
// disabled it lets everything through.
export default function AuthGate({ children }) {
  const [state, setState] = useState({
    status: 'loading', // 'loading' | 'authed' | 'anon' | 'change'
    enabled: true,
    identity: '',
    role: '',
    roles: [],
    canCreateNamespaces: false,
    mustChangePassword: false,
  });

  const refresh = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE_URL}/auth/me`);
      const data = await res.json();
      const enabled = data.enabled !== false;
      const role = data.role || (Array.isArray(data.roles) && data.roles[0]) || '';
      setState({
        status: !data.authenticated ? 'anon' : data.must_change_password ? 'change' : 'authed',
        enabled,
        identity: data.identity || data.username || '',
        role,
        roles: Array.isArray(data.roles) ? data.roles : role ? [role] : [],
        canCreateNamespaces: !enabled || data.can_create_namespaces === true || isAdminRole(role),
        mustChangePassword: data.must_change_password === true,
      });
    } catch {
      setState((s) => ({ ...s, status: 'anon' }));
    }
  }, []);

  const logout = useCallback(async () => {
    try {
      await fetch(`${API_BASE_URL}/auth/logout`, { method: 'POST' });
    } catch {
      // ignore network errors; we drop to login regardless
    }
    setState((s) => ({ ...s, status: 'anon', identity: '', role: '', roles: [] }));
  }, []);

  useEffect(() => {
    refresh();
    const onUnauthorized = () => setState((s) => ({ ...s, status: 'anon' }));
    const onChangeRequired = () =>
      setState((s) => ({ ...s, status: 'change', mustChangePassword: true }));
    window.addEventListener('kubendt:unauthorized', onUnauthorized);
    window.addEventListener('kubendt:password-change-required', onChangeRequired);
    return () => {
      window.removeEventListener('kubendt:unauthorized', onUnauthorized);
      window.removeEventListener('kubendt:password-change-required', onChangeRequired);
    };
  }, [refresh]);

  if (state.status === 'loading') {
    return (
      <div className="authgate-loading">
        <div className="authgate-spinner" />
        <span>Loading…</span>
      </div>
    );
  }

  if (state.status === 'anon') {
    return <Login onSuccess={refresh} />;
  }

  if (state.status === 'change') {
    return <SetPassword username={state.identity} onDone={refresh} onLogout={logout} />;
  }

  return (
    <AuthContext.Provider
      value={{
        enabled: state.enabled,
        authenticated: true,
        identity: state.identity,
        username: state.identity,
        role: state.role,
        roles: state.roles,
        isAdmin: !state.enabled || isAdminRole(state.role),
        canCreateNamespaces: state.canCreateNamespaces,
        mustChangePassword: state.mustChangePassword,
        refresh,
        logout,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}
