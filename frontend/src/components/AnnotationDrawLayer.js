import React, { useRef, useState } from 'react';
import { useReactFlow } from 'reactflow';
import { useViewportGestures } from './useViewportGestures';
import './Annotations.css';

// While a drawing tool is picked this layer covers the graph: press, drag
// and release draws the shape in graph coordinates, whatever the zoom. A
// click with no drag draws nothing. The right button and the wheel still
// move the view, to reach where the shape goes.
const MIN_DRAG_PX = 6;
const MIN_SIZE = 24;

const AnnotationDrawLayer = ({ kind, onDraw, onDone }) => {
  const { screenToFlowPosition } = useReactFlow();
  const { pan, onWheel } = useViewportGestures();
  const [box, setBox] = useState(null);
  const startRef = useRef(null);

  const local = (e) => {
    const r = e.currentTarget.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };

  const cancel = () => {
    pan.onPointerUp();
    startRef.current = null;
    setBox(null);
  };

  return (
    <div
      className="annotation-draw-layer"
      onPointerDown={(e) => {
        if (e.button === 2) {
          pan.onPointerDown(e);
          return;
        }
        if (e.button !== 0) return;
        e.currentTarget.setPointerCapture(e.pointerId);
        const p = local(e);
        startRef.current = { clientX: e.clientX, clientY: e.clientY };
        setBox({ x0: p.x, y0: p.y, x1: p.x, y1: p.y });
      }}
      onPointerMove={(e) => {
        pan.onPointerMove(e);
        if (!startRef.current) return;
        const p = local(e);
        setBox((b) => b && { ...b, x1: p.x, y1: p.y });
      }}
      onPointerUp={(e) => {
        const start = startRef.current;
        cancel();
        if (!start) return;
        const dx = Math.abs(e.clientX - start.clientX);
        const dy = Math.abs(e.clientY - start.clientY);
        if (dx < MIN_DRAG_PX && dy < MIN_DRAG_PX) return;
        const a = screenToFlowPosition({ x: start.clientX, y: start.clientY });
        const b = screenToFlowPosition({ x: e.clientX, y: e.clientY });
        onDraw(kind, {
          x: Math.round(Math.min(a.x, b.x)),
          y: Math.round(Math.min(a.y, b.y)),
          width: Math.max(MIN_SIZE, Math.round(Math.abs(b.x - a.x))),
          height: Math.max(MIN_SIZE, Math.round(Math.abs(b.y - a.y))),
        });
        onDone();
      }}
      onPointerCancel={cancel}
      onContextMenu={pan.onContextMenu}
      onWheel={onWheel}
    >
      {box && (
        <div
          className={`annotation-preview annotation-preview-${kind}`}
          style={{
            left: Math.min(box.x0, box.x1),
            top: Math.min(box.y0, box.y1),
            width: Math.abs(box.x1 - box.x0),
            height: Math.abs(box.y1 - box.y0),
          }}
        />
      )}
    </div>
  );
};

export default AnnotationDrawLayer;
