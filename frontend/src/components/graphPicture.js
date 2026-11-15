// The graph as a picture of its own, not a screenshot: an SVG built from the
// nodes, links and annotations with the geometry and the tokens the screen
// uses, so it opens anywhere and turns into a PNG at any scale or a vector
// PDF. States of the moment (selection, search dimming, creating) are left
// out, the picture is the diagram.
import { NODE_SIZE } from './CustomNode';
import { anchorAxes, defaultAnchor } from './useLayout';
import hostIcon from '../assets/images/nodes/host.svg';
import routerIcon from '../assets/images/nodes/router.svg';
import switchIcon from '../assets/images/nodes/switch.svg';
import externalIcon from '../assets/images/nodes/switch_gray.svg';
import internetIcon from '../assets/images/nodes/internet.svg';

// One family the three outputs share, so a pill measured here fits its text
// in the SVG, the PNG and the PDF alike (jsPDF has Helvetica built in).
export const FONT_FAMILY = 'Helvetica, Arial, sans-serif';

// The same tuning as the interface labels on screen (InnerGraph).
const RADIUS_V = NODE_SIZE * 0.54;
const RADIUS_H = NODE_SIZE * 0.72;
const HOST_DELTA = NODE_SIZE * 0.12;
const SHIFT = 5;
const LABEL_STAGGER = NODE_SIZE * 0.45;
const labelRadii = (type) =>
  type === 'host'
    ? { v: RADIUS_V + HOST_DELTA, h: RADIUS_H - HOST_DELTA, s: SHIFT }
    : { v: RADIUS_V, h: RADIUS_H, s: SHIFT };

const ICON_URLS = {
  host: hostIcon,
  router: routerIcon,
  switch: switchIcon,
  external: externalIcon,
  internet: internetIcon,
};
const TOKEN_NAMES = [
  'surface',
  'border',
  'text-body',
  'text-muted',
  'graph-edge',
  'primary',
  'primary-hover',
  'success',
  'success-accent',
  'warning',
  'warning-strong',
  'danger',
];

// The tokens as the page resolves them, with the hex of index.css as the
// fallback when there is no document (tests).
export const readTokens = () => {
  const fallback = {
    surface: '#ffffff',
    border: '#d3dfec',
    'text-body': '#334155',
    'text-muted': '#64748b',
    'graph-edge': '#2b333f',
    primary: '#1976d2',
    'primary-hover': '#1565c0',
    success: '#2e7d32',
    'success-accent': '#4caf50',
    warning: '#f0a020',
    'warning-strong': '#f57c00',
    danger: '#d32f2f',
  };
  if (typeof document === 'undefined') return fallback;
  const style = getComputedStyle(document.documentElement);
  const tokens = { ...fallback };
  TOKEN_NAMES.forEach((name) => {
    const v = style.getPropertyValue(`--${name}`).trim();
    if (v) tokens[name] = v;
  });
  return tokens;
};

let iconCache = null;
// The node icons as SVG text, fetched once; they go inline so the picture is
// vector through and through.
export const loadIcons = async () => {
  if (iconCache) return iconCache;
  const entries = await Promise.all(
    Object.entries(ICON_URLS).map(async ([kind, url]) => {
      try {
        const res = await fetch(url);
        return [kind, res.ok ? await res.text() : ''];
      } catch {
        return [kind, ''];
      }
    })
  );
  iconCache = Object.fromEntries(entries);
  return iconCache;
};

let insetCache = null;
// How much air each icon carries inside its box, as fractions of the box,
// measured once by drawing it; the frame hugs the ink, not the box.
export const loadIconInsets = async (icons) => {
  if (insetCache) return insetCache;
  if (typeof document === 'undefined') return {};
  const measure = (text) =>
    new Promise((resolve) => {
      const none = { l: 0, t: 0, r: 0, b: 0 };
      if (!text) return resolve(none);
      const img = new Image();
      const n = 128;
      img.onload = () => {
        try {
          const canvas = document.createElement('canvas');
          canvas.width = n;
          canvas.height = n;
          const ctx = canvas.getContext('2d');
          ctx.drawImage(img, 0, 0, n, n);
          const a = ctx.getImageData(0, 0, n, n).data;
          let minX = n;
          let minY = n;
          let maxX = -1;
          let maxY = -1;
          for (let y = 0; y < n; y++) {
            for (let x = 0; x < n; x++) {
              if (a[(y * n + x) * 4 + 3] > 8) {
                if (x < minX) minX = x;
                if (x > maxX) maxX = x;
                if (y < minY) minY = y;
                if (y > maxY) maxY = y;
              }
            }
          }
          if (maxX < 0) return resolve(none);
          resolve({ l: minX / n, t: minY / n, r: (n - 1 - maxX) / n, b: (n - 1 - maxY) / n });
        } catch {
          resolve(none);
        }
      };
      img.onerror = () => resolve(none);
      img.src = svgDataUrl(text);
    });
  const entries = await Promise.all(
    Object.entries(icons).map(async ([kind, text]) => [kind, await measure(text)])
  );
  insetCache = Object.fromEntries(entries);
  return insetCache;
};

