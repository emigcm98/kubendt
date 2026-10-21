// Interface addresses as the API reports them (`addresses` is every global
// address, both families, comma separated; older entries only carry ipv4),
// and the subnet check the link panel shows.

export const interfaceAddresses = (intf) => {
  if (!intf) return [];
  if (typeof intf.addresses === 'string' && intf.addresses) {
    return intf.addresses.split(',').filter(Boolean);
  }
  return [intf.ipv4, intf.ipv6].filter(Boolean);
};

const v4Bytes = (ip) => {
  const parts = ip.split('.');
  if (parts.length !== 4) return null;
  const bytes = parts.map((p) => (/^\d{1,3}$/.test(p) ? Number(p) : -1));
  return bytes.every((n) => n >= 0 && n <= 255) ? bytes : null;
};

const v6Bytes = (ip) => {
  const halves = ip.split('::');
  if (halves.length > 2) return null;
  const head = halves[0] ? halves[0].split(':') : [];
  const tail = halves[1] ? halves[1].split(':') : [];
  const groups =
    halves.length === 2
      ? [...head, ...Array(Math.max(0, 8 - head.length - tail.length)).fill('0'), ...tail]
      : head;
  if (groups.length !== 8) return null;
  const bytes = [];
  for (const g of groups) {
    if (!/^[0-9a-f]{1,4}$/i.test(g)) return null;
    const n = parseInt(g, 16);
    bytes.push(n >> 8, n & 0xff);
  }
  return bytes;
};

// parseCidr reads "10.0.0.1/24" or "fd00::1/64" into its family, prefix
// length and network (the masked bytes), null when it is not an address.
export const parseCidr = (cidr) => {
  const [ip, lenText] = String(cidr || '')
    .trim()
    .split('/');
  const v6 = ip.includes(':');
  const bytes = v6 ? v6Bytes(ip) : v4Bytes(ip);
  if (!bytes) return null;
  const bits = bytes.length * 8;
  const len = lenText === undefined ? bits : Number(lenText);
  if (!Number.isInteger(len) || len < 0 || len > bits) return null;
  const network = bytes.map((b, i) => {
    const keep = Math.max(0, Math.min(8, len - i * 8));
    return keep === 0 ? 0 : b & ((0xff << (8 - keep)) & 0xff);
  });
  return { family: v6 ? 6 : 4, len, network: network.join('.') };
};

// sharedSubnet says whether any address of one end sits in the same subnet
// as one of the other end; null when either end has none to compare.
export const sharedSubnet = (a, b) => {
  if (!a?.length || !b?.length) return null;
  for (const x of a) {
    const px = parseCidr(x);
    if (!px) continue;
    for (const y of b) {
      const py = parseCidr(y);
      if (py && px.family === py.family && px.len === py.len && px.network === py.network) {
        return true;
      }
    }
  }
  return false;
};

// nodeAddresses lists every address of a /pods/ips listing as a target the
// tools can take: the interface, the bare IP and the family.
export const nodeAddresses = (data) => {
  const out = [];
  for (const intf of data?.interfaces || []) {
    for (const cidr of interfaceAddresses(intf)) {
      const ip = String(cidr).split('/')[0];
      if (!ip) continue;
      out.push({ iface: intf.interface, ip, family: ip.includes(':') ? 6 : 4 });
    }
  }
  return out;
};
