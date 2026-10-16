import React from 'react';
import { BaseEdge, getStraightPath } from 'reactflow';

// A straight cable between two node centers. data.offset moves it sideways,
// so several links between the same two nodes sit next to each other
// instead of on top of one another.
const LinkEdge = ({
  sourceX,
  sourceY,
  targetX,
  targetY,
  data,
  style,
  markerEnd,
  interactionWidth,
}) => {
  const offset = data?.offset || 0;
  const dx = targetX - sourceX;
  const dy = targetY - sourceY;
  const len = Math.hypot(dx, dy) || 1;
  const ox = (-dy / len) * offset;
  const oy = (dx / len) * offset;
  const [path] = getStraightPath({
    sourceX: sourceX + ox,
    sourceY: sourceY + oy,
    targetX: targetX + ox,
    targetY: targetY + oy,
  });
  return (
    <BaseEdge path={path} style={style} markerEnd={markerEnd} interactionWidth={interactionWidth} />
  );
};

export default LinkEdge;
