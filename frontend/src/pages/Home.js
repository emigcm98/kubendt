import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import Modal from 'react-modal';
import AlertModal from '../components/AlertModal';
import AppHeader from '../components/AppHeader';
import DeleteExtrasCheckboxes from '../components/DeleteExtrasCheckboxes';
import ErrorModal from '../components/ErrorModal';
import HomeSide from '../components/HomeSide';
import HoverCard from '../components/HoverCard';
import K8sNodeInfoPanel from '../components/K8sNodeInfoPanel';
import ShareNamespaceModal from '../components/ShareNamespaceModal';
import { useAuth } from '../auth/AuthContext';
import { API_BASE_URL } from '../config';
import { LEVEL_ORDER, age, createdDate, matchesFilter, peopleOf, shortDate } from './homeRows';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import { ReactComponent as LoadingIcon } from '../assets/images/icons/loading.svg';
import { ReactComponent as SearchIcon } from '../assets/images/icons/search.svg';
import { ReactComponent as PlusIcon } from '../assets/images/icons/plus.svg';
import { ReactComponent as CloseIcon } from '../assets/images/icons/close.svg';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as GaugeIcon } from '../assets/images/icons/gauge.svg';
import { ReactComponent as TopologyIcon } from '../assets/images/icons/topology.svg';
import { ReactComponent as ChevronIcon } from '../assets/images/icons/chevron-right.svg';
import { ReactComponent as MoreIcon } from '../assets/images/icons/more.svg';
import { ReactComponent as ShareIcon } from '../assets/images/icons/users.svg';
import { ReactComponent as FolderIcon } from '../assets/images/icons/folder.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import '../components/Dialog.css';
import './Home.css';

Modal.setAppElement('#root');

// Names the UI routes as pages of its own; the backend refuses them too.
const RESERVED_NAMESPACE_NAMES = ['drivers', 'users', 'state', 'cluster', 'profile', 'settings'];

const SORT_KEYS = { field: 'kubendt.home.nsSortField', dir: 'kubendt.home.nsSortDir' };
const stored = (key, fallback) => {
  try {
    return localStorage.getItem(key) || fallback;
  } catch {
    return fallback;
  }
};

// Usage is read when a row's gauge is hovered, never for the whole list, and
// kept a little so sweeping over the rows does not ask again.
const USAGE_TTL = 15000;
const usageCache = new Map();

const UsageBody = ({ namespace }) => {
  const cached = usageCache.get(namespace);
  const fresh = cached && Date.now() - cached.at < USAGE_TTL;
  const [state, setState] = useState(fresh ? cached.state : { loading: true });
  useEffect(() => {
    if (fresh) return undefined;
    const ctl = new AbortController();
    fetch(`${API_BASE_URL}/namespaces/metrics/${namespace}`, { signal: ctl.signal })
      .then(async (res) => {
        const data = await res.json().catch(() => ({}));
        const next = res.ok && data.available !== false ? { data } : { unavailable: true };
        usageCache.set(namespace, { at: Date.now(), state: next });
        setState(next);
      })
      .catch((e) => e.name !== 'AbortError' && setState({ unavailable: true }));
    return () => ctl.abort();
  }, [namespace, fresh]);

  if (state.loading) return <div className="hover-card-head">Reading usage…</div>;
  if (state.unavailable) {
    return (
      <div className="home-usage-none">No usage figures, metrics-server is not answering.</div>
    );
  }
  const d = state.data;
  const top = [...(d.pods || [])].sort((a, b) => (b.cpu_milli || 0) - (a.cpu_milli || 0));
  return (
    <div className="home-usage">
      <div className="hover-card-head">
        Usage right now <span>{d.pod_count || 0} pods</span>
      </div>
      <div className="home-usage-big">
        <div>
          <small>CPU</small>
          <b>{((d.total_cpu_milli || 0) / 1000).toFixed(2)}</b> vCPU
        </div>
        <div>
          <small>Memory</small>
          <b>{d.total_memory_mib || 0}</b> MiB
        </div>
      </div>
      {top.slice(0, 3).map((p) => (
        <div className="hover-card-row" key={p.pod}>
          <span>{p.pod}</span>
          <span>
            {((p.cpu_milli || 0) / 1000).toFixed(2)} vCPU · {p.memory_mib || 0} MiB
          </span>
        </div>
      ))}
    </div>
  );
};

