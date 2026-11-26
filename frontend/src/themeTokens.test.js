import fs from 'fs';
import path from 'path';

// Every color token has a dark value, unless it is meant to be the same in
// both themes. A token that only names another (var(--x)) follows it.
const SAME_IN_BOTH = new Set([
  '--text-on-brand',
  '--brand-tile',
  '--console-bg',
  '--console-fg',
  '--console-muted',
  '--console-accent',
  '--shadow-color',
  '--scrim-color',
  '--warning',
  '--warning-accent',
  '--status-pending',
  '--graph-label-border',
  '--minimap-toggle-hover',
  '--minimap-fill-other',
  '--trace-path',
  '--trace-path-active',
]);

const COLOR = /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(|color-mix\(|gradient\(/;

const sheets = (dir) =>
  fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) return sheets(p);
    return p.endsWith('.css') ? [p] : [];
  });

const declared = (block) => {
  const out = {};
  block.replace(/(--[\w-]+)\s*:\s*([^;]+);/g, (_, name, value) => {
    out[name] = value.trim();
    return '';
  });
  return out;
};

const blocks = (text, selector) => {
  const out = {};
  const re = new RegExp(`${selector}\\s*\\{([^}]*)\\}`, 'g');
  let m;
  while ((m = re.exec(text))) Object.assign(out, declared(m[1]));
  return out;
};

test('every color token has its dark value', () => {
  const problems = [];
  sheets(__dirname).forEach((file) => {
    const rel = path.relative(__dirname, file);
    const text = fs.readFileSync(file, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
    const light = blocks(text, ":root(?:,\\s*\\[data-theme='light'\\])?");
    const dark = blocks(text, ":root\\[data-theme='dark'\\]");
    Object.entries(light).forEach(([name, value]) => {
      if (!COLOR.test(value) || SAME_IN_BOTH.has(name) || name in dark) return;
      problems.push(`${rel}: ${name} has no dark value`);
    });
    Object.keys(dark).forEach((name) => {
      if (!(name in light)) problems.push(`${rel}: ${name} is dark only`);
    });
  });
  expect(problems).toEqual([]);
});
