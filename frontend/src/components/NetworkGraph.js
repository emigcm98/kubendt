import React, { useEffect, useRef, useState } from 'react';
import Modal from 'react-modal';
import { useNodesState, useEdgesState, ReactFlowProvider } from 'reactflow';
import 'reactflow/dist/style.css';
import {
  forceSimulation,
  forceManyBody,
  forceLink,
  forceCenter,
  forceCollide,
  forceX,
  forceY,
} from 'd3-force';
import PodInfoPanel from './PodInfoPanel';
import ExternalNodeInfoPanel from './ExternalNodeInfoPanel';
import LinkInfoPanel from './LinkInfoPanel';
import TopologyInputModal from './TopologyInputModal';
import PodInteractiveShellModal from './PodInteractiveShellModal';
import CapturePanel from './CapturePanel';
import { ReactComponent as PcapIcon } from '../assets/images/pcap.svg';
import InnerGraph from './InnerGraph';
import LoadingOverlay from './LoadingOverlay';
import './NetworkGraph.css';
import { NODE_SIZE } from './CustomNode';
import ResultDialog from './ResultDialog';
import AlertModal from './AlertModal';
import ErrorModal from './ErrorModal';
import { API_BASE_URL } from '../config';

Modal.setAppElement('#root');

const RELOAD_TIME = 60;
const MIN_TOPOLOGY_FETCH_GAP_MS = 1500;
const OP_LOCK_POLL_MS = 5000; // cheap lock-status poll while an op runs
const MAX_REPLICAS = 128; // matches backend types.MaxReplicas

// Mirror of backend/types/types.go, enforced client-side too so typos
// (e.g. 'typee', 'replicaas') surface with a clear error before submit.
const NODE_SPEC_FIELDS = new Set([
  'name',
  'image',
  'type',
  'shellMode',
  'privileged',
  'replicas',
  'commands',
  'env',
  'mounts',
  'devices',
  'driver',
]);
const LINK_SPEC_FIELDS = new Set([
  'localIntf',
  'node',
  'peerNode',
  'peerIntf',
  'uid',
  'localIp',
  'peerIp',
  'peerLabel',
  'name',
]);
const MOUNT_SPEC_FIELDS = new Set(['file', 'mountTo', 'sensitive']);
const DEVICE_SPEC_FIELDS = new Set(['path']);
const CONFIGURE_TARGET_FIELDS = new Set(['pod', 'actions']);
const SCALE_SPEC_FIELDS = new Set(['name', 'replicas']);

const ensureAllowedFields = (obj, allowed, label) => {
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return;
  for (const key of Object.keys(obj)) {
    if (!allowed.has(key)) {
      throw new Error(
        `Unknown field '${key}' in ${label}. Allowed fields: ${[...allowed].join(', ')}.`
      );
    }
  }
};

const isPlainObject = (v) => !!v && typeof v === 'object' && !Array.isArray(v);
const isNonEmptyString = (v) => typeof v === 'string' && v.trim().length > 0;

// Type-strict validation of a NodeSpec, shared by import and modify.add.
const validateNodeSpecFields = (node, label) => {
  if (!isPlainObject(node)) throw new Error(`${label}: must be an object.`);
  ensureAllowedFields(node, NODE_SPEC_FIELDS, label);

  for (const f of ['name', 'image', 'type']) {
    if (!isNonEmptyString(node[f])) throw new Error(`${label}: '${f}' must be a non-empty string.`);
  }
  if (!['host', 'router', 'switch'].includes(node.type)) {
    throw new Error(`${label}: invalid type '${node.type}'. Must be 'host', 'router' or 'switch'.`);
  }
  if (node.replicas !== undefined && node.replicas !== null) {
    if (!Number.isInteger(node.replicas) || node.replicas < 1 || node.replicas > MAX_REPLICAS) {
      throw new Error(
        `${label}: 'replicas' must be an integer between 1 and ${MAX_REPLICAS} (got ${JSON.stringify(node.replicas)}).`
      );
    }
  }
  if (node.privileged !== undefined && typeof node.privileged !== 'boolean') {
    throw new Error(`${label}: 'privileged' must be a boolean.`);
  }
  for (const f of ['shellMode', 'driver']) {
    if (node[f] !== undefined && node[f] !== null && typeof node[f] !== 'string') {
      throw new Error(`${label}: '${f}' must be a string.`);
    }
  }
  if (node.commands !== undefined && node.commands !== null) {
    if (!Array.isArray(node.commands) || !node.commands.every((c) => typeof c === 'string')) {
      throw new Error(`${label}: 'commands' must be an array of strings.`);
    }
  }
  if (node.env !== undefined && node.env !== null) {
    if (!isPlainObject(node.env) || !Object.values(node.env).every((v) => typeof v === 'string')) {
      throw new Error(`${label}: 'env' must be an object with string values.`);
    }
  }
  if (node.mounts !== undefined && node.mounts !== null) {
    if (!Array.isArray(node.mounts)) throw new Error(`${label}: 'mounts' must be an array.`);
    node.mounts.forEach((m, mi) => {
      const ml = `${label}.mounts[${mi}]`;
      if (!isPlainObject(m)) throw new Error(`${ml}: must be an object.`);
      ensureAllowedFields(m, MOUNT_SPEC_FIELDS, ml);
      if (!isNonEmptyString(m.file)) throw new Error(`${ml}: 'file' must be a non-empty string.`);
      if (!isNonEmptyString(m.mountTo))
        throw new Error(`${ml}: 'mountTo' must be a non-empty string.`);
      if (m.sensitive !== undefined && typeof m.sensitive !== 'boolean') {
        throw new Error(`${ml}: 'sensitive' must be a boolean.`);
      }
    });
  }
  if (node.devices !== undefined && node.devices !== null) {
    if (!Array.isArray(node.devices)) throw new Error(`${label}: 'devices' must be an array.`);
    node.devices.forEach((d, di) => {
      const dl = `${label}.devices[${di}]`;
      if (!isPlainObject(d)) throw new Error(`${dl}: must be an object.`);
      ensureAllowedFields(d, DEVICE_SPEC_FIELDS, dl);
      if (!isNonEmptyString(d.path)) throw new Error(`${dl}: 'path' must be a non-empty string.`);
    });
  }
};

// Type-strict validation of a LinkSpec's fields, shared by import and modify.add.
const validateLinkSpecFields = (link, label) => {
  if (!isPlainObject(link)) throw new Error(`${label}: must be an object.`);
  ensureAllowedFields(link, LINK_SPEC_FIELDS, label);
  for (const f of ['localIntf', 'node', 'peerNode', 'peerIntf']) {
    if (!isNonEmptyString(link[f])) throw new Error(`${label}: '${f}' must be a non-empty string.`);
  }
  if (link.uid !== undefined && link.uid !== null && !Number.isInteger(link.uid)) {
    throw new Error(`${label}: 'uid' must be an integer.`);
  }
  for (const f of ['localIp', 'peerIp', 'peerLabel', 'name']) {
    if (link[f] !== undefined && link[f] !== null && typeof link[f] !== 'string') {
      throw new Error(`${label}: '${f}' must be a string.`);
    }
  }
};

const parseGoDurationToSeconds = (durationText) => {
  if (!durationText || typeof durationText !== 'string') return null;

  const unitToSeconds = {
    h: 3600,
    m: 60,
    s: 1,
    ms: 0.001,
    us: 0.000001,
    µs: 0.000001,
    ns: 0.000000001,
  };

  const regex = /([+-]?\d+(?:\.\d+)?)(h|ms|us|µs|ns|m|s)/g;
  let totalSeconds = 0;
  let found = false;
  let match;

  while ((match = regex.exec(durationText)) !== null) {
    const value = Number(match[1]);
    const unit = match[2];
    const factor = unitToSeconds[unit];
    if (Number.isFinite(value) && factor !== undefined) {
      totalSeconds += value * factor;
      found = true;
    }
  }

  if (!found) return null;
  return Math.round(totalSeconds);
};

// Simple hash function for topology versioning
const hashTopology = (nodes, edges) => {
  const str = JSON.stringify({ nodes, edges });
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    const char = str.charCodeAt(i);
    hash = (hash << 5) - hash + char;
    hash = hash & hash; // Convert to 32-bit integer
  }
  return Math.abs(hash).toString(16);
};

const expandNodesIntoPods = (nodes) => {
  const pods = [];

  for (const node of nodes) {
    const count = node.replicas || 1;
    for (let i = 0; i < count; i++) {
      pods.push({
        ...node,
        name: `${node.name}-${i}`,
        baseName: node.name,
        replicaIndex: i,
      });
    }
  }

  return pods;
};

// Interfaces reserved platform-wide: eth0 is the primary CNI/management
// interface on every pod and lo is loopback. Neither can be a topology link
// interface (the backend rejects them too; this gives instant feedback).
const RESERVED_IFACE_NAMES = new Set(['eth0', 'lo']);

const resolvePodName = (name, nodes) => {
  const matchingNode = nodes.find((n) => n.name === name);
  if (!matchingNode) return name;

  if ((matchingNode.replicas || 1) > 1) {
    console.warn(`⚠️ Ambiguous reference: '${name}' has multiple replicas. Use '${name}-N'`);
    return null; // or throw error
  }

  return `${name}-0`;
};

const normalizeInterfaces = (rawInterfacesData) => {
  const normalized = {};
  for (const pod in rawInterfacesData) {
    const podEntry = rawInterfacesData[pod];
    normalized[pod] = {
      internet: typeof podEntry.internet === 'string' ? podEntry.internet : false,
      interfaces: podEntry.interfaces || {},
    };
  }
  return normalized;
};

const generateNodePositions = (pods) => {
  const podPositions = {};
  const SPACING_X = 140;
  const SPACING_Y = 120;
  const GROUP_GAP = 100;

  // Horizontal layout: groups laid out left → right (routers | switches | hosts).
  // Each group fills a column block of roughly sqrt(N) rows, then wraps right.
  const placeGroup = (group, startX) => {
    if (group.length === 0) return startX;
    const rows = Math.ceil(Math.sqrt(group.length));
    const totalHeight = (rows - 1) * SPACING_Y;
    group.forEach((pod, i) => {
      const row = i % rows;
      const col = Math.floor(i / rows);
      podPositions[pod.name] = {
        x: startX + col * SPACING_X,
        y: row * SPACING_Y - totalHeight / 2 + 300,
      };
    });
    const cols = Math.ceil(group.length / rows);
    return startX + cols * SPACING_X + GROUP_GAP;
  };

  const routers = pods.filter((p) => p.type === 'router');
  const switchs = pods.filter((p) => p.type === 'switch' || p.type === 'external');
  const hosts = pods.filter((p) => p.type === 'host');

  let x = 50;
  x = placeGroup(routers, x);
  x = placeGroup(switchs, x);
  placeGroup(hosts, x);

  return podPositions;
};

// Force-directed initial layout (d3-force): nodes repel each other and links
// act as springs, so the topology untangles from the centre outward. A weak
// per-type horizontal bias keeps the router/switch/host grouping of the grid
// layout. Run headless to convergence and frozen, so the result is static and
// deterministic for a given graph (no live physics, drag/save unaffected).
// Shared force-layout configuration. No per-type lanes (those forced same-type
// nodes into a single column and made the graph grow tall). Instead a stronger
// vertical centring than horizontal biases the layout to spread sideways, which
// fits wide screens, and repulsion/link distance scale with node count so large
// topologies do not clump. Initial positions are a deterministic ring around the
// centre so the result is reproducible.
const buildForceSim = (pods, links, originalNodes, initRadius) => {
  const N = pods.length;
  // Sort by id so the initial ring placement depends only on the graph
  // structure, not on the order the caller passed the nodes in. This makes
  // the layout identical whether it is computed from import placeholders or
  // from the deployed pods, so the import view matches the post-refresh view.
  const ordered = [...pods].sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
  const simNodes = ordered.map((p, i) => {
    const a = (2 * Math.PI * i) / N;
    return { id: p.name, type: p.type, x: initRadius * Math.cos(a), y: initRadius * Math.sin(a) };
  });
  const idSet = new Set(simNodes.map((n) => n.id));

  // Resolve link endpoints to pod ids; keep only links whose both ends exist
  // (transient modify states can reference pods that are not present yet).
  const names = pods.map((p) => p.name);
  const simLinks = (links || [])
    .map((l) => {
      const s = names.includes(l.node) ? l.node : resolvePodName(l.node, originalNodes);
      const t = names.includes(l.peerNode) ? l.peerNode : resolvePodName(l.peerNode, originalNodes);
      return { source: s, target: t };
    })
    .filter((l) => l.source && l.target && idSet.has(l.source) && idSet.has(l.target));

  const charge = Math.max(-1500, -(300 + 20 * N)); // more nodes -> more repulsion
  const linkDistance = Math.min(260, 110 + 3 * N);

  const sim = forceSimulation(simNodes)
    .force('charge', forceManyBody().strength(charge))
    // High link strength makes each subnet cling tightly around its switch
    // so the groups stay coherent and do not interleave with one another.
    .force(
      'link',
      forceLink(simLinks)
        .id((d) => d.id)
        .distance(linkDistance)
        .strength(0.75)
    )
    .force('center', forceCenter(0, 0))
    .force('collide', forceCollide(NODE_SIZE * 0.9))
    .force('y', forceY(0).strength(0.13)) // vertical centring -> stays short
    .force('x', forceX(0).strength(0.045)); // weak horizontal centring -> spreads wide

  const byId = new Map(simNodes.map((n) => [n.id, n]));
  return { sim, simNodes, byId };
};

// Headless force layout: run to convergence and return final positions.
const generateNodePositionsForce = (pods, links = [], originalNodes = []) => {
  if (!pods || pods.length === 0) return {};
  const { sim, simNodes } = buildForceSim(pods, links, originalNodes, 40);
  sim.stop();
  const ticks = Math.min(400, Math.max(150, pods.length * 6));
  for (let i = 0; i < ticks; i++) sim.tick();
  const positions = {};
  for (const n of simNodes) positions[n.id] = { x: Math.round(n.x), y: Math.round(n.y) };
  return positions;
};

// Tween nodes from the centre (0,0) to their target positions with an
// ease-out, used to glide imported nodes into their saved layout. Nodes are
// assumed to start at the origin; the final frame snaps exactly to target.
const animateToPositions = (targets, setNodes, duration = 750) =>
  new Promise((resolve) => {
    const ids = Object.keys(targets || {});
    if (ids.length === 0) {
      resolve();
      return;
    }
    const ease = (t) => 1 - Math.pow(1 - t, 3); // easeOutCubic
    let startTs = null;
    const step = (ts) => {
      if (startTs === null) startTs = ts;
      const t = Math.min(1, (ts - startTs) / duration);
      const k = ease(t);
      setNodes((prev) =>
        prev.map((n) => {
          const tgt = targets[n.id];
          return tgt ? { ...n, position: { x: tgt.x * k, y: tgt.y * k } } : n;
        })
      );
      if (t < 1) {
        requestAnimationFrame(step);
      } else {
        setNodes((prev) =>
          prev.map((n) => (targets[n.id] ? { ...n, position: { ...targets[n.id] } } : n))
        );
        resolve();
      }
    };
    requestAnimationFrame(step);
  });

