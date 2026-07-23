import React, { useRef, useLayoutEffect, useEffect, useState, useMemo } from 'react';
import { ReactFlow, Controls, Background, EdgeLabelRenderer, useReactFlow } from 'reactflow';
import 'reactflow/dist/style.css';
import MiniMapOverlay from './MiniMapOverlay.js';
import ContextMenu from './ContextMenu.js';
import EdgeContextMenu from './EdgeContextMenu.js';
import InterfaceContextMenu from './InterfaceContextMenu.js';
import './InnerGraph.css';
import { API_BASE_URL } from '../config';
import { ReactComponent as PacketEnvelope } from '../assets/images/icons/trace-packet.svg';
import { ReactComponent as PacketDrop } from '../assets/images/icons/trace-drop.svg';
import { ReactComponent as PacketCheck } from '../assets/images/icons/trace-check.svg';

import CustomNode, { NODE_SIZE } from './CustomNode';
const nodeTypes = { custom: CustomNode };

const EdgeLabel = ({ x, y, intfName, status, loading, dimmed, onClick, onHover, onRightClick }) => {
  const ref = useRef(null);
  const [size, setSize] = useState({ width: 0, height: 0 });

  useLayoutEffect(() => {
    if (ref.current && ref.current.offsetWidth && ref.current.offsetHeight) {
      setSize({
        width: ref.current.offsetWidth,
        height: ref.current.offsetHeight,
      });
    }
  }, [intfName, loading]);

  const borderColor = loading
    ? '#888'
    : status === true
      ? '#4caf50'
      : status === false
        ? '#f44336'
        : '#ddd';

  return (
    <div
      ref={ref}
      className={`edge-label${loading ? ' edge-label-loading' : ''}${dimmed ? ' edge-label-dimmed' : ''}`}
      style={{
        transform: `translate(${x - size.width / 2}px, ${y - size.height / 2}px)`,
        fontSize: `${NODE_SIZE / 10}px`,
        border: `${1 + NODE_SIZE / 64}px solid ${borderColor}`,
      }}
      onMouseEnter={onHover}
      onMouseLeave={() => onHover(null)}
      onDoubleClick={onClick}
      onContextMenu={(e) => {
        e.preventDefault();
        e.stopPropagation();
        onRightClick && onRightClick(e);
      }}
    >
      {intfName}
      {loading && <span className="edge-label-spinner" />}
    </div>
  );
};

/** Centred badge shown in the middle of a link when a `name` is defined for that link. */
const LinkNameLabel = ({ x, y, name, dimmed }) => {
  const ref = useRef(null);
  const [size, setSize] = useState({ width: 0, height: 0 });

  useLayoutEffect(() => {
    if (ref.current && ref.current.offsetWidth && ref.current.offsetHeight) {
      setSize({
        width: ref.current.offsetWidth,
        height: ref.current.offsetHeight,
      });
    }
  }, [name]);

  return (
    <div
      ref={ref}
      className={`link-name-label${dimmed ? ' link-name-label-dimmed' : ''}`}
      style={{
        transform: `translate(${x - size.width / 2}px, ${y - size.height / 2}px)`,
        fontSize: `${NODE_SIZE / 9}px`,
      }}
    >
      {name}
    </div>
  );
};

