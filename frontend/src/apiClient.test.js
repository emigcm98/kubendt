import { installApiClient } from './apiClient';
import { API_BASE_URL } from './config';

const reply = (status, body) => ({
  status,
  clone: () => ({ json: async () => body }),
});

let answer;
let signals;
const onUnauthorized = () => signals++;

beforeAll(() => {
  window.fetch = async () => answer;
  installApiClient();
  window.addEventListener('kubendt:unauthorized', onUnauthorized);
});

afterAll(() => window.removeEventListener('kubendt:unauthorized', onUnauthorized));

beforeEach(() => {
  signals = 0;
});

test('a wrong current password is the page to show, not a lost session', async () => {
  answer = reply(401, { code: 'INVALID_CREDENTIALS' });
  await window.fetch(`${API_BASE_URL}/auth/password`, { method: 'PUT' });
  expect(signals).toBe(0);
});

test('a 401 without a wrong password sends the app back to the sign-in', async () => {
  answer = reply(401, { code: 'UNAUTHENTICATED' });
  await window.fetch(`${API_BASE_URL}/namespaces`);
  expect(signals).toBe(1);
});