const esc = (s) =>
  String(s ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');

const hex = (c) => {
  const m = /^#([0-9a-f]{6})$/i.exec(c.trim());
  if (!m) return null;
  const n = parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
};
// color-mix(in srgb, ink t%, base) for two hex colours.
const mix = (ink, base, t) => {
  const a = hex(ink);
  const b = hex(base);
  if (!a || !b) return ink;
  const c = a.map((v, i) => Math.round(v * t + b[i] * (1 - t)));
  return `#${c.map((v) => v.toString(16).padStart(2, '0')).join('')}`;
};

let measureCtx = null;
// A text's ink for a font: width, and how far it rises and drops from the
// baseline, by the canvas when there is one, else by a plain estimate.
const textInk = (text, size, weight = 400) => {
  if (!measureCtx && typeof document !== 'undefined') {
    const canvas = document.createElement('canvas');
    measureCtx = canvas.getContext && canvas.getContext('2d');
  }
  if (measureCtx) {
    measureCtx.font = `${weight} ${size}px ${FONT_FAMILY}`;
    const m = measureCtx.measureText(text);
    return {
      width: m.width,
      ascent: m.actualBoundingBoxAscent ?? size * 0.75,
      descent: m.actualBoundingBoxDescent ?? size * 0.2,
    };
  }
  return { width: text.length * size * 0.55, ascent: size * 0.75, descent: size * 0.2 };
};
const textWidth = (text, size, weight = 400) => textInk(text, size, weight).width;

// Lines of a text that fit a width, breaking on words and on its own
// newlines, as pre-wrap does.
const wrap = (text, size, weight, maxWidth) => {
  const lines = [];
  String(text ?? '')
    .split('\n')
    .forEach((para) => {
      const words = para.split(/(\s+)/);
      let line = '';
      words.forEach((w) => {
        const next = line + w;
        if (line && textWidth(next, size, weight) > maxWidth) {
          lines.push(line.trimEnd());
          line = w.trimStart();
        } else {
          line = next;
        }
      });
      lines.push(line.trimEnd());
    });
  return lines;
};

// The inner content of an icon file as a nested svg of the given box.
const iconSvg = (text, x, y, size) => {
  if (!text) return '';
  const open = /<svg\b[^>]*>/i.exec(text);
  const close = text.lastIndexOf('</svg>');
  if (!open || close < 0) return '';
  const vb = /viewBox="([^"]+)"/i.exec(open[0]);
  const inner = text.slice(open.index + open[0].length, close);
  return `<svg x="${x}" y="${y}" width="${size}" height="${size}" viewBox="${vb ? vb[1] : '0 0 1024 1024'}">${inner}</svg>`;
};

const pill = ({
  x,
  y,
  text,
  size,
  weight,
  padX,
  padY,
  fill,
  stroke,
  strokeWidth,
  radius,
  color,
  dash,
}) => {
  const w = textWidth(text, size, weight) + padX * 2;
  const h = size * 1.25 + padY * 2;
  const left = x - w / 2;
  const top = y - h / 2;
  const half = strokeWidth / 2;
  const box = {
    left: left - half,
    top: top - half,
    right: left + w + half,
    bottom: top + h + half,
  };
  const svg =
    `<rect x="${left.toFixed(2)}" y="${top.toFixed(2)}" width="${w.toFixed(2)}" height="${h.toFixed(2)}" rx="${radius}" fill="${fill}" stroke="${stroke}" stroke-width="${strokeWidth}"${dash ? ` stroke-dasharray="${dash}"` : ''}/>` +
    `<text x="${x.toFixed(2)}" y="${(y + size * 0.36).toFixed(2)}" text-anchor="middle" font-size="${size}" font-weight="${weight}" fill="${color}">${esc(text)}</text>`;
  return { svg, box };
};