const generateNodes = ({
  pods,
  interfacesData,
  podPositions,
  podStatusMap,
  podDetailsMap,
  currentPositions,
  restartingPods = new Set(),
}) => {
  return pods.map((pod) => {
    const isExternal = pod.type === 'external';
    const podInterfaceData = interfacesData[pod.name] || {};
    const interfaces = isExternal
      ? pod.hostInterfaces || []
      : Object.keys(podInterfaceData.interfaces || {});
    const internet =
      typeof podInterfaceData.internet === 'string' ? podInterfaceData.internet : false;

    const position = currentPositions[pod.name] || podPositions[pod.name] || { x: 0, y: 0 };

    const displayName =
      pod.replicaIndex === 0 && (pod.replicas === 1 || pod.replicas === undefined)
        ? pod.baseName
        : pod.name;

    const fullInfo = isExternal
      ? {
          id: pod.name,
          name: pod.name,
          baseName: pod.baseName,
          type: 'external',
          runtime: 'external-uplink',
          hostInterfaces: pod.hostInterfaces || [],
          connectedNodes: pod.connectedNodes || [],
          connectedWorkers: pod.connectedWorkers || [],
          connectedLinks: pod.connectedLinks || [],
          status: 'External',
        }
      : podDetailsMap[pod.name];

    return {
      id: pod.name,
      type: 'custom',
      data: {
        label: displayName,
        type: pod.type,
        interfaces,
        status: isExternal ? 'External' : podStatusMap[pod.name] || 'Unknown',
        internet,
        restarting: !isExternal && restartingPods.has(pod.name),
        fullInfo,
      },
      position,
    };
  });
};

const generateEdges = (links, expandedPods, originalNodes) => {
  const podNames = expandedPods.map((p) => p.name);

  return links
    .map((link, index) => {
      const src = podNames.includes(link.node)
        ? link.node
        : resolvePodName(link.node, originalNodes);

      const dst = podNames.includes(link.peerNode)
        ? link.peerNode
        : resolvePodName(link.peerNode, originalNodes);

      return {
        id: `${src}-${dst}-${index}`,
        source: src,
        target: dst,
        sourceHandle: 'center',
        targetHandle: 'center',
        type: 'straight',
        animated: false,
        data: {
          localIntf: link.localIntf,
          peerIntf: link.peerIntf,
          uid: link.uid,
          linkName: link.name || null,
        },
        style: { strokeWidth: 1 + NODE_SIZE / 64, stroke: 'black' },
      };
    })
    .filter((e) => e.source && e.target); // remove invalid edges
};

