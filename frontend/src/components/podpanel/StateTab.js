import React, { useCallback, useEffect, useState } from 'react';
import { API_BASE_URL } from '../../config';
import { capabilityShortLabel } from './history';
import { ReactComponent as RefreshIcon } from '../../assets/images/icons/refresh.svg';
import { ReactComponent as ChevronDown } from '../../assets/images/icons/chevron-down.svg';
import { ReactComponent as ChevronRight } from '../../assets/images/icons/chevron-right.svg';

// Live reads of the node, what GET /pods/inspect offers for its driver:
// one collapsible section per inspection, grouped by capability, read
// when opened and on demand, never on a timer. Each row shows its first
// column as the headline and the rest as small fields, which is what fits
// the panel's width.
const StateTab = ({ namespace, pod, driverCaps }) => {
  const [inspections, setInspections] = useState([]);
  const [loadingList, setLoadingList] = useState(false);
  const [errorList, setErrorList] = useState(null);
  const [open, setOpen] = useState(() => new Set());
  const [data, setData] = useState({});
  const [lastRead, setLastRead] = useState(null);

  const podName = pod?.name;

  const readOne = useCallback(
    async (what) => {
      setData((prev) => ({
        ...prev,
        [what]: { ...(prev[what] || {}), loading: true, error: null },
      }));
      try {
        const res = await fetch(
          `${API_BASE_URL}/pods/inspect/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}?what=${encodeURIComponent(what)}`
        );
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(body?.error || `Error ${res.status}`);
        setData((prev) => ({
          ...prev,
          [what]: {
            rows: body.rows || [],
            columns: body.columns || [],
            note: body.note || '',
            loading: false,
            error: null,
          },
        }));
        setLastRead(new Date());
      } catch (err) {
        setData((prev) => ({
          ...prev,
          [what]: { ...(prev[what] || {}), loading: false, error: err.message || 'Error' },
        }));
      }
    },
    [namespace, podName]
  );

  // The list of what the driver offers, then the first section open.
  useEffect(() => {
    let cancelled = false;
    setInspections([]);
    setData({});
    setOpen(new Set());
    setLastRead(null);
    if (!namespace || !podName) return undefined;
    setLoadingList(true);
    setErrorList(null);
    fetch(
      `${API_BASE_URL}/pods/inspect/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}`
    )
      .then(async (res) => {
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(body?.error || `Error ${res.status}`);
        return body.inspections || [];
      })
      .then((list) => {
        if (cancelled) return;
        setInspections(list);
        if (list.length > 0) {
          setOpen(new Set([list[0].id]));
          readOne(list[0].id);
        }
      })
      .catch((err) => {
        if (!cancelled) setErrorList(err.message || 'Error loading the inspections');
      })
      .finally(() => {
        if (!cancelled) setLoadingList(false);
      });
    return () => {
      cancelled = true;
    };
  }, [namespace, podName, readOne]);

  const toggle = (what) => {
    setOpen((prev) => {
      const next = new Set(prev);
      if (next.has(what)) {
        next.delete(what);
      } else {
        next.add(what);
        if (!data[what]) readOne(what);
      }
      return next;
    });
  };

  const refreshOpen = () => {
    open.forEach((what) => readOne(what));
  };

  const groups = [];
  inspections.forEach((insp) => {
    let g = groups.find((x) => x.id === insp.capability);
    if (!g) {
      const cap = (driverCaps || []).find((c) => c.id === insp.capability);
      g = {
        id: insp.capability,
        label: capabilityShortLabel(cap || { id: insp.capability }),
        items: [],
      };
      groups.push(g);
    }
    g.items.push(insp);
  });

  return (
    <div className="state-tab">
      <div className="state-toolbar">
        <span className="state-toolbar-title">Live state</span>
        <span className="state-toolbar-note">
          {lastRead ? `read at ${lastRead.toLocaleTimeString()}` : 'read on open'}
        </span>
        <button
          className="driver-nav-btn"
          title="Read the open sections again"
          onClick={refreshOpen}
          disabled={open.size === 0}
        >
          <RefreshIcon className="app-icon" />
        </button>
      </div>
      <div className="state-body">
        {loadingList && <p className="links-loading">Loading...</p>}
        {errorList && <p className="links-error">{errorList}</p>}
        {!loadingList && !errorList && inspections.length === 0 && (
          <p className="no-interfaces">Nothing to read on this node</p>
        )}
        {groups.map((g) => (
          <div className="state-group" key={g.id}>
            <div className="state-group-title">{g.label}</div>
            {g.items.map((insp) => {
              const isOpen = open.has(insp.id);
              const d = data[insp.id] || {};
              const count = Array.isArray(d.rows) ? d.rows.length : null;
              return (
                <div className={`state-section${isOpen ? ' open' : ''}`} key={insp.id}>
                  <div className="state-section-head" onClick={() => toggle(insp.id)}>
                    {isOpen ? (
                      <ChevronDown className="app-icon state-chevron" />
                    ) : (
                      <ChevronRight className="app-icon state-chevron" />
                    )}
                    <span className="state-section-label">{insp.label}</span>
                    {count !== null && <span className="state-count">{count}</span>}
                    {isOpen && (
                      <button
                        className="state-refresh-btn"
                        title={`Read ${insp.label} again`}
                        onClick={(e) => {
                          e.stopPropagation();
                          readOne(insp.id);
                        }}
                        disabled={d.loading}
                      >
                        <RefreshIcon className="app-icon" />
                      </button>
                    )}
                  </div>
                  {isOpen && (
                    <div className="state-section-body">
                      {d.loading && !d.rows && <p className="links-loading">Reading...</p>}
                      {d.error && <p className="links-error">{d.error}</p>}
                      {d.rows && d.rows.length === 0 && !d.error && (
                        <p className="state-empty">{d.note || 'Nothing'}</p>
                      )}
                      {d.rows &&
                        d.rows.map((row, idx) => (
                          <InspectionRow
                            key={idx}
                            row={row}
                            columns={d.columns || insp.columns || []}
                          />
                        ))}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
};

const InspectionRow = ({ row, columns }) => {
  const [head, ...rest] = columns;
  const headline = head ? row[head.name] : '';
  return (
    <div className="state-row">
      <div className="state-row-head" title={head ? head.label : ''}>
        {headline || '—'}
      </div>
      <div className="state-row-fields">
        {rest.map((col) => {
          const value = row[col.name];
          if (value === undefined || value === '' || value === null) return null;
          return (
            <span className="state-field" key={col.name} title={col.description || col.label}>
              <em>{col.label}</em>
              {value === 'yes' ? '' : ` ${value}`}
            </span>
          );
        })}
      </div>
    </div>
  );
};

export default StateTab;
