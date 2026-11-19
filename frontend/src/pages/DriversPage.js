import React, { useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { API_BASE_URL, SWAGGER_UI_URL } from '../config';
import '../components/NamespaceNavbar.css';
import './DriversPage.css';
import kubendtLogo from '../assets/images/kubendt-logo.svg';
import hostIcon from '../assets/images/nodes/host.svg';
import routerIcon from '../assets/images/nodes/router.svg';
import switchIcon from '../assets/images/nodes/switch.svg';
import { ReactComponent as HomeIcon } from '../assets/images/icons/home.svg';
import { ReactComponent as BoxIcon } from '../assets/images/icons/box.svg';
import { ReactComponent as BookIcon } from '../assets/images/icons/book.svg';

const TYPE_ORDER = ['router', 'switch', 'host', 'controller'];
const TYPE_ICONS = { router: routerIcon, switch: switchIcon, host: hostIcon, controller: hostIcon };
const TOOL_LABELS = { trace: 'Traceroute', iperf: 'Throughput test' };

const typeLabel = (type) =>
  type === 'switch' ? 'Switches' : `${type.charAt(0).toUpperCase()}${type.slice(1)}s`;

const typeRank = (type) => {
  const i = TYPE_ORDER.indexOf(type);
  return i < 0 ? TYPE_ORDER.length : i;
};

// Every driver as GET /drivers publishes it: what its nodes can do, read
// and run, for whoever writes a topology or an action.
const DriversPage = () => {
  const navigate = useNavigate();
  const { driver: wanted } = useParams();
  const [drivers, setDrivers] = useState(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let stopped = false;
    (async () => {
      try {
        const res = await fetch(`${API_BASE_URL}/drivers/`);
        if (!res.ok) throw new Error(`Error ${res.status}`);
        const data = await res.json();
        if (!stopped) setDrivers(Array.isArray(data.drivers) ? data.drivers : []);
      } catch (e) {
        if (!stopped) {
          setError(e.message || 'Could not load the drivers');
          setDrivers([]);
        }
      }
    })();
    return () => {
      stopped = true;
    };
  }, []);

  // By type in the order a lab is read, the default of each type first.
  const groups = useMemo(() => {
    if (!drivers) return [];
    const byType = {};
    for (const d of drivers) (byType[d.type] = byType[d.type] || []).push(d);
    return Object.keys(byType)
      .sort((a, b) => typeRank(a) - typeRank(b) || a.localeCompare(b))
      .map((type) => ({
        type,
        drivers: byType[type]
          .slice()
          .sort(
            (a, b) => Number(b.isDefault) - Number(a.isDefault) || a.name.localeCompare(b.name)
          ),
      }));
  }, [drivers]);

  const selected = useMemo(() => {
    if (!drivers || drivers.length === 0) return null;
    if (wanted) return drivers.find((d) => d.name === wanted) || null;
    return groups[0]?.drivers[0] || drivers[0];
  }, [drivers, groups, wanted]);

  return (
    <div className="drivers-root">
      <div className="navbar-container">
        <div className="navbar-left">
          <button
            type="button"
            className="navbar-brand"
            onClick={() => navigate('/')}
            title="KubeNDT"
          >
            <img src={kubendtLogo} alt="KubeNDT" className="navbar-brand-logo" />
          </button>
          <button
            className="navbar-button"
            onClick={() => navigate('/')}
            title="Back to the namespace list"
          >
            <HomeIcon className="app-icon" aria-hidden="true" />
            Home
          </button>
        </div>
        <div
          className="navbar-center navbar-center-static"
          title="Every driver and what its nodes can do"
        >
          <BoxIcon className="navbar-ns-icon" aria-hidden="true" />
          <div className="navbar-center-text">
            <span className="navbar-namespace">Drivers</span>
          </div>
        </div>
        <div className="navbar-right">
          <button
            className="navbar-button"
            onClick={() => window.open(SWAGGER_UI_URL, '_blank', 'noopener,noreferrer')}
            title="The REST API, in Swagger UI"
          >
            <BookIcon className="app-icon" aria-hidden="true" />
            Open API
          </button>
        </div>
      </div>

      <div className="drivers-page">
        <aside className="drivers-list">
          {drivers === null && <p className="drivers-note">Loading…</p>}
          {error && <p className="drivers-note drivers-error">{error}</p>}
          {groups.map((g) => (
            <div className="drivers-group" key={g.type}>
              <div className="drivers-group-title">{typeLabel(g.type)}</div>
              {g.drivers.map((d) => (
                <button
                  key={d.name}
                  type="button"
                  className={`drivers-item${selected?.name === d.name ? ' active' : ''}`}
                  onClick={() => navigate(`/drivers/${d.name}`)}
                >
                  <img src={TYPE_ICONS[d.type] || hostIcon} alt="" className="drivers-item-icon" />
                  <span className="drivers-item-name">{d.name}</span>
                  {d.isDefault && <span className="drivers-tag">default</span>}
                </button>
              ))}
            </div>
          ))}
        </aside>
        <main className="drivers-detail">
          {drivers && drivers.length > 0 && !selected && (
            <p className="drivers-note">
              No driver named <code>{wanted}</code>.
            </p>
          )}
          {selected && <DriverDetail driver={selected} />}
        </main>
      </div>
    </div>
  );
};

