import React from 'react';
import { NodeToolbar, Position } from 'reactflow';
import { ReactComponent as DashedIcon } from '../assets/images/icons/dashed.svg';
import { ReactComponent as FillNoneIcon } from '../assets/images/icons/fill-none.svg';
import { ReactComponent as FillGlassIcon } from '../assets/images/icons/fill-glass.svg';
import { ReactComponent as FillSolidIcon } from '../assets/images/icons/fill-solid.svg';
import { ReactComponent as SendBackIcon } from '../assets/images/icons/send-back.svg';
import { ReactComponent as BringFrontIcon } from '../assets/images/icons/bring-front.svg';
import { ReactComponent as LockIcon } from '../assets/images/icons/lock.svg';
import { ReactComponent as UnlockIcon } from '../assets/images/icons/unlock.svg';
import { ReactComponent as TrashIcon } from '../assets/images/icons/trash.svg';
import { defaultAnchor } from './useLayout';
import './Annotations.css';

// The style bar over a selected shape: its ink, border, text size, order
// among the shapes, lock and delete. A locked shape only offers the unlock.
// The names are the palette's; the CSS maps them to the tokens.
const COLORS = [
  ['', 'slate', 'Slate'],
  ['blue', 'blue', 'Blue'],
  ['green', 'green', 'Green'],
  ['amber', 'amber', 'Amber'],
  ['red', 'red', 'Red'],
];
const SIZES = [
  ['sm', 'S', 'Small text'],
  ['', 'M', 'Medium text'],
  ['lg', 'L', 'Large text'],
];
const FILLS = [
  ['none', FillNoneIcon, 'No fill'],
  ['', FillGlassIcon, 'Glass fill'],
  ['solid', FillSolidIcon, 'Solid fill'],
];
// Where the text sits, a three by three grid in reading order.
const ANCHORS = [
  ['top-left', 'Text top left'],
  ['top', 'Text top'],
  ['top-right', 'Text top right'],
  ['left', 'Text left'],
  ['center', 'Text centred'],
  ['right', 'Text right'],
  ['bottom-left', 'Text bottom left'],
  ['bottom', 'Text bottom'],
  ['bottom-right', 'Text bottom right'],
];

const BAR_LEVEL = { zIndex: 10 };

const AnnotationStyleBar = ({ id, data }) => {
  const locked = !!data.locked;
  const anchor = data.textAnchor || defaultAnchor(data.kind);
  const set = (patch) => data.onChange?.(id, patch);
  const btn = (active) => `annotation-bar-btn${active ? ' annotation-bar-btn-active' : ''}`;

  return (
    // React Flow stacks the bar just over its node, which sits under the
    // pane, so the bar needs a level of its own to take clicks.
    <NodeToolbar
      position={Position.Top}
      offset={8}
      className="annotation-bar nopan"
      style={BAR_LEVEL}
    >
      {!locked && (
        <>
          <div className="annotation-bar-group">
            {COLORS.map(([value, name, title]) => (
              <button
                key={name}
                type="button"
                className={`annotation-swatch ann-color-${name}${(data.color || '') === value ? ' annotation-swatch-active' : ''}`}
                title={title}
                onClick={() => set({ color: value })}
              />
            ))}
          </div>
          <div className="annotation-bar-sep" />
          {data.kind !== 'note' && (
            <>
              <button
                type="button"
                className={btn(data.border === 'dashed')}
                title="Dashed border"
                onClick={() => set({ border: data.border === 'dashed' ? '' : 'dashed' })}
              >
                <DashedIcon className="app-icon" aria-hidden="true" />
              </button>
              {FILLS.map(([value, Icon, title]) => (
                <button
                  key={title}
                  type="button"
                  className={btn((data.fill || '') === value)}
                  title={title}
                  onClick={() => set({ fill: value })}
                >
                  <Icon className="app-icon" aria-hidden="true" />
                </button>
              ))}
              <div className="annotation-bar-sep" />
            </>
          )}
          {SIZES.map(([value, label, title]) => (
            <button
              key={label}
              type="button"
              className={`${btn((data.textSize || '') === value)} annotation-bar-text-${value || 'md'}`}
              title={title}
              onClick={() => set({ textSize: value })}
            >
              {label}
            </button>
          ))}
          <div className="annotation-anchors">
            {ANCHORS.map(([value, title]) => (
              <button
                key={value}
                type="button"
                className={`annotation-anchor${anchor === value ? ' annotation-anchor-active' : ''}`}
                title={title}
                onClick={() => set({ textAnchor: value })}
              />
            ))}
          </div>
          <div className="annotation-bar-sep" />
          <button
            type="button"
            className={btn(false)}
            title="Send to back"
            onClick={() => data.onReorder?.(id, 'back')}
          >
            <SendBackIcon className="app-icon" aria-hidden="true" />
          </button>
          <button
            type="button"
            className={btn(false)}
            title="Bring to front"
            onClick={() => data.onReorder?.(id, 'front')}
          >
            <BringFrontIcon className="app-icon" aria-hidden="true" />
          </button>
        </>
      )}
      <button
        type="button"
        className={btn(locked)}
        title={locked ? 'Unlock' : 'Lock: no move, resize or edit'}
        onClick={() => set({ locked: !locked })}
      >
        {locked ? (
          <LockIcon className="app-icon" aria-hidden="true" />
        ) : (
          <UnlockIcon className="app-icon" aria-hidden="true" />
        )}
      </button>
      {!locked && (
        <button
          type="button"
          className="annotation-bar-btn annotation-bar-danger"
          title="Delete"
          onClick={() => data.onDelete?.(id)}
        >
          <TrashIcon className="app-icon" aria-hidden="true" />
        </button>
      )}
    </NodeToolbar>
  );
};

export default AnnotationStyleBar;
