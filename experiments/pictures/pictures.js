#!/usr/bin/env node
// The pictures of the examples' graphs for their READMEs. Each example is
// deployed on the cluster the backend points at, with the layout its topology
// file carries, the graph is exported through the UI's own Export > Image,
// the modify steps a second picture needs are applied in between, and the
// namespace is deleted at the end.
//
//   KUBENDT_API=http://localhost:8080 KUBENDT_UI=http://localhost:3000 node pictures.js [example ...] [--keep]
const fs = require("fs");
const os = require("os");
const path = require("path");
const puppeteer = require("puppeteer-core");

const API = process.env.KUBENDT_API || "http://localhost:8080";
const UI = process.env.KUBENDT_UI || "http://localhost:3000";
const PASSWORD = process.env.KUBENDT_PASSWORD || "admin123";
const CHROME = process.env.CHROME || "/usr/bin/chromium-browser";
const EXAMPLES = path.resolve(__dirname, "../../deploy/examples");
const OUT =
  process.env.OUT || path.resolve(__dirname, "../../doc/images/tests");
const MARGIN = process.env.MARGIN || "24";
const SCALE = process.env.SCALE || "2";

// Per example: the namespace its README uses, the topology file, the files it
// mounts (a folder or a zip) and the pictures, each after the modify steps it
// comes after.
const TABLE = [
  {
    name: "1-test-small",
    ns: "small",
    topology: "topology-network-test-small.json",
    pictures: [["1-test-small", []]],
  },
  {
    name: "2-test-frr-ospf-rip",
    ns: "frr-ospf-rip",
    topology: "topology-network-test-frr-ospf-rip.json",
    pictures: [["2-test-frr-ospf-rip", []]],
  },
  {
    name: "3-test-modify-ospf",
    ns: "modify-ospf",
    topology: "topology-network-test-modify-ospf.json",
    pictures: [
      ["3-test-modify-ospf-1", []],
      [
        "3-test-modify-ospf-2",
        ["modify-phase2-scaleup.json", "modify-phase3-add.json"],
      ],
    ],
  },
  {
    name: "4-test-medium-allfeatures",
    ns: "allfeatures",
    topology: "topology-network-test-allfeatures.json",
    pictures: [["4-test-medium-allfeatures", []]],
  },
  {
    name: "5-test-full-open5gs",
    ns: "open5gs",
    topology: "topology-network-test-big.json",
    zip: "open5gs_files.zip",
    pictures: [["5-test-full-open5gs", []]],
  },
  {
    name: "6-test-vyos",
    ns: "vyos",
    topology: "topology-network-test-vyos.json",
    pictures: [["6-test-vyos", []]],
  },
  {
    name: "7-test-dual-stack",
    ns: "dual-stack",
    topology: "topology-network-test-dual-stack.json",
    pictures: [["7-test-dual-stack", []]],
  },
  {
    name: "8-test-two-orgs-rip-ospf",
    ns: "two-orgs",
    topology: "topology-network-test-two-orgs-rip-ospf.json",
    pictures: [["8-test-two-orgs-rip-ospf", []]],
  },
  {
    name: "9-test-four-orgs-bgp",
    ns: "four-orgs",
    topology: "topology-network-test-four-orgs-bgp.json",
    pictures: [["9-test-four-orgs-bgp", []]],
  },
  {
    name: "10-test-layer2-sdn",
    ns: "layer2-sdn",
    topology: "topology-network-test-layer2-sdn.json",
    pictures: [["10-test-layer2-sdn", []]],
  },
  {
    name: "11-test-p4",
    ns: "p4",
    topology: "topology-network-test-p4.json",
    pictures: [["11-test-p4", []]],
  },
];

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);

// ---------- the API ----------
let cookie = "";

async function api(method, route, body, form) {
  const headers = { Cookie: cookie };
  let payload = form;
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = JSON.stringify(body);
  }
  const res = await fetch(`${API}${route}`, { method, headers, body: payload });
  const text = await res.text();
  let data = text;
  try {
    data = JSON.parse(text);
  } catch {
    // not JSON, keep the text
  }
  return { status: res.status, data };
}

