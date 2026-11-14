import React, { useEffect, useRef, useState } from 'react';
import { NodeResizer, useStore } from 'reactflow';
import { useViewportGestures } from './useViewportGestures';
import './Annotations.css';

// A note, rectangle or ellipse on the graph: a React Flow node with no
// handles that sits under the pods and the links. Double click edits the
// text in place and the resizer frames it while it is selected. Colours
// and sizes are palette names, the CSS maps them.

const AnnotationNode = ({ id, data, selected }) => {
  // A note opens for writing as soon as it is drawn.
  const [editing, setEditing] = useState(() => !!data.autoEdit);
  const [draft, setDraft] = useState(data.text || '');
  const inputRef = useRef(null);
  // React Flow does not pan from a drag that starts on a node, and a shape
  // can cover a good part of the graph, so the right button pans from here.
  const { pan } = useViewportGestures();
  const kind = data.kind || 'rect';
  const locked = !!data.locked;
  // React Flow keeps a node hidden until it has measured it, and a hidden
  // textarea cannot take the focus, so the editor waits for the measure.
  const measured = useStore((s) => !!s.nodeInternals.get(id)?.width);

  useEffect(() => {
    if (!editing) setDraft(data.text || '');
  }, [data.text, editing]);
  useEffect(() => {
    if (editing && measured) inputRef.current?.focus();
  }, [editing, measured]);
  useEffect(() => {
    if (!editing && data.autoEdit) data.onChange?.(id, { autoEdit: false });
  }, [editing, data, id]);

  const finish = (text) => {
    setEditing(false);
    if (text !== (data.text || '')) data.onChange?.(id, { text });
  };

  const cls = [
    'annotation',
    `annotation-${kind}`,
    data.color ? `ann-color-${data.color}` : '',
    data.border ? `ann-border-${data.border}` : '',
    data.textSize ? `ann-text-${data.textSize}` : '',
    selected ? 'annotation-selected' : '',
    locked ? 'annotation-locked' : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <>
      <NodeResizer
        isVisible={selected && !locked}
        minWidth={40}
        minHeight={24}
        lineClassName="annotation-resize-line"
        handleClassName="annotation-resize-handle"
      />
      <div
        className={cls}
        {...pan}
        onDoubleClick={(e) => {
          e.stopPropagation();
          if (!locked) setEditing(true);
        }}
      >
        {editing ? (
          <textarea
            ref={inputRef}
            className="annotation-editor nodrag nowheel"
            value={draft}
            placeholder={kind === 'note' ? 'Write a note' : 'Label'}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => finish(draft)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                e.stopPropagation();
                finish(data.text || '');
              } else if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                finish(draft);
              }
            }}
          />
        ) : data.text || kind !== 'note' ? (
          <div className="annotation-text">{data.text}</div>
        ) : (
          <div className="annotation-text annotation-placeholder">Note</div>
        )}
      </div>
    </>
  );
};

export default AnnotationNode;
