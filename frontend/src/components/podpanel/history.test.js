import { groupHistoryByCapability } from './history';

const caps = [
  {
    id: 'L3Capable',
    label: 'Level 3',
    methods: [{ name: 'set_ip' }, { name: 'set_default_route' }],
  },
  { id: 'OSPFCapable', label: 'OSPF Routing', methods: [{ name: 'ospf_add_network' }] },
  { id: 'NATCapable', label: 'NAT', methods: [{ name: 'enable_snat' }] },
];

test('groups the history by the capability of each action, in driver order, unknown last', () => {
  const history = [
    { id: 1, action_type: 'ospf_add_network' },
    { id: 2, action_type: 'set_ip' },
    { id: 3, action: { type: 'set_default_route' } },
    { id: 4, action_type: 'custom' },
  ];
  const groups = groupHistoryByCapability(history, caps);
  expect(groups.map((g) => g.id)).toEqual(['L3Capable', 'OSPFCapable', 'other']);
  expect(groups[0].entries.map((e) => e.id)).toEqual([2, 3]);
  expect(groups[1].entries.map((e) => e.id)).toEqual([1]);
  expect(groups[2].label).toBe('Other');
});

test('an empty history makes no groups', () => {
  expect(groupHistoryByCapability([], caps)).toEqual([]);
  expect(groupHistoryByCapability([{ id: 1, action_type: 'set_ip' }], [])[0].id).toBe('other');
});
