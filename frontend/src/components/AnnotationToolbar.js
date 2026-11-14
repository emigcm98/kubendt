import React from 'react';
import { ReactComponent as CursorIcon } from '../assets/images/icons/cursor.svg';
import { ReactComponent as NoteIcon } from '../assets/images/icons/note.svg';
import { ReactComponent as SquareIcon } from '../assets/images/icons/square.svg';
import { ReactComponent as CircleIcon } from '../assets/images/icons/circle.svg';

// The drawing tools, a strip of their own above the graph's controls.
// Select is the resting state. A drawing tool stays lit while it is picked,
// and drawing a shape, Esc or clicking it again goes back to select.
const TOOLS = [
  ['select', CursorIcon, 'Select and move'],
  ['note', NoteIcon, 'Note: drag on the graph to place one'],
  ['rect', SquareIcon, 'Rectangle: drag on the graph to draw one'],
  ['ellipse', CircleIcon, 'Ellipse: drag on the graph to draw one'],
];

const AnnotationToolbar = ({ tool, setTool, disabled = false }) => (
  <div className="graph-controls graph-tools">
    {TOOLS.map(([id, Icon, title]) => (
      <button
        key={id}
        type="button"
        className={`graph-control-btn${tool === id ? ' graph-control-btn-active' : ''}`}
        title={title}
        disabled={disabled}
        onClick={() => setTool(tool === id ? 'select' : id)}
      >
        <Icon className="app-icon" aria-hidden="true" />
      </button>
    ))}
  </div>
);

export default AnnotationToolbar;