const InnerGraph = ({
  namespace,
  nodes,
  edges,
  onNodesChange,
  onEdgesChange,
  selectedNodeInfo,
  setSelectedNodeInfo,
  selectedLink,
  setSelectedLink,
  handleNodesChange,
  hoveredInfo,
  setHoveredInfo,
  setTooltipPos,
  tooltipPos,
  tooltipLoading,
  setTooltipLoading,
  interfacesData,
  fetchTopology,
  onUpdateInterface,
  onOpenInteractiveShell,
  onRestartPod,
  onDeleteNode,
  onDeleteLink,
  onDeleteExternal,
  onShowError,
  searchQuery = '',
  isBusy = false,
  onStartCapture,
  onStartTrace,
  trace = null,
}) => {
  const containerRef = useRef(null);
  const hoverTimeoutRef = useRef(null);
  const hoverAbortRef = useRef(null);
  const dragInfoRef = useRef(null);
  // Defer single-click panel-open so a follow-up double-click can cancel it
  // (and open the shell instead, without opening the panel underneath).
  const nodeClickTimerRef = useRef(null);
  const NODE_CLICK_DELAY_MS = 220;
  const { getViewport, setViewport, setCenter } = useReactFlow();
  const [contextMenu, setContextMenu] = useState(null);
  const [edgeContextMenu, setEdgeContextMenu] = useState(null);
  const [interfaceContextMenu, setInterfaceContextMenu] = useState(null);
  const [loadingInterfaces, setLoadingInterfaces] = useState(new Set());

  const normalizedSearch = (searchQuery || '').trim().toLowerCase();
  const matchedNodeIds = useMemo(() => {
    if (!normalizedSearch) return null;
    const matched = new Set();
    for (const n of nodes) {
      const label = n.data?.label?.toLowerCase() || '';
      const id = n.id?.toLowerCase() || '';
      if (label.includes(normalizedSearch) || id.includes(normalizedSearch)) {
        matched.add(n.id);
      }
    }
    return matched;
  }, [nodes, normalizedSearch]);

  // Arrow-key panning. Skip when a node is focused, ReactFlow's built-in
  // a11y already moves the node, so we let it win.
  useEffect(() => {
    const PAN_STEP = 60;
    const handleKeydown = (e) => {
      if (!['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight'].includes(e.key)) return;
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      // While a trace is active the panel owns the arrows (steps the packet).
      if (trace) return;

      // Don't pan while the user is typing in any input / editor (CodeMirror, modals, etc.)
      const t = e.target;
      if (
        t instanceof HTMLInputElement ||
        t instanceof HTMLTextAreaElement ||
        t instanceof HTMLSelectElement ||
        (t && t.isContentEditable) ||
        (t && t.closest && (t.closest('.cm-editor') || t.closest("[role='dialog']")))
      ) {
        return;
      }

      // If a graph node is focused, let ReactFlow handle moving it with the arrows.
      if (t && t.closest && t.closest('.react-flow__node')) return;

      e.preventDefault();
      const vp = getViewport();
      let dx = 0,
        dy = 0;
      switch (e.key) {
        case 'ArrowUp':
          dy = PAN_STEP;
          break;
        case 'ArrowDown':
          dy = -PAN_STEP;
          break;
        case 'ArrowLeft':
          dx = PAN_STEP;
          break;
        case 'ArrowRight':
          dx = -PAN_STEP;
          break;
        default:
          return;
      }
      setViewport({ x: vp.x + dx, y: vp.y + dy, zoom: vp.zoom });
    };
    document.addEventListener('keydown', handleKeydown);
    return () => document.removeEventListener('keydown', handleKeydown);
  }, [getViewport, setViewport, trace]);

  const fetchInterfaceInfo = async (podName, intfName, mouseEvent, signal) => {
    setTooltipLoading(true);
    try {
      const intfQuery = intfName ? `?intf=${encodeURIComponent(intfName)}` : '';
      const res = await fetch(`${API_BASE_URL}/pods/ips/${namespace}/${podName}${intfQuery}`, {
        signal,
      });
      if (signal?.aborted) return;
      const data = await res.json();
      const match = data.interfaces.find((i) => i.interface === intfName);

      if (match && containerRef.current) {
        const bounds = containerRef.current.getBoundingClientRect();
        const relativeX = mouseEvent.clientX - bounds.left;
        const relativeY = mouseEvent.clientY - bounds.top;

        setHoveredInfo({
          label: intfName,
          ipv4: match.ipv4,
          mac: match.mac,
          guestInterface: match.guestInterface,
        });

        setTooltipPos({
          x: relativeX + 10,
          y: relativeY + 10,
        });
      }
    } catch (err) {
      if (err.name !== 'AbortError') console.error('❌ Error fetching IP/MAC data:', err);
    } finally {
      if (!signal?.aborted) setTooltipLoading(false);
    }
  };

  const toggleLinkState = async (pod, iface, currentStatus) => {
    const key = `${pod}:${iface}`;
    setLoadingInterfaces((prev) => new Set([...prev, key]));
    const actionType = currentStatus ? 'link_down' : 'link_up';
    try {
      await fetch(`${API_BASE_URL}/network/configure/${namespace}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          targets: [
            {
              pod,
              actions: [
                {
                  type: actionType,
                  iface,
                  options: { persist_history: false },
                },
              ],
            },
          ],
        }),
      });
      // Targeted refresh: only fetch the affected pod/interface instead of the full namespace
      const res = await fetch(
        `${API_BASE_URL}/pods/ips/${namespace}/${pod}?intf=${encodeURIComponent(iface)}`
      );
      if (res.ok) {
        const data = await res.json();
        const match = data.interfaces?.find((i) => i.interface === iface);
        if (match !== undefined && onUpdateInterface) {
          onUpdateInterface(pod, iface, match.state === 'up');
        }
      } else {
        await fetchTopology();
      }
    } catch (err) {
      if (onShowError) onShowError(`Could not toggle interface state on ${pod}:${iface}.`);
      else console.error('Error toggling interface state:', err);
    } finally {
      setLoadingInterfaces((prev) => {
        const next = new Set(prev);
        next.delete(key);
        return next;
      });
    }
  };

  const displayedEdges = edges.map((e) => {
    let styled = e;
    // When the user is searching, dim every link that doesn't touch a match.
    if (matchedNodeIds) {
      const touchesMatch = matchedNodeIds.has(e.source) || matchedNodeIds.has(e.target);
      styled = {
        ...styled,
        style: { ...styled.style, opacity: touchesMatch ? 1 : 0.12 },
      };
    }
    if (selectedLink && e.id === selectedLink.id) {
      styled = {
        ...styled,
        style: {
          ...styled.style,
          stroke: '#1976d2',
          strokeWidth: (styled.style?.strokeWidth || 2) + 1,
          strokeDasharray: '8 4',
        },
        animated: true,
      };
    }
    if (e.data?.creating) {
      styled = {
        ...styled,
        style: {
          ...styled.style,
          stroke: '#4caf50',
          strokeWidth: (styled.style?.strokeWidth || 2) + 1,
          strokeDasharray: '6,4',
          opacity: 0.9,
        },
        animated: true,
      };
    }
    if (e.data?.deleting) {
      styled = {
        ...styled,
        style: {
          ...styled.style,
          stroke: '#f44336',
          strokeWidth: (styled.style?.strokeWidth || 2) + 1,
          strokeDasharray: '6,4',
          opacity: 0.85,
        },
        animated: true,
      };
    }
    return styled;
  });

  const selectedNodeId = selectedNodeInfo?.name || selectedNodeInfo?.id;
  const displayedNodes = nodes.map((n) => {
    let next = n;
    if (n.id === selectedNodeId) {
      next = { ...next, data: { ...next.data, selected: true } };
    }
    if (matchedNodeIds && !matchedNodeIds.has(n.id)) {
      next = { ...next, style: { ...next.style, opacity: 0.18 } };
    } else if (matchedNodeIds && matchedNodeIds.has(n.id)) {
      next = { ...next, style: { ...next.style, opacity: 1 } };
    }
    return next;
  });

  // Single-match result: pan to centre without changing zoom (setCenter,
  // not fitView, fitView would reset the user's zoom level).
  useEffect(() => {
    if (!matchedNodeIds || matchedNodeIds.size === 0 || matchedNodeIds.size > 1) return;
    const id = [...matchedNodeIds][0];
    const node = nodes.find((n) => n.id === id);
    if (!node || !node.position) return;
    const cx = node.position.x + NODE_SIZE / 2;
    const cy = node.position.y + NODE_SIZE / 2;
    try {
      const { zoom } = getViewport();
      setCenter(cx, cy, { zoom, duration: 300 });
    } catch {
      /* setCenter may not be ready on first render */
    }
  }, [matchedNodeIds, nodes, getViewport, setCenter]);

  // Pick the most useful shell for a given pod: QEMU/serial-capable → "serial",
  // FRRouterDriver → "vtysh", otherwise the default "sh".
  const pickPreferredShellMode = (info) => {
    if (!info) return 'sh';
    if (info.runtime === 'qemu' || info.serialShell || info.shellMode === 'serial') {
      return 'serial';
    }
    if (info.driver === 'FRRRouterDriver') return 'vtysh';
    return 'sh';
  };

  return (
    <div ref={containerRef} className="graph-container">
      <ReactFlow
        nodes={displayedNodes}
        edges={displayedEdges}
        onNodesChange={handleNodesChange}
        onEdgesChange={onEdgesChange}
        panOnDrag={[2]}
        nodesDraggable={!isBusy}
        nodeDragThreshold={8}
        onPaneContextMenu={(e) => e.preventDefault()}
        onNodeClick={(_, node) => {
          if (isBusy) return;
          // External nodes don't have a shell, open the info panel immediately.
          if (node.data?.type === 'external') {
            setSelectedNodeInfo(node.data.fullInfo || null);
            if (setSelectedLink) setSelectedLink(null);
            setContextMenu(null);
            setEdgeContextMenu(null);
            setInterfaceContextMenu(null);
            return;
          }
          // Defer so a follow-up double-click can cancel and open the shell instead.
          if (nodeClickTimerRef.current) clearTimeout(nodeClickTimerRef.current);
          nodeClickTimerRef.current = setTimeout(() => {
            nodeClickTimerRef.current = null;
            setSelectedNodeInfo(node.data.fullInfo || null);
            if (setSelectedLink) setSelectedLink(null);
            setContextMenu(null);
            setEdgeContextMenu(null);
            setInterfaceContextMenu(null);
          }, NODE_CLICK_DELAY_MS);
        }}
        onNodeDoubleClick={(_, node) => {
          if (nodeClickTimerRef.current) {
            clearTimeout(nodeClickTimerRef.current);
            nodeClickTimerRef.current = null;
          }
          if (isBusy) return;
          if (node.data?.type === 'external') return;
          if (!onOpenInteractiveShell) return;
          const info = node.data?.fullInfo;
          if (!info) return;
          const mode = pickPreferredShellMode(info);
          onOpenInteractiveShell(info, mode);
        }}
        onEdgeClick={(_, edge) => {
          if (isBusy) return;
          if (setSelectedLink) setSelectedLink(edge);
          setSelectedNodeInfo(null);
          setContextMenu(null);
          setEdgeContextMenu(null);
          setInterfaceContextMenu(null);
        }}
        onNodeContextMenu={(e, node) => {
          e.preventDefault();

          if (isBusy) return;

          setEdgeContextMenu(null);
          setInterfaceContextMenu(null);
          setContextMenu({
            x: e.clientX,
            y: e.clientY,
            node: node.data,
            nodeId: node.id,
          });
        }}
        onEdgeContextMenu={(e, edge) => {
          e.preventDefault();

          if (isBusy) return;

          // External-endpoint edges aren't deletable through modify-network
          const sourceType = nodes.find((n) => n.id === edge.source)?.data?.type;
          const targetType = nodes.find((n) => n.id === edge.target)?.data?.type;
          const involvesExternal = sourceType === 'external' || targetType === 'external';

          setContextMenu(null);
          setInterfaceContextMenu(null);
          setEdgeContextMenu({
            x: e.clientX,
            y: e.clientY,
            edge,
            involvesExternal,
            sourceLabel: nodes.find((n) => n.id === edge.source)?.data?.label || edge.source,
            targetLabel: nodes.find((n) => n.id === edge.target)?.data?.label || edge.target,
          });
        }}
        onPaneClick={() => {
          setSelectedNodeInfo(null);
          if (setSelectedLink) setSelectedLink(null);
          setContextMenu(null);
          setEdgeContextMenu(null);
          setInterfaceContextMenu(null);
        }}
        onNodeDragStart={(_, node) => {
          setContextMenu(null);
          setEdgeContextMenu(null);
          setInterfaceContextMenu(null);
          dragInfoRef.current = {
            nodeId: node.id,
            position: { ...node.position },
            time: Date.now(),
          };
        }}
        onNodeDragStop={(_, node) => {
          const info = dragInfoRef.current;
          dragInfoRef.current = null;
          if (!info || info.nodeId !== node.id) return;

          const dx = node.position.x - info.position.x;
          const dy = node.position.y - info.position.y;
          const dist = Math.sqrt(dx * dx + dy * dy);
          const elapsed = Date.now() - info.time;

          // Momentum-click protection: short + small drag → revert and treat as click.
          if (elapsed < 250 && dist < 25) {
            onNodesChange([{ id: node.id, type: 'position', position: info.position }]);
            setSelectedNodeInfo(node.data?.fullInfo || null);
            if (setSelectedLink) setSelectedLink(null);
          }
        }}
        onMoveStart={() => {
          setContextMenu(null);
          setEdgeContextMenu(null);
          setInterfaceContextMenu(null);
        }}
        fitView
        nodeTypes={nodeTypes}
      >
        <MiniMapOverlay nodes={nodes} selectedNodeId={selectedNodeInfo?.id} />
        <Controls />
        <Background gap={12} size={1} />
        <EdgeLabelRenderer>
          {edges.map((edge) => {
            const sourceNode = nodes.find((n) => n.id === edge.source);
            const targetNode = nodes.find((n) => n.id === edge.target);
            if (!sourceNode || !targetNode) return null;

            const sourceCenter = {
              x: sourceNode.position.x + NODE_SIZE / 2,
              y: sourceNode.position.y + NODE_SIZE / 2,
            };
            const targetCenter = {
              x: targetNode.position.x + NODE_SIZE / 2,
              y: targetNode.position.y + NODE_SIZE / 2,
            };

            const dx = targetCenter.x - sourceCenter.x;
            const dy = targetCenter.y - sourceCenter.y;
            const length = Math.sqrt(dx * dx + dy * dy) || 1;

            const LABEL_OFFSET = NODE_SIZE * 0.3;

            const offsetX = (dx / length) * (NODE_SIZE / 2 + LABEL_OFFSET);
            const offsetY = (dy / length) * (NODE_SIZE / 2 + LABEL_OFFSET);

            const localStatus = interfacesData?.[edge.source]?.interfaces?.[edge.data?.localIntf];
            const peerStatus = interfacesData?.[edge.target]?.interfaces?.[edge.data?.peerIntf];

            // Centre point for the optional link-name badge
            const midX = (sourceCenter.x + targetCenter.x) / 2;
            const midY = (sourceCenter.y + targetCenter.y) / 2;

            // Dim labels too on edges that don't touch a search match.
            const dimmedByLabel =
              !!matchedNodeIds &&
              !matchedNodeIds.has(edge.source) &&
              !matchedNodeIds.has(edge.target);

            return (
              <React.Fragment key={edge.id}>
                {sourceNode?.data?.type !== 'external' && (
                  <EdgeLabel
                    x={sourceCenter.x + offsetX}
                    y={sourceCenter.y + offsetY}
                    intfName={edge.data?.localIntf}
                    status={localStatus}
                    loading={loadingInterfaces.has(`${edge.source}:${edge.data?.localIntf}`)}
                    dimmed={dimmedByLabel}
                    onClick={() => {
                      if (isBusy) return;
                      toggleLinkState(edge.source, edge.data?.localIntf, localStatus);
                    }}
                    onRightClick={(e) => {
                      if (isBusy) return;
                      setInterfaceContextMenu({
                        x: e.clientX,
                        y: e.clientY,
                        pod: edge.source,
                        iface: edge.data?.localIntf,
                        status: localStatus,
                      });
                    }}
                    onHover={(e) => {
                      if (isBusy) return;
                      if (e) {
                        if (containerRef.current) {
                          const bounds = containerRef.current.getBoundingClientRect();
                          setTooltipPos({
                            x: e.clientX - bounds.left + 10,
                            y: e.clientY - bounds.top + 10,
                          });
                          setHoveredInfo({ label: edge.data?.localIntf });
                          setTooltipLoading(true);
                        }
                        hoverTimeoutRef.current = setTimeout(() => {
                          hoverAbortRef.current = new AbortController();
                          fetchInterfaceInfo(
                            edge.source,
                            edge.data?.localIntf,
                            e,
                            hoverAbortRef.current.signal
                          );
                        }, 500);
                      } else {
                        clearTimeout(hoverTimeoutRef.current);
                        hoverAbortRef.current?.abort();
                        setHoveredInfo(null);
                      }
                    }}
                  />
                )}
                {targetNode?.data?.type !== 'external' && (
                  <EdgeLabel
                    x={targetCenter.x - offsetX}
                    y={targetCenter.y - offsetY}
                    intfName={edge.data?.peerIntf}
                    status={peerStatus}
                    loading={loadingInterfaces.has(`${edge.target}:${edge.data?.peerIntf}`)}
                    dimmed={dimmedByLabel}
                    onClick={() => {
                      if (isBusy) return;
                      toggleLinkState(edge.target, edge.data?.peerIntf, peerStatus);
                    }}
                    onRightClick={(e) => {
                      if (isBusy) return;
                      setInterfaceContextMenu({
                        x: e.clientX,
                        y: e.clientY,
                        pod: edge.target,
                        iface: edge.data?.peerIntf,
                        status: peerStatus,
                      });
                    }}
                    onHover={(e) => {
                      if (isBusy) return;
                      if (e) {
                        if (containerRef.current) {
                          const bounds = containerRef.current.getBoundingClientRect();
                          setTooltipPos({
                            x: e.clientX - bounds.left + 10,
                            y: e.clientY - bounds.top + 10,
                          });
                          setHoveredInfo({ label: edge.data?.peerIntf });
                          setTooltipLoading(true);
                        }
                        hoverTimeoutRef.current = setTimeout(() => {
                          hoverAbortRef.current = new AbortController();
                          fetchInterfaceInfo(
                            edge.target,
                            edge.data?.peerIntf,
                            e,
                            hoverAbortRef.current.signal
                          );
                        }, 500);
                      } else {
                        clearTimeout(hoverTimeoutRef.current);
                        hoverAbortRef.current?.abort();
                        setHoveredInfo(null);
                      }
                    }}
                  />
                )}
                {edge.data?.linkName && (
                  <LinkNameLabel
                    x={midX}
                    y={midY}
                    name={edge.data.linkName}
                    dimmed={dimmedByLabel}
                  />
                )}
              </React.Fragment>
            );
          })}
          {trace &&
            Array.isArray(trace.waypoints) &&
            trace.waypoints.length >= 1 &&
            (trace.waypoints.length > 1 || trace.drop) &&
            (() => {
              const centerOf = (pod) => {
                const n = nodes.find((x) => x.id === pod);
                if (!n || !n.position) return null;
                return { x: n.position.x + NODE_SIZE / 2, y: n.position.y + NODE_SIZE / 2 };
              };
              // Graph centroid → direction to fling the packet "toward the
              // internet" (a hop that leaves the topology).
              let cx = 0;
              let cy = 0;
              let cnt = 0;
              for (const n of nodes) {
                if (n.position) {
                  cx += n.position.x + NODE_SIZE / 2;
                  cy += n.position.y + NODE_SIZE / 2;
                  cnt += 1;
                }
              }
              if (cnt) {
                cx /= cnt;
                cy /= cnt;
              }
              const outward = (from) => {
                if (!from) return null;
                let dx = from.x - cx;
                let dy = from.y - cy;
                const d = Math.sqrt(dx * dx + dy * dy);
                if (d < 1) {
                  dx = 0.7;
                  dy = -0.7;
                } else {
                  dx /= d;
                  dy /= d;
                }
                return { x: from.x + dx * 90, y: from.y + dy * 90 };
              };
              // Resolve every waypoint to a point. "__internet__" waypoints all
              // sit at the same outward point, derived from the last real node.
              const wps = trace.waypoints;
              const pts = [];
              let lastReal = centerOf(wps[0].pod);
              for (let i = 0; i < wps.length; i += 1) {
                if (wps[i].pod === '__internet__') {
                  pts.push(outward(lastReal));
                } else {
                  const c = centerOf(wps[i].pod);
                  pts.push(c);
                  if (c) lastReal = c;
                }
              }
              const els = [];
              for (let i = 1; i < wps.length; i += 1) {
                const a = pts[i - 1];
                const b = pts[i];
                if (!a || !b) continue;
                const done = i <= trace.step;
                const active = i === trace.step;
                const seg = wps[i].seg;
                const dx = b.x - a.x;
                const dy = b.y - a.y;
                const len = Math.sqrt(dx * dx + dy * dy);
                const angle = (Math.atan2(dy, dx) * 180) / Math.PI;
                // Internet egress draws a blinking arrow, not a line. Only on
                // the step that leaves the last real node, not on the following
                // internet steps that sit at the same point.
                if (seg === 'internet') {
                  if (wps[i - 1].pod === '__internet__' || len < 1) continue;
                  const mx = (a.x + b.x) / 2;
                  const my = (a.y + b.y) / 2;
                  els.push(
                    <div
                      key={`trace-out-${i}`}
                      className={`trace-out${done || active ? ' trace-out-on' : ''}`}
                      style={{
                        transformOrigin: '50% 50%',
                        transform: `translate(${mx}px, ${my}px) translate(-50%, -50%) rotate(${angle}deg)`,
                      }}
                    >
                      ➤➤➤
                    </div>
                  );
                  continue;
                }
                if (len < 1) continue; // zero-length, nothing to draw
                els.push(
                  <div
                    key={`trace-seg-${i}`}
                    className={`trace-seg${done ? ' trace-seg-done' : ''}${active ? ' trace-seg-active' : ''}${seg === 'tunnel' ? ' trace-seg-tunnel' : ''}${seg === 'external' ? ' trace-seg-external' : ''}`}
                    style={{
                      transformOrigin: '0 50%',
                      transform: `translate(${a.x}px, ${a.y - 1.5}px) rotate(${angle}deg)`,
                      width: `${len}px`,
                    }}
                  />
                );
              }
              const curStep = Math.min(trace.step, wps.length - 1);
              const pos = pts[curStep];
              const leaving = curStep > 0 && wps[curStep] && wps[curStep].seg === 'internet';
              // The packet dies at its own final spot, wherever it ended. Dim
              // the envelope there and stamp the mark over it, not over a node
              // behind it.
              const atEnd = curStep === wps.length - 1;
              const atDeath = !!trace.drop && atEnd;
              const arrived = !!trace.delivered && atEnd;
              if (pos) {
                // Packet = an inline SVG envelope; the drop ✕ (dropped) or the
                // ✓ (delivered) is a sibling SVG inside the SAME container, so
                // it shares the packet's position and, on a drop, its fade.
                els.push(
                  <div
                    key="trace-packet"
                    className={`trace-packet${atDeath ? ' trace-packet-dropped' : leaving ? ' trace-packet-out' : ''}`}
                    style={{ transform: `translate(${pos.x}px, ${pos.y}px) translate(-50%, -50%)` }}
                  >
                    <PacketEnvelope
                      className="trace-packet-env"
                      width="24"
                      height="24"
                      aria-hidden="true"
                    />
                    {atDeath && (
                      <PacketDrop
                        className="trace-packet-x"
                        width="18"
                        height="18"
                        aria-hidden="true"
                      />
                    )}
                    {arrived && (
                      <PacketCheck
                        className="trace-packet-x"
                        width="18"
                        height="18"
                        aria-hidden="true"
                      />
                    )}
                  </div>
                );
              }
              return els;
            })()}
        </EdgeLabelRenderer>
      </ReactFlow>

      {hoveredInfo && (
        <div
          className="tooltip"
          style={{
            left: tooltipPos.x,
            top: tooltipPos.y,
            fontSize: `${NODE_SIZE / 5}px`,
          }}
        >
          {tooltipLoading ? (
            <div>Loading...</div>
          ) : (
            <>
              {hoveredInfo.guestInterface && (
                <div style={{ opacity: 0.75, fontStyle: 'italic' }}>
                  {hoveredInfo.guestInterface}
                </div>
              )}
              {hoveredInfo.ipv4 && <div>{hoveredInfo.ipv4}</div>}
              <div>{hoveredInfo.mac || 'MAC unknown'}</div>
            </>
          )}
        </div>
      )}

      {contextMenu && (
        <ContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          node={contextMenu.node}
          onClose={() => setContextMenu(null)}
          onOpenInfoPanel={() => {
            const pod = nodes.find((n) => n.id === contextMenu.nodeId)?.data?.fullInfo;
            if (pod) {
              setSelectedNodeInfo(pod);
              if (setSelectedLink) setSelectedLink(null);
            }
          }}
          onOpenShell={(mode = 'sh') => {
            if (onOpenInteractiveShell) {
              const pod = nodes.find((n) => n.id === contextMenu.nodeId)?.data?.fullInfo;
              onOpenInteractiveShell(pod, mode);
            }
          }}
          onStartTrace={
            onStartTrace
              ? () => {
                  const info = nodes.find((n) => n.id === contextMenu.nodeId)?.data?.fullInfo;
                  if (info) onStartTrace(info);
                }
              : null
          }
          onRestartPod={
            isBusy
              ? null
              : () => {
                  if (onRestartPod) {
                    onRestartPod(contextMenu.nodeId);
                  }
                }
          }
          onDelete={
            !isBusy
              ? () => {
                  const info = nodes.find((n) => n.id === contextMenu.nodeId)?.data?.fullInfo;
                  if (!info) return;
                  if (info.type === 'external') {
                    if (onDeleteExternal) onDeleteExternal(info);
                  } else {
                    if (onDeleteNode) onDeleteNode(info);
                  }
                }
              : null
          }
        />
      )}

      {edgeContextMenu &&
        (() => {
          const edge = edgeContextMenu.edge;
          const srcExternal = nodes.find((n) => n.id === edge.source)?.data?.type === 'external';
          const tgtExternal = nodes.find((n) => n.id === edge.target)?.data?.type === 'external';
          const srcIntf = edge.data?.localIntf;
          const tgtIntf = edge.data?.peerIntf;
          return (
            <EdgeContextMenu
              x={edgeContextMenu.x}
              y={edgeContextMenu.y}
              edge={edge}
              sourceLabel={edgeContextMenu.sourceLabel}
              targetLabel={edgeContextMenu.targetLabel}
              onClose={() => setEdgeContextMenu(null)}
              onOpenInfoPanel={() => {
                if (setSelectedLink) setSelectedLink(edge);
                setSelectedNodeInfo(null);
              }}
              onDelete={
                onDeleteLink && !isBusy
                  ? () => {
                      onDeleteLink(edge);
                    }
                  : null
              }
              onCaptureSource={
                onStartCapture && !srcExternal && srcIntf
                  ? () => onStartCapture(edge.source, srcIntf)
                  : null
              }
              onCaptureTarget={
                onStartCapture && !tgtExternal && tgtIntf
                  ? () => onStartCapture(edge.target, tgtIntf)
                  : null
              }
            />
          );
        })()}

      {interfaceContextMenu && (
        <InterfaceContextMenu
          x={interfaceContextMenu.x}
          y={interfaceContextMenu.y}
          pod={interfaceContextMenu.pod}
          iface={interfaceContextMenu.iface}
          status={interfaceContextMenu.status}
          namespace={namespace}
          apiBase={API_BASE_URL}
          onClose={() => setInterfaceContextMenu(null)}
          onToggle={() =>
            toggleLinkState(
              interfaceContextMenu.pod,
              interfaceContextMenu.iface,
              interfaceContextMenu.status
            )
          }
        />
      )}
    </div>
  );
};

export default InnerGraph;