const annotationInk = (data, tokens) => {
  const byName = {
    slate: tokens['text-muted'],
    blue: tokens.primary,
    green: tokens.success,
    amber: tokens['warning-strong'],
    red: tokens.danger,
  };
  if (data.color && byName[data.color]) return byName[data.color];
  return data.kind === 'note' ? tokens['text-body'] : tokens['text-muted'];
};

const annotationSvg = (n, tokens) => {
  const d = n.data || {};
  const w = n.style?.width ?? n.width ?? 120;
  const h = n.style?.height ?? n.height ?? 60;
  const { x, y } = n.position;
  const ink = annotationInk(d, tokens);
  const size = d.textSize === 'sm' ? 8 : d.textSize === 'lg' ? 18 : 12;
  const weight = d.kind === 'note' ? 400 : 600;
  const padX = 6;
  const padY = 3;
  let shape = '';
  if (d.kind !== 'note') {
    const tint = d.color === 'amber' ? 0.12 : 0.08;
    let fill = 'none';
    let opacity = '';
    if (d.fill === 'solid') fill = mix(ink, tokens.surface, tint);
    else if (d.fill !== 'none') {
      fill = ink;
      opacity = ` fill-opacity="${tint}"`;
    }
    const dash = d.border === 'dashed' ? ' stroke-dasharray="4.5 4.5"' : '';
    shape =
      d.kind === 'ellipse'
        ? `<ellipse cx="${x + w / 2}" cy="${y + h / 2}" rx="${w / 2}" ry="${h / 2}" fill="${fill}"${opacity} stroke="${ink}" stroke-width="1.5"${dash}/>`
        : `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="8" fill="${fill}"${opacity} stroke="${ink}" stroke-width="1.5"${dash}/>`;
  }
  const lines = wrap(d.text, size, weight, Math.max(1, w - padX * 2));
  const lineStep = size * 1.4;
  const blockH = lines.length * lineStep;
  const [row, col] = anchorAxes(d.textAnchor || defaultAnchor(d.kind));
  const top =
    row === 'middle' ? y + (h - blockH) / 2 : row === 'bottom' ? y + h - padY - blockH : y + padY;
  const anchor = col === 'center' ? 'middle' : col === 'right' ? 'end' : 'start';
  const tx = col === 'center' ? x + w / 2 : col === 'right' ? x + w - padX : x + padX;
  const inks = lines.map((l) => textInk(l, size, weight));
  const textWidthMax = Math.max(...inks.map((k) => k.width), 0);
  const textLeft =
    col === 'center' ? tx - textWidthMax / 2 : col === 'right' ? tx - textWidthMax : tx;
  const baseline = (i) => top + i * lineStep + size * 0.95 + (lineStep - size) / 2;
  const spans = lines
    .map(
      (l, i) => `<tspan x="${tx.toFixed(2)}" y="${baseline(i).toFixed(2)}">${esc(l) || ' '}</tspan>`
    )
    .join('');
  const text = lines.some((l) => l)
    ? `<text text-anchor="${anchor}" font-size="${size}" font-weight="${weight}" fill="${ink}">${spans}</text>`
    : '';
  // a note counts by its text's ink, its box is invisible; a shape by its
  // stroke, which sits half outside the geometry
  const box =
    d.kind === 'note'
      ? {
          left: textLeft,
          top: Math.min(...inks.map((k, i) => baseline(i) - k.ascent)),
          right: textLeft + textWidthMax,
          bottom: Math.max(...inks.map((k, i) => baseline(i) + k.descent)),
        }
      : { left: x - 0.75, top: y - 0.75, right: x + w + 0.75, bottom: y + h + 0.75 };
  return { svg: shape + text, box };
};

