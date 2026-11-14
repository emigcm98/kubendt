import { useRef } from 'react';
import { useReactFlow, useStoreApi } from 'reactflow';

// Pan with the right button and zoom with the wheel from an element React
// Flow's own gestures do not reach: a node, which is nopan, or a layer over
// the pane. The wheel maths is d3-zoom's, so it feels the same.
export const useViewportGestures = () => {
  const { getViewport, setViewport } = useReactFlow();
  const store = useStoreApi();
  const panRef = useRef(null);

  const onPointerDown = (e) => {
    if (e.button !== 2) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    panRef.current = { x: e.clientX, y: e.clientY, viewport: getViewport() };
  };
  const onPointerMove = (e) => {
    const pan = panRef.current;
    if (!pan) return;
    const { x, y, zoom } = pan.viewport;
    setViewport({ x: x + e.clientX - pan.x, y: y + e.clientY - pan.y, zoom });
  };
  const onPointerUp = () => {
    panRef.current = null;
  };
  const onWheel = (e) => {
    const { minZoom, maxZoom } = store.getState();
    const { x, y, zoom } = getViewport();
    const factor = Math.pow(2, -e.deltaY * (e.deltaMode === 1 ? 0.05 : 0.002));
    const next = Math.min(maxZoom, Math.max(minZoom, zoom * factor));
    if (next === zoom) return;
    const r = e.currentTarget.getBoundingClientRect();
    const px = e.clientX - r.left;
    const py = e.clientY - r.top;
    setViewport({ x: px - ((px - x) / zoom) * next, y: py - ((py - y) / zoom) * next, zoom: next });
  };

  return {
    pan: {
      onPointerDown,
      onPointerMove,
      onPointerUp,
      onPointerCancel: onPointerUp,
      onContextMenu: (e) => e.preventDefault(),
    },
    onWheel,
  };
};
