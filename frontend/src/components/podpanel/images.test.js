import { imageRef, imageKnown } from './images';

test('normalizes the registry, the library namespace and a missing tag', () => {
  expect(imageRef('alpine')).toEqual({ name: 'docker.io/library/alpine', version: ':latest' });
  expect(imageRef('docker.io/alpine:3.24.2')).toEqual({
    name: 'docker.io/library/alpine',
    version: ':3.24.2',
  });
  expect(imageRef('globocom/openvswitch@sha256:abc')).toEqual({
    name: 'docker.io/globocom/openvswitch',
    version: '@sha256:abc',
  });
  expect(imageRef('localhost:5000/kubendt/toolbox:1.8.0')).toEqual({
    name: 'localhost:5000/kubendt/toolbox',
    version: ':1.8.0',
  });
  expect(imageRef('localhost/vyos-router:dev')).toEqual({
    name: 'localhost/vyos-router',
    version: ':dev',
  });
});

test('matches the same name and tag, or the same digest, never a tag to a digest', () => {
  const known = [
    'quay.io/frrouting/frr:10.7.1',
    'globocom/openvswitch@sha256:abc',
    'alpine:3.24.2',
  ];
  expect(imageKnown(known, 'quay.io/frrouting/frr:10.7.1')).toBe(true);
  expect(imageKnown(known, 'quay.io/frrouting/frr:10.6.0')).toBe(false);
  expect(imageKnown(known, 'docker.io/library/alpine:3.24.2')).toBe(true);
  expect(imageKnown(known, 'alpine')).toBe(false);
  expect(imageKnown(known, 'docker.io/globocom/openvswitch@sha256:abc')).toBe(true);
  expect(imageKnown(known, 'globocom/openvswitch:latest')).toBe(false);
  expect(imageKnown([], 'alpine')).toBe(false);
});