const nodeSvg = (n, tokens, icons, iconInsets) => {
  const d = n.data || {};
  const { x, y } = n.position;
  const cx = x + NODE_SIZE / 2;
  const cy = y + NODE_SIZE / 2;
  const kind =
    d.type === 'router'
      ? 'router'
      : d.type === 'switch'
        ? 'switch'
        : d.type === 'external'
          ? 'external'
          : 'host';
  let svg = iconSvg(icons[kind], x, y, NODE_SIZE);
  const ins = iconInsets[kind] || { l: 0, t: 0, r: 0, b: 0 };
  const boxes = [
    {
      left: x + ins.l * NODE_SIZE,
      top: y + ins.t * NODE_SIZE,
      right: x + NODE_SIZE - ins.r * NODE_SIZE,
      bottom: y + NODE_SIZE - ins.b * NODE_SIZE,
    },
  ];
  if (d.internet) {
    const s = NODE_SIZE / 3;
    svg += iconSvg(icons.internet, x + NODE_SIZE - s, y, s);
  }
  if (d.type !== 'external') {
    const s = NODE_SIZE / 6;
    const bottom = d.type === 'switch' ? NODE_SIZE / 5 : NODE_SIZE / 7;
    const right = d.type === 'switch' ? NODE_SIZE / 8 : NODE_SIZE / 10;
    const color =
      d.status === 'Running'
        ? tokens['success-accent']
        : d.status === 'Pending'
          ? '#ffeb3b'
          : d.status === 'running-not-ready' || d.status === 'container-not-ready'
            ? tokens.warning
            : tokens.danger;
    const dcx = x + NODE_SIZE - right - s / 2;
    const dcy = y + NODE_SIZE - bottom - s / 2;
    svg += `<circle cx="${dcx.toFixed(2)}" cy="${dcy.toFixed(2)}" r="${(s / 2).toFixed(2)}" fill="${color}" stroke="${tokens.surface}" stroke-width="1"/>`;
    boxes.push({ left: dcx - s / 2, top: dcy - s / 2, right: dcx + s / 2, bottom: dcy + s / 2 });
  }
  // the label hangs under the icon, a bit lower on a host
  const size = NODE_SIZE * 0.13;
  const padX = NODE_SIZE * 0.06;
  const padY = NODE_SIZE * 0.03;
  const h = size * 1.25 + padY * 2;
  const label = pill({
    x: cx,
    y: cy + h * (d.type === 'host' ? 1.5 : 1) + h / 2,
    text: d.label || n.id,
    size,
    weight: 700,
    padX,
    padY,
    fill: 'rgba(184, 208, 223, 0.8)',
    stroke: '#4695c9',
    strokeWidth: 1,
    radius: 6,
    color: tokens['text-body'],
  });
  svg += label.svg;
  boxes.push(label.box);
  return { svg, boxes };
};

const union = (boxes) =>
  boxes.reduce(
    (b, c) => ({
      left: Math.min(b.left, c.left),
      top: Math.min(b.top, c.top),
      right: Math.max(b.right, c.right),
      bottom: Math.max(b.bottom, c.bottom),
    }),
    { left: Infinity, top: Infinity, right: -Infinity, bottom: -Infinity }
  );

