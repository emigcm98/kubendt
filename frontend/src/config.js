const API_BASE_URL = process.env.REACT_APP_API_BASE_URL || 'http://localhost:8080';

// Derive an always-absolute ws:// or wss:// base. When API_BASE_URL is an
// absolute http(s) URL (local dev) we just swap the scheme. When it is a
// relative path (e.g. "/api" behind the nginx proxy) we resolve it against the
// current page origin, so WebSockets work in any browser and regardless of
// whether the app is reached via localhost, an IP or a hostname.
const toWsBase = (apiBase) => {
  if (/^https?:/i.test(apiBase)) {
    return apiBase.replace(/^http:/i, 'ws:').replace(/^https:/i, 'wss:');
  }
  const wsScheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const path = `/${apiBase}`.replace(/\/+/g, '/').replace(/\/+$/, '');
  return `${wsScheme}//${window.location.host}${path}`;
};

const WS_BASE_URL = toWsBase(API_BASE_URL);
const SWAGGER_UI_URL = `${API_BASE_URL.replace(/\/+$/, '')}/swagger/index.html`;

export { API_BASE_URL, WS_BASE_URL, SWAGGER_UI_URL };