async function must(label, p, ok = (s) => s === 200) {
  const { status, data } = await p;
  if (!ok(status))
    throw new Error(
      `${label}: HTTP ${status} ${JSON.stringify(data).slice(0, 300)}`,
    );
  return data;
}

async function login() {
  const res = await fetch(`${API}/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: "admin", password: PASSWORD }),
  });
  if (!res.ok) throw new Error(`login failed: HTTP ${res.status}`);
  cookie = (res.headers.get("set-cookie") || "").split(";")[0];
}

const fileForm = (file, fields = {}) => {
  const form = new FormData();
  for (const [k, v] of Object.entries(fields)) form.append(k, v);
  form.append("file", new Blob([fs.readFileSync(file)]), path.basename(file));
  return form;
};

async function uploadFiles(ns, dir) {
  if (!fs.existsSync(dir)) return;
  const walk = (d) =>
    fs
      .readdirSync(d, { withFileTypes: true })
      .flatMap((e) =>
        e.isDirectory() ? walk(path.join(d, e.name)) : [path.join(d, e.name)],
      );
  for (const file of walk(dir).sort()) {
    const rel = path.relative(dir, file);
    await must(
      `upload ${rel}`,
      api("POST", `/files/${ns}/`, undefined, fileForm(file, { path: rel })),
      (s) => s === 200 || s === 201,
    );
  }
}

// ---------- the UI ----------
async function openGraph(page, ns) {
  await page.goto(`${UI}/${ns}`, { waitUntil: "networkidle2", timeout: 60000 });
  const login = await page.$("#login-password");
  if (login) {
    await login.type(PASSWORD);
    await page.click(".login-btn");
    await page.waitForSelector(".react-flow", { timeout: 60000 });
  }
  await page.waitForSelector(".react-flow__node", { timeout: 60000 });
  // The layout lands after the pods: wait until nothing moves any more.
  const snapshot = () =>
    page.$$eval(".react-flow__node", (els) =>
      els.map((e) => e.style.transform).join("|"),
    );
  let last = await snapshot();
  for (let i = 0; i < 20; i++) {
    await sleep(1000);
    const now = await snapshot();
    if (now === last && i >= 2) break;
    last = now;
  }
}

// A click from inside the page, by selector and text, so an overlay or a
// hidden input never gets in the way; waits until the element is enabled.
async function clickByText(page, selector, text, timeout = 60000) {
  await page.waitForFunction(
    (sel, txt) => {
      const el = [...document.querySelectorAll(sel)].find((e) =>
        (e.textContent || "").includes(txt),
      );
      return !!el && !el.disabled;
    },
    { timeout },
    selector,
    text,
  );
  await page.evaluate(
    (sel, txt) =>
      [...document.querySelectorAll(sel)]
        .find((e) => (e.textContent || "").includes(txt))
        .click(),
    selector,
    text,
  );
}

async function exportPicture(page, cdp, downloads, ns, target) {
  for (const f of fs.readdirSync(downloads))
    fs.unlinkSync(path.join(downloads, f));
  await clickByText(page, ".topbar-btn", "Export");
  await clickByText(page, ".topbar-menu-item", "Image", 5000);
  await page.waitForSelector('input[name="image-format"]', { timeout: 5000 });
  await page.$eval('input[name="image-format"]', (e) => e.click());
  await page.$$eval('.alert-modal-extra input[type="checkbox"]', (els) =>
    els.forEach((e) => e.checked && e.click()),
  );
  const margin = await page.$('.alert-modal-field input[type="number"]');
  await margin.click({ clickCount: 3 });
  await margin.type(MARGIN);
  await page.select(".alert-modal-field select", SCALE);
  const done = new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error("download timed out")),
      120000,
    );
    cdp.on("Browser.downloadProgress", (e) => {
      if (e.state === "completed") {
        clearTimeout(timer);
        resolve();
      } else if (e.state === "canceled") {
        clearTimeout(timer);
        reject(new Error("download canceled"));
      }
    });
  });
  await clickByText(
    page,
    ".alert-modal-btn:not(.alert-modal-btn-cancel)",
    "Export",
    5000,
  );
  await done;
  await sleep(500);
  const file = fs.readdirSync(downloads).find((f) => f.endsWith(".png"));
  if (!file) throw new Error(`no png downloaded for ${ns}`);
  fs.mkdirSync(OUT, { recursive: true });
  fs.copyFileSync(path.join(downloads, file), target);
  log(`  ${target} (${(fs.statSync(target).size / 1024).toFixed(0)} KiB)`);
}

// ---------- one example ----------
async function pictureOf(browser, ex, keep) {
  const dir = path.join(EXAMPLES, ex.name);
  log(`${ex.name} in namespace ${ex.ns}`);
  const existing = await must("list namespaces", api("GET", "/namespaces/"));
  if ((existing.namespaces || []).some((n) => n.name === ex.ns))
    throw new Error(`namespace ${ex.ns} already exists, delete it first`);
  await must(
    "create namespace",
    api("POST", "/namespaces/", { namespace: ex.ns }),
  );
  try {
    await uploadFiles(ex.ns, path.join(dir, "files"));
    if (ex.zip)
      await must(
        `import ${ex.zip}`,
        api(
          "POST",
          `/file-ops/${ex.ns}/import`,
          undefined,
          fileForm(path.join(dir, ex.zip)),
        ),
      );
    const spec = JSON.parse(
      fs.readFileSync(path.join(dir, ex.topology), "utf8"),
    );
    log("  deploying");
    await must("deploy", api("POST", `/network/deploy-network/${ex.ns}`, spec));
    const downloads = fs.mkdtempSync(
      path.join(os.tmpdir(), "kubendt-pictures-"),
    );
    const page = await browser.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    const cdp = await page.createCDPSession();
    await cdp.send("Browser.setDownloadBehavior", {
      behavior: "allow",
      downloadPath: downloads,
      eventsEnabled: true,
    });
    try {
      for (const [file, steps] of ex.pictures) {
        for (const step of steps) {
          log(`  applying ${step}`);
          const body = JSON.parse(
            fs.readFileSync(path.join(dir, step), "utf8"),
          );
          await must(
            step,
            api("POST", `/network/modify-network/${ex.ns}`, body),
          );
        }
        try {
          await openGraph(page, ex.ns);
          await exportPicture(
            page,
            cdp,
            downloads,
            ex.ns,
            path.join(OUT, `${file}.png`),
          );
        } catch (err) {
          // What the page looked like when it went wrong, next to the picture it owed.
          const shot = path.join(OUT, `${file}-failed.png`);
          await page.screenshot({ path: shot }).catch(() => {});
          throw new Error(
            `${err.message} (screen in ${shot}${errors.length ? `, page errors: ${errors.slice(0, 3).join(" | ")}` : ""})`,
          );
        }
      }
    } finally {
      await page.close();
      fs.rmSync(downloads, { recursive: true, force: true });
    }
  } finally {
    if (keep) log(`  namespace ${ex.ns} kept`);
    else await must("delete namespace", api("DELETE", `/namespaces/${ex.ns}`));
  }
}

(async () => {
  const args = process.argv.slice(2);
  const keep = args.includes("--keep");
  const wanted = args.filter((a) => !a.startsWith("--"));
  const rows = wanted.length
    ? TABLE.filter((ex) => wanted.includes(ex.name))
    : TABLE;
  const unknown = wanted.filter((w) => !TABLE.some((ex) => ex.name === w));
  if (unknown.length)
    throw new Error(`unknown examples: ${unknown.join(", ")}`);
  await login();
  const browser = await puppeteer.launch({
    executablePath: CHROME,
    headless: true,
    args: ["--no-sandbox", "--disable-gpu", "--window-size=1600,1000"],
    defaultViewport: { width: 1600, height: 1000 },
  });
  let failed = 0;
  try {
    for (const ex of rows) {
      try {
        await pictureOf(browser, ex, keep);
      } catch (err) {
        failed++;
        log(`  ${ex.name} failed: ${err.message}`);
      }
    }
  } finally {
    await browser.close();
  }
  process.exit(failed ? 1 : 0);
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
