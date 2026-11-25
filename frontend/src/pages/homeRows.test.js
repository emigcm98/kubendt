import { age, matchesFilter, peopleOf, shortDate } from './homeRows';

const crowd = Object.fromEntries(
  ['bob', 'carol', 'dave', 'erin', 'frank', 'grace', 'heidi'].map((n) => [n, 'viewer'])
);

test('a lone guest is named, a crowd is stacked with the rest counted', () => {
  const one = peopleOf({ owner: 'alice', guests: { bob: 'editor' } }, 'admin');
  expect(one.named).toEqual([['bob', 'editor']]);
  expect(one.faces).toEqual([]);

  const many = peopleOf({ owner: 'alice', guests: { ...crowd, admin: 'editor' } }, 'admin');
  expect(many.self).toEqual(['admin', 'editor']);
  expect(many.named).toEqual([]);
  expect(many.faces.map(([n]) => n)).toEqual(['bob', 'carol', 'dave', 'erin']);
  expect(many.extra).toBe(3);
  expect(many.total).toBe(9);
  expect(many.byLevel.viewer).toHaveLength(7);
});

test('one guest beside oneself goes to the stack, so the row keeps its width', () => {
  const p = peopleOf({ owner: 'admin', guests: { alice: 'editor', bob: 'viewer' } }, 'alice');
  expect(p.self).toEqual(['alice', 'editor']);
  expect(p.named).toEqual([]);
  expect(p.faces).toEqual([['bob', 'viewer']]);
});

test('the filter looks at the name and at the people', () => {
  const ns = { name: 'ospf-lab', owner: 'alice', guests: { bob: 'viewer' } };
  expect(matchesFilter(ns, 'OSPF')).toBe(true);
  expect(matchesFilter(ns, 'bob')).toBe(true);
  expect(matchesFilter(ns, 'carol')).toBe(false);
});

test('dates read short and ages round to the unit that matters', () => {
  const now = new Date('2026-11-25T12:00:00Z');
  expect(shortDate(new Date('2026-11-19T10:00:00Z'), now)).toBe('19 Nov');
  expect(shortDate(new Date('2025-03-02T10:00:00Z'), now)).toBe('2 Mar 2025');
  expect(age(new Date('2026-11-25T11:59:30Z'), now)).toBe('just now');
  expect(age(new Date('2026-11-25T10:00:00Z'), now)).toBe('2 hours');
  expect(age(new Date('2026-11-20T12:00:00Z'), now)).toBe('5 days');
  expect(age(new Date('2026-10-14T12:00:00Z'), now)).toBe('6 weeks');
});
