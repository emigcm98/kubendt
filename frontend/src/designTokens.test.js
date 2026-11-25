import fs from 'fs';
import path from 'path';

// Colors belong to the tokens. The pages still waiting for their redesign
// keep some literals and the ceiling goes down as they are redone. The
// fallbacks graphPicture.js and the shell keep for tests without CSS stay.
const CEILING = 152;

const CSS_COLOR = /#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(|(?<![-\w])(white|black)(?![-\w])/g;
const JS_COLOR = /['"`]#[0-9a-fA-F]{3,8}['"`]|\brgba?\(|\bhsla?\(/g;

const sources = (dir) =>
  fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    return e.isDirectory() ? sources(p) : [p];
  });

const literals = () => {
  const counts = {};
  sources(__dirname).forEach((file) => {
    const rel = path.relative(__dirname, file);
    if (rel === 'index.css' || /\.test\.js$/.test(rel)) return;
    let text;
    let re;
    if (rel.endsWith('.css')) {
      text = fs.readFileSync(file, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
      // a stylesheet's own :root block is where its tokens are defined
      text = text.replace(/:root\s*\{[^}]*\}/g, '');
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

test('colors outside the tokens do not grow', () => {
  const counts = literals();
  const total = Object.values(counts).reduce((a, b) => a + b, 0);
  if (total > CEILING) {
    const worst = Object.entries(counts)
      .sort((a, b) => b[1] - a[1])
      .map(([f, n]) => `${n} ${f}`)
      .join('\n');
    throw new Error(
      `${total} color literals outside the tokens, the ceiling is ${CEILING}:\n${worst}`
    );
  }
});
