import { createContext, useContext } from 'react';

// Shared authentication state, provided by AuthGate and consumed anywhere
// (e.g. the Home header for the logout button and token management).
export const AuthContext = createContext({
  enabled: false,
  authenticated: false,
  identity: '',
  roles: [],
  refresh: () => {},
  logout: async () => {},
});

export const useAuth = () => useContext(AuthContext);
