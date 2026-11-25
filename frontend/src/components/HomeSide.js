import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { API_BASE_URL } from '../config';
import { Meter, meshnetDot, vcpu, gib, fixed } from './K8sBits';
import { ReactComponent as ChevronIcon } from '../assets/images/icons/chevron-right.svg';

const EXAMPLES_URL = 'https://github.com/emigcm98/kubendt/tree/main/deploy/examples';

// Reads a JSON endpoint now and then every `every` ms while mounted.
export const usePolled = (path, every) => {
  const [state, setState] = useState({ data: null, status: 0 });
  useEffect(() => {
    let alive = true;
    const read = async () => {
      try {
        const res = await fetch(`${API_BASE_URL}${path}`);
        const data = res.ok ? await res.json() : null;
        if (alive) setState({ data, status: res.status });
      } catch {
        if (alive) setState((s) => ({ ...s, status: -1 }));
      }
    };
    read();
    const id = every ? setInterval(read, every) : null;
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [path, every]);
  return state;
};

const meshnetPill = (m) => {
  if (!m || !m.state) return null;
  if (m.state === 'ok') return { cls: 'ok', text: `Meshnet ${m.ready}/${m.desired}` };
  if (m.state === 'degraded') return { cls: 'warn', text: `Meshnet ${m.ready}/${m.desired}` };
  if (m.state === 'missing') return { cls: 'bad', text: 'No Meshnet' };
  return null;
};

const ClusterCard = ({ onOpenNode }) => {
  const navigate = useNavigate();
  const { data, status } = usePolled('/cluster/status', 30000);
  const nodes = data?.nodes || [];
  const metrics = nodes.some((n) => n.cpu_usage && n.cpu_usage !== 'N/A');
  const mesh = meshnetPill(data?.meshnet);
  return (
    <section className="home-card home-card-grow">
      <h3 className="home-card-title">
        Cluster
        {data && (
          <span className={`k8s-pill k8s-pill-${data.ready === data.total ? 'ok' : 'warn'}`}>
            Ready {data.ready}/{data.total}
          </span>
        )}
        {mesh && <span className={`k8s-pill k8s-pill-${mesh.cls}`}>{mesh.text}</span>}
      </h3>
      {status === 503 && <p className="home-card-note">No kubeconfig loaded yet.</p>}
      {!data && status !== 503 && <p className="home-card-note">Reading the cluster…</p>}
      {data && (
        <>
          <div className="home-stats">
            <div>
              <small>Nodes</small>
              <b>{data.total}</b>
            </div>
            <div>
              <small>Avg CPU</small>
              <b>{metrics ? `${fixed(data.avg_cpu_percentage, 1)}%` : '?'}</b>
            </div>
            <div>
              <small>Avg RAM</small>
              <b>{metrics ? `${fixed(data.avg_memory_percentage, 1)}%` : '?'}</b>
            </div>
          </div>
          {!metrics && <p className="home-card-note">No metrics-server, so no usage figures.</p>}
          <div className="home-card-scroll">
            {nodes.map((n) => (
              <button
                type="button"
                key={n.name}
                className="home-node"
                onClick={() => onOpenNode(n.name)}
                title={`${n.name}, ${n.status}`}
              >
                <span className="home-node-top">
                  <b>{n.name}</b>
                  {(n.roles || []).slice(0, 1).map((r) => (
                    <span key={r} className="k8s-role">
                      {r}
                    </span>
                  ))}
                  {n.status !== 'Ready' && (
                    <span className="k8s-pill k8s-pill-bad">{n.status}</span>
                  )}
                  <span className="home-node-space" />
                  <span className={`k8s-dot k8s-dot-${meshnetDot(n.meshnet)}`} />
                  <span className="home-node-mesh">Meshnet</span>
                  <ChevronIcon className="app-icon home-node-go" aria-hidden="true" />
                </span>
                {metrics && (
                  <>
                    <span className="home-node-row">
                      <span>CPU</span>
                      <Meter value={n.cpu_percentage} />
                      <span>
                        {fixed(vcpu(n.cpu_usage), 2)} / {fixed(vcpu(n.cpu_capacity), 0)} vCPU
                      </span>
                    </span>
                    <span className="home-node-row">
                      <span>RAM</span>
                      <Meter value={n.memory_percentage} kind="ram" />
                      <span>
                        {fixed(gib(n.memory_usage), 1)} / {fixed(gib(n.memory_capacity), 1)} GiB
                      </span>
                    </span>
                  </>
                )}
              </button>
            ))}
          </div>
        </>
      )}
      <button type="button" className="home-card-link" onClick={() => navigate('/cluster')}>
        Open cluster, kubeconfig and context →
      </button>
    </section>
  );
};

const AccountsCard = () => {
  const navigate = useNavigate();
  const { data } = usePolled('/users', 60000);
  const users = data?.users || [];
  const by = (role) => users.filter((u) => u.role === role).length;
  const roles = [
    [by('owner'), 'owner', 'owners'],
    [by('admin'), 'admin', 'admins'],
    [by('user'), 'user', 'users'],
  ]
    .filter(([n]) => n)
    .map(([n, one, many]) => `${n} ${n === 1 ? one : many}`)
    .join(' · ');
  return (
    <section className="home-card">
      <h3 className="home-card-title">Accounts</h3>
      <div className="home-line">
        By role <span>{data ? roles : '…'}</span>
      </div>
      <div className="home-line">
        Waiting for a first sign-in{' '}
        <span>{users.filter((u) => u.must_change_password).length}</span>
      </div>
      <div className="home-line">
        Disabled <span>{users.filter((u) => u.disabled).length}</span>
      </div>
      <button type="button" className="home-card-link" onClick={() => navigate('/users')}>
        Manage accounts →
      </button>
    </section>
  );
};

const limitLine = (label, used, limit) => (
  <>
    <div className="home-line">
      {label}
      <span>{limit ? `${used} of ${limit}` : `${used}, no limit`}</span>
    </div>
    {limit > 0 && <Meter value={(used / limit) * 100} />}
  </>
);

export const QuotaCard = ({ namespaces, me }) => {
  const { data } = usePolled('/auth/quota', 60000);
  const q = data?.quota || {};
  const u = data?.usage || {};
  const mine = namespaces.filter((ns) => ns.owner === me);
  const biggest = mine.reduce((a, b) => ((b.pods || 0) > (a?.pods || 0) ? b : a), null);
  return (
    <section className="home-card">
      <h3 className="home-card-title">Your quota</h3>
      {!data && <p className="home-card-note">Reading…</p>}
      {data && (
        <>
          {limitLine('Namespaces you own', u.namespaces || 0, q.namespaces || 0)}
          {limitLine('Pods across them', u.pods || 0, q.pods || 0)}
          <div className="home-line">
            Pods in one namespace
            <span>{q.pods_per_namespace ? `up to ${q.pods_per_namespace}` : 'no limit'}</span>
          </div>
          {biggest && biggest.pods > 0 && (
            <p className="home-card-note">
              The biggest is {biggest.name} with {biggest.pods}.
            </p>
          )}
        </>
      )}
    </section>
  );
};

const SharedCard = ({ namespaces, me, onOpen }) => {
  const shared = namespaces.filter((ns) => ns.owner !== me);
  return (
    <section className="home-card home-card-grow">
      <h3 className="home-card-title">
        Shared with you <span className="home-count">{shared.length}</span>
      </h3>
      {shared.length === 0 && <p className="home-card-note">Nothing yet.</p>}
      <div className="home-card-scroll">
        {shared.map((ns) => (
          <button type="button" key={ns.name} className="home-shared" onClick={() => onOpen(ns)}>
            <span className="home-shared-name">
              <b title={ns.name}>{ns.name}</b>
              <span className="home-card-note">owned by {ns.owner || 'nobody'}</span>
            </span>
            <span className={`home-level home-level-${ns.level}`}>{ns.level}</span>
          </button>
        ))}
      </div>
    </section>
  );
};

const StartCard = ({ onOpenTokens }) => {
  const navigate = useNavigate();
  return (
    <section className="home-card">
      <h3 className="home-card-title">Start here</h3>
      <button type="button" className="home-start" onClick={onOpenTokens}>
        Your API tokens <span>Tokens →</span>
      </button>
      <button type="button" className="home-start" onClick={() => navigate('/drivers')}>
        What each node kind can do <span>Drivers →</span>
      </button>
      <a className="home-start" href={EXAMPLES_URL} target="_blank" rel="noopener noreferrer">
        Labs to start from <span>Examples →</span>
      </a>
    </section>
  );
};

// The column beside the namespaces: the cluster and the accounts for an
// admin, one's quota and what others share for everybody else.
const HomeSide = ({ admin, accounts, namespaces, me, onOpen, onOpenTokens, onOpenNode }) => (
  <aside className="home-side">
    {admin ? (
      <>
        <ClusterCard onOpenNode={onOpenNode} />
        {accounts && <AccountsCard />}
      </>
    ) : (
      <>
        <QuotaCard namespaces={namespaces} me={me} />
        <SharedCard namespaces={namespaces} me={me} onOpen={onOpen} />
        <StartCard onOpenTokens={onOpenTokens} />
      </>
    )}
  </aside>
);

export default HomeSide;
