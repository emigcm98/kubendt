import { API_BASE_URL } from './config';

// Whether a fetch call targets our backend API.
const isApiRequest = (input) => {
  const url = typeof input === 'string' ? input : input?.url || '';
  return url.startsWith(API_BASE_URL);
};

// Install a small fetch interceptor, once, for the whole app. It:
//  1. Sends the session cookie on API calls (needed in dev, where the frontend
//     on :3000 talks cross-origin to the backend on :8080).
//  2. Signals a global "unauthorized" event on a 401 that is not a wrong
//     password, so the AuthGate can drop back to the login screen when a
//     session expires.
export function installApiClient() {
  const originalFetch = window.fetch.bind(window);

  window.fetch = async (input, init = {}) => {
    const opts = isApiRequest(input)
      ? { ...init, credentials: init.credentials || 'include' }
      : init;

    const res = await originalFetch(input, opts);

    if (isApiRequest(input) && res.status === 401) {
      const url = typeof input === 'string' ? input : input?.url || '';
      // A wrong password, at the login or when changing it, is the caller's
      // to show; any other 401 means the session is gone, so back to login.
      const data = await res
        .clone()
        .json()
        .catch(() => null);
      if (!url.includes('/auth/login') && data?.code !== 'INVALID_CREDENTIALS') {
        window.dispatchEvent(new CustomEvent('kubendt:unauthorized'));
      }
    }
    // An account with a password to change gets nothing else until it does:
    // the gate shows that screen.
    if (isApiRequest(input) && res.status === 403) {
      const data = await res
        .clone()
        .json()
        .catch(() => null);
      if (data?.code === 'PASSWORD_CHANGE_REQUIRED') {
        window.dispatchEvent(new CustomEvent('kubendt:password-change-required'));
      }
    }
    return res;
  };
}