const Person = ({ name, level, owner, me }) => (
  <span
    className={`ns-person${owner ? ' ns-person-owner' : ''}${name === me ? ' ns-person-you' : ''}`}
  >
    <i>{name.charAt(0).toUpperCase()}</i>
    {name === me ? 'you' : name}
    <em>{level}</em>
  </span>
);

const People = ({ ns, me }) => {
  const p = peopleOf(ns, me);
  return (
    <>
      {p.owner ? (
        <Person name={p.owner} level="owner" owner me={me} />
      ) : (
        <span
          className="ns-person ns-person-none"
          title="Made before there were accounts. Give it an owner with Share or hand over."
        >
          no owner
        </span>
      )}
      {p.self && <Person name={p.self[0]} level={p.self[1]} me={me} />}
      {p.named.map(([n, l]) => (
        <Person key={n} name={n} level={l} me={me} />
      ))}
      {p.faces.length > 0 && (
        <HoverCard
          label={`Everyone in ${ns.name}`}
          trigger={
            <span className="ns-stack">
              {p.faces.map(([n]) => (
                <i key={n}>{n.charAt(0).toUpperCase()}</i>
              ))}
              {p.extra > 0 && <span className="ns-more">+{p.extra}</span>}
            </span>
          }
        >
          {() => (
            <div className="home-people">
              <div className="hover-card-head">
                {p.total} people <span>by level</span>
              </div>
              <div className="hover-card-row">
                <span>owner</span>
                <span>{p.owner || 'nobody'}</span>
              </div>
              {LEVEL_ORDER.filter((l) => p.byLevel[l]).map((l) => (
                <div className="hover-card-row" key={l}>
                  <span>
                    {l} · {p.byLevel[l].length}
                  </span>
                  <span>{p.byLevel[l].join(', ')}</span>
                </div>
              ))}
            </div>
          )}
        </HoverCard>
      )}
      {!p.self && !p.named.length && !p.faces.length && p.owner && (
        <span className="ns-sub">only the owner</span>
      )}
    </>
  );
};

