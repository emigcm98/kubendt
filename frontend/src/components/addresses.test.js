import { interfaceAddresses, nodeAddresses, parseCidr, sharedSubnet } from './addresses';

test('interfaceAddresses prefers the full list and falls back to ipv4/ipv6', () => {
  expect(interfaceAddresses({ addresses: '10.0.0.1/24,fd00::1/64', ipv4: '10.0.0.1/24' })).toEqual([
    '10.0.0.1/24',
    'fd00::1/64',
  ]);
  expect(interfaceAddresses({ ipv4: '10.0.0.1/24', ipv6: '' })).toEqual(['10.0.0.1/24']);
  expect(interfaceAddresses({ addresses: '' })).toEqual([]);
  expect(interfaceAddresses(null)).toEqual([]);
});

test('parseCidr handles both families', () => {
  expect(parseCidr('10.0.1.7/24')).toEqual({ family: 4, len: 24, network: '10.0.1.0' });
  expect(parseCidr('10.0.1.7')).toEqual({ family: 4, len: 32, network: '10.0.1.7' });
  expect(parseCidr('fd00:1::9/64').network).toBe(parseCidr('fd00:1::1/64').network);
  expect(parseCidr('fd00:1::9/64').network).not.toBe(parseCidr('fd00:2::1/64').network);
  expect(parseCidr('fd00::1/64').family).toBe(6);
  expect(parseCidr('300.0.0.1/24')).toBeNull();
  expect(parseCidr('fd00:::1/64')).toBeNull();
  expect(parseCidr('10.0.0.1/33')).toBeNull();
  expect(parseCidr('')).toBeNull();
});

test('sharedSubnet compares across lists and families', () => {
  expect(sharedSubnet(['10.0.0.1/30'], ['10.0.0.2/30'])).toBe(true);
  expect(sharedSubnet(['10.0.0.1/24'], ['10.0.1.2/24'])).toBe(false);
  expect(sharedSubnet(['10.0.0.1/30', 'fd00::1/64'], ['fd00::2/64'])).toBe(true);
  expect(sharedSubnet(['10.0.0.1/30'], ['fd00::2/64'])).toBe(false);
  expect(sharedSubnet(['10.0.0.1/24'], ['10.0.0.2/25'])).toBe(false);
  expect(sharedSubnet([], ['10.0.0.2/30'])).toBeNull();
  expect(sharedSubnet(null, null)).toBeNull();
});

test('nodeAddresses flattens a listing into targets', () => {
  const data = {
    interfaces: [
      { interface: 'eth1', addresses: '10.0.12.1/30,fd00:12::1/64' },
      { interface: 'eth2', ipv4: '10.0.1.1/24', ipv6: '' },
      { interface: 'eth3', addresses: '' },
    ],
  };
  expect(nodeAddresses(data)).toEqual([
    { iface: 'eth1', ip: '10.0.12.1', family: 4 },
    { iface: 'eth1', ip: 'fd00:12::1', family: 6 },
    { iface: 'eth2', ip: '10.0.1.1', family: 4 },
  ]);
  expect(nodeAddresses(null)).toEqual([]);
});