const DriverDetail = ({ driver }) => {
  const own = (driver.capabilities || []).filter((c) => !c.universal);
  const universal = (driver.capabilities || []).filter((c) => c.universal);
  const naming = driver.interfaceNameConstraints;
  return (
    <>
      <header className="drivers-head">
        <img src={TYPE_ICONS[driver.type] || hostIcon} alt="" className="drivers-head-icon" />
        <div>
          <h1 className="drivers-head-name">{driver.name}</h1>
          <div className="drivers-head-sub">
            <span>{driver.type}</span>
            {driver.isDefault && (
              <span className="drivers-tag">
                default for {typeLabel(driver.type).toLowerCase()}
              </span>
            )}
            {driver.aliases?.length > 0 && <span>also answers to {driver.aliases.join(', ')}</span>}
          </div>
        </div>
      </header>

      <dl className="drivers-facts">
        <dt>Executor</dt>
        <dd>
          <code>{driver.executor || 'kubectl'}</code>
        </dd>
        <dt>Interface naming</dt>
        <dd>
          {naming ? (
            <>
              <code>{naming.patternHuman || naming.pattern}</code>
              {naming.reserved?.length > 0 && (
                <>
                  {' '}
                  reserved <code>{naming.reserved.join(', ')}</code>
                </>
              )}
            </>
          ) : (
            'Linux default'
          )}
        </dd>
        <dt>Images tried</dt>
        <dd>
          {driver.knownImages?.length > 0 ? (
            driver.knownImages.map((img) => <code key={img}>{img}</code>)
          ) : (
            <span className="drivers-muted">any image</span>
          )}
        </dd>
        <dt>Tools</dt>
        <dd>
          {driver.tools?.length > 0 ? (
            driver.tools.map((t) => TOOL_LABELS[t] || t).join(', ')
          ) : (
            <span className="drivers-muted">none</span>
          )}
        </dd>
        <dt>Shells</dt>
        <dd>
          {driver.shells?.length > 0 ? (
            driver.shells.map((s) => s.label).join(', ')
          ) : (
            <span className="drivers-muted">none</span>
          )}
        </dd>
      </dl>

      <section className="drivers-caps">
        <h2 className="drivers-section-title">
          Capabilities <span className="drivers-count">{own.length}</span>
        </h2>
        {own.map((cap) => (
          <CapabilityCard key={cap.id} cap={cap} />
        ))}
      </section>

      {universal.length > 0 && (
        <section className="drivers-caps">
          <h2 className="drivers-section-title">On every node</h2>
          <p className="drivers-section-note">
            Run by the platform outside the driver, on any pod.
          </p>
          {universal.map((cap) => (
            <CapabilityCard key={cap.id} cap={cap} />
          ))}
        </section>
      )}
    </>
  );
};

// The sample parameters of an example, as a request would carry them.
const exampleParams = (params) =>
  Object.entries(params || {})
    .map(([k, v]) => `${k}=${Array.isArray(v) || typeof v === 'object' ? JSON.stringify(v) : v}`)
    .join('  ');

// A capability: its actions with their parameters and, unfolded, the
// commands the driver runs for the sample parameters of each.
const CapabilityCard = ({ cap }) => {
  const [showCommands, setShowCommands] = useState(false);
  const hasExamples = (cap.methods || []).some((m) => m.example);
  return (
    <article className="drivers-cap" id={cap.id}>
      <div className="drivers-cap-head">
        <h3 className="drivers-cap-title">{cap.label || cap.id}</h3>
        <code className="drivers-cap-id">{cap.id}</code>
        {hasExamples && (
          <button
            type="button"
            className="drivers-cap-toggle"
            onClick={() => setShowCommands((v) => !v)}
            title="The commands the driver runs, for sample parameters"
          >
            {showCommands ? 'Hide commands' : 'Show commands'}
          </button>
        )}
      </div>
      {cap.description && <p className="drivers-cap-desc">{cap.description}</p>}
      {cap.methods?.length > 0 && (
        <table className="drivers-table">
          <thead>
            <tr>
              <th>Action</th>
              <th>What it does</th>
              <th>Parameters</th>
            </tr>
          </thead>
          <tbody>
            {cap.methods.map((m) => (
              <React.Fragment key={m.name}>
                <tr>
                  <td>
                    <code>{m.name}</code>
                  </td>
                  <td>{m.label}</td>
                  <td>
                    {m.params?.length > 0 ? (
                      m.params.map((p) => (
                        <span key={p.name} className="drivers-param">
                          <code>{p.name}</code>{' '}
                          <em>
                            {p.type}
                            {p.required === false ? ', optional' : ''}
                          </em>
                        </span>
                      ))
                    ) : (
                      <span className="drivers-muted">none</span>
                    )}
                  </td>
                </tr>
                {showCommands && m.example && (
                  <tr className="drivers-example-row">
                    <td colSpan="3">
                      <div className="drivers-example">
                        {Object.keys(m.example.params || {}).length > 0 && (
                          <span className="drivers-example-params">
                            with {exampleParams(m.example.params)}
                          </span>
                        )}
                        {(m.example.commands || []).map((c, i) => (
                          <code key={i} className="drivers-example-cmd">
                            {c}
                          </code>
                        ))}
                      </div>
                    </td>
                  </tr>
                )}
              </React.Fragment>
            ))}
          </tbody>
        </table>
      )}
      {cap.inspections?.length > 0 && (
        <div className="drivers-inspections">
          <span className="drivers-inspections-label">Reads</span>
          {cap.inspections.map((i) => (
            <span
              key={i.id}
              className="drivers-inspection"
              title={(i.columns || []).map((c) => c.label).join(', ')}
            >
              {i.label}
            </span>
          ))}
        </div>
      )}
    </article>
  );
};

export default DriversPage;