const Home = () => {
  const navigate = useNavigate();
  const auth = useAuth();
  const me = auth.identity || auth.username || '';
  const [namespaces, setNamespaces] = useState([]);
  const [loading, setLoading] = useState(false);
  // null until known; false when the backend has no kubeconfig.
  const [kubeConfigured, setKubeConfigured] = useState(null);
  const [search, setSearch] = useState('');
  const [sortField, setSortField] = useState(() => stored(SORT_KEYS.field, 'name'));
  const [sortDir, setSortDir] = useState(() => stored(SORT_KEYS.dir, 'asc'));
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [newNamespaceName, setNewNamespaceName] = useState('');
  const [creatingNamespace, setCreatingNamespace] = useState(false);
  const [nsNameValidation, setNsNameValidation] = useState(null);
  const [errorMessage, setErrorMessage] = useState('');
  const [namespaceWarningOpen, setNamespaceWarningOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deletePositionsChecked, setDeletePositionsChecked] = useState(false);
  const [deleteFilesChecked, setDeleteFilesChecked] = useState(false);
  const [deletingNamespace, setDeletingNamespace] = useState(false);
  const [shareTarget, setShareTarget] = useState('');
  const [nodeOpen, setNodeOpen] = useState(null);

  // Kubernetes RFC 1123 label rules: lowercase alphanumeric and '-',
  // must start and end with alphanumeric, max 63 chars.
  const K8S_NAMESPACE_REGEX = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
  const validateNamespaceName = (name) => {
    if (!name) return { ok: null, message: '' };
    if (name.length > 63) return { ok: false, message: 'Too long (max 63 characters).' };
    if (RESERVED_NAMESPACE_NAMES.includes(name))
      return { ok: false, message: `'${name}' is a page of the UI, pick another name.` };
    if (/[A-Z]/.test(name)) return { ok: false, message: 'Use lowercase letters only.' };
    if (/[^a-z0-9-]/.test(name))
      return { ok: false, message: "Only lowercase letters, numbers and '-' are allowed." };
    if (name.startsWith('-') || name.endsWith('-'))
      return { ok: false, message: 'Must start and end with a letter or number.' };
    if (!K8S_NAMESPACE_REGEX.test(name)) return { ok: false, message: 'Invalid name (RFC 1123).' };
    return { ok: true, message: '' };
  };

  // Per-rule live checks for the rule list shown in the modal.
  const namespaceRules = (raw) => {
    const name = (raw || '').trim();
    return [
      {
        id: 'len',
        label: 'Between 1 and 63 characters',
        ok: name.length >= 1 && name.length <= 63,
      },
      {
        id: 'chars',
        label: "Only lowercase letters, numbers and '-'",
        ok: /^[a-z0-9-]+$/.test(name),
      },
      {
        id: 'bounds',
        label: 'Starts and ends with a letter or number',
        ok: /^[a-z0-9].*[a-z0-9]$/.test(name) || /^[a-z0-9]$/.test(name),
      },
      { id: 'rfc', label: 'Matches Kubernetes RFC 1123 label', ok: K8S_NAMESPACE_REGEX.test(name) },
    ];
  };

  const fetchNamespaces = async () => {
    try {
      setLoading(true);
      const res = await fetch(`${API_BASE_URL}/namespaces`);
      if (res.status === 503) {
        setKubeConfigured(false);
        setNamespaces([]);
        return;
      }
      const data = await res.json();
      setKubeConfigured(true);
      setNamespaces(
        (data.namespaces || []).filter(
          (ns) => ns.status === 'Active' || ns.status === 'Terminating'
        )
      );
    } catch (err) {
      console.error('Error fetching namespaces:', err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchNamespaces();
    const interval = setInterval(fetchNamespaces, 30000);
    return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSort = (field) => {
    const dir =
      field === sortField
        ? sortDir === 'asc'
          ? 'desc'
          : 'asc'
        : field === 'created'
          ? 'desc'
          : 'asc';
    setSortField(field);
    setSortDir(dir);
    try {
      localStorage.setItem(SORT_KEYS.field, field);
      localStorage.setItem(SORT_KEYS.dir, dir);
    } catch {
      /* localStorage unavailable */
    }
  };

  const openNamespace = (ns) => {
    if (ns.status === 'Terminating') {
      setNamespaceWarningOpen(true);
      return;
    }
    navigate(`/${ns.name}`);
  };

  const openCreate = () => {
    setNsNameValidation(null);
    setNewNamespaceName('');
    setIsModalOpen(true);
  };

  const handleCreateNamespace = async () => {
    const ns = newNamespaceName.trim();
    if (!ns) {
      setErrorMessage('Namespace name cannot be empty.');
      return;
    }
    setCreatingNamespace(true);
    try {
      const res = await fetch(`${API_BASE_URL}/namespaces/`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ namespace: ns }),
      });
      if (res.ok) {
        setIsModalOpen(false);
        setNewNamespaceName('');
        setTimeout(async () => {
          await fetchNamespaces();
          navigate(`/${ns}`);
        }, 100);
      } else {
        const data = await res.json();
        setErrorMessage(data.error || 'Error creating namespace');
      }
    } catch (err) {
      console.error('Error creating namespace:', err);
      setErrorMessage('Error creating namespace');
    } finally {
      setCreatingNamespace(false);
    }
  };

  const handleConfirmDeleteNamespace = async () => {
    if (!deleteTarget) return;
    setDeletingNamespace(true);
    try {
      const params = new URLSearchParams();
      if (deletePositionsChecked) params.set('deletePositions', 'true');
      if (deleteFilesChecked) params.set('deleteFiles', 'true');
      const qs = params.toString() ? `?${params.toString()}` : '';
      const res = await fetch(`${API_BASE_URL}/namespaces/${deleteTarget.name}${qs}`, {
        method: 'DELETE',
      });
      if (!res.ok) {
        const data = await res.json().catch(() => null);
        setErrorMessage(data?.error || `Could not delete namespace '${deleteTarget.name}'`);
      }
      setDeleteTarget(null);
      // The API needs a moment to flip the namespace to Terminating.
      setTimeout(fetchNamespaces, 250);
    } catch (err) {
      console.error('Error deleting namespace:', err);
      setErrorMessage(err.message || 'Error deleting namespace');
      setDeleteTarget(null);
    } finally {
      setDeletingNamespace(false);
    }
  };

  const rows = namespaces
    .filter((ns) => matchesFilter(ns, search))
    .sort((a, b) => {
      const cmp =
        sortField === 'created'
          ? (createdDate(a)?.getTime() || 0) - (createdDate(b)?.getTime() || 0)
          : a.name.localeCompare(b.name);
      return sortDir === 'asc' ? cmp : -cmp;
    });

  const showPeople = auth.enabled;
  const canOwn = (ns) => !auth.enabled || ns.level === 'owner';
  const arrow = (field) => (sortField === field ? (sortDir === 'asc' ? ' ↑' : ' ↓') : '');

  const rowMenu = (ns) => {
    const terminating = ns.status === 'Terminating';
    return (close) => (
      <div className="ns-menu">
        <button
          type="button"
          onClick={() => {
            close();
            openNamespace(ns);
          }}
        >
          <ChevronIcon className="app-icon" aria-hidden="true" /> Open
        </button>
        <button type="button" disabled={terminating} onClick={() => navigate(`/${ns.name}/files`)}>
          <FolderIcon className="app-icon" aria-hidden="true" /> Files
        </button>
        {auth.enabled && (
          <button
            type="button"
            disabled={terminating}
            onClick={() => {
              close();
              setShareTarget(ns.name);
            }}
          >
            <ShareIcon className="app-icon" aria-hidden="true" />
            {ns.level === 'owner' ? 'Share or hand over' : 'Who is in'}
          </button>
        )}
        {canOwn(ns) && (
          <button
            type="button"
            className="ns-menu-danger"
            disabled={terminating}
            onClick={() => {
              close();
              setDeleteTarget(ns);
              setDeletePositionsChecked(false);
              setDeleteFilesChecked(false);
            }}
          >
            <TrashIcon className="app-icon" aria-hidden="true" /> Delete
          </button>
        )}
      </div>
    );
  };

  const renderRow = (ns) => {
    const terminating = ns.status === 'Terminating';
    const created = createdDate(ns);
    return (
      <div
        key={ns.name}
        className={`ns-row${terminating ? ' ns-row-terminating' : ''}`}
        role="link"
        tabIndex={0}
        onClick={() => openNamespace(ns)}
        onKeyDown={(e) => e.key === 'Enter' && e.target === e.currentTarget && openNamespace(ns)}
      >
        <div className="ns-col-name" title={ns.name}>
          <span
            className={`ns-dot${terminating ? ' ns-dot-terminating' : ns.has_topology ? '' : ' ns-dot-empty'}`}
          />
          <span className="ns-name">{ns.name}</span>
        </div>
        {showPeople && (
          <div className="ns-col-people">
            <People ns={ns} me={me} />
          </div>
        )}
        <div className="ns-col-lab">
          {terminating ? (
            <span className="ns-terminating">Terminating</span>
          ) : ns.has_topology ? (
            <span className="ns-lab">
              <TopologyIcon className="app-icon" aria-hidden="true" />
              <span>
                <b>{ns.pods || 0}</b> {ns.pods === 1 ? 'pod' : 'pods'} · <b>{ns.links || 0}</b>{' '}
                {ns.links === 1 ? 'link' : 'links'}
              </span>
            </span>
          ) : (
            <span className="ns-empty">
              <TopologyIcon className="app-icon" aria-hidden="true" />
              No topology
            </span>
          )}
        </div>
        <div className="ns-col-created" title={created ? created.toLocaleString() : undefined}>
          {shortDate(created)}
          <span className="ns-sub">{age(created)}</span>
        </div>
        <div className="ns-col-acts">
          {!terminating && ns.has_topology ? (
            <HoverCard
              align="end"
              label={`Usage of ${ns.name}`}
              trigger={
                <span className="ns-icon-btn" title="Usage, read when hovered">
                  <GaugeIcon className="app-icon" aria-hidden="true" />
                </span>
              }
            >
              {() => <UsageBody namespace={ns.name} />}
            </HoverCard>
          ) : (
            <span className="ns-icon-btn ns-icon-btn-off" aria-hidden="true" />
          )}
          <HoverCard
            openOn="click"
            align="end"
            label={`More on ${ns.name}`}
            trigger={
              <span className="ns-icon-btn">
                <MoreIcon className="app-icon" aria-hidden="true" />
              </span>
            }
          >
            {rowMenu(ns)}
          </HoverCard>
          <ChevronIcon className="app-icon ns-go" aria-hidden="true" />
        </div>
      </div>
    );
  };

  return (
    <div className="home-page">
      <AppHeader links />

      {kubeConfigured === false && (
        <div className="home-notice" role="alert">
          <WarningIcon className="app-icon" aria-hidden="true" />
          {auth.isAdmin ? (
            <span>
              <strong>No kubeconfig loaded.</strong> Load one on the{' '}
              <button type="button" onClick={() => navigate('/cluster')}>
                cluster page
              </button>{' '}
              to start.
            </span>
          ) : (
            <span>
              <strong>KubeNDT has no cluster yet.</strong> An admin has to load a kubeconfig.
            </span>
          )}
        </div>
      )}

      <main className="home-main">
        <section className={`home-list${showPeople ? '' : ' home-list-no-people'}`}>
          <div className="home-toolbar">
            <h2>
              Namespaces <span className="home-count">{namespaces.length}</span>
            </h2>
            <label className="home-filter">
              <SearchIcon className="app-icon" aria-hidden="true" />
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={showPeople ? 'Filter by name or person…' : 'Filter by name…'}
                aria-label="Filter namespaces"
              />
              {search && (
                <button type="button" onClick={() => setSearch('')} title="Clear filter">
                  <CloseIcon className="app-icon" />
                </button>
              )}
            </label>
            <div className="home-seg" role="group" aria-label="Sort namespaces">
              <button
                type="button"
                className={sortField === 'name' ? 'on' : ''}
                onClick={() => handleSort('name')}
                aria-pressed={sortField === 'name'}
              >
                Name{arrow('name')}
              </button>
              <button
                type="button"
                className={sortField === 'created' ? 'on' : ''}
                onClick={() => handleSort('created')}
                aria-pressed={sortField === 'created'}
              >
                Created{arrow('created')}
              </button>
            </div>
            <button
              type="button"
              className="home-icon-btn"
              onClick={fetchNamespaces}
              disabled={loading || kubeConfigured === false}
              title="Refresh the list"
            >
              <RefreshIcon className="app-icon" aria-hidden="true" />
            </button>
            {auth.canCreateNamespaces && (
              <button
                type="button"
                className="home-new"
                onClick={openCreate}
                disabled={kubeConfigured === false}
              >
                <PlusIcon className="app-icon" aria-hidden="true" /> New namespace
              </button>
            )}
          </div>
          {auth.enabled && (
            <p className="home-scope">
              {auth.isAdmin ? (
                <>
                  As an admin you have the <b>owner</b> level in every namespace.
                </>
              ) : (
                'Yours and those shared with you.'
              )}
            </p>
          )}
          <div className="ns-row ns-head" aria-hidden="true">
            <span>Namespace</span>
            {showPeople && <span>Who is in</span>}
            <span>Lab</span>
            <span className="ns-col-created">Created</span>
            <span />
          </div>
          <div className="ns-body">
            {loading && namespaces.length === 0 && <p className="ns-none">Loading namespaces…</p>}
            {!loading && namespaces.length === 0 && kubeConfigured !== false && (
              <p className="ns-none">
                {auth.canCreateNamespaces
                  ? 'No namespaces yet. Start one with New namespace.'
                  : 'No namespaces yet. Ask an owner to share one with you.'}
              </p>
            )}
            {namespaces.length > 0 && rows.length === 0 && (
              <p className="ns-none">Nothing matches “{search}”.</p>
            )}
            {rows.map(renderRow)}
          </div>
        </section>

        <HomeSide
          admin={auth.isAdmin}
          accounts={auth.enabled}
          namespaces={namespaces}
          me={me}
          onOpen={openNamespace}
          onOpenTokens={() => navigate('/profile#tokens')}
          onOpenNode={setNodeOpen}
        />
      </main>

      {nodeOpen && <K8sNodeInfoPanel nodeName={nodeOpen} onClose={() => setNodeOpen(null)} />}
      {shareTarget && (
        <ShareNamespaceModal
          namespace={shareTarget}
          readOnly={namespaces.find((n) => n.name === shareTarget)?.level !== 'owner'}
          onClose={() => setShareTarget('')}
          onChanged={() => fetchNamespaces()}
        />
      )}

      {/* Modal to create namespace */}
      <Modal
        isOpen={isModalOpen}
        onRequestClose={() => !creatingNamespace && setIsModalOpen(false)}
        className="dlg create-namespace-modal"
        overlayClassName="dlg-overlay"
        contentLabel="New namespace"
        shouldCloseOnEsc={!creatingNamespace}
        shouldCloseOnOverlayClick={!creatingNamespace}
      >
        <div className="dlg-head">
          <PlusIcon className="app-icon" aria-hidden="true" />
          <h2 className="dlg-title">New namespace</h2>
          <button
            className="dlg-close"
            title="Close"
            onClick={() => setIsModalOpen(false)}
            disabled={creatingNamespace}
          >
            <CloseIcon className="app-icon" />
          </button>
        </div>

        <div className="dlg-body">
          <p className="dlg-text dlg-muted ns-modal-intro">
            A namespace holds one lab, its pods, links and files.
          </p>

          <label htmlFor="ns-input" className="dlg-label">
            Name
          </label>
          <div className="ns-input-wrapper">
            <input
              id="ns-input"
              className="dlg-input"
              type="text"
              value={newNamespaceName}
              onChange={(e) => {
                const v = e.target.value;
                setNewNamespaceName(v);
                setNsNameValidation(validateNamespaceName(v.trim()));
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && nsNameValidation?.ok) handleCreateNamespace();
              }}
              placeholder="e.g., my-namespace"
              disabled={creatingNamespace}
              autoFocus
              aria-invalid={nsNameValidation?.ok === false || undefined}
              maxLength={63}
            />
            <span
              className={`ns-input-counter${newNamespaceName.length > 63 ? ' ns-input-counter-over' : ''}`}
              title="Character count (max 63)"
            >
              {newNamespaceName.length}/63
            </span>
          </div>

          <ul className="ns-rules" aria-label="Namespace name rules">
            {namespaceRules(newNamespaceName).map((r) => {
              const state = !newNamespaceName.trim() ? 'pending' : r.ok ? 'ok' : 'fail';
              return (
                <li key={r.id} className={`ns-rule ns-rule-${state}`}>
                  <span className="ns-rule-icon" aria-hidden="true">
                    {state === 'ok' ? '✓' : state === 'fail' ? '✕' : '•'}
                  </span>
                  <span className="ns-rule-text">{r.label}</span>
                </li>
              );
            })}
          </ul>

          {newNamespaceName.trim() &&
            nsNameValidation?.ok !== false &&
            namespaces.some((n) => n.name === newNamespaceName.trim()) && (
              <p className="ns-dup-warning" role="alert">
                <WarningIcon className="app-icon" aria-hidden="true" /> “{newNamespaceName.trim()}”
                already exists, pick another name.
              </p>
            )}
        </div>

        <div className="dlg-foot">
          <button
            className="dlg-btn dlg-btn-quiet"
            onClick={() => setIsModalOpen(false)}
            disabled={creatingNamespace}
          >
            Cancel
          </button>
          <button
            className="dlg-btn dlg-btn-primary"
            onClick={handleCreateNamespace}
            disabled={
              creatingNamespace ||
              !newNamespaceName.trim() ||
              nsNameValidation?.ok === false ||
              namespaces.some((n) => n.name === newNamespaceName.trim())
            }
            title={nsNameValidation?.ok === false ? nsNameValidation.message : undefined}
          >
            {creatingNamespace ? (
              <>
                <LoadingIcon className="app-icon dlg-spin" aria-hidden="true" /> Creating…
              </>
            ) : (
              'Create'
            )}
          </button>
        </div>
      </Modal>

      <AlertModal
        isOpen={!!deleteTarget}
        type="warning"
        danger
        title="Delete namespace"
        message={
          <>
            Delete the namespace <strong>'{deleteTarget?.name}'</strong>?
          </>
        }
        confirmText="Delete namespace"
        cancelText="Cancel"
        loading={deletingNamespace}
        loadingText="Deleting…"
        onConfirm={handleConfirmDeleteNamespace}
        onCancel={() => !deletingNamespace && setDeleteTarget(null)}
        extraContent={
          <DeleteExtrasCheckboxes
            positionsChecked={deletePositionsChecked}
            onPositionsChange={setDeletePositionsChecked}
            filesChecked={deleteFilesChecked}
            onFilesChange={setDeleteFilesChecked}
          />
        }
      />

      <ErrorModal
        isOpen={errorMessage !== ''}
        message={errorMessage}
        onClose={() => setErrorMessage('')}
        title="Error"
      />

      <AlertModal
        isOpen={namespaceWarningOpen}
        type="warning"
        title="Namespace terminating"
        message="This namespace is still terminating and cannot be opened yet. Wait until it disappears or becomes active again."
        onConfirm={() => setNamespaceWarningOpen(false)}
        onCancel={() => setNamespaceWarningOpen(false)}
        confirmText="OK"
        cancelText="Cancel"
      />
    </div>
  );
};

export default Home;
