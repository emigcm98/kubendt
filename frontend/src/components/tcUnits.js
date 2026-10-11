// Parsing and formatting of the values tc prints and accepts. The panel keeps
// numbers and units apart, so a read-back such as "500Kbit" or "4Kb" is shown
// as it is instead of being squeezed into whole Mbit or mislabeled as kbit.

// Kernel defaults that mean "no shaping" on a pod interface.
const DEFAULT_QDISCS = new Set(['noqueue', 'pfifo_fast', 'mq', 'noop']);

export const isShapedQdisc = (kind) => !!kind && !DEFAULT_QDISCS.has(kind);

// The kinds the panel can edit. Anything else was set outside KubeNDT.
export const isManagedQdisc = (kind) => kind === 'netem' || kind === 'tbf';

// "50ms", "1.5s", "200us" or a bare number (ms) to milliseconds.
export const timeToMs = (s) => {
  const m = /^\s*([0-9]+(?:\.[0-9]+)?)\s*(us|usec|ms|msec|s|sec)?\s*$/i.exec(String(s ?? ''));
  if (!m) return 0;
  const n = parseFloat(m[1]);
  const unit = (m[2] || 'ms').toLowerCase();
  if (unit.startsWith('us')) return n / 1000;
  if (unit.startsWith('s')) return n * 1000;
  return n;
};

export const percentToNumber = (s) => {
  const m = /^\s*([0-9]+(?:\.[0-9]+)?)\s*%?\s*$/.exec(String(s ?? ''));
  return m ? parseFloat(m[1]) : 0;
};

export const RATE_UNITS = ['kbit', 'mbit', 'gbit'];

// "500Kbit", "1Mbit", "100bit" (tc prints bits per second) to a value and the
// largest unit that keeps it a whole number.
export const parseRate = (s) => {
  const m = /^\s*([0-9]+(?:\.[0-9]+)?)\s*([kmgt]?)(bit|bps)?\s*$/i.exec(String(s ?? ''));
  if (!m) return { value: 1, unit: 'mbit' };
  const scale = { '': 1, k: 1e3, m: 1e6, g: 1e9, t: 1e12 }[m[2].toLowerCase()];
  let bits = parseFloat(m[1]) * scale;
  if ((m[3] || '').toLowerCase() === 'bps') bits *= 8;
  if (bits >= 1e9 && Number.isInteger(bits / 1e9)) return { value: bits / 1e9, unit: 'gbit' };
  if (bits >= 1e6 && Number.isInteger(bits / 1e6)) return { value: bits / 1e6, unit: 'mbit' };
  return { value: Math.round(bits) / 1e3, unit: 'kbit' };
};

export const formatRate = ({ value, unit }) => `${value}${unit}`;

export const SIZE_UNITS = ['b', 'kb', 'mb', 'kbit', 'mbit'];

// "4Kb", "1600b", "32kbit" to a value and unit. tc prints sizes in bytes
// (b, Kb, Mb), users mostly type kbit.
export const parseSize = (s) => {
  const m = /^\s*([0-9]+(?:\.[0-9]+)?)\s*([a-z]*)\s*$/i.exec(String(s ?? ''));
  if (!m) return { value: 32, unit: 'kbit' };
  const n = parseFloat(m[1]);
  switch (m[2].toLowerCase()) {
    case 'k':
    case 'kb':
      return { value: n, unit: 'kb' };
    case 'm':
    case 'mb':
      return { value: n, unit: 'mb' };
    case 'g':
    case 'gb':
      return { value: n * 1024, unit: 'mb' };
    case 'kbit':
      return { value: n, unit: 'kbit' };
    case 'mbit':
      return { value: n, unit: 'mbit' };
    case 'gbit':
      return { value: n * 1000, unit: 'mbit' };
    default:
      return { value: n, unit: 'b' };
  }
};

export const formatSize = ({ value, unit }) => `${value}${unit}`;
