import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import IperfPanel, { formatBps, formatBytes, validate } from './IperfPanel';

// A WebSocket stand-in that records the url and lets a test push frames.
class FakeSocket {
  constructor(url) {
    this.url = url;
    FakeSocket.last = this;
  }
  close() {}
}
const frame = (ws, obj) => act(() => ws.onmessage({ data: JSON.stringify(obj) }));

beforeEach(() => {
  global.WebSocket = FakeSocket;
  global.fetch = jest.fn(() => Promise.resolve({ ok: false }));
});

test('formats rates and sizes', () => {
  expect(formatBps(0)).toBe('0 bit/s');
  expect(formatBps(950)).toBe('950 bit/s');
  expect(formatBps(48_000)).toBe('48 kbit/s');
  expect(formatBps(46_600_000)).toBe('46.6 Mbit/s');
  expect(formatBps(1_215_000_000)).toBe('1.22 Gbit/s');
  expect(formatBytes(20_316_160)).toBe('20.3 MB');
});

test('the form says what is out of range', () => {
  const ok = { duration: '30', parallel: '2', bandwidth: '20M', serverMode: 'auto', port: '' };
  expect(validate(ok)).toBeNull();
  expect(validate({ ...ok, duration: '500' })).toMatch(/duration must be between 1 and 120/);
  expect(validate({ ...ok, duration: '' })).toMatch(/duration/);
  expect(validate({ ...ok, parallel: '9' })).toMatch(/streams must be between 1 and 8/);
  expect(validate({ ...ok, bandwidth: '10 Mbit' })).toMatch(/rate must be/);
  expect(validate({ ...ok, bandwidth: '1.5G' })).toBeNull();
  expect(validate({ ...ok, serverMode: 'none', port: '70000' })).toMatch(/port must be/);
  expect(validate({ ...ok, serverMode: 'auto', port: '70000' })).toBeNull();
});

test('an out-of-range duration shows in red and holds Run', () => {
  render(<IperfPanel namespace="lab" source="host-0" nodes={[]} onClose={() => {}} />);
  fireEvent.change(screen.getByPlaceholderText('IP address'), { target: { value: '10.0.0.2' } });
  const seconds = screen.getByTitle(/Duration in seconds/);
  fireEvent.change(seconds, { target: { value: '' } });
  expect(screen.getByText(/duration must be between/)).toBeInTheDocument();
  expect(screen.getByText('Run')).toBeDisabled();
  fireEvent.change(seconds, { target: { value: '500' } });
  expect(seconds.className).toContain('iperf-input-bad');
  expect(screen.getByText('Run')).toBeDisabled();
  fireEvent.change(seconds, { target: { value: '30' } });
  expect(screen.queryByText(/duration must be between/)).toBeNull();
  expect(screen.getByText('Run')).not.toBeDisabled();
  fireEvent.click(screen.getByText('Run'));
  expect(FakeSocket.last.url).toContain('duration=30');
});

test('an address outside the topology gets the hint about an existing server', () => {
  render(<IperfPanel namespace="lab" source="host-0" nodes={[]} onClose={() => {}} />);
  fireEvent.change(screen.getByPlaceholderText('IP address'), { target: { value: '8.8.8.8' } });
  fireEvent.click(screen.getByText('Run'));
  frame(FakeSocket.last, {
    type: 'error',
    message:
      '"8.8.8.8" is not an address of a node in the namespace; run your own server there and pass server=none',
  });
  expect(screen.getByText(/choose Already running/)).toBeInTheDocument();
});

test('a run opens the socket with the options and draws what arrives', () => {
  render(
    <IperfPanel namespace="lab" source="host-0" sourceLabel="host" nodes={[]} onClose={() => {}} />
  );
  fireEvent.change(screen.getByPlaceholderText('IP address'), {
    target: { value: '192.168.3.10' },
  });
  fireEvent.click(screen.getByText('UDP'));
  fireEvent.change(screen.getByPlaceholderText(/offered rate/), { target: { value: '20M' } });
  fireEvent.click(screen.getByText('Run'));
  const ws = FakeSocket.last;
  expect(ws.url).toContain('/iperf/ws/lab/host-0?');
  expect(ws.url).toContain('dest=192.168.3.10');
  expect(ws.url).toContain('proto=udp');
  expect(ws.url).toContain('bandwidth=20M');
  expect(ws.url).toContain('server=auto');
  expect(screen.getByText('Stop')).toBeInTheDocument();

  frame(ws, { type: 'status', state: 'starting' });
  expect(screen.getByText('starting toolbox…')).toBeInTheDocument();
  frame(ws, { type: 'meta', server: { pod: 'web-0', port: 5312 } });
  frame(ws, { type: 'status', state: 'running' });
  frame(ws, {
    type: 'interval',
    start: 0,
    end: 1,
    bytes: 2_500_000,
    bitsPerSecond: 20_000_000,
    packets: 76,
  });
  frame(ws, {
    type: 'interval',
    start: 1,
    end: 2,
    bytes: 2_500_000,
    bitsPerSecond: 19_500_000,
    packets: 75,
  });
  expect(document.querySelectorAll('.iperf-bar')).toHaveLength(2);
  frame(ws, {
    type: 'summary',
    seconds: 2,
    sentBytes: 5_000_000,
    sentBitsPerSecond: 20_000_000,
    receivedBytes: 4_980_000,
    receivedBitsPerSecond: 19_920_000,
    jitterMs: 0.0421,
    packets: 151,
    lostPackets: 1,
    lostPercent: 0.66,
    cpuClientPercent: 1.2,
    cpuServerPercent: 0.8,
  });
  frame(ws, { type: 'status', state: 'done' });
  expect(screen.getByText('done')).toBeInTheDocument();
  expect(screen.getByText('0.042 ms')).toBeInTheDocument();
  expect(screen.getByText(/1 of 151 datagrams/)).toBeInTheDocument();
  expect(screen.getByText('web-0:5312')).toBeInTheDocument();
  expect(screen.getByText('Run')).toBeInTheDocument();
});

test('an error frame is shown and the run can be cleared', () => {
  render(<IperfPanel namespace="lab" source="host-0" nodes={[]} onClose={() => {}} />);
  fireEvent.change(screen.getByPlaceholderText('IP address'), { target: { value: '8.8.8.8' } });
  fireEvent.click(screen.getByText('Run'));
  frame(FakeSocket.last, { type: 'error', message: 'not an address of a node' });
  expect(screen.getByText('not an address of a node')).toBeInTheDocument();
  fireEvent.click(screen.getByText('Clear'));
  expect(screen.queryByText('not an address of a node')).toBeNull();
  expect(screen.getByText('ready')).toBeInTheDocument();
});
