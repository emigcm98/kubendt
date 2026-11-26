import fs from 'fs';
import path from 'path';

// Colors belong to the tokens. Only the fallbacks a few modules keep for tests
// without CSS hold literals, exactly these many.
const ALLOWED = {
  'components/graphPicture.js': 16,
  'components/PodInteractiveShellModal.js': 2,
};

const CSS_COLOR = /#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(|(?<![-\w])(white|black)(?![-\w])/g;
const JS_COLOR = /['"`]#[0-9a-fA-F]{3,8}['"`]|\brgba?\(|\bhsla?\(/g;

// A block whose selectors are only :root and [data-theme='…'], alone or joined.
const THEME_SEL = "(?::root(?:\\[data-theme='\\w+'\\])?|\\[data-theme='\\w+'\\])";
const TOKEN_BLOCK = new RegExp(`${THEME_SEL}(?:\\s*,\\s*${THEME_SEL})*\\s*\\{[^}]*\\}`, 'g');

const sources = (dir) =>
  fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    return e.isDirectory() ? sources(p) : [p];
  });

const literals = () => {
  const counts = {};
  sources(__dirname).forEach((file) => {
    const rel = path.relative(__dirname, file);
    if (/\.test\.js$/.test(rel)) return;
    let text;
    let re;
    if (rel.endsWith('.css')) {
      text = fs.readFileSync(file, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
      // the token blocks, :root and the themes, are where colors are defined
      text = text.replace(TOKEN_BLOCK, '');
      re = CSS_COLOR;
    } else if (rel.endsWith('.js')) {
      text = fs.readFileSync(file, 'utf8');
      re = JS_COLOR;
    } else {
      return;
    }
    const n = (text.match(re) || []).length;
    if (n) counts[rel] = n;
  });
  return counts;
};

test('colors live in the tokens', () => {
  const counts = literals();
  const off = Object.entries(counts)
    .filter(([f, n]) => n !== (ALLOWED[f] || 0))
    .map(([f, n]) => `${n} ${f} (allowed ${ALLOWED[f] || 0})`);
  Object.keys(ALLOWED)
    .filter((f) => !counts[f])
    .forEach((f) => off.push(`0 ${f} (allowed ${ALLOWED[f]}, lower it)`));
  if (off.length) throw new Error(`color literals outside the tokens:\n${off.join('\n')}`);
});
