// What a row of the Home's namespace list shows, kept apart from the page so
// it can be tested without rendering it.

const STACK = 4;
export const LEVEL_ORDER = ['editor', 'operator', 'viewer'];

// The owner and oneself by name; a lone other guest by name when one's own
// chip does not take the room; everyone else as a stack of faces.
export const peopleOf = (ns, me) => {
  const guests = Object.entries(ns.guests || {}).sort(([a], [b]) => a.localeCompare(b));
  const self = guests.find(([name]) => name === me) || null;
  const others = guests.filter(([name]) => name !== me);
  const named = others.length === 1 && !self ? others : [];
  const stacked = named.length ? [] : others;
  const byLevel = {};
  guests.forEach(([name, level]) => {
    (byLevel[level] = byLevel[level] || []).push(name);
  });
  return {
    owner: ns.owner || '',
    self,
    named,
    faces: stacked.slice(0, STACK),
    extra: Math.max(0, stacked.length - STACK),
    total: guests.length + (ns.owner ? 1 : 0),
    byLevel,
  };
};

export const matchesFilter = (ns, q) => {
  const s = q.trim().toLowerCase();
  if (!s) return true;
  return [ns.name, ns.owner || '', ...Object.keys(ns.guests || {})].some((v) =>
    v.toLowerCase().includes(s)
  );
};

// The creation time: RFC 3339 when the backend gives it, the older local
// string otherwise.
export const createdDate = (ns) => {
  if (ns.created) return new Date(ns.created);
  if (ns.createdAt) return new Date(ns.createdAt.replace(' ', 'T'));
  return null;
};

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

export const shortDate = (d, now = new Date()) => {
  if (!d || Number.isNaN(d.getTime())) return '';
  const day = `${d.getDate()} ${MONTHS[d.getMonth()]}`;
  return d.getFullYear() === now.getFullYear() ? day : `${day} ${d.getFullYear()}`;
};

export const age = (d, now = new Date()) => {
  if (!d || Number.isNaN(d.getTime())) return '';
  const mins = Math.max(0, Math.floor((now - d) / 60000));
  const plural = (n, unit) => `${n} ${unit}${n === 1 ? '' : 's'}`;
  if (mins < 60) return mins < 2 ? 'just now' : plural(mins, 'minute');
  const hours = Math.floor(mins / 60);
  if (hours < 24) return plural(hours, 'hour');
  const days = Math.floor(hours / 24);
  if (days < 14) return plural(days, 'day');
  if (days < 61) return plural(Math.floor(days / 7), 'week');
  if (days < 730) return plural(Math.floor(days / 30), 'month');
  return plural(Math.floor(days / 365), 'year');
};
