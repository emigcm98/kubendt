import { API_BASE_URL } from './config';

// Whether a fetch call targets our backend API.
const isApiRequest = (input) => {
  const url = typeof input === 'string' ? input : input?.url || '';
  return url.startsWith(API_BASE_URL);
};

// Install a small fetch interceptor, once, for the whole app. It:
//  1. Sends the session cookie on API calls (needed in dev, where the frontend
//     on :3000 talks cross-origin to the backend on :8080).
//  2. Signals a global "unauthorized" event on a 401 from a non-auth endpoint,
//     so the AuthGate can drop back to the login screen when a session expires.
export function installApiClient() {
  const originalFetch = window.fetch.bind(window);

  window.fetch = async (input, init = {}) => {
    const opts = isApiRequest(input)
      ? { ...init, credentials: init.credentials || 'include' }
      : init;

    const res = await originalFetch(input, opts);

    if (isApiRequest(input) && res.status === 401) {
      const url = typeof input === 'string' ? input : input?.url || '';
      // Only the login attempt handles its own 401 (wrong password); a 401
      // anywhere else means the session expired, so bounce back to login.
      if (!url.includes('/auth/login')) {
        window.dispatchEvent(new CustomEvent('kubendt:unauthorized'));
      }
    }
    return res;
  };
}