const NetworkGraph = ({ namespace, onError, onImportingChange, refreshTrigger = 0 }) => {
  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);
  const nodesRef = useRef([]);
  const edgesRef = useRef([]);
  const [selectedNodeInfo, setSelectedNodeInfo] = useState(null);
  const [selectedLink, setSelectedLink] = useState(null);
  const [openShells, setOpenShells] = useState([]); // Array of {id, podName, pod, shellMode, zIndex, minimized}
  const openShellsRef = useRef(openShells);
  const [openCaptures, setOpenCaptures] = useState([]); // Array of {id, pod, iface, zIndex, minimized}
  const [maxZIndex, setMaxZIndex] = useState(1000);

  const positionRef = useRef({});
  const [hoveredInfo, setHoveredInfo] = useState(null);
  const [tooltipPos, setTooltipPos] = useState({ x: 0, y: 0 });
  const [tooltipLoading, setTooltipLoading] = useState(false);
  const [interfacesData, setInterfacesData] = useState({});
  const [, setSavedPositions] = useState({});
  const [importing, setImporting] = useState(false);
  const [loadingConfig, setLoadingConfig] = useState(false);
  const [modifyingTopology, setModifyingTopology] = useState(false);
  const [clearingTopology, setClearingTopology] = useState(false);
  // operation_type from the backend lock while an op runs, else null.
  const [operationInProgress, setOperationInProgress] = useState(null);
  const opRunningRef = useRef(false);
  const [topologyInputKind, setTopologyInputKind] = useState(null); // 'import' | 'modify' | 'config' | null
  const [graphSearchQuery, setGraphSearchQuery] = useState('');
  const [hideExternal, setHideExternal] = useState(false); // hide external-network nodes and their links when checked

  const isDeployingRef = useRef(false);
  const animatingRef = useRef(false); // true while the import force-layout animation runs
  const restartingPodsRef = useRef(new Set());
  const deletingPodsRef = useRef(new Set());
  const deletingLinksRef = useRef(new Set());
  const knownLinkUidsRef = useRef(new Set());
  const knownNodeNamesRef = useRef(new Set());
  const isFirstTopologyFetchRef = useRef(true);
  const creatingUidsRef = useRef(new Set());
  const creatingTimersRef = useRef(new Map());
  const creatingNodeNamesRef = useRef(new Set());
  const creatingNodeTimersRef = useRef(new Map());
  const isBusyRef = useRef(false);
  const CREATE_FLASH_IDLE_MS = 4000; // periodic-refresh detection
  const CREATE_FLASH_AFTER_OP_MS = 2500; // delay after a busy op ends
  const CREATE_FLASH_SAFETY_MS = 120000; // hard cap if no busy transition happens

  const addRestartingPod = (podName) => {
    restartingPodsRef.current = new Set([...restartingPodsRef.current, podName]);
    setNodes((prev) =>
      prev.map((n) => (n.id === podName ? { ...n, data: { ...n.data, restarting: true } } : n))
    );
  };

  const removeRestartingPod = (podName) => {
    restartingPodsRef.current = new Set(
      [...restartingPodsRef.current].filter((n) => n !== podName)
    );
    setNodes((prev) =>
      prev.map((n) => (n.id === podName ? { ...n, data: { ...n.data, restarting: false } } : n))
    );
  };

  // Brief green flash on the node when a restart completes successfully.
  const flashRestartSuccess = (podName) => {
    setNodes((prev) =>
      prev.map((n) => (n.id === podName ? { ...n, data: { ...n.data, restartSuccess: true } } : n))
    );
    setTimeout(() => {
      setNodes((prev) =>
        prev.map((n) =>
          n.id === podName ? { ...n, data: { ...n.data, restartSuccess: false } } : n
        )
      );
    }, 700);
  };

  const addDeletingPod = (podName) => {
    deletingPodsRef.current = new Set([...deletingPodsRef.current, podName]);
    setNodes((prev) =>
      prev.map((n) => (n.id === podName ? { ...n, data: { ...n.data, deleting: true } } : n))
    );
  };

  const removeDeletingPod = (podName) => {
    deletingPodsRef.current = new Set([...deletingPodsRef.current].filter((n) => n !== podName));
    setNodes((prev) =>
      prev.map((n) => (n.id === podName ? { ...n, data: { ...n.data, deleting: false } } : n))
    );
  };

  const addDeletingLink = (edgeId) => {
    deletingLinksRef.current = new Set([...deletingLinksRef.current, edgeId]);
    setEdges((prev) =>
      prev.map((e) => (e.id === edgeId ? { ...e, data: { ...e.data, deleting: true } } : e))
    );
  };

  const removeDeletingLink = (edgeId) => {
    deletingLinksRef.current = new Set([...deletingLinksRef.current].filter((id) => id !== edgeId));
    setEdges((prev) =>
      prev.map((e) => (e.id === edgeId ? { ...e, data: { ...e.data, deleting: false } } : e))
    );
  };

  const clearCreatingUids = (uids) => {
    if (!uids || uids.length === 0) return;
    const uidSet = new Set(uids);
    uids.forEach((uid) => {
      creatingUidsRef.current.delete(uid);
      const t = creatingTimersRef.current.get(uid);
      if (t) clearTimeout(t);
      creatingTimersRef.current.delete(uid);
    });
    setEdges((prev) =>
      prev.map((e) =>
        uidSet.has(e.data?.uid) ? { ...e, data: { ...e.data, creating: false } } : e
      )
    );
  };

  // Build "creating" placeholder nodes from the submitted JSON so they appear
  // immediately. Positions pinned in positionRef so the post-op fetch doesn't shuffle them.
  const buildPlaceholderNodes = (newNodeSpecs) => {
    if (!Array.isArray(newNodeSpecs) || newNodeSpecs.length === 0) return [];

    const existing = (nodesRef.current || []).filter((n) => n.data?.type !== 'external');
    const existingIds = new Set(existing.map((n) => n.id));

    // 1) Lock current positions of existing pods so the post-op auto-layout
    //    (recomputed with N+1 pods) doesn't push them around.
    for (const n of existing) {
      if (!positionRef.current[n.id] && n.position) {
        positionRef.current[n.id] = { ...n.position };
      }
    }

    // 2) Gather the "cluster" stats per type (median x, bottom y) so new pods drop
    //    right into the column they belong to (router / switch / host).
    const clusterByType = {};
    for (const n of existing) {
      const t = n.data?.type;
      if (!t) continue;
      if (!clusterByType[t]) clusterByType[t] = { xs: [], maxY: -Infinity };
      clusterByType[t].xs.push(n.position?.x ?? 0);
      clusterByType[t].maxY = Math.max(clusterByType[t].maxY, n.position?.y ?? 0);
    }

    // 3) Build the flat list of placeholder pods (one per replica).
    const placeholderPods = [];
    for (const spec of newNodeSpecs) {
      const replicas = spec.replicas || 1;
      for (let i = 0; i < replicas; i++) {
        const podName = `${spec.name}-${i}`;
        if (existingIds.has(podName)) continue;
        placeholderPods.push({ spec, podName, replicas, replicaIndex: i });
      }
    }

    // 4) Positions: empty namespace → auto-layout by columns; existing
    //    topology → stack each new pod below its type's cluster.
    const positions = {};
    if (existing.length === 0) {
      const podsForLayout = placeholderPods.map((p) => ({ name: p.podName, type: p.spec.type }));
      Object.assign(positions, generateNodePositions(podsForLayout));
    } else {
      const nextYByType = {};
      const rightmost = Math.max(0, ...existing.map((n) => n.position?.x ?? 0));
      for (const p of placeholderPods) {
        const t = p.spec.type;
        const cluster = clusterByType[t];
        let baseX;
        if (cluster && cluster.xs.length > 0) {
          const sortedXs = [...cluster.xs].sort((a, b) => a - b);
          baseX = sortedXs[Math.floor(sortedXs.length / 2)];
          if (nextYByType[t] === undefined) nextYByType[t] = cluster.maxY + 120;
        } else {
          // No existing pod of this type, drop the new column to the right.
          baseX = rightmost + 250;
          if (nextYByType[t] === undefined) nextYByType[t] = 50;
        }
        positions[p.podName] = { x: baseX, y: nextYByType[t] };
        nextYByType[t] += 120;
      }
    }

    // 5) Build the graph node entries and pin positions in positionRef.
    const placeholders = [];
    for (const p of placeholderPods) {
      const { spec, podName, replicas, replicaIndex } = p;
      const displayName = replicas === 1 ? spec.name : podName;
      const position = positionRef.current[podName] || positions[podName] || { x: 0, y: 0 };
      placeholders.push({
        id: podName,
        type: 'custom',
        data: {
          label: displayName,
          type: spec.type,
          interfaces: [],
          status: 'Pending',
          internet: false,
          restarting: false,
          creating: true,
          fullInfo: {
            name: podName,
            baseName: spec.name,
            type: spec.type,
            image: spec.image,
            status: 'Pending',
            replicaCount: replicas,
            replicaIndex,
            env: spec.env || {},
            mounts: spec.mounts || [],
            driver: spec.driver,
          },
        },
        position,
      });
      positionRef.current[podName] = position;
    }
    return placeholders;
  };

  // Extract external-endpoint placeholder nodes from link specs, rewriting link.node /
  // link.peerNode from the "external" keyword to the actual peerLabel id so that
  // buildPlaceholderEdges can later resolve them.
  const buildPlaceholderExternalNodes = (linkSpecs) => {
    const seen = new Map();
    for (const link of linkSpecs) {
      const nodeIsExternal = link.node === 'external';
      const peerIsExternal = link.peerNode === 'external';
      if (!nodeIsExternal && !peerIsExternal) continue;

      const hostIntf = nodeIsExternal ? link.localIntf : link.peerIntf;
      const externalId = link.peerLabel?.trim() || `uplink (${hostIntf})`;
      if (!externalId) continue;

      if (nodeIsExternal) link.node = externalId;
      else link.peerNode = externalId;

      if (!seen.has(externalId)) {
        const position = positionRef.current[externalId] || { x: 0, y: 0 };
        seen.set(externalId, {
          id: externalId,
          type: 'custom',
          data: {
            label: externalId,
            type: 'external',
            interfaces: [],
            status: 'External',
            internet: false,
            restarting: false,
            fullInfo: {
              name: externalId,
              baseName: externalId,
              type: 'external',
              status: 'External',
              hostInterfaces: [],
              connectedNodes: [],
              connectedWorkers: [],
              connectedLinks: [],
            },
          },
          position,
        });
      }
    }
    return Array.from(seen.values());
  };

  // Build placeholder edges for the new links in the user-submitted JSON. References
  // may be base names (single replica), we resolve to indexed pod names if possible.
  const buildPlaceholderEdges = (newLinkSpecs, placeholderNodes = []) => {
    if (!Array.isArray(newLinkSpecs) || newLinkSpecs.length === 0) return [];

    const existing = nodesRef.current || [];
    const allNodes = [...existing, ...placeholderNodes];
    const idSet = new Set(allNodes.map((n) => n.id));

    // Build a base-name → unique id resolver (only for single-replica bases)
    const bySingletonBase = new Map();
    for (const n of allNodes) {
      const baseName = n.data?.fullInfo?.baseName;
      if (!baseName) continue;
      if (n.data?.fullInfo?.replicaCount === 1) {
        bySingletonBase.set(baseName, n.id);
      }
    }
    const resolve = (name) => {
      if (!name) return null;
      if (idSet.has(name)) return name;
      if (bySingletonBase.has(name)) return bySingletonBase.get(name);
      const candidate = `${name}-0`;
      if (idSet.has(candidate)) return candidate;
      return null;
    };

    const stamp = Date.now();
    return newLinkSpecs
      .map((link, idx) => {
        const src = resolve(link.node);
        const dst = resolve(link.peerNode);
        if (!src || !dst) return null;
        return {
          id: `placeholder-${src}-${dst}-${idx}-${stamp}`,
          source: src,
          target: dst,
          sourceHandle: 'center',
          targetHandle: 'center',
          type: 'straight',
          animated: false,
          data: {
            localIntf: link.localIntf,
            peerIntf: link.peerIntf,
            creating: true,
          },
          style: { strokeWidth: 1 + NODE_SIZE / 64, stroke: 'black' },
        };
      })
      .filter(Boolean);
  };

  // Mark existing pods/edges as deleting based on the JSON's `delete` section.
  // Pre-marks the soon-to-be-removed pods and edges so the UI flips them red
  // immediately when the user submits a modify. Returns the IDs it marked so
  // the caller can revert on backend error.
  const preMarkDeleteFromJson = (deleteSpec) => {
    const markedPods = new Set();
    const markedLinks = new Set();
    if (!deleteSpec) return { markedPods, markedLinks };

    const podsBeingDeleted = new Set();

    // Resolve a name in the delete payload (which may be a base name like
    // "router") to the actual pod id used by the graph edges (which is
    // always an indexed form like "router-0"). Falls back to "name-0".
    const resolvePodId = (name) => {
      if (!name || name === 'external') return name;
      if (/-\d+$/.test(name)) return name;
      const existing = nodesRef.current || [];
      if (existing.some((n) => n.id === name)) return name;
      const byBase = existing.filter((n) => n.data?.fullInfo?.baseName === name);
      if (byBase.length > 0) return byBase[0].id;
      return `${name}-0`;
    };

    if (Array.isArray(deleteSpec.nodes)) {
      const existing = nodesRef.current || [];
      for (const baseName of deleteSpec.nodes) {
        const matches = existing.filter((n) => n.data?.fullInfo?.baseName === baseName);
        matches.forEach((m) => {
          addDeletingPod(m.id);
          markedPods.add(m.id);
          podsBeingDeleted.add(m.id);
        });
      }
    }

    // Mark explicit link removals (delete.links).
    if (Array.isArray(deleteSpec.links)) {
      const existingEdges = edgesRef.current || [];
      for (const linkSpec of deleteSpec.links) {
        const srcId = resolvePodId(linkSpec.node);
        const dstId = resolvePodId(linkSpec.peerNode);
        const match = existingEdges.find(
          (e) =>
            (e.source === srcId &&
              e.target === dstId &&
              e.data?.localIntf === linkSpec.localIntf &&
              e.data?.peerIntf === linkSpec.peerIntf) ||
            (e.source === dstId &&
              e.target === srcId &&
              e.data?.localIntf === linkSpec.peerIntf &&
              e.data?.peerIntf === linkSpec.localIntf)
        );
        if (match) {
          addDeletingLink(match.id);
          markedLinks.add(match.id);
        }
      }
    }

    // Flash edges red when their pod is being deleted, otherwise the
    // node turns red while its links stay black until the roundtrip ends.
    if (podsBeingDeleted.size > 0) {
      const existingEdges = edgesRef.current || [];
      for (const edge of existingEdges) {
        if (podsBeingDeleted.has(edge.source) || podsBeingDeleted.has(edge.target)) {
          addDeletingLink(edge.id);
          markedLinks.add(edge.id);
        }
      }
    }

    return { markedPods, markedLinks };
  };

  const clearCreatingNodeNames = (names) => {
    if (!names || names.length === 0) return;
    const nameSet = new Set(names);
    names.forEach((name) => {
      creatingNodeNamesRef.current.delete(name);
      const t = creatingNodeTimersRef.current.get(name);
      if (t) clearTimeout(t);
      creatingNodeTimersRef.current.delete(name);
    });
    setNodes((prev) =>
      prev.map((n) => (nameSet.has(n.id) ? { ...n, data: { ...n.data, creating: false } } : n))
    );
  };

  const [hasGraph, setHasGraph] = useState(false); // true when are nodes painted
  const [resultOpen, setResultOpen] = useState(false);
  const [applyResult, setApplyResult] = useState(null);
  const [initialLoading, setInitialLoading] = useState(true); // true until the first load

  // Estados para AlertModal
  const [alertModal, setAlertModal] = useState({
    isOpen: false,
    type: 'info',
    title: '',
    message: '',
    onConfirm: null,
    onCancel: null,
    confirmText: 'Accept',
    cancelText: 'Cancel',
    showPositionCheckbox: false,
    timingData: null,
  });
  const [clearPositionsChecked, setClearPositionsChecked] = useState(false);
  const clearPositionsRef = useRef(false);
  const [clearFilesChecked, setClearFilesChecked] = useState(false);
  const clearFilesRef = useRef(false);
  // null when hidden; otherwise { title, details, note } for the error modal.
  const [deploymentError, setDeploymentError] = useState(null);
  const skipInitialFetchRef = useRef(false);

  // showError opens the shared error modal (copyable error box) for genuine
  // failures that block an operation. Warnings/confirmations stay on AlertModal.
  const showError = (title, details, note = '') => setDeploymentError({ title, details, note });
  const topologyVersionRef = useRef(null); // Track current topology version
  const topologyFetchInFlightRef = useRef(false);
  const lastTopologyFetchAtRef = useRef(0);
  const clearingTopologyRef = useRef(false);
  const modifyingTopologyRef = useRef(false);

  // Actualizar ref cuando cambia openShells
  useEffect(() => {
    openShellsRef.current = openShells;
  }, [openShells]);

  useEffect(() => {
    nodesRef.current = nodes;
  }, [nodes]);

  useEffect(() => {
    edgesRef.current = edges;
  }, [edges]);

  useEffect(() => {
    clearingTopologyRef.current = clearingTopology;
  }, [clearingTopology]);

  useEffect(() => {
    modifyingTopologyRef.current = modifyingTopology;
  }, [modifyingTopology]);

  const openInteractiveShell = (pod, shellMode = 'sh') => {
    const shellId = `shell-${Date.now()}-${Math.random()}`;
    setMaxZIndex((prev) => prev + 1);
    setOpenShells((prev) => [
      ...prev,
      {
        id: shellId,
        podName: pod?.metadata?.name || pod?.name,
        pod,
        shellMode,
        zIndex: maxZIndex + 1,
        minimized: false,
      },
    ]);
  };

  const closeInteractiveShell = (shellId) => {
    setOpenShells((prev) => prev.filter((s) => s.id !== shellId));
  };

  const bringShellToFront = (shellId) => {
    setMaxZIndex((prev) => prev + 1);
    setOpenShells((prev) =>
      prev.map((s) => (s.id === shellId ? { ...s, zIndex: maxZIndex + 1 } : s))
    );
  };

  const minimizeShell = (shellId) => {
    setOpenShells((prev) => prev.map((s) => (s.id === shellId ? { ...s, minimized: true } : s)));
  };

  const restoreShell = (shellId) => {
    setMaxZIndex((prev) => prev + 1);
    setOpenShells((prev) =>
      prev.map((s) => (s.id === shellId ? { ...s, minimized: false, zIndex: maxZIndex + 1 } : s))
    );
  };

  // --- Packet capture panels (mirror the shell-window manager) ---
  const openCapture = (pod, iface) => {
    if (!pod || !iface) return;
    const id = `cap-${Date.now()}-${Math.random()}`;
    setMaxZIndex((prev) => prev + 1);
    setOpenCaptures((prev) => [
      ...prev,
      {
        id,
        pod,
        iface,
        zIndex: maxZIndex + 1,
        minimized: false,
      },
    ]);
  };

  const closeCapture = (id) => setOpenCaptures((prev) => prev.filter((c) => c.id !== id));

  const bringCaptureToFront = (id) => {
    setMaxZIndex((prev) => prev + 1);
    setOpenCaptures((prev) => prev.map((c) => (c.id === id ? { ...c, zIndex: maxZIndex + 1 } : c)));
  };

  const minimizeCapture = (id) =>
    setOpenCaptures((prev) => prev.map((c) => (c.id === id ? { ...c, minimized: true } : c)));

  const restoreCapture = (id) => {
    setMaxZIndex((prev) => prev + 1);
    setOpenCaptures((prev) =>
      prev.map((c) => (c.id === id ? { ...c, minimized: false, zIndex: maxZIndex + 1 } : c))
    );
  };

  const isBusy =
    importing || loadingConfig || modifyingTopology || clearingTopology || !!operationInProgress;

  // Keep ref synced so fetchTopology can read the current busy state synchronously
  useEffect(() => {
    isBusyRef.current = isBusy;
  }, [isBusy]);

  // When a busy operation ends, fade out any "creating" flashes shortly after
  useEffect(() => {
    if (!isBusy && (creatingUidsRef.current.size > 0 || creatingNodeNamesRef.current.size > 0)) {
      const t = setTimeout(() => {
        clearCreatingUids([...creatingUidsRef.current]);
        clearCreatingNodeNames([...creatingNodeNamesRef.current]);
      }, CREATE_FLASH_AFTER_OP_MS);
      return () => clearTimeout(t);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isBusy]);

  const showBusyWarning = () => {
    setAlertModal({
      isOpen: true,
      type: 'warning',
      title: 'Operation in progress',
      message:
        'There is an operation in progress in this namespace. Please wait for it to finish before starting another.',
      onConfirm: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
      onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
    });
  };

  const handleRestartPod = (podName) => {
    if (isBusy) {
      showBusyWarning();
      return;
    }
    setAlertModal({
      isOpen: true,
      type: 'confirm',
      title: 'Restart Pod',
      message: `Are you sure you want to restart pod ${podName}?`,
      onConfirm: () => {
        setAlertModal((prev) => ({ ...prev, isOpen: false }));
        executeRestartPod(podName);
      },
      onCancel: () => {
        setAlertModal((prev) => ({ ...prev, isOpen: false }));
      },
    });
  };

  const executeRestartPod = async (podName) => {
    addRestartingPod(podName);
    try {
      const scheduleStatusRefreshes = () => {
        // Pod phase often changes shortly after restart request is accepted.
        [700, 1800, 3500, 5500].forEach((delayMs) => {
          setTimeout(() => {
            refreshPodStatuses();
          }, delayMs);
        });
      };

      const restartPromise = fetch(`${API_BASE_URL}/pods/restart/${namespace}/${podName}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
      });

      // Suppress background topology polling while the pod restarts and
      // driver replay runs, avoids cascading SSH timeouts hitting the guest VM.
      isDeployingRef.current = true;

      // Refresh immediately via /pods endpoint so pod status changes appear sooner.
      refreshPodStatuses();
      scheduleStatusRefreshes();

      const res = await restartPromise;

      if (res.ok) {
        removeRestartingPod(podName);
        flashRestartSuccess(podName);
        await fetchTopology(true);
        setTimeout(() => {
          fetchTopology(true);
        }, 1200);
      } else {
        isDeployingRef.current = false;
        removeRestartingPod(podName);
        const text = await res.text();
        showError('Error restarting', text || 'Could not restart pod');
      }
    } catch (err) {
      isDeployingRef.current = false;
      removeRestartingPod(podName);
      console.error('Error restarting pod:', err);
      showError('Connection error', err.message || 'Could not connect to server');
    }
  };

  class HTTPError extends Error {
    constructor(status, message) {
      super(message);
      this.name = 'HTTPError';
      this.status = status;
    }
  }

  const checkResponse = async (res) => {
    if (!res.ok) {
      const text = await res.text();
      throw new HTTPError(res.status, text);
    }
    return res.json();
  };

  const showOperationSuccessModal = (title, message, tookTime, warnings) => {
    let timingData = null;
    if (tookTime && typeof tookTime === 'object') {
      timingData = tookTime;
    } else if (typeof tookTime === 'string') {
      const secs = parseGoDurationToSeconds(tookTime);
      if (secs !== null) timingData = { total: `${secs}s` };
    }
    const warningsList = Array.isArray(warnings) ? warnings : [];
    setAlertModal({
      isOpen: true,
      type: 'success',
      title,
      message,
      timingData,
      warningsList,
      confirmText: 'OK',
      cancelText: 'Cancel',
      onConfirm: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
      onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
    });
  };

  const addExternalNodesFromLinks = (links, podsList = [], originalNodes = []) => {
    const externalNodeMap = new Map();
    const podNames = new Set();
    const podNodeMap = new Map();
    const podsByBase = new Map();

    for (const pod of podsList) {
      podNames.add(pod.name);
      podNodeMap.set(pod.name, pod.node || 'unknown-worker');

      if (pod.baseName) {
        const names = podsByBase.get(pod.baseName) || [];
        names.push(pod.name);
        podsByBase.set(pod.baseName, names);
      }
    }

    const resolveLinkedPodName = (name) => {
      if (!name) return '';
      if (podNames.has(name)) return name;

      const singletonName = `${name}-0`;
      if (podNames.has(singletonName)) return singletonName;

      const byBase = podsByBase.get(name);
      if (byBase && byBase.length === 1) return byBase[0];

      const resolvedFromTopology = resolvePodName(name, originalNodes);
      if (resolvedFromTopology && podNames.has(resolvedFromTopology)) return resolvedFromTopology;

      return name;
    };

    for (const link of links) {
      // "external" can appear on either side of a link (node or peerNode)
      const nodeIsExternal = link.node === 'external';
      const peerIsExternal = link.peerNode === 'external';
      if (!nodeIsExternal && !peerIsExternal) continue;

      // The host-facing interface and the real-pod side depend on which end is external
      const hostIntf = nodeIsExternal ? link.localIntf : link.peerIntf;
      const podSide = nodeIsExternal ? link.peerNode : link.node;
      const podIntf = nodeIsExternal ? link.peerIntf : link.localIntf;

      const externalId = link.peerLabel?.trim() || `uplink (${hostIntf})`;
      if (!externalId) {
        console.warn('⚠️ missing peerLabel field on external link:', link);
        continue;
      }

      // Rewrite the external side so generateEdges sees a real node id
      if (nodeIsExternal) {
        link.node = externalId;
      } else {
        link.peerNode = externalId;
      }

      if (!externalNodeMap.has(externalId)) {
        externalNodeMap.set(externalId, {
          name: externalId,
          baseName: externalId,
          type: 'external',
          replicas: 1,
          replicaIndex: 0,
          commands: [],
          hostInterfaces: [],
          connectedNodes: [],
          connectedWorkers: [],
          connectedLinks: [],
        });
      }

      const externalNode = externalNodeMap.get(externalId);
      const resolvedPodName = resolveLinkedPodName(podSide);
      const workerName = podNodeMap.get(resolvedPodName) || 'unknown-worker';
      if (hostIntf && !externalNode.hostInterfaces.includes(hostIntf)) {
        externalNode.hostInterfaces.push(hostIntf);
      }
      if (resolvedPodName && !externalNode.connectedNodes.includes(resolvedPodName)) {
        externalNode.connectedNodes.push(resolvedPodName);
      }
      if (workerName && !externalNode.connectedWorkers.includes(workerName)) {
        externalNode.connectedWorkers.push(workerName);
      }

      const linkDescriptor = `${resolvedPodName}:${podIntf} -> host:${hostIntf} @ ${workerName}`;
      if (!externalNode.connectedLinks.includes(linkDescriptor)) {
        externalNode.connectedLinks.push(linkDescriptor);
      }
    }

    return Array.from(externalNodeMap.values());
  };

  const refreshPodStatuses = async () => {
    try {
      const podsRes = await fetch(`${API_BASE_URL}/pods/${namespace}`);
      const podsData = await checkResponse(podsRes);
      const podsList = podsData.pods || [];

      const podStatusMap = {};
      for (const pod of podsList) {
        podStatusMap[pod.name] = pod.status;
      }

      setNodes((prevNodes) =>
        prevNodes.map((node) => {
          if (node.data?.type === 'external') return node;

          const nextStatus = podStatusMap[node.id];
          if (!nextStatus || node.data?.status === nextStatus) return node;

          const nextFullInfo = node.data?.fullInfo
            ? { ...node.data.fullInfo, status: nextStatus }
            : node.data?.fullInfo;

          return {
            ...node,
            data: {
              ...node.data,
              status: nextStatus,
              fullInfo: nextFullInfo,
            },
          };
        })
      );

      setSelectedNodeInfo((prev) => {
        if (!prev || prev.type === 'external') return prev;
        const nextStatus = podStatusMap[prev.name];
        if (!nextStatus || prev.status === nextStatus) return prev;
        return { ...prev, status: nextStatus };
      });
    } catch (err) {
      console.error('❌ Error refreshing pod statuses:', err);
    }
  };

  const fetchTopology = async (force = false) => {
    const now = Date.now();

    if (topologyFetchInFlightRef.current) {
      return;
    }

    // Don't let background refreshes touch the graph while the import
    // force-layout animation is playing (the backend has no pods yet, a
    // refresh would wipe the placeholders mid-animation).
    if (animatingRef.current) {
      return;
    }

    // Defensive guard: skip background refreshes while a deploy/modify/
    // clear/configure is in progress. Avoids hammering /namespaces/ips
    // with sshqemu probes against pods that are still booting (each one
    // costs ~5s of dead-session detection inside the backend). Explicit
    // force=true calls (post-operation refresh, manual refresh button)
    // still go through.
    if (!force && isBusyRef.current) {
      return;
    }

    if (!force && now - lastTopologyFetchAtRef.current < MIN_TOPOLOGY_FETCH_GAP_MS) {
      return;
    }

    topologyFetchInFlightRef.current = true;
    lastTopologyFetchAtRef.current = now;

    try {
      const [topologyRes, podsRes, ipRes] = await Promise.all([
        fetch(`${API_BASE_URL}/network/get-network/${namespace}`),
        fetch(`${API_BASE_URL}/pods/${namespace}`),
        fetch(`${API_BASE_URL}/namespaces/ips/${namespace}`),
      ]);

      const topologyData = await checkResponse(topologyRes);
      const podsData = await checkResponse(podsRes);
      const rawInterfacesData = await checkResponse(ipRes);

      const normalized = normalizeInterfaces(rawInterfacesData);
      setInterfacesData(normalized);

      const podsList = podsData.pods || [];
      const podStatusMap = {};
      for (const pod of podsList) {
        podStatusMap[pod.name] = pod.status;
      }

      // Nodes-but-zero-links is still a valid topology (isolated pod).
      // Backend serializes nil links as JSON null, so don't treat null
      // links as "no topology", only a missing `nodes` field is empty.
      if (!topologyData.nodes) {
        // No topology (e.g. after Clear): drop any nodes/edges still in state so
        // a stale graph isn't left rendered under the "No topology" message.
        setInitialLoading(false);
        setNodes([]);
        setEdges([]);
        deletingPodsRef.current = new Set();
        deletingLinksRef.current = new Set();
        setHasGraph(false);
        return;
      }

      // const expandedPods = expandNodesIntoPods(topologyData.nodes || []);
      // const podPositions = generateNodePositions(expandedPods);
      const realNodes = topologyData.nodes || [];
      const nodeSpecByName = {};
      for (const node of realNodes) {
        nodeSpecByName[node.name] = node;
      }
      const podDetailsMap = {};
      for (const pod of podsList) {
        const spec = nodeSpecByName[pod.baseName] || nodeSpecByName[pod.name];
        podDetailsMap[pod.name] = {
          ...pod,
          env: spec?.env || {},
          mounts: spec?.mounts || [],
        };
      }
      const expandedPods = expandNodesIntoPods(realNodes);
      const externalNodes = addExternalNodesFromLinks(
        topologyData.links || [],
        podsList,
        realNodes
      );
      const allPods = [...expandedPods, ...externalNodes];
      const podPositions = generateNodePositionsForce(
        allPods,
        topologyData.links || [],
        topologyData.nodes || []
      );

      const newNodes = generateNodes({
        pods: allPods,
        interfacesData: normalized,
        podPositions,
        podStatusMap,
        podDetailsMap,
        currentPositions: positionRef.current,
        restartingPods: restartingPodsRef.current,
      });

      //const newEdges = generateEdges(topologyData.links);
      const newEdges = generateEdges(topologyData.links || [], allPods, topologyData.nodes || []);

      // Detect newly-created links by comparing UIDs against known set.
      // Skip on the very first fetch for this namespace (initial load shouldn't flash).
      const currentUids = new Set();
      const newlyCreatedUids = new Set();
      for (const e of newEdges) {
        const uid = e.data?.uid;
        if (uid === undefined || uid === null) continue;
        currentUids.add(uid);
        if (!isFirstTopologyFetchRef.current && !knownLinkUidsRef.current.has(uid)) {
          newlyCreatedUids.add(uid);
        }
      }
      // Detect newly-created nodes by comparing names (skip external endpoints).
      const currentNodeNames = new Set();
      const newlyCreatedNodeNames = new Set();
      for (const n of newNodes) {
        if (n.data?.type === 'external') continue;
        currentNodeNames.add(n.id);
        if (!isFirstTopologyFetchRef.current && !knownNodeNamesRef.current.has(n.id)) {
          newlyCreatedNodeNames.add(n.id);
        }
      }
      // Add new ones to the persistent "still creating" set, with a per-uid safety net.
      // If a busy op is active, give a long safety window (the isBusy→false transition
      // will clear it sooner). Otherwise, short window since this came from a periodic refresh.
      const safetyMs = isBusyRef.current ? CREATE_FLASH_SAFETY_MS : CREATE_FLASH_IDLE_MS;
      newlyCreatedUids.forEach((uid) => {
        creatingUidsRef.current.add(uid);
        if (creatingTimersRef.current.has(uid)) {
          clearTimeout(creatingTimersRef.current.get(uid));
        }
        const t = setTimeout(() => clearCreatingUids([uid]), safetyMs);
        creatingTimersRef.current.set(uid, t);
      });
      newlyCreatedNodeNames.forEach((name) => {
        creatingNodeNamesRef.current.add(name);
        if (creatingNodeTimersRef.current.has(name)) {
          clearTimeout(creatingNodeTimersRef.current.get(name));
        }
        const t = setTimeout(() => clearCreatingNodeNames([name]), safetyMs);
        creatingNodeTimersRef.current.set(name, t);
      });
      // Apply creating flag from the persistent refs (so flag survives across refetches)
      if (creatingUidsRef.current.size > 0) {
        for (const e of newEdges) {
          if (creatingUidsRef.current.has(e.data?.uid)) {
            e.data = { ...e.data, creating: true };
          }
        }
      }
      if (creatingNodeNamesRef.current.size > 0) {
        for (const n of newNodes) {
          if (creatingNodeNamesRef.current.has(n.id)) {
            n.data = { ...n.data, creating: true };
          }
        }
      }
      knownLinkUidsRef.current = currentUids;
      knownNodeNamesRef.current = currentNodeNames;
      isFirstTopologyFetchRef.current = false;

      // Compute hash of new topology
      const newVersion = hashTopology(newNodes, newEdges);

      // If topology version changed (e.g., deleted and recreated), clear cache
      if (topologyVersionRef.current && topologyVersionRef.current !== newVersion) {
        const cacheKey = `kubendt.networkGraph.${namespace}`;
        sessionStorage.removeItem(cacheKey);
      }

      if (isDeployingRef.current) {
        const expectedNames = new Set(expandedPods.map((p) => p.name));
        const allReady = Array.from(expectedNames).every(
          (name) => podStatusMap[name] === 'Running' || podStatusMap[name] === 'Succeeded'
        );

        if (!allReady) {
          // Still mid-deployment: don't render anything or change positions
          return;
        }

        // Ya estable: desbloqueamos refrescos
        isDeployingRef.current = false;
      }

      setNodes(newNodes);
      setEdges(newEdges);
      setHasGraph(newNodes.length > 0);
      setInitialLoading(false); // Disable initial loading after first fetch
    } catch (err) {
      console.error('❌ Error fetching topology:', err);
      if (onError) onError(err);
      setInitialLoading(false); // Also disable on error
    } finally {
      topologyFetchInFlightRef.current = false;
    }
  };

  const handleNodesChange = (changes) => {
    changes.forEach((change) => {
      if (change.type === 'position' && change.id && change.position) {
        positionRef.current[change.id] = change.position;
      }
    });
    onNodesChange(changes);
  };

  const handleSavePositions = async () => {
    if (nodes.length === 0) {
      setAlertModal({
        isOpen: true,
        type: 'warning',
        title: 'No nodes',
        message: 'There are no nodes to save positions for.',
        onConfirm: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
        onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
      });
      return;
    }

    const positionsToSave = Object.fromEntries(nodes.map((n) => [n.id, n.position]));

    try {
      await fetch(`${API_BASE_URL}/network/positions/${namespace}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(positionsToSave),
      });
      setAlertModal({
        isOpen: true,
        type: 'success',
        title: 'Positions saved',
        message: 'Node positions (x, y) have been saved successfully.',
        onConfirm: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
        onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
      });
      setSavedPositions(positionsToSave);
    } catch (error) {
      console.error('❌ Error saving positions:', error);
      showError('Error saving positions', 'An error occurred while saving node positions.');
    }
  };

  const handleExportTopology = async () => {
    if (!namespace) return;

    try {
      const res = await fetch(`${API_BASE_URL}/network/get-network/${namespace}`);
      const data = await res.json();

      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `topology-${namespace}.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      console.error('❌ Error exporting topology:', err);
      showError('Export failed', err.message || 'Could not export the current topology.');
    }
  };

  // Pure validator for the import (deploy) payload. Throws on the first error encountered.
  // Does NOT mutate the input, name resolution happens in applyImportTopology.
  const validateImportTopologyPayload = (json) => {
    if (!json || typeof json !== 'object' || Array.isArray(json)) {
      throw new Error('Topology JSON must be an object.');
    }
    const allowedKeys = new Set(['nodes', 'links']);
    for (const key of Object.keys(json)) {
      if (!allowedKeys.has(key)) {
        throw new Error(
          `Unrecognized root-level field: '${key}'. Only 'nodes' and 'links' are allowed.`
        );
      }
    }
    if (!Array.isArray(json.nodes) || json.nodes.length < 1) {
      throw new Error('Must have at least 1 node.');
    }

    const nodeNames = new Set();
    for (let i = 0; i < json.nodes.length; i++) {
      const node = json.nodes[i];
      const label = isNonEmptyString(node?.name) ? `node '${node.name}'` : `nodes[${i}]`;
      validateNodeSpecFields(node, label);
      if (nodeNames.has(node.name)) {
        throw new Error(`Duplicate node name '${node.name}'.`);
      }
      nodeNames.add(node.name);
    }

    if (json.links !== undefined && !Array.isArray(json.links)) {
      throw new Error("'links' must be an array if present.");
    }

    if (Array.isArray(json.links) && json.links.length > 0) {
      const replicaMap = new Map(json.nodes.map((n) => [n.name, n.replicas || 1]));
      const resolveName = (input) => {
        if (nodeNames.has(input)) return input;
        if (typeof input === 'string' && input.match(/-\d+$/)) return input;
        const replicas = replicaMap.get(input);
        if (replicas === 1) return `${input}-0`;
        return null;
      };
      const ifaceCheck = (name, field) => {
        if (!name) return;
        if (RESERVED_IFACE_NAMES.has(name)) {
          throw new Error(
            `link.${field} "${name}": reserved interface (primary CNI/management or loopback). Use eth1+ or another name.`
          );
        }
        if (name.length > 15) {
          throw new Error(
            `link.${field} "${name}": interface name exceeds 15 characters (Linux limit).`
          );
        }
        if (/[/: \t\n]/.test(name)) {
          throw new Error(
            `link.${field} "${name}": invalid characters ('/', ':', whitespace). Use hyphens (e.g. Gi0-0-0-0).`
          );
        }
      };
      const uids = new Set();
      for (let i = 0; i < json.links.length; i++) {
        const link = json.links[i];
        validateLinkSpecFields(link, `links[${i}]`);
        ifaceCheck(link.localIntf, 'localIntf');
        ifaceCheck(link.peerIntf, 'peerIntf');
        const nodeIsExternal = link.node === 'external';
        const peerIsExternal = link.peerNode === 'external';
        if (nodeIsExternal && peerIsExternal) {
          throw new Error(
            `links[${i}]: both 'node' and 'peerNode' cannot be 'external'; at least one must be a real node.`
          );
        }
        if (!nodeIsExternal && !resolveName(link.node)) {
          throw new Error(
            `Link references non-existent or ambiguous node: ${link.node} ↔ ${link.peerNode}`
          );
        }
        if (!peerIsExternal && !resolveName(link.peerNode)) {
          throw new Error(
            `Link references non-existent or ambiguous node: ${link.node} ↔ ${link.peerNode}`
          );
        }
        if (link.uid !== undefined && link.uid !== null) {
          if (uids.has(link.uid)) throw new Error(`Duplicated UID in links: ${link.uid}`);
          uids.add(link.uid);
        }
      }
    }
  };

  const applyImportTopology = async (json) => {
    try {
      // Re-validate as a safety net (modal already validated; this is harmless if valid).
      validateImportTopologyPayload(json);

      const nodeNames = new Set(json.nodes.map((n) => n.name));
      const replicaMap = new Map(json.nodes.map((n) => [n.name, n.replicas || 1]));

      const resolveNodeName = (input) => {
        if (nodeNames.has(input)) return input;
        if (input.match(/-\d+$/)) return input;
        const replicas = replicaMap.get(input);
        if (replicas === 1) return `${input}-0`;
        return null;
      };

      // Mutate links to use resolved (indexed) pod names, backend expects this form.
      // "external" is a reserved name; skip resolution for whichever side uses it.
      for (const link of json.links || []) {
        link.node = link.node === 'external' ? 'external' : resolveNodeName(link.node) || link.node;
        link.peerNode =
          link.peerNode === 'external'
            ? 'external'
            : resolveNodeName(link.peerNode) || link.peerNode;
      }

      isDeployingRef.current = true;

      // Snapshot the real saved positions (loaded from the DB on namespace
      // change) BEFORE buildPlaceholderNodes runs, because that function
      // pins its own grid layout into positionRef and would otherwise make
      // every fresh import look like it already had saved positions.
      const savedSnapshot = { ...positionRef.current };

      // Render "creating" placeholders straight from the JSON so the user
      // sees the graph appear before the backend finishes. Deep-copy links
      // so buildPlaceholderExternalNodes can rewrite "external" → peerLabel
      // without touching the json sent to the backend.
      const phLinksCopy = (json.links || []).map((l) => ({ ...l }));
      const phNodes = buildPlaceholderNodes(json.nodes || []);
      const phExternalNodes = buildPlaceholderExternalNodes(phLinksCopy);
      phNodes.forEach((n) => creatingNodeNamesRef.current.add(n.id));
      const allPhNodes = [...phNodes, ...phExternalNodes];
      const phEdges = buildPlaceholderEdges(phLinksCopy, allPhNodes);

      // Fresh import (empty graph): drop the nodes at the centre and animate
      // them spreading apart with a live force layout before the import
      // overlay appears. Adding to an existing topology keeps the previous
      // behaviour (no animation, overlay straight away).
      const isFreshImport =
        (nodesRef.current || []).filter((n) => n.data?.type !== 'external').length === 0 &&
        allPhNodes.length > 0;

      // If this namespace already has saved positions for every imported
      // node (loaded into positionRef on namespace change), animate the
      // nodes from the centre to their saved spots instead of running the
      // force layout, and keep the saved positions intact.
      const savedTargets = {};
      let allHaveSaved = allPhNodes.length > 0;
      allPhNodes.forEach((n) => {
        const p = savedSnapshot[n.id];
        if (p) savedTargets[n.id] = p;
        else allHaveSaved = false;
      });

      if (isFreshImport && allHaveSaved) {
        // Real saved layout exists: restore it into positionRef (overwriting
        // the grid that buildPlaceholderNodes pinned) and glide to it.
        allPhNodes.forEach((n) => {
          n.position = { x: 0, y: 0 };
          positionRef.current[n.id] = savedTargets[n.id];
        });
        setNodes((prev) => [...prev, ...allPhNodes]);
        if (phEdges.length > 0) setEdges((prev) => [...prev, ...phEdges]);
        animatingRef.current = true;
        try {
          await animateToPositions(savedTargets, setNodes);
        } finally {
          animatingRef.current = false;
        }
        setImporting(true);
      } else if (isFreshImport) {
        // No saved layout: compute the force layout (deterministic, the
        // same one a refresh produces) and glide the nodes out to it from
        // the centre. Drop the grid positions buildPlaceholderNodes pinned
        // and pin the force result so the post-deploy fetch reuses it.
        const animPods = allPhNodes.map((n) => ({ name: n.id, type: n.data?.type }));
        const finalPos = generateNodePositionsForce(animPods, phLinksCopy, json.nodes || []);
        allPhNodes.forEach((n) => {
          n.position = { x: 0, y: 0 };
          positionRef.current[n.id] = finalPos[n.id] || { x: 0, y: 0 };
        });
        setNodes((prev) => [...prev, ...allPhNodes]);
        if (phEdges.length > 0) setEdges((prev) => [...prev, ...phEdges]);
        animatingRef.current = true;
        try {
          await animateToPositions(finalPos, setNodes);
        } finally {
          animatingRef.current = false;
        }
        setImporting(true);
      } else {
        setImporting(true);
        if (allPhNodes.length > 0) {
          setNodes((prev) => [...prev, ...allPhNodes]);
        }
        if (phEdges.length > 0) {
          setEdges((prev) => [...prev, ...phEdges]);
        }
      }

      // Track what we optimistically added so we can roll it back on
      // backend errors (lock conflict, validation, etc.), otherwise the
      // partial graph lingers until the user manually refreshes.
      const phNodeIds = new Set(allPhNodes.map((n) => n.id));
      const phEdgeIds = new Set(phEdges.map((e) => e.id));
      const rollbackPlaceholders = () => {
        if (phNodeIds.size > 0) {
          setNodes((prev) => prev.filter((n) => !phNodeIds.has(n.id)));
          phNodes.forEach((n) => creatingNodeNamesRef.current.delete(n.id));
        }
        if (phEdgeIds.size > 0) {
          setEdges((prev) => prev.filter((e) => !phEdgeIds.has(e.id)));
        }
      };

      let res;
      try {
        res = await fetch(`${API_BASE_URL}/network/deploy-network/${namespace}`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(json),
        });
      } catch (netErr) {
        rollbackPlaceholders();
        throw netErr;
      }

      if (res.ok) {
        const payload = await res.json().catch(() => null);
        await fetchTopology(true);
        showOperationSuccessModal(
          'Topology deployed',
          payload?.message || 'Network infrastructure deployed successfully.',
          payload?.took_time,
          payload?.warnings
        );
      } else {
        const payload = await res.json().catch(() => null);
        // The backend rolled back the partial deploy. Show the attempted
        // nodes/edges as "deleting" briefly so it's clear they are being
        // removed (not left as if deployed and tempting a re-import), then drop
        // them and refresh to the true, empty state.
        setNodes((prev) =>
          prev.map((n) =>
            phNodeIds.has(n.id) ? { ...n, data: { ...n.data, creating: false, deleting: true } } : n
          )
        );
        setEdges((prev) =>
          prev.map((e) =>
            phEdgeIds.has(e.id) ? { ...e, data: { ...e.data, creating: false, deleting: true } } : e
          )
        );
        setTimeout(() => {
          rollbackPlaceholders();
          fetchTopology(true);
        }, 1200);
        showError(
          'Deployment Error',
          payload?.error || 'Backend returned an error during deployment.',
          payload?.rolledBack ? 'The partially created topology was rolled back.' : ''
        );
        return;
      }
    } catch (err) {
      showError('Deployment Error', err.message || 'Deployment failed.');
    } finally {
      setImporting(false);
    }
  };

  const validateNetworkConfigure = (json) => {
    if (!json || typeof json !== 'object' || Array.isArray(json)) {
      throw new Error('Network configuration must be a JSON object.');
    }
    // Root accepts only 'targets'
    for (const key of Object.keys(json)) {
      if (key !== 'targets') {
        throw new Error(`Unknown root-level field '${key}'. Only 'targets' is allowed.`);
      }
    }
    if (!Array.isArray(json.targets) || json.targets.length === 0) {
      throw new Error("'targets' must exist as a non-empty array.");
    }
    for (let i = 0; i < json.targets.length; i++) {
      const t = json.targets[i];
      if (!t || typeof t !== 'object' || Array.isArray(t)) {
        throw new Error(`targets[${i}]: must be an object.`);
      }
      ensureAllowedFields(t, CONFIGURE_TARGET_FIELDS, `targets[${i}]`);
      if (!t.pod || typeof t.pod !== 'string') {
        throw new Error(`targets[${i}]: must include 'pod' (string).`);
      }
      if (!Array.isArray(t.actions) || t.actions.length === 0) {
        throw new Error(`Target '${t.pod}': must include 'actions' (non-empty array).`);
      }
      for (let j = 0; j < t.actions.length; j++) {
        const a = t.actions[j];
        if (!a || typeof a !== 'object' || Array.isArray(a)) {
          throw new Error(`Target '${t.pod}'.actions[${j}]: must be an object.`);
        }
        if (!a.type || typeof a.type !== 'string') {
          throw new Error(`Target '${t.pod}'.actions[${j}]: missing 'type'.`);
        }
        // Action fields beyond 'type' are driver-specific (varies per action type),
        // so we don't strict-check them, backend's driver validates per-action.
      }
    }
  };

  const handleLoadNetworkConfClick = () => {
    if (!hasGraph) {
      setAlertModal({
        isOpen: true,
        type: 'warning',
        title: 'No Topology Loaded',
        message: 'Import a topology before applying a network configuration.',
        onConfirm: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
        onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
      });
      return;
    }
    setTopologyInputKind('config');
  };

  const validateModifyTopologyPayload = (json) => {
    if (!json || typeof json !== 'object' || Array.isArray(json)) {
      throw new Error('File must be a JSON object.');
    }

    const keys = Object.keys(json);
    const allowedKeys = new Set(['add', 'delete', 'scale']);
    for (const key of keys) {
      if (!allowedKeys.has(key)) {
        throw new Error(
          `Unrecognized root-level field: '${key}'. Only 'add', 'delete' and 'scale' are allowed.`
        );
      }
    }

    const hasAdd = Object.prototype.hasOwnProperty.call(json, 'add');
    const hasDelete = Object.prototype.hasOwnProperty.call(json, 'delete');
    const hasScale = Object.prototype.hasOwnProperty.call(json, 'scale');
    if (!hasAdd && !hasDelete && !hasScale) {
      throw new Error("Modify topology JSON must include 'add', 'delete' and/or 'scale'.");
    }

    const validateAdd = (addObj) => {
      if (!addObj || typeof addObj !== 'object' || Array.isArray(addObj)) {
        throw new Error("'add' must be an object.");
      }
      const addKeys = Object.keys(addObj);
      for (const key of addKeys) {
        if (key !== 'nodes' && key !== 'links') {
          throw new Error(`'add' contains unsupported field '${key}'. Allowed: 'nodes', 'links'.`);
        }
      }
      if (addObj.nodes !== undefined && !Array.isArray(addObj.nodes)) {
        throw new Error("'add.nodes' must be an array.");
      }
      if (addObj.links !== undefined && !Array.isArray(addObj.links)) {
        throw new Error("'add.links' must be an array.");
      }
      if (Array.isArray(addObj.nodes)) {
        addObj.nodes.forEach((n, i) => {
          const label = isNonEmptyString(n?.name) ? `add.nodes['${n.name}']` : `add.nodes[${i}]`;
          validateNodeSpecFields(n, label);
        });
      }
      if (Array.isArray(addObj.links)) {
        addObj.links.forEach((l, i) => {
          validateLinkSpecFields(l, `add.links[${i}]`);
          for (const f of ['localIntf', 'peerIntf']) {
            if (RESERVED_IFACE_NAMES.has(l[f])) {
              throw new Error(
                `add.links[${i}].${f} "${l[f]}": reserved interface (primary CNI/management or loopback). Use eth1+ or another name.`
              );
            }
          }
        });
      }
    };

    const validateDelete = (delObj) => {
      if (!delObj || typeof delObj !== 'object' || Array.isArray(delObj)) {
        throw new Error("'delete' must be an object.");
      }
      const delKeys = Object.keys(delObj);
      for (const key of delKeys) {
        if (key !== 'nodes' && key !== 'links') {
          throw new Error(
            `'delete' contains unsupported field '${key}'. Allowed: 'nodes', 'links'.`
          );
        }
      }
      if (delObj.nodes !== undefined && !Array.isArray(delObj.nodes)) {
        throw new Error("'delete.nodes' must be an array.");
      }
      if (delObj.links !== undefined && !Array.isArray(delObj.links)) {
        throw new Error("'delete.links' must be an array.");
      }
      // delete.nodes is just an array of base names (strings) → only type check
      if (Array.isArray(delObj.nodes)) {
        delObj.nodes.forEach((n, i) => {
          if (typeof n !== 'string' || !n.trim()) {
            throw new Error(`delete.nodes[${i}]: must be a non-empty string (node base name).`);
          }
        });
      }
      // delete.links carries LinkSpec objects → field-strict
      if (Array.isArray(delObj.links)) {
        delObj.links.forEach((l, i) => {
          if (!l || typeof l !== 'object' || Array.isArray(l)) {
            throw new Error(`delete.links[${i}]: must be an object.`);
          }
          ensureAllowedFields(l, LINK_SPEC_FIELDS, `delete.links[${i}]`);
        });
      }
    };

    const validateScale = (scaleArr) => {
      if (!Array.isArray(scaleArr)) {
        throw new Error("'scale' must be an array of objects.");
      }
      const seen = new Set();
      scaleArr.forEach((entry, i) => {
        if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
          throw new Error(`scale[${i}]: must be an object.`);
        }
        ensureAllowedFields(entry, SCALE_SPEC_FIELDS, `scale[${i}]`);
        if (typeof entry.name !== 'string' || !entry.name.trim()) {
          throw new Error(`scale[${i}]: 'name' is required and must be a non-empty string.`);
        }
        if (/-\d+$/.test(entry.name)) {
          throw new Error(
            `scale[${i}]: 'name' must be a node base name (e.g. 'host'), not an indexed pod ('${entry.name}').`
          );
        }
        if (seen.has(entry.name)) {
          throw new Error(`scale: duplicate entry for node '${entry.name}'.`);
        }
        seen.add(entry.name);
        if (!Number.isInteger(entry.replicas)) {
          throw new Error(`scale[${i}] ('${entry.name}'): 'replicas' must be an integer.`);
        }
        if (entry.replicas < 1) {
          throw new Error(
            `scale[${i}] ('${entry.name}'): 'replicas' must be >= 1. To remove the node entirely use delete.nodes.`
          );
        }
        if (entry.replicas > MAX_REPLICAS) {
          throw new Error(`scale[${i}] ('${entry.name}'): 'replicas' max is ${MAX_REPLICAS}.`);
        }
      });

      // Cross-section consistency: a node cannot be scaled if it's also
      // being added or deleted in the same request.
      if (hasAdd && Array.isArray(json.add?.nodes)) {
        const addNames = new Set(json.add.nodes.map((n) => n?.name).filter(Boolean));
        for (const entry of scaleArr) {
          if (addNames.has(entry.name)) {
            throw new Error(
              `scale: node '${entry.name}' is also in add.nodes; a node cannot be both newly created and scaled in the same request.`
            );
          }
        }
      }
      if (hasDelete && Array.isArray(json.delete?.nodes)) {
        const delNames = new Set(json.delete.nodes);
        for (const entry of scaleArr) {
          if (delNames.has(entry.name)) {
            throw new Error(
              `scale: node '${entry.name}' is also in delete.nodes; use one or the other.`
            );
          }
        }
      }
    };

    if (hasAdd) validateAdd(json.add);
    if (hasDelete) validateDelete(json.delete);
    if (hasScale) validateScale(json.scale);
  };

  const handleModifyTopologyClick = () => {
    if (!hasGraph) {
      setAlertModal({
        isOpen: true,
        type: 'warning',
        title: 'No Topology Loaded',
        message: 'Import a topology before applying a modify payload.',
        onConfirm: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
        onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
      });
      return;
    }
    setTopologyInputKind('modify');
  };

  const resetTopologyView = () => {
    setNodes([]);
    setEdges([]);
    setHasGraph(false);
    setSelectedNodeInfo(null);
    setSelectedLink(null);
    setInterfacesData({});
    topologyVersionRef.current = null;
  };

  const executeClearTopology = async () => {
    const shouldClearPositions = clearPositionsRef.current;
    const shouldDeleteFiles = clearFilesRef.current;

    // Mark ALL non-external nodes and ALL edges as deleting upfront, same visual
    // pattern as modify-delete: red translucent nodes + red dashed edges from t=0.
    // No polling: we just wait for the DELETE response and refetch once at the end.
    const targetPodNames = (nodesRef.current || [])
      .filter((n) => n.data?.type !== 'external')
      .map((n) => n.id);
    const targetEdgeIds = (edgesRef.current || []).map((e) => e.id);

    targetPodNames.forEach(addDeletingPod);
    targetEdgeIds.forEach(addDeletingLink);

    try {
      setClearingTopology(true);

      const params = new URLSearchParams();
      if (shouldClearPositions) params.set('deletePositions', 'true');
      if (shouldDeleteFiles) params.set('deleteFiles', 'true');
      const queryString = params.toString() ? `?${params.toString()}` : '';

      const res = await fetch(`${API_BASE_URL}/network/clear-topology/${namespace}${queryString}`, {
        method: 'DELETE',
      });
      const payload = await res.json().catch(() => null);
      if (!res.ok) {
        throw new Error(payload?.error || `Backend error (${res.status}) while clearing topology.`);
      }

      resetTopologyView();
      if (shouldClearPositions) {
        // Clear local state; backend already cleaned the DB.
        positionRef.current = {};
        sessionStorage.removeItem(`kubendt.networkGraph.${namespace}`);
      }
      await fetchTopology(true);
      showOperationSuccessModal(
        'Topology cleared',
        payload?.message || `All topology resources were removed from namespace '${namespace}'.`,
        payload?.took_time
      );
    } catch (err) {
      // Rollback the deleting visuals so the user can see/retry the topology.
      targetPodNames.forEach(removeDeletingPod);
      targetEdgeIds.forEach(removeDeletingLink);
      showError('Clear Topology Error', err.message || 'Could not clear topology resources.');
    } finally {
      setClearingTopology(false);
    }
  };

  const handleClearTopologyClick = () => {
    if (!hasGraph) {
      setAlertModal({
        isOpen: true,
        type: 'warning',
        title: 'No Topology Loaded',
        message: 'There is no topology to clear in this namespace.',
        confirmText: 'OK',
        cancelText: 'Cancel',
        showPositionCheckbox: false,
        onConfirm: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
        onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
      });
      return;
    }

    clearPositionsRef.current = false;
    setClearPositionsChecked(false);
    clearFilesRef.current = false;
    setClearFilesChecked(false);
    const podCount = (nodesRef.current || []).filter((n) => n.data?.type !== 'external').length;
    const linkCount = (edgesRef.current || []).length;
    const inventory = `This will remove ${podCount} pod${podCount === 1 ? '' : 's'} and ${linkCount} link${linkCount === 1 ? '' : 's'}.`;
    setAlertModal({
      isOpen: true,
      type: 'confirm',
      title: 'Clear Topology',
      message: `Delete all KubeNDT topology resources in namespace '${namespace}'? ${inventory} The namespace will be kept.`,
      confirmText: 'Clear topology',
      cancelText: 'Cancel',
      showPositionCheckbox: true,
      onConfirm: () => {
        setAlertModal((prev) => ({ ...prev, isOpen: false }));
        executeClearTopology();
      },
      onCancel: () => {
        setAlertModal((prev) => ({ ...prev, isOpen: false }));
      },
    });
  };

  const executeDeleteModify = async (
    deletePayload,
    successTitle,
    onLocalSuccess,
    onLocalCleanup
  ) => {
    setModifyingTopology(true);
    try {
      const res = await fetch(`${API_BASE_URL}/network/modify-network/${namespace}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ delete: deletePayload }),
      });
      if (!res.ok) {
        const payload = await res.json().catch(() => null);
        throw new Error(payload?.error || `Backend error (${res.status})`);
      }
      const payload = await res.json().catch(() => null);
      if (onLocalSuccess) onLocalSuccess();
      await fetchTopology(true);
      showOperationSuccessModal(
        successTitle,
        payload?.message || 'Delete applied successfully.',
        payload?.took_time
      );
    } catch (err) {
      showError('Delete error', err.message || 'Could not apply delete.');
    } finally {
      if (onLocalCleanup) onLocalCleanup();
      setModifyingTopology(false);
    }
  };

  const handleDeleteNode = (pod) => {
    if (!pod) return;
    if (pod.type === 'external') return; // external endpoints are not managed via modify-network
    if (isBusy) {
      showBusyWarning();
      return;
    }

    const baseName = pod.baseName || pod.name;
    const replicaCount = pod.replicaCount || 1;
    const message =
      replicaCount > 1
        ? `Delete node '${baseName}'? This will remove ALL ${replicaCount} replicas (${baseName}-0 … ${baseName}-${replicaCount - 1}) and their links.`
        : `Delete node '${baseName}' (pod '${pod.name}')? This will remove it from the topology along with its links.`;

    const targetPodNames = [];
    for (let i = 0; i < replicaCount; i++) {
      targetPodNames.push(`${baseName}-${i}`);
    }

    setAlertModal({
      isOpen: true,
      type: 'confirm',
      title: 'Delete pod',
      message,
      confirmText: 'Delete',
      cancelText: 'Cancel',
      onConfirm: () => {
        setAlertModal((prev) => ({ ...prev, isOpen: false }));
        const targetPodNameSet = new Set(targetPodNames);
        const targetEdgeIds = (edgesRef.current || [])
          .filter((e) => targetPodNameSet.has(e.source) || targetPodNameSet.has(e.target))
          .map((e) => e.id);
        targetPodNames.forEach(addDeletingPod);
        targetEdgeIds.forEach(addDeletingLink);
        executeDeleteModify(
          { nodes: [baseName] },
          'Pod deleted',
          () => {
            setSelectedNodeInfo((prev) =>
              prev && (prev.baseName === baseName || prev.name === baseName) ? null : prev
            );
          },
          () => {
            targetPodNames.forEach(removeDeletingPod);
            targetEdgeIds.forEach(removeDeletingLink);
          }
        );
      },
      onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
    });
  };

  const handleDeleteExternal = (externalNode) => {
    if (!externalNode) return;
    if (isBusy) {
      showBusyWarning();
      return;
    }

    // The external node carries the friendly label as its id in the graph
    // (e.g. "External Network"). Find every edge touching that id.
    const externalId = externalNode.name || externalNode.baseName;
    if (!externalId) return;

    const allEdges = edgesRef.current || [];
    const externalEdges = allEdges.filter(
      (e) => e.source === externalId || e.target === externalId
    );
    if (externalEdges.length === 0) return;

    // Build the delete payload. Each edge contributes one link with the
    // external side translated back to the "external" keyword (the API
    // does not understand the friendly label).
    const linksToDelete = externalEdges.map((edge) => {
      const sourceType = (nodesRef.current || []).find((n) => n.id === edge.source)?.data?.type;
      const sourceIsExternal = sourceType === 'external' || edge.source === externalId;
      const node = sourceIsExternal ? 'external' : edge.source;
      const peerNode = sourceIsExternal ? edge.target : 'external';
      return {
        node,
        localIntf: edge.data?.localIntf,
        peerNode,
        peerIntf: edge.data?.peerIntf,
        ...(edge.data?.uid !== undefined && edge.data?.uid !== null ? { uid: edge.data.uid } : {}),
      };
    });

    // Build a readable summary of which pods will lose connectivity.
    const affectedPods = new Set();
    for (const edge of externalEdges) {
      if (edge.source !== externalId) affectedPods.add(edge.source);
      if (edge.target !== externalId) affectedPods.add(edge.target);
    }
    const podList = Array.from(affectedPods).sort().join(', ');
    const linksCountStr = `${externalEdges.length} link${externalEdges.length === 1 ? '' : 's'}`;

    setAlertModal({
      isOpen: true,
      type: 'confirm',
      title: 'Delete external network',
      message: `Delete external network '${externalId}'? This will remove ${linksCountStr} connecting [${podList}] to it. The external node will disappear from the graph and the affected pods will be restarted.`,
      confirmText: 'Delete',
      cancelText: 'Cancel',
      onConfirm: () => {
        setAlertModal((prev) => ({ ...prev, isOpen: false }));
        const edgeIds = externalEdges.map((e) => e.id);
        edgeIds.forEach(addDeletingLink);
        addDeletingPod(externalId);
        executeDeleteModify(
          { links: linksToDelete },
          'External network deleted',
          () => {
            setSelectedNodeInfo((prev) =>
              prev && (prev.name === externalId || prev.baseName === externalId) ? null : prev
            );
          },
          () => {
            edgeIds.forEach(removeDeletingLink);
            removeDeletingPod(externalId);
          }
        );
      },
      onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
    });
  };

  const handleDeleteLink = (edge) => {
    if (!edge) return;
    const localIntf = edge.data?.localIntf;
    const peerIntf = edge.data?.peerIntf;
    const rawNode = edge.source;
    const rawPeerNode = edge.target;
    if (!localIntf || !peerIntf || !rawNode || !rawPeerNode) return;
    if (isBusy) {
      showBusyWarning();
      return;
    }

    // The graph carries external endpoints under their human label
    // (e.g. "External Network") but the API only understands the
    // "external" keyword. Translate before sending. The backend then
    // removes the link entry from the real-pod's Topology CRD; the
    // external side has no CRD of its own.
    const sourceType = nodes.find((n) => n.id === rawNode)?.data?.type;
    const targetType = nodes.find((n) => n.id === rawPeerNode)?.data?.type;
    const sourceIsExternal = sourceType === 'external';
    const targetIsExternal = targetType === 'external';
    const node = sourceIsExternal ? 'external' : rawNode;
    const peerNode = targetIsExternal ? 'external' : rawPeerNode;

    // Display: keep the friendly label on external sides so the user
    // recognises which link they are deleting.
    const displaySrc = sourceIsExternal ? rawNode : node;
    const displayDst = targetIsExternal ? rawPeerNode : peerNode;

    setAlertModal({
      isOpen: true,
      type: 'confirm',
      title: 'Delete link',
      message: `Delete link '${displaySrc}:${localIntf} ↔ ${displayDst}:${peerIntf}'?`,
      confirmText: 'Delete',
      cancelText: 'Cancel',
      onConfirm: () => {
        setAlertModal((prev) => ({ ...prev, isOpen: false }));
        addDeletingLink(edge.id);
        executeDeleteModify(
          {
            links: [
              {
                node,
                localIntf,
                peerNode,
                peerIntf,
                ...(edge.data?.uid !== undefined && edge.data?.uid !== null
                  ? { uid: edge.data.uid }
                  : {}),
              },
            ],
          },
          'Link deleted',
          () => {
            setSelectedLink((prev) => (prev && prev.id === edge.id ? null : prev));
          },
          () => {
            removeDeletingLink(edge.id);
          }
        );
      },
      onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
    });
  };

  const applyModifyTopology = async (json) => {
    try {
      validateModifyTopologyPayload(json);

      setModifyingTopology(true);

      // Optimistic UI: placeholders for `add` + pre-mark `delete`, with
      // scale entries surfacing as either creating (up) or deleting (down).
      const scaleEntries = Array.isArray(json.scale) ? json.scale : [];
      const scaleUpPlaceholderSpecs = [];
      const scaleDownPodNames = [];
      for (const entry of scaleEntries) {
        const baseName = entry?.name;
        const targetReplicas = entry?.replicas;
        if (!baseName || typeof targetReplicas !== 'number') continue;
        // Find the node spec by matching baseName of any of its pods.
        const existingPodNodes = (nodesRef.current || []).filter(
          (n) => n.data?.fullInfo?.baseName === baseName && n.data?.type !== 'external'
        );
        if (existingPodNodes.length === 0) continue;
        const currentReplicas = existingPodNodes.length;
        if (targetReplicas > currentReplicas) {
          // Scale-up: passing replicas=targetReplicas is safe,
          // buildPlaceholderNodes skips already-existing pods, so only
          // ordinals currentReplicas..targetReplicas-1 become placeholders.
          const sample = existingPodNodes[0].data?.fullInfo || {};
          scaleUpPlaceholderSpecs.push({
            name: baseName,
            type: sample.type || 'host',
            image: sample.image || 'alpine',
            replicas: targetReplicas,
            driver: sample.driver,
          });
        } else if (targetReplicas < currentReplicas) {
          // Scale-down: mark soon-to-be-removed pods as deleting.
          for (let i = targetReplicas; i < currentReplicas; i++) {
            scaleDownPodNames.push(`${baseName}-${i}`);
          }
        }
      }

      // Single buildPlaceholderNodes call so nextYByType increments
      // across add.nodes AND scale-up specs (two calls would stack
      // same-type pods on top of each other). Pass them explicitly to
      // buildPlaceholderEdges too, setNodes is async, nodesRef.current
      // won't reflect them yet.
      const combinedPlaceholderSpecs = [
        ...(json.add && Array.isArray(json.add.nodes) ? json.add.nodes : []),
        ...scaleUpPlaceholderSpecs,
      ];
      const allPlaceholderNodes = buildPlaceholderNodes(combinedPlaceholderSpecs);

      // Deep-copy add.links so buildPlaceholderExternalNodes can rewrite
      // "external" → peerLabel without mutating the JSON the backend
      // receives. Without this rewrite the placeholder edge builder
      // can't resolve the "external" endpoint and silently drops the
      // edge (the user sees no green dashed link on the new external
      // attachment until the post-success refresh).
      const phAddLinksCopy =
        json.add && Array.isArray(json.add.links) ? json.add.links.map((l) => ({ ...l })) : [];
      const phExternalNodes = buildPlaceholderExternalNodes(phAddLinksCopy);
      const allPlaceholderNodesWithExternal = [...allPlaceholderNodes, ...phExternalNodes];

      allPlaceholderNodes.forEach((n) => creatingNodeNamesRef.current.add(n.id));
      const placeholderNodesToInsert = [...allPlaceholderNodes, ...phExternalNodes];
      if (placeholderNodesToInsert.length > 0) {
        setNodes((prev) => [...prev, ...placeholderNodesToInsert]);
      }

      const phNodeIds = new Set(placeholderNodesToInsert.map((n) => n.id));
      const phEdgeIds = new Set();

      if (json.add) {
        const phEdges = buildPlaceholderEdges(phAddLinksCopy, allPlaceholderNodesWithExternal);
        if (phEdges.length > 0) {
          phEdges.forEach((e) => phEdgeIds.add(e.id));
          setEdges((prev) => [...prev, ...phEdges]);
        }
      }

      // Track everything we pre-marked for delete so we can revert on
      // backend error. preMarkDeleteFromJson returns the IDs it touched.
      const preMarked = json.delete
        ? preMarkDeleteFromJson(json.delete)
        : { markedPods: new Set(), markedLinks: new Set() };

      // Visually mark scaled-down pods AND every edge touching them as
      // deleting, same rationale as in preMarkDeleteFromJson for
      // node deletions: a node turning red while its links stay solid
      // is jarring.
      const scaleDownMarkedLinks = new Set();
      scaleDownPodNames.forEach(addDeletingPod);
      if (scaleDownPodNames.length > 0) {
        const scaleDownSet = new Set(scaleDownPodNames);
        const existingEdges = edgesRef.current || [];
        for (const edge of existingEdges) {
          if (scaleDownSet.has(edge.source) || scaleDownSet.has(edge.target)) {
            addDeletingLink(edge.id);
            scaleDownMarkedLinks.add(edge.id);
          }
        }
      }

      // On backend error we have to drop every optimistic mutation we
      // applied above, otherwise the user keeps seeing "creating"
      // placeholders and red "deleting" pods/edges forever even though
      // nothing actually happened server-side.
      const rollbackOptimistic = () => {
        if (phNodeIds.size > 0) {
          setNodes((prev) => prev.filter((n) => !phNodeIds.has(n.id)));
          phNodeIds.forEach((id) => creatingNodeNamesRef.current.delete(id));
        }
        if (phEdgeIds.size > 0) {
          setEdges((prev) => prev.filter((e) => !phEdgeIds.has(e.id)));
        }
        preMarked.markedPods.forEach(removeDeletingPod);
        preMarked.markedLinks.forEach(removeDeletingLink);
        scaleDownPodNames.forEach(removeDeletingPod);
        scaleDownMarkedLinks.forEach(removeDeletingLink);
      };

      let res;
      try {
        res = await fetch(`${API_BASE_URL}/network/modify-network/${namespace}`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(json),
        });
      } catch (netErr) {
        rollbackOptimistic();
        throw netErr;
      }

      if (!res.ok) {
        const payload = await res.json().catch(() => null);
        rollbackOptimistic();
        throw new Error(payload?.error || `Backend error (${res.status})`);
      }

      const payload = await res.json().catch(() => null);
      await fetchTopology(true);
      showOperationSuccessModal(
        'Topology modified',
        payload?.message || 'Network modify applied successfully.',
        payload?.took_time,
        payload?.warnings
      );
    } catch (err) {
      showError(
        'Modify Topology Error',
        err.message || 'Invalid modify payload or error applying.'
      );
    } finally {
      setModifyingTopology(false);
    }
  };

  const applyLoadNetworkConf = async (json) => {
    try {
      validateNetworkConfigure(json);

      setLoadingConfig(true);
      const res = await fetch(`${API_BASE_URL}/network/configure/${namespace}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(json),
      });

      if (!res.ok) {
        const text = await res.text();
        throw new Error(`Backend error (${res.status}): ${text}`);
      }

      const data = await res.json();
      const {
        status,
        successes = 0,
        failures = 0,
        skipped = 0,
        errors = [],
        action_results = [],
        took_time,
        speedup,
      } = data || {};

      setApplyResult({
        status,
        successes,
        failures,
        skipped,
        errors,
        action_results,
        took_time,
        speedup,
      });
      setResultOpen(true);

      // refresco visual tras aplicar
      fetchTopology(true);
    } catch (err) {
      showError('Network Config Error', err.message || 'Invalid config or error applying.');
    } finally {
      setLoadingConfig(false);
    }
  };

  useEffect(() => {
    if (!namespace) return;

    const cacheKey = `kubendt.networkGraph.${namespace}`;
    const restoreFlag = `kubendt.restoreCache.${namespace}`;
    const shouldRestore = sessionStorage.getItem(restoreFlag) === 'true';

    const cached = sessionStorage.getItem(cacheKey);
    if (shouldRestore && cached) {
      try {
        const payload = JSON.parse(cached);
        if (payload?.nodes && payload?.edges) {
          // Restore cached topology ONLY when coming back from Files
          const cachedVersion = payload.version;
          topologyVersionRef.current = cachedVersion;

          setNodes(payload.nodes);
          setEdges(payload.edges);
          setSelectedNodeInfo(payload.selectedNodeInfo || null);
          setInterfacesData(payload.interfacesData || {});
          // Derive hasGraph from the restored nodes, not the cached flag: a
          // snapshot taken mid-import has "creating" placeholders but a stale
          // hasGraph=false, which would show the "No topology" banner on top of
          // the graph.
          const hasRestoredNodes = (payload.nodes || []).some((n) => n.data?.type !== 'external');
          setHasGraph(hasRestoredNodes);
          setInitialLoading(false);
          positionRef.current = payload.positionRef || {};
          // The cache gives an instant paint (no blank flash); still let the
          // initial fetch run so an in-flight import/change reconciles to the
          // real state instead of freezing on the cached snapshot.

          // Pre-populate known link UIDs and node names so we don't flash existing
          // entities on the next fetch after returning from the Files page.
          knownLinkUidsRef.current = new Set(
            (payload.edges || [])
              .map((e) => e.data?.uid)
              .filter((uid) => uid !== undefined && uid !== null)
          );
          knownNodeNamesRef.current = new Set(
            (payload.nodes || []).filter((n) => n.data?.type !== 'external').map((n) => n.id)
          );
          isFirstTopologyFetchRef.current = false;

          sessionStorage.removeItem(restoreFlag);
          return;
        }
      } catch (e) {
        console.warn('Cache restore failed:', e);
      }
    }

    // If we shouldn't restore, ensure we don't skip the initial fetch
    sessionStorage.removeItem(restoreFlag);

    setNodes([]);
    setEdges([]);
    setSelectedNodeInfo(null);
    setSelectedLink(null);
    setInitialLoading(true); // Reset loading when switching namespace
    topologyVersionRef.current = null;
    skipInitialFetchRef.current = false;
    knownLinkUidsRef.current = new Set();
    knownNodeNamesRef.current = new Set();
    isFirstTopologyFetchRef.current = true;
    creatingUidsRef.current = new Set();
    creatingTimersRef.current.forEach((t) => clearTimeout(t));
    creatingTimersRef.current = new Map();
    creatingNodeNamesRef.current = new Set();
    creatingNodeTimersRef.current.forEach((t) => clearTimeout(t));
    creatingNodeTimersRef.current = new Map();

    const fetchSavedPositions = async () => {
      try {
        const res = await fetch(`${API_BASE_URL}/network/positions/${namespace}`);
        const data = await res.json();
        setSavedPositions(data);
        positionRef.current = { ...data };
      } catch (error) {
        console.error('❌ Error fetching saved positions:', error);
      }
    };

    fetchSavedPositions();
  }, [namespace, setNodes, setEdges]);

  useEffect(() => {
    if (!namespace) return;
    const cacheKey = `kubendt.networkGraph.${namespace}`;
    const currentVersion = hashTopology(nodes, edges);
    topologyVersionRef.current = currentVersion;

    const payload = {
      nodes,
      edges,
      selectedNodeInfo,
      interfacesData,
      hasGraph,
      positionRef: positionRef.current,
      version: currentVersion, // Store version for validation
    };
    sessionStorage.setItem(cacheKey, JSON.stringify(payload));
  }, [namespace, nodes, edges, selectedNodeInfo, interfacesData, hasGraph]);

  useEffect(() => {
    if (!namespace) return;

    if (!skipInitialFetchRef.current) {
      fetchTopology(true); // primer fetch
    } else {
      skipInitialFetchRef.current = false;
    }

    const interval = setInterval(() => {
      if (
        !isDeployingRef.current &&
        !clearingTopologyRef.current &&
        !modifyingTopologyRef.current
      ) {
        fetchTopology();
      }
    }, RELOAD_TIME * 1000);

    return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namespace]);

  // Reflect a running operation from the backend lock. Fast-polls only while a
  // lock exists; loads the topology once when it clears.
  useEffect(() => {
    if (!namespace) return;
    let cancelled = false;
    let interval = null;
    const stop = () => {
      if (interval) {
        clearInterval(interval);
        interval = null;
      }
    };

    const check = async () => {
      try {
        const res = await fetch(`${API_BASE_URL}/namespaces/operation/${namespace}`);
        if (!res.ok || cancelled) return;
        const lock = (await res.json())?.operation_lock;
        if (cancelled) return;
        if (lock) {
          opRunningRef.current = true;
          setOperationInProgress(lock.operationType);
          if (!interval) interval = setInterval(check, OP_LOCK_POLL_MS);
        } else {
          const justFinished = opRunningRef.current;
          opRunningRef.current = false;
          setOperationInProgress(null);
          stop();
          if (justFinished) fetchTopology(true);
        }
      } catch {
        /* ignore */
      }
    };

    check();
    return () => {
      cancelled = true;
      stop();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namespace]);

  useEffect(() => {
    if (!namespace) return;
    if (!refreshTrigger) return;
    fetchTopology(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshTrigger, namespace]);

  // Keep selectedLink in sync with edges: drop it if the edge no longer exists,
  // otherwise refresh its reference so panel sees up-to-date data.
  useEffect(() => {
    if (!selectedLink) return;
    const match = edges.find((e) => e.id === selectedLink.id);
    if (!match) {
      setSelectedLink(null);
    } else if (match !== selectedLink) {
      setSelectedLink(match);
    }
  }, [edges, selectedLink]);

  // Propagate any in-flight op (or the initial topology load) to the parent
  // page so its navbar can be locked uniformly, the user must not be able
  // to delete the namespace or its history while we're mid-operation or
  // before we even know what's in the namespace.
  useEffect(() => {
    if (onImportingChange) {
      onImportingChange(
        initialLoading ||
          importing ||
          loadingConfig ||
          modifyingTopology ||
          clearingTopology ||
          !!operationInProgress
      );
    }
  }, [
    initialLoading,
    importing,
    loadingConfig,
    modifyingTopology,
    clearingTopology,
    operationInProgress,
    onImportingChange,
  ]);

  // External-network visibility filter (display-only; positions untouched).
  const hasExternalNodes = nodes.some((n) => n.data?.type === 'external');
  const externalNodeIds = new Set(
    nodes.filter((n) => n.data?.type === 'external').map((n) => n.id)
  );
  const visibleNodes =
    hideExternal && hasExternalNodes ? nodes.filter((n) => n.data?.type !== 'external') : nodes;
  const visibleEdges =
    hideExternal && hasExternalNodes
      ? edges.filter((e) => !externalNodeIds.has(e.source) && !externalNodeIds.has(e.target))
      : edges;

  return (
    <div style={{ display: 'flex', flex: 1, minHeight: 0, width: '100%' }}>
      {selectedNodeInfo &&
        (selectedNodeInfo.type === 'external' ? (
          <ExternalNodeInfoPanel
            node={selectedNodeInfo}
            onClosePanel={() => setSelectedNodeInfo(null)}
            onDeleteExternal={handleDeleteExternal}
            isBusy={isBusy}
          />
        ) : (
          <PodInfoPanel
            pod={selectedNodeInfo}
            namespace={namespace}
            onOpenInteractiveShell={openInteractiveShell}
            onRestartPod={handleRestartPod}
            onDeletePod={handleDeleteNode}
            isBusy={isBusy}
            showInteractiveShell={openShells.length > 0}
            onClosePanel={() => setSelectedNodeInfo(null)}
          />
        ))}

      {!selectedNodeInfo && selectedLink && (
        <LinkInfoPanel
          link={selectedLink}
          nodes={nodes}
          namespace={namespace}
          onClosePanel={() => setSelectedLink(null)}
          onDeleteLink={handleDeleteLink}
          onStartCapture={openCapture}
          isBusy={isBusy}
        />
      )}

      <div style={{ flex: 1, position: 'relative' }}>
        {/* Subtle initial loading animation */}
        {initialLoading && !hasGraph && (
          <div
            style={{
              position: 'absolute',
              top: '50%',
              left: '50%',
              transform: 'translate(-50%, -50%)',
              zIndex: 20,
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              gap: '1rem',
            }}
          >
            <div className="loading-dots">
              <div className="dot"></div>
              <div className="dot"></div>
              <div className="dot"></div>
            </div>
            <div
              style={{
                color: '#666',
                fontSize: '0.9rem',
                fontWeight: 500,
              }}
            >
              Loading network topology...
            </div>
          </div>
        )}

        {/* Empty state: load finished, no topology, no op in progress */}
        {!initialLoading &&
          !hasGraph &&
          !importing &&
          !modifyingTopology &&
          !clearingTopology &&
          !loadingConfig &&
          !operationInProgress && (
            <div className="empty-topology" role="status" aria-live="polite">
              <h3>No topology in this namespace</h3>
              <p>
                Use the <strong>Import topology</strong> action above to deploy nodes and links from
                a JSON payload.
              </p>
            </div>
          )}

        {importing && <LoadingOverlay variant="import" message="Importing topology..." />}

        {loadingConfig && (
          <LoadingOverlay
            variant="config"
            message="Loading network configuration..."
            withBackdrop={false}
          />
        )}

        {modifyingTopology && (
          <LoadingOverlay variant="modify" message="Applying topology changes..." />
        )}

        {clearingTopology && (
          <LoadingOverlay variant="clear" message="Clearing topology resources..." />
        )}

        {/* Op reconstructed from the backend lock (after reload/navigation). */}
        {operationInProgress &&
          !importing &&
          !modifyingTopology &&
          !clearingTopology &&
          !loadingConfig &&
          (() => {
            const map = {
              'deploy-network': { variant: 'import', message: 'Deploying topology...' },
              'modify-network': { variant: 'modify', message: 'Applying topology changes...' },
              'clear-topology': { variant: 'clear', message: 'Clearing topology resources...' },
            };
            const o = map[operationInProgress] || {
              variant: 'info',
              message: 'Operation in progress...',
            };
            return <LoadingOverlay variant={o.variant} message={o.message} />;
          })()}

        <div className="topbar">
          {/* IZQUIERDA */}
          <div className="topbar-left">
            <button
              className="topbar-action-btn topbar-action-import"
              disabled={
                initialLoading ||
                nodes.length > 0 ||
                openShells.length > 0 ||
                clearingTopology ||
                loadingConfig ||
                modifyingTopology ||
                importing ||
                topologyInputKind !== null
              }
              onClick={() => {
                if (initialLoading) return;
                if (openShells.length > 0) return;
                if (!namespace) {
                  setAlertModal({
                    isOpen: true,
                    type: 'warning',
                    title: 'Namespace Required',
                    message: 'Select a namespace first.',
                    onConfirm: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
                    onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
                  });
                  return;
                }
                if (nodes.length > 0) {
                  setAlertModal({
                    isOpen: true,
                    type: 'warning',
                    title: 'Namespace Not Empty',
                    message: 'This namespace already has a topology loaded.',
                    onConfirm: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
                    onCancel: () => setAlertModal((prev) => ({ ...prev, isOpen: false })),
                  });
                  return;
                }
                setTopologyInputKind('import');
              }}
              title="Import topology JSON and deploy it in this namespace"
            >
              Import topology
            </button>

            <button
              className="topbar-action-btn topbar-action-clear"
              disabled={
                !hasGraph ||
                openShells.length > 0 ||
                clearingTopology ||
                loadingConfig ||
                modifyingTopology ||
                importing ||
                topologyInputKind !== null
              }
              onClick={handleClearTopologyClick}
              title="Delete all deployed topology resources in this namespace without deleting the namespace"
            >
              Clear topology
            </button>

            <button
              className="topbar-action-btn topbar-action-modify"
              disabled={
                !hasGraph ||
                openShells.length > 0 ||
                clearingTopology ||
                loadingConfig ||
                modifyingTopology ||
                importing ||
                topologyInputKind !== null
              }
              onClick={handleModifyTopologyClick}
              title="Apply modify topology JSON with 'add' and/or 'delete'"
            >
              Modify topology
            </button>

            <button
              className="topbar-action-btn topbar-action-config"
              disabled={
                !hasGraph ||
                clearingTopology ||
                loadingConfig ||
                modifyingTopology ||
                importing ||
                topologyInputKind !== null
              }
              onClick={handleLoadNetworkConfClick}
              title="Apply a network configuration JSON (targets/actions)"
            >
              Load network conf
            </button>
          </div>

          {/* DERECHA */}
          <div className="topbar-right">
            <div className="topbar-right-buttons">
              <button
                onClick={!isBusy ? handleSavePositions : undefined}
                disabled={nodes.length === 0 || isBusy}
                title="Save node positions (x,y)"
              >
                Save positions
              </button>

              <button
                disabled={nodes.length === 0 || isBusy}
                onClick={!isBusy ? handleExportTopology : undefined}
                title="Export current topology as JSON"
              >
                Export topology
              </button>
            </div>

            <div className="topbar-tools-row">
              <label
                className={`topbar-toggle${hasExternalNodes ? '' : ' disabled'}`}
                title={
                  hasExternalNodes
                    ? 'Hide external networks and their links from the graph'
                    : 'No external networks in this topology'
                }
              >
                <input
                  type="checkbox"
                  checked={hideExternal}
                  disabled={!hasExternalNodes}
                  onChange={(e) => setHideExternal(e.target.checked)}
                />
                <span>Hide external</span>
              </label>

              <div className="topbar-search">
                <span className="topbar-search-icon" aria-hidden="true">
                  🔍
                </span>
                <input
                  type="text"
                  value={graphSearchQuery}
                  onChange={(e) => setGraphSearchQuery(e.target.value)}
                  placeholder="Search node…"
                  aria-label="Search node by name"
                  disabled={nodes.length === 0}
                />
                <span className="topbar-search-count">
                  {graphSearchQuery &&
                    (() => {
                      const q = graphSearchQuery.trim().toLowerCase();
                      const count = nodes.filter((n) => {
                        const label = (n.data?.label || '').toLowerCase();
                        const id = (n.id || '').toLowerCase();
                        return label.includes(q) || id.includes(q);
                      }).length;
                      return `${count} match${count === 1 ? '' : 'es'}`;
                    })()}
                </span>
                {graphSearchQuery && (
                  <button
                    type="button"
                    className="topbar-search-clear"
                    onClick={() => setGraphSearchQuery('')}
                    title="Clear search"
                  >
                    ✖
                  </button>
                )}
              </div>
            </div>
          </div>
        </div>
        <ReactFlowProvider>
          <InnerGraph
            namespace={namespace}
            nodes={visibleNodes}
            edges={visibleEdges}
            onNodesChange={handleNodesChange}
            onEdgesChange={onEdgesChange}
            selectedNodeInfo={selectedNodeInfo}
            setSelectedNodeInfo={setSelectedNodeInfo}
            selectedLink={selectedLink}
            setSelectedLink={setSelectedLink}
            handleNodesChange={handleNodesChange}
            hoveredInfo={hoveredInfo}
            setHoveredInfo={setHoveredInfo}
            setTooltipPos={setTooltipPos}
            tooltipPos={tooltipPos}
            tooltipLoading={tooltipLoading}
            setTooltipLoading={setTooltipLoading}
            interfacesData={interfacesData}
            fetchTopology={fetchTopology}
            onUpdateInterface={(podName, ifaceName, newStatus) => {
              setInterfacesData((prev) => ({
                ...prev,
                [podName]: {
                  ...prev[podName],
                  interfaces: {
                    ...(prev[podName]?.interfaces || {}),
                    [ifaceName]: newStatus,
                  },
                },
              }));
            }}
            onOpenInteractiveShell={openInteractiveShell}
            onRestartPod={handleRestartPod}
            onDeleteNode={handleDeleteNode}
            onDeleteLink={handleDeleteLink}
            onDeleteExternal={handleDeleteExternal}
            onShowError={(msg) => showError('Operation failed', msg)}
            searchQuery={graphSearchQuery}
            isBusy={isBusy}
            onStartCapture={openCapture}
          />
        </ReactFlowProvider>
      </div>

      {/* Render multiple open terminals */}
      {openShells.map((shell) => (
        <PodInteractiveShellModal
          key={shell.id}
          shellId={shell.id}
          podName={shell.podName}
          namespace={namespace}
          shellMode={shell.shellMode}
          zIndex={shell.zIndex}
          minimized={!!shell.minimized}
          onClose={() => closeInteractiveShell(shell.id)}
          onMinimize={() => minimizeShell(shell.id)}
          onBringToFront={() => bringShellToFront(shell.id)}
        />
      ))}

      {/* Render open capture panels */}
      {openCaptures.map((cap) => (
        <CapturePanel
          key={cap.id}
          captureId={cap.id}
          namespace={namespace}
          pod={cap.pod}
          iface={cap.iface}
          zIndex={cap.zIndex}
          minimized={!!cap.minimized}
          onClose={() => closeCapture(cap.id)}
          onMinimize={() => minimizeCapture(cap.id)}
          onBringToFront={() => bringCaptureToFront(cap.id)}
        />
      ))}

      {/* Bottom-centered taskbar with chips for minimized shells and captures */}
      {(openShells.some((s) => s.minimized) || openCaptures.some((c) => c.minimized)) && (
        <div className="shell-taskbar" role="toolbar" aria-label="Minimized windows">
          {openShells
            .filter((s) => s.minimized)
            .map((shell) => (
              <div key={shell.id} className="shell-taskbar-chip">
                <button
                  type="button"
                  className="shell-taskbar-chip-restore"
                  onClick={() => restoreShell(shell.id)}
                  title={`Restore shell: ${shell.podName} (${shell.shellMode})`}
                >
                  <span className="shell-taskbar-chip-icon">▶</span>
                  <span className="shell-taskbar-chip-name">{shell.podName}</span>
                  <span className="shell-taskbar-chip-mode">{shell.shellMode}</span>
                </button>
                <button
                  type="button"
                  className="shell-taskbar-chip-close"
                  onClick={() => closeInteractiveShell(shell.id)}
                  title="Close shell"
                >
                  ✖
                </button>
              </div>
            ))}
          {openCaptures
            .filter((c) => c.minimized)
            .map((cap) => (
              <div key={cap.id} className="shell-taskbar-chip">
                <button
                  type="button"
                  className="shell-taskbar-chip-restore"
                  onClick={() => restoreCapture(cap.id)}
                  title={`Restore capture: ${cap.pod} · ${cap.iface}`}
                >
                  <span className="shell-taskbar-chip-icon">
                    <PcapIcon />
                  </span>
                  <span className="shell-taskbar-chip-name">{cap.pod}</span>
                  <span className="shell-taskbar-chip-mode">{cap.iface}</span>
                </button>
                <button
                  type="button"
                  className="shell-taskbar-chip-close"
                  onClick={() => closeCapture(cap.id)}
                  title="Close capture"
                >
                  ✖
                </button>
              </div>
            ))}
        </div>
      )}
      <ResultDialog open={resultOpen} onClose={() => setResultOpen(false)} result={applyResult} />

      {/* Unified topology input modal: paste JSON or load from file */}
      <TopologyInputModal
        isOpen={topologyInputKind === 'import'}
        onClose={() => setTopologyInputKind(null)}
        title="Import Topology"
        description="Paste a topology JSON (or load it from a file). It will deploy the described nodes and links in this namespace."
        warningText="This will deploy all pods and links in the current namespace."
        confirmLabel="Import topology"
        confirmVariant="warning"
        placeholder={'{\n  "nodes": [ ... ],\n  "links": [ ... ]\n}'}
        sampleSnippet={JSON.stringify(
          {
            nodes: [
              {
                name: 'host',
                image: 'alpine',
                type: 'host',
                replicas: 2,
                commands: ['sh', '-c', 'apk add --no-cache iproute2 && sleep infinity'],
              },
              {
                name: 'router1',
                image: 'frrouting/frr',
                type: 'router',
                driver: 'FRRRouterDriver',
                commands: [
                  'sh',
                  '-c',
                  'echo hostname router1 > /etc/frr/vtysh.conf && chown frr:frr /etc/frr/vtysh.conf && /usr/lib/frr/zebra -d && sleep infinity',
                ],
              },
            ],
            links: [
              {
                node: 'host-0',
                localIntf: 'eth1',
                peerNode: 'router1',
                peerIntf: 'eth1',
                localIp: '10.0.1.10/24',
                peerIp: '10.0.1.1/24',
              },
              {
                node: 'host-1',
                localIntf: 'eth1',
                peerNode: 'router1',
                peerIntf: 'eth2',
                localIp: '10.0.2.10/24',
                peerIp: '10.0.2.1/24',
              },
            ],
          },
          null,
          2
        )}
        semanticValidator={validateImportTopologyPayload}
        onSubmit={(json) => {
          try {
            validateImportTopologyPayload(json);
          } catch (err) {
            showError('Invalid topology payload', err.message || 'Invalid JSON payload.');
            return; // keep TopologyInputModal open with the JSON intact
          }
          setTopologyInputKind(null);
          applyImportTopology(json);
        }}
      />

      <TopologyInputModal
        isOpen={topologyInputKind === 'modify'}
        onClose={() => setTopologyInputKind(null)}
        title="Modify Topology"
        description="Paste a JSON with 'add' and/or 'delete' sections (nodes and/or links) to modify the current topology."
        confirmLabel="Apply changes"
        confirmVariant="primary"
        placeholder={
          '{\n  "scale": [ ... ],\n  "add": { "nodes": [ ... ], "links": [ ... ] },\n  "delete": { "nodes": [ ... ], "links": [ ... ] }\n}'
        }
        sampleSnippet={JSON.stringify(
          {
            scale: [{ name: 'host', replicas: 3 }],
            add: {
              nodes: [
                {
                  name: 'newnode',
                  image: 'alpine',
                  type: 'host',
                  driver: 'BasicHostDriver',
                  commands: ['sh', '-c', 'apk add --no-cache iproute2 && sleep infinity'],
                },
              ],
              links: [
                {
                  node: 'host-2',
                  localIntf: 'eth1',
                  peerNode: 'router1',
                  peerIntf: 'eth3',
                  localIp: '10.0.3.10/24',
                  peerIp: '10.0.3.1/24',
                },
                {
                  node: 'newnode',
                  localIntf: 'eth1',
                  peerNode: 'router1',
                  peerIntf: 'eth4',
                  localIp: '10.0.4.10/24',
                  peerIp: '10.0.4.1/24',
                },
              ],
            },
          },
          null,
          2
        )}
        semanticValidator={validateModifyTopologyPayload}
        onSubmit={(json) => {
          try {
            validateModifyTopologyPayload(json);
          } catch (err) {
            showError('Invalid modify payload', err.message || 'Invalid JSON payload.');
            return;
          }
          setTopologyInputKind(null);
          applyModifyTopology(json);
        }}
      />

      <TopologyInputModal
        isOpen={topologyInputKind === 'config'}
        onClose={() => setTopologyInputKind(null)}
        title="Load Network Configuration"
        description="Paste a network configuration JSON (targets + actions) to apply driver operations on pods."
        confirmLabel="Apply configuration"
        confirmVariant="primary"
        placeholder={'{\n  "targets": [ { "pod": "...", "actions": [ ... ] } ]\n}'}
        sampleSnippet={JSON.stringify(
          {
            targets: [
              {
                pod: 'host-0',
                actions: [{ type: 'set_default_route', gateway: '10.0.1.1' }],
              },
              {
                pod: 'host-1',
                actions: [{ type: 'set_default_route', gateway: '10.0.2.1' }],
              },
              {
                pod: 'router1',
                actions: [{ type: 'enable_snat', iface: 'eth0' }],
              },
            ],
          },
          null,
          2
        )}
        semanticValidator={validateNetworkConfigure}
        onSubmit={(json) => {
          try {
            validateNetworkConfigure(json);
          } catch (err) {
            showError('Invalid network configuration', err.message || 'Invalid JSON payload.');
            return;
          }
          setTopologyInputKind(null);
          applyLoadNetworkConf(json);
        }}
      />

      {/* AlertModal for confirmations and results */}
      <AlertModal
        isOpen={alertModal.isOpen}
        type={alertModal.type}
        title={alertModal.title}
        message={alertModal.message}
        onConfirm={alertModal.onConfirm}
        onCancel={alertModal.onCancel}
        confirmText={alertModal.confirmText}
        cancelText={alertModal.cancelText}
        extraContent={(() => {
          if (alertModal.showPositionCheckbox) {
            return (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
                <label
                  style={{ display: 'flex', alignItems: 'center', gap: '8px', cursor: 'pointer' }}
                >
                  <input
                    type="checkbox"
                    checked={clearPositionsChecked}
                    onChange={(e) => {
                      setClearPositionsChecked(e.target.checked);
                      clearPositionsRef.current = e.target.checked;
                    }}
                  />
                  Also delete saved node positions
                </label>
                <label
                  style={{ display: 'flex', alignItems: 'center', gap: '8px', cursor: 'pointer' }}
                >
                  <input
                    type="checkbox"
                    checked={clearFilesChecked}
                    onChange={(e) => {
                      setClearFilesChecked(e.target.checked);
                      clearFilesRef.current = e.target.checked;
                    }}
                  />
                  Also delete namespace files (file manager)
                </label>
              </div>
            );
          }
          const hasTiming = !!alertModal.timingData;
          const warnings = Array.isArray(alertModal.warningsList) ? alertModal.warningsList : [];
          if (!hasTiming && warnings.length === 0) return null;
          return (
            <>
              {hasTiming &&
                (() => {
                  const { total, ...phases } = alertModal.timingData;
                  const phaseEntries = Object.entries(phases);
                  return (
                    <div className="am-timing">
                      <div className="am-timing-total">
                        <span className="am-timing-total-label">Duration</span>
                        <span className="am-timing-total-value">{total}</span>
                      </div>
                      {phaseEntries.length > 0 && (
                        <div className="am-timing-phases">
                          {phaseEntries.map(([k, v]) => (
                            <div key={k} className="am-timing-phase">
                              <span className="am-timing-phase-label">{k.replace(/_/g, ' ')}</span>
                              <span className="am-timing-phase-value">{v}</span>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  );
                })()}
              {warnings.length > 0 && (
                <div className="am-warnings">
                  <div className="am-warnings-header">
                    ⚠ {warnings.length} warning{warnings.length === 1 ? '' : 's'}
                  </div>
                  <ul className="am-warnings-list">
                    {warnings.map((w, i) => (
                      <li key={i} className="am-warnings-item">
                        {w.node && <span className="am-warnings-node">{w.node}</span>}
                        <span className="am-warnings-detail">{w.detail || w.kind}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </>
          );
        })()}
      />

      {/* ErrorModal for deployment errors */}
      <ErrorModal
        isOpen={deploymentError !== null}
        title={deploymentError?.title || 'Error'}
        details={deploymentError?.details}
        note={deploymentError?.note}
        onClose={() => setDeploymentError(null)}
      />
    </div>
  );
};

export default NetworkGraph;
