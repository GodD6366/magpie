// Real UI, isolated API fixtures: save, order, clear and cancel model sequences.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = process.env.MAGPIE_MODEL_ASSETS || path.resolve(__dirname, "../assets");
const options = [{ value: "gpt-5.4", label: "GPT-5.4" }, { value: "relay/m1", label: "m1", ref: "relay/m1", note: "Relay · via magpie" }];
const fresh = () => ({
  agents: [
    { id: "codex", name: "Codex", icon: "generic", path: "/fixture/codex", fields: [{ key: "model", label: "model", value: "gpt-5.4", options }] },
    { id: "openclaw", name: "OpenClaw", icon: "openclaw-color", path: "/fixture/openclaw/openclaw.json", fields: [{ key: "model", label: "model", value: "gpt-5.4", options }] },
    { id: "cursor", name: "Cursor", icon: "generic", path: "/fixture/cursor", fields: [{ key: "model", label: "model", value: "auto", options: [{ value: "auto", label: "Auto" }] }] },
  ],
  profiles: [],
});

function server(lang, posts) {
  let cur = fresh();
  cur.agents = [cur.agents[1]];
  Object.assign(cur.agents[0], {wired:true, modelChain:{primary:"magpie/group/auto",fallbacks:[]}});
  cur.agents[0].fields[0].value="magpie/group/auto";
  const reply = (route) => route.fulfill({ json: { ...cur, settings: { lang, theme: "light", guideHidden: true } } });
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/agents/model-chain/openclaw") {
      const c=req.postDataJSON(); posts.push(c); cur.agents[0].modelChain=c; cur.agents[0].fields[0].value=c.primary; return reply(route);
    }
    if (url.pathname === "/api/state") return reply(route);
    const act = url.pathname.match(/^\/api\/agents\/(connect|disconnect)\/(\w+)$/);
    if (act) {
      posts.push(url.pathname);
      cur = JSON.parse(JSON.stringify(cur));
      const a = cur.agents.find((x) => x.id === act[2]);
      if (act[1] === "connect") { a.wired = true; a.fields[0].value = "relay/m1"; }
      else { delete a.wired; a.fields[0].value = "gpt-5.4"; }
      return reply(route);
    }
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/agents/cli") return json({ agents: {}, pending: false });
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}


for (const engine of ["chromium", "webkit"]) for (const lang of ["en","zh","zh-TW","ja","de"]) {
 test(`${engine} ${lang}: OpenClaw primary and ordered fallbacks`, async t => {
  const browser=await (engine==="webkit"?webkit.launch():chromium.launch({channel:"chromium"}));
  t.after(()=>browser.close());
  const page=await browser.newPage({viewport:{width:440,height:700},reducedMotion:"reduce"});
  page.setDefaultTimeout(5000);
  const posts=[],errors=[]; page.on("pageerror",e=>errors.push(e.message));
  await page.route("**/*",server(lang,posts));
  await page.goto("http://magpie.test/?view=agents");
  await page.locator('.row.agent[data-id="openclaw"] .ag-link').click();
  await page.locator('.model-chain-edit').click();
  assert.deepEqual(errors,[]);
  const ed=page.locator('.model-chain-editor');
  await ed.locator('.ebody > input').fill('magpie/group/auto');
  const add=ed.locator('.model-chain-add');
  await add.click(); await ed.locator('.fbrow input').nth(0).fill('magpie/group/fast');
  await add.click(); await ed.locator('.fbrow input').nth(1).fill('anthropic/claude-opus-5');
  await ed.locator('.fbrow').nth(1).locator('button').first().click();
  assert.deepEqual(await ed.locator('.fbrow input').evaluateAll(es=>es.map(e=>e.value)),['anthropic/claude-opus-5','magpie/group/fast']);
  await ed.locator('.fbrow').nth(1).locator('button').first().click();
  assert.equal(posts.length,0,'draft is not live');
  if (process.env.ARTIFACT_DIR) { await fs.mkdir(process.env.ARTIFACT_DIR,{recursive:true}); await page.screenshot({path:path.join(process.env.ARTIFACT_DIR,`openclaw-${engine}-${lang}.png`)}); }
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  await ed.locator('.bar .primary').click();
  await ed.waitFor({state:'detached'});
  assert.deepEqual(posts,[{primary:'magpie/group/auto',fallbacks:['magpie/group/fast','anthropic/claude-opus-5']}]);
  await page.locator('.model-chain-edit').click();
  assert.equal(await ed.locator('.fbrow input').nth(1).inputValue(),'anthropic/claude-opus-5');
  await ed.locator('.ebody > input').fill('bad'); await ed.locator('.bar .primary').click();
  assert.ok(await ed.locator('[role=alert]').textContent()); assert.equal(posts.length,1);
  await ed.locator('.bar button').first().click();
  await page.locator('.model-chain-edit').click();
  while(await ed.locator('.fbrow').count()) await ed.locator('.fbrow').first().locator('button').last().click();
  await ed.locator('.bar .primary').click(); await ed.waitFor({state:'detached'});
  assert.deepEqual(posts.at(-1).fallbacks,[]); assert.deepEqual(errors,[]);
 });
}
