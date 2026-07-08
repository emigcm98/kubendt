import React, { useCallback, useEffect, useState } from 'react';
import { API_BASE_URL } from '../config';
import { AuthContext } from '../auth/AuthContext';
import Login from '../pages/Login';
import './AuthGate.css';

// AuthGate resolves the current auth state before rendering the app. When auth
// is enabled and there is no valid session it shows the login screen; when auth
// is disabled it lets everything through.
export default function AuthGate({ children }) {
  const [state, setState] = useState({
    status: 'loading', // 'loading' | 'authed' | 'anon'
    enabled: true,
    identity: '',
    roles: [],
  });

  const refresh = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE_URL}/auth/me`);
      const data = await res.json();
      setState({
        status: data.authenticated ? 'authed' : 'anon',
        enabled: data.enabled !== false,
        identity: data.identity || '',
        roles: Array.isArray(data.roles) ? data.roles : [],
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
    setState((s) => ({ ...s, status: 'anon', identity: '', roles: [] }));
  }, []);

  useEffect(() => {
    refresh();
    const onUnauthorized = () => setState((s) => ({ ...s, status: 'anon' }));
    window.addEventListener('kubendt:unauthorized', onUnauthorized);
    return () => window.removeEventListener('kubendt:unauthorized', onUnauthorized);
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

  return (
    <AuthContext.Provider
      value={{
        enabled: state.enabled,
        authenticated: true,
        identity: state.identity,
        roles: state.roles,
        refresh,
        logout,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}
