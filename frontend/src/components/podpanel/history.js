// The history of a pod grouped by the capability of each action, in the
// order the driver lists its capabilities; actions the driver no longer
// declares go last under "Other".
export const groupHistoryByCapability = (history, driverCaps) => {
  const byAction = new Map();
  (driverCaps || []).forEach((cap) => {
    (cap.methods || []).forEach((m) => {
      if (m?.name && !byAction.has(m.name)) byAction.set(m.name, cap.id);
    });
  });
  const groups = new Map();
  (driverCaps || []).forEach((cap) =>
    groups.set(cap.id, { id: cap.id, label: cap.label || cap.id, entries: [] })
  );
  const other = { id: 'other', label: 'Other', entries: [] };
  (history || []).forEach((entry) => {
    const type = entry.action_type || entry?.action?.type || '';
    const capId = byAction.get(type);
    (capId && groups.get(capId) ? groups.get(capId) : other).entries.push(entry);
  });
  const out = [...groups.values()].filter((g) => g.entries.length > 0);
  if (other.entries.length > 0) out.push(other);
  return out;
};

// A capability label without the Go interface suffix, for chips and
// section titles: OSPFCapable reads as OSPF.
export const capabilityShortLabel = (cap) => {
  if (!cap) return '';
  if (cap.label) return cap.label;
  return String(cap.id || '').replace(/Capable$/, '');
};

// The short code of a capability, its interface name without the suffix:
// OSPFCapable reads as OSPF.
export const capabilityCode = (cap) => String(cap?.id || '').replace(/Capable$/, '');
