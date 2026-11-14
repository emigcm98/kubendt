import { act, renderHook } from '@testing-library/react';
import {
  annotationToNode,
  nodeToAnnotation,
  isAnnotationId,
  anchorAxes,
  defaultAnchor,
  useLayout,
  ANNOTATION_PREFIX,
} from './useLayout';

test('an annotation becomes a node under the links and comes back the same', () => {
  const a = {
    id: 'a1',
    type: 'rect',
    x: 10.4,
    y: 20,
    width: 300,
    height: 200,
    text: 'Site A',
    color: 'green',
    border: 'dashed',
    fill: 'solid',
    text_size: 'lg',
    text_anchor: 'bottom-right',
    locked: true,
    z: 3,
  };
  const n = annotationToNode(a);
  expect(n.id).toBe(ANNOTATION_PREFIX + 'a1');
  expect(n.type).toBe('annotation');
  expect(n.draggable).toBe(false);
  expect(n.style).toEqual({ width: 300, height: 200 });
  // Measured from the start, so React Flow never hides it.
  expect([n.width, n.height]).toEqual([300, 200]);
  expect(nodeToAnnotation(n)).toEqual({ ...a, x: 10 });
});

test('a selected annotation still sits under the links', () => {
  // React Flow adds 1000 to a selected node's z-index.
  expect(annotationToNode({ id: 'a', type: 'rect', z: 10 }).zIndex + 1000).toBeLessThan(0);
});

test('a bare annotation gets a size and saves only what it has', () => {
  const n = annotationToNode({ id: 'n1', type: 'note', x: 0, y: 0 });
  expect(n.style.width).toBeGreaterThan(0);
  expect(n.draggable).toBe(true);
  expect(nodeToAnnotation(n)).toEqual({
    id: 'n1',
    type: 'note',
    x: 0,
    y: 0,
    width: n.style.width,
    height: n.style.height,
  });
});

test('the annotations know when they differ from what was saved', () => {
  const { result } = renderHook(() => useLayout());
  expect(result.current.annotationsDirty).toBe(false);
  act(() => result.current.addAnnotation('rect', { x: 0, y: 0, width: 100, height: 50 }));
  expect(result.current.annotationNodes).toHaveLength(1);
  expect(result.current.annotationsDirty).toBe(true);
  act(() =>
    result.current.applyAnnotationChanges([
      { id: result.current.annotationNodes[0].id, type: 'select', selected: false },
    ])
  );
  expect(result.current.annotationsDirty).toBe(true);
});

test('a shape goes behind or in front of the others', () => {
  const { result } = renderHook(() => useLayout());
  act(() => result.current.addAnnotation('rect', { x: 0, y: 0, width: 10, height: 10 }));
  act(() => result.current.addAnnotation('rect', { x: 5, y: 5, width: 10, height: 10 }));
  const [a, b] = result.current.annotationNodes;
  act(() => a.data.onReorder(a.id, 'front'));
  const front = result.current.annotationNodes.find((n) => n.id === a.id);
  expect(front.data.z).toBeGreaterThan(b.data.z || 0);
  expect(front.zIndex + 1000).toBeLessThan(0);
  act(() => a.data.onReorder(a.id, 'back'));
  const back = result.current.annotationNodes.find((n) => n.id === a.id);
  expect(back.data.z).toBeLessThan(b.data.z || 0);
});

test('the text anchor reads as a row and a column', () => {
  expect(anchorAxes('bottom-right')).toEqual(['bottom', 'right']);
  expect(anchorAxes('top')).toEqual(['top', 'center']);
  expect(anchorAxes('left')).toEqual(['middle', 'left']);
  expect(anchorAxes('center')).toEqual(['middle', 'center']);
  expect(defaultAnchor('ellipse')).toBe('center');
  expect(defaultAnchor('rect')).toBe('top-left');
});

test('the order among shapes holds while one is selected', () => {
  const { result } = renderHook(() => useLayout());
  act(() => result.current.addAnnotation('rect', { x: 0, y: 0, width: 10, height: 10 }));
  act(() => result.current.addAnnotation('rect', { x: 5, y: 5, width: 10, height: 10 }));
  const [a, b] = result.current.annotationNodes;
  expect(b.selected).toBe(true);
  // React Flow adds 1000 to the selected one, which lands it level with a.
  expect(b.zIndex + 1000).toBe(a.zIndex);
});

test('annotation ids are told apart from pod ids', () => {
  expect(isAnnotationId('ann:a1')).toBe(true);
  expect(isAnnotationId('router1-0')).toBe(false);
  expect(isAnnotationId(undefined)).toBe(false);
});
