import { createContext, useContext } from 'react';

// Shared authentication state, provided by AuthGate and consumed anywhere:
// who is signed in, their role, and what the pages may show them.
export const AuthContext = createContext({
  enabled: false,
  authenticated: false,
  identity: '',
  username: '',
  role: '',
  roles: [],
  isAdmin: false,
  canCreateNamespaces: false,
  mustChangePassword: false,
  refresh: () => {},
  logout: async () => {},
});

export const useAuth = () => useContext(AuthContext);