// The picture: `interfaceStatus(pod, iface)` says up (true), down (false) or
// unknown for the colour of a label's border.
export const paintGraph = ({
  nodes = [],
  edges = [],
  margin = 40,
  transparent = false,
  tokens = readTokens(),
  icons = {},
  iconInsets = {},
  interfaceStatus = () => undefined,
}) => {
  const shown = nodes.filter((n) => !n.hidden);
  const byId = new Map(shown.map((n) => [n.id, n]));
  const annotations = shown
    .filter((n) => n.type === 'annotation')
    .sort((a, b) => (a.data?.z || 0) - (b.data?.z || 0));
  const pods = shown.filter((n) => n.type !== 'annotation');
  const boxes = [];
  let annLayer = '';
  annotations.forEach((n) => {
    const a = annotationSvg(n, tokens);
    annLayer += a.svg;
    boxes.push(a.box);
  });
  let edgeLayer = '';
  let labelLayer = '';
  edges.forEach((e) => {
    const s = byId.get(e.source);
    const t = byId.get(e.target);
    if (!s || !t || s.type === 'annotation' || t.type === 'annotation') return;
    const sc = { x: s.position.x + NODE_SIZE / 2, y: s.position.y + NODE_SIZE / 2 };
    const tc = { x: t.position.x + NODE_SIZE / 2, y: t.position.y + NODE_SIZE / 2 };
    const dx = tc.x - sc.x;
    const dy = tc.y - sc.y;
    const length = Math.hypot(dx, dy) || 1;
    const ux = dx / length;
    const uy = dy / length;
    const offset = e.data?.offset || 0;
    const ox = -uy * offset;
    const oy = ux * offset;
    edgeLayer += `<line x1="${(sc.x + ox).toFixed(2)}" y1="${(sc.y + oy).toFixed(2)}" x2="${(tc.x + ox).toFixed(2)}" y2="${(tc.y + oy).toFixed(2)}" stroke="${tokens['graph-edge']}" stroke-width="1.4" stroke-linecap="round"/>`;
    const sr = labelRadii(s.data?.type);
    const tr = labelRadii(t.data?.type);
    const vfactor = Math.abs(uy);
    const srcR = 1 / Math.hypot(ux / sr.h, uy / sr.v);
    const tgtR = 1 / Math.hypot(ux / tr.h, uy / tr.v);
    const columns = e.data?.labelColumns || 1;
    const stagger =
      columns > 1
        ? (e.data?.labelStep || 0) *
          Math.min(
            LABEL_STAGGER,
            Math.max(0, (length - srcR - tgtR - LABEL_STAGGER) / (2 * (columns - 1)))
          )
        : 0;
    const ends = [
      {
        pod: e.source,
        iface: e.data?.localIntf,
        x: sc.x + ux * (srcR + stagger) + ox,
        y: sc.y + uy * (srcR + stagger) + sr.s * vfactor + oy,
        external: s.data?.type === 'external',
      },
      {
        pod: e.target,
        iface: e.data?.peerIntf,
        x: tc.x - ux * (tgtR + stagger) + ox,
        y: tc.y - uy * (tgtR + stagger) + tr.s * vfactor + oy,
        external: t.data?.type === 'external',
      },
    ];
    ends.forEach((end) => {
      if (end.external || !end.iface) return;
      const status = interfaceStatus(end.pod, end.iface);
      const stroke =
        status === true
          ? tokens['success-accent']
          : status === false
            ? tokens.danger
            : tokens.border;
      const p = pill({
        x: end.x,
        y: end.y,
        text: end.iface,
        size: NODE_SIZE / 10,
        weight: 400,
        padX: 5,
        padY: 1,
        fill: mix(tokens.surface, tokens.surface, 1),
        stroke,
        strokeWidth: 1.5,
        radius: 6,
        color: tokens['text-body'],
      });
      labelLayer += p.svg;
      boxes.push(p.box);
    });
    if (e.data?.linkName) {
      const p = pill({
        x: (sc.x + tc.x) / 2 + ox,
        y: (sc.y + tc.y) / 2 + oy,
        text: e.data.linkName,
        size: NODE_SIZE / 9,
        weight: 700,
        padX: 6,
        padY: 1,
        fill: tokens['primary-hover'],
        stroke: 'none',
        strokeWidth: 0,
        radius: 8,
        color: '#ffffff',
      });
      labelLayer += p.svg;
      boxes.push(p.box);
    }
  });
  let nodeLayer = '';
  pods.forEach((n) => {
    const p = nodeSvg(n, tokens, icons, iconInsets);
    nodeLayer += p.svg;
    boxes.push(...p.boxes);
  });
  if (boxes.length === 0) return null;
  const b = union(boxes);
  const width = Math.ceil(b.right - b.left + margin * 2);
  const height = Math.ceil(b.bottom - b.top + margin * 2);
  const shift = `translate(${(margin - b.left).toFixed(2)} ${(margin - b.top).toFixed(2)})`;
  const background = transparent
    ? ''
    : `<rect width="100%" height="100%" fill="${tokens.surface}"/>`;
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" font-family="${FONT_FAMILY}">` +
    background +
    `<g transform="${shift}">${annLayer}${edgeLayer}${labelLayer}${nodeLayer}</g></svg>`;
  return { svg, width, height };
};

export const svgDataUrl = (svg) => `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;

// The SVG drawn on a canvas at a scale, as a PNG data URL.
export const pictureToPng = (svg, width, height, scale = 2) =>
  new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      const canvas = document.createElement('canvas');
      canvas.width = Math.round(width * scale);
      canvas.height = Math.round(height * scale);
      const ctx = canvas.getContext('2d');
      ctx.scale(scale, scale);
      ctx.drawImage(img, 0, 0, width, height);
      resolve(canvas.toDataURL('image/png'));
    };
    img.onerror = () => reject(new Error('the picture could not be drawn'));
    img.src = svgDataUrl(svg);
  });

// The SVG as a vector PDF page of its size, jsPDF and svg2pdf loaded here only.
export const pictureToPdf = async (svg, width, height) => {
  const { jsPDF } = await import('jspdf');
  await import('svg2pdf.js');
  const pdf = new jsPDF({
    orientation: width > height ? 'landscape' : 'portrait',
    unit: 'px',
    format: [width, height],
    hotfixes: ['px_scaling'],
  });
  const el = new DOMParser().parseFromString(svg, 'image/svg+xml').documentElement;
  await pdf.svg(el, { x: 0, y: 0, width, height });
  return pdf;
};
