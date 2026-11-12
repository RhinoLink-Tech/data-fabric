#!/usr/bin/env node
// Exercise the UI served by the selected release image against its synthetic source.
// Node 22+ supplies fetch and WebSocket; Chrome is launched with an isolated profile.
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";

const origin = new URL(process.env.DF_UI_BASE_URL || "http://127.0.0.1:8080");
if (origin.protocol !== "http:" || !["127.0.0.1", "localhost", "[::1]"].includes(origin.hostname)) {
  throw new Error("UI acceptance requires an HTTP loopback origin");
}
const token = process.env.DF_UI_BEARER_TOKEN;
const password = process.env.DF_UI_SAMPLE_PASSWORD;
const browserPath = process.env.DF_UI_BROWSER || "google-chrome";
if (!token || !password) throw new Error("Set the temporary management token and synthetic sample password");

const profile = await mkdtemp(path.join(tmpdir(), "df-ui-accept-"));
let browser, socket;
const pending = new Map();
let nextId = 0;

async function until(description, check, timeoutMs = 45_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const result = await check();
    if (result) return result;
    await delay(200);
  }
  throw new Error(`Timed out waiting for ${description}`);
}

function command(method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = ++nextId;
    const timeout = setTimeout(() => { pending.delete(id); reject(new Error("Browser protocol timed out")); }, 15_000);
    pending.set(id, { resolve, reject, timeout });
    socket.send(JSON.stringify({ id, method, params }));
  });
}

async function evaluate(expression) {
  const result = await command("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error("Browser UI evaluation failed");
  return result.result.value;
}

try {
  browser = spawn(browserPath, ["--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
    "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank"],
    { stdio: "ignore", windowsHide: true });
  let launchError = false;
  browser.on("error", () => { launchError = true; });
  const port = await until("the isolated browser", async () => {
    if (launchError || browser.exitCode !== null) throw new Error("Could not start the browser");
    try { return Number((await readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]) || false; }
    catch { return false; }
  }, 20_000);
  const target = await until("a browser page", async () => {
    try {
      const pages = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
      return pages.find((entry) => entry.type === "page" && entry.webSocketDebuggerUrl) || false;
    } catch { return false; }
  });
  socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("Could not connect to the browser")), { once: true });
  });
  socket.addEventListener("message", (event) => {
    const msg = JSON.parse(event.data);
    const item = pending.get(msg.id);
    if (!item) return;
    pending.delete(msg.id); clearTimeout(item.timeout);
    if (msg.error) item.reject(new Error("Browser protocol command failed"));
    else item.resolve(msg.result);
  });
  await command("Page.enable");
  await command("Runtime.enable");
  // Observe only status and connector type; never retain a request body or credential.
  await command("Page.addScriptToEvaluateOnNewDocument", { source: `(() => {
    const original = window.fetch;
    window.__dfAcceptance = { probes: [], createType: null };
    window.fetch = async function(input, init) {
      const url = typeof input === "string" ? input : input.url;
      const method = String(init && init.method || "GET").toUpperCase();
      if (method === "POST" && new URL(url, location.href).pathname === "/api/v1/datasources") {
        try { window.__dfAcceptance.createType = JSON.parse(init.body).type || ""; } catch (_) {}
      }
      const response = await original.apply(this, arguments);
      if (method === "POST" && new URL(url, location.href).pathname === "/api/v1/database/test-connection") {
        response.clone().json().then((body) => {
          const data = body && Object.prototype.hasOwnProperty.call(body, "data") ? body.data : body;
          window.__dfAcceptance.probes.push({ http: response.status, status: data && data.status });
        }).catch(() => { window.__dfAcceptance.probes.push({ http: response.status, status: "" }); });
      }
      return response;
    };
  })();` });
  await command("Page.navigate", { url: new URL("/ui/", origin).href });
  await until("the management UI", () => evaluate("document.readyState === 'complete' && !!document.querySelector('#addButton')"));
  await evaluate(`document.querySelector('#bearerToken').value = ${JSON.stringify(token)}; document.querySelector('#refreshButton').click();`);
  await until("the create form", () => evaluate("document.querySelector('#addButton').click(); document.querySelector('#editorDialog').open"));
  const sourceName = `ui-release-smoke-${Date.now()}`;
  await evaluate(`(() => {
    const set = (id, value) => { document.querySelector('#' + id).value = value; };
    set('sourceName', ${JSON.stringify(sourceName)});
    set('sourceType', 'mysql'); document.querySelector('#sourceType').dispatchEvent(new Event('change'));
    set('sourceHost', 'sample-source'); set('sourcePort', '1'); set('sourceDatabase', 'sample');
    set('sourceUsername', 'sample_reader'); set('sourcePassword', ${JSON.stringify(password)}); set('sourceTimeout', '3');
    document.querySelector('#testButton').click();
  })()`);
  await until("the unhealthy connection result", () => evaluate("window.__dfAcceptance.probes.length >= 1 && !document.querySelector('#testResult').hidden && !document.querySelector('#testButton').disabled"));
  if (!await evaluate("window.__dfAcceptance.probes[0].http === 200 && window.__dfAcceptance.probes[0].status === 'unhealthy' && document.querySelector('#testResult').classList.contains('error')")) {
    throw new Error("The unhealthy HTTP 200 probe was not shown as a failure");
  }
  await evaluate("document.querySelector('#sourcePort').value = '3306'; document.querySelector('#testButton').click()");
  await until("the healthy connection result", () => evaluate("window.__dfAcceptance.probes.length >= 2 && !document.querySelector('#testResult').hidden && !document.querySelector('#testButton').disabled"));
  if (!await evaluate("window.__dfAcceptance.probes[1].http === 200 && window.__dfAcceptance.probes[1].status === 'healthy' && document.querySelector('#testResult').classList.contains('success')")) {
    throw new Error("The healthy probe was not shown as a success");
  }
  await evaluate("document.querySelector('#sourceForm').requestSubmit()");
  await until("the saved data source in the refreshed list", () => evaluate(`document.querySelector('#sourceRows').textContent.includes(${JSON.stringify(sourceName)})`));
  if (!await evaluate("window.__dfAcceptance.createType === 'mysql'")) throw new Error("The create request omitted the selected connector type");
  console.log("PASS: synthetic MySQL unhealthy/healthy feedback, connector type, save and refreshed UI list");
} catch (error) {
  console.error(`FAIL: ${error.message}`);
  process.exitCode = 1;
} finally {
  if (socket && socket.readyState === WebSocket.OPEN) {
    try { socket.send(JSON.stringify({ id: ++nextId, method: "Browser.close" })); } catch {}
    socket.close();
  }
  if (browser && browser.exitCode === null) browser.kill();
  if (browser && browser.exitCode === null) await Promise.race([new Promise((resolve) => browser.once("exit", resolve)), delay(3000)]);
  const resolvedProfile = path.resolve(profile);
  if (path.dirname(resolvedProfile) === path.resolve(tmpdir()) && path.basename(resolvedProfile).startsWith("df-ui-accept-")) {
    await rm(resolvedProfile, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 }).catch(() => {});
  }
}
