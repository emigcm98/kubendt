import { useCallback, useMemo, useState } from 'react';
import { applyNodeChanges } from 'reactflow';
import { API_BASE_URL } from '../config';

// The annotations of the graph as React Flow nodes of their own, next to
// the topology's: the same drag, select and resize, kept apart so a
// topology reload never touches them and the positions map never carries
// them. The layout document, positions and annotations, is what the
// backend keeps per namespace.

export const ANNOTATION_PREFIX = 'ann:';

// React Flow lifts a selected node by 1000, so this keeps a selected shape
// under the links (z 0) too.
const ANNOTATION_Z = -2000;

// For a stored annotation that says no size.
const FALLBACK_SIZE = { width: 120, height: 60 };

export const isAnnotationId = (id) => typeof id === 'string' && id.startsWith(ANNOTATION_PREFIX);

// Where the text sits when the shape does not say: an ellipse reads from
// its middle, the others from the corner.
export const defaultAnchor = (kind) => (kind === 'ellipse' ? 'center' : 'top-left');

// An anchor such as 'top-left', 'right' or 'center' as its row and column.
export const anchorAxes = (anchor) => [
  anchor.startsWith('top') ? 'top' : anchor.startsWith('bottom') ? 'bottom' : 'middle',
  anchor.endsWith('left') ? 'left' : anchor.endsWith('right') ? 'right' : 'center',
];

// The node carries its size as measured too: React Flow hides a node until
// it has measured it, and re-observes one that loses its measure, which
// Firefox reports as a resize observer loop.
export const annotationToNode = (a, handlers = {}) => {
  const width = a.width || FALLBACK_SIZE.width;
  const height = a.height || FALLBACK_SIZE.height;
  return {
    id: ANNOTATION_PREFIX + a.id,
    type: 'annotation',
    position: { x: a.x || 0, y: a.y || 0 },
    style: { width, height },
    width,
    height,
    zIndex: ANNOTATION_Z + (a.z || 0),
    draggable: !a.locked,
    selectable: true,
    data: {
      kind: a.type,
      text: a.text || '',
      color: a.color || '',
      border: a.border || '',
      fill: a.fill || '',
      textSize: a.text_size || '',
      textAnchor: a.text_anchor || '',
      locked: !!a.locked,
      z: a.z || 0,
      ...handlers,
    },
  };
};

export const nodeToAnnotation = (n) => {
  const d = n.data || {};
  const a = {
    id: n.id.slice(ANNOTATION_PREFIX.length),
    type: d.kind,
    x: Math.round(n.position?.x || 0),
    y: Math.round(n.position?.y || 0),
    width: Math.round(n.style?.width ?? n.width ?? 0),
    height: Math.round(n.style?.height ?? n.height ?? 0),
  };
  if (d.text) a.text = d.text;
  if (d.color) a.color = d.color;
  if (d.border) a.border = d.border;
  if (d.fill) a.fill = d.fill;
  if (d.textSize) a.text_size = d.textSize;
  if (d.textAnchor) a.text_anchor = d.textAnchor;
  if (d.locked) a.locked = true;
  if (d.z) a.z = d.z;
  return a;
};

const newId = () =>
  typeof crypto !== 'undefined' && crypto.randomUUID
    ? crypto.randomUUID().slice(0, 8)
    : Math.random().toString(16).slice(2, 10);

export const useLayout = () => {
  const [rawNodes, setAnnotationNodes] = useState([]);
  // React Flow lifts a selected node by 1000; taken back here, so the order
  // among the shapes holds while one is selected and all stay under the
  // links.
  const annotationNodes = useMemo(
    () =>
      rawNodes.map((n) => ({
        ...n,
        zIndex: ANNOTATION_Z + (n.data.z || 0) - (n.selected ? 1000 : 0),
      })),
    [rawNodes]
  );
  // Changed since the layout was loaded or saved.
  const [annotationsDirty, setAnnotationsDirty] = useState(false);

  // The node's own edits, in place: text, size, and later its style.
  const onChange = useCallback((id, patch) => {
    if (Object.keys(patch).some((k) => k !== 'autoEdit')) setAnnotationsDirty(true);
    setAnnotationNodes((prev) =>
      prev.map((n) => {
        if (n.id !== id) return n;
        const next = { ...n, data: { ...n.data } };
        if (patch.width !== undefined || patch.height !== undefined) {
          next.width = patch.width ?? n.width;
          next.height = patch.height ?? n.height;
          next.style = { ...n.style, width: next.width, height: next.height };
        }
        for (const key of [
          'text',
          'color',
          'border',
          'fill',
          'textSize',
          'textAnchor',
          'locked',
          'z',
          'autoEdit',
        ]) {
          if (patch[key] !== undefined) next.data[key] = patch[key];
        }
        if (patch.locked !== undefined) next.draggable = !patch.locked;
        return next;
      })
    );
  }, []);

  const removeAnnotations = useCallback((ids) => {
    const gone = new Set(Array.isArray(ids) ? ids : [ids]);
    if (gone.size > 0) setAnnotationsDirty(true);
    setAnnotationNodes((prev) => prev.filter((n) => !gone.has(n.id)));
  }, []);

  // To the back or the front of the other shapes; z stays small enough for
  // a selected shape to remain under the links.
  const reorder = useCallback((id, where) => {
    setAnnotationsDirty(true);
    setAnnotationNodes((prev) => {
      const zs = prev.map((n) => n.data.z || 0);
      const z = Math.max(
        -500,
        Math.min(500, where === 'back' ? Math.min(...zs) - 1 : Math.max(...zs) + 1)
      );
      return prev.map((n) => (n.id === id ? { ...n, data: { ...n.data, z } } : n));
    });
  }, []);

  const handlers = { onChange, onReorder: reorder, onDelete: removeAnnotations };

  const loadLayout = useCallback(async (namespace) => {
    const res = await fetch(`${API_BASE_URL}/network/layout/${namespace}`);
    if (!res.ok) throw new Error(`layout: ${res.status}`);
    const doc = await res.json();
    setAnnotationNodes((doc.annotations || []).map((a) => annotationToNode(a, handlers)));
    setAnnotationsDirty(false);
    return doc.positions || {};
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const saveLayout = useCallback(
    async (namespace, positions) => {
      const body = { positions, annotations: annotationNodes.map(nodeToAnnotation) };
      const res = await fetch(`${API_BASE_URL}/network/layout/${namespace}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (!res.ok) throw new Error(`layout: ${res.status}`);
      setAnnotationsDirty(false);
    },
    [annotationNodes]
  );

  // The shape the user just drew, selected so it can be resized or deleted
  // right away; a note opens for writing.
  const addAnnotation = useCallback((kind, rect) => {
    const node = annotationToNode({ id: newId(), type: kind, ...rect }, handlers);
    if (kind === 'note') node.data.autoEdit = true;
    setAnnotationsDirty(true);
    setAnnotationNodes((prev) => [
      ...prev.map((n) => (n.selected ? { ...n, selected: false } : n)),
      { ...node, selected: true },
    ]);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Moves and resizes are the user's; measurements and selection are not.
  const applyAnnotationChanges = useCallback((changes) => {
    if (changes.some((c) => c.type === 'position' || (c.type === 'dimensions' && c.resizing))) {
      setAnnotationsDirty(true);
    }
    setAnnotationNodes((prev) => applyNodeChanges(changes, prev));
  }, []);

  return {
    annotationNodes,
    annotationsDirty,
    loadLayout,
    saveLayout,
    addAnnotation,
    removeAnnotations,
    applyAnnotationChanges,
  };
};
