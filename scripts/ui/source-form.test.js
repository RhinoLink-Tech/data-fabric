// Run with: node --test scripts/ui/*.test.js
"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const appSource = fs.readFileSync(path.join(__dirname, "../../cmd/server/ui/app.js"), "utf8");

class Element {
  constructor(id = "") {
    this.id = id;
    this.value = "";
    this.textContent = "";
    this.className = "";
    this.hidden = false;
    this.disabled = false;
    this.checked = false;
    this.children = [];
    this.listeners = {};
    this.style = {};
  }
  addEventListener(type, handler) { (this.listeners[type] ||= []).push(handler); }
  dispatchEvent(event) { for (const handler of this.listeners[event.type] || []) handler(event); return true; }
  appendChild(child) { this.children.push(child); return child; }
  replaceChildren(...children) { this.children = children; }
  setAttribute() {}
  focus() {}
  select() {}
  close() { this.dispatchEvent({ type: "close" }); }
  showModal() {}
  reportValidity() { return true; }
}

async function loadApp(existing = []) {
  const elements = new Map();
  const $ = (id) => { if (!elements.has(id)) elements.set(id, new Element(id)); return elements.get(id); };
  $("sourceForm").reset = () => {
    for (const id of ["sourceName", "sourceType", "sourceHost", "sourcePort", "sourceDatabase", "sourceSchema",
      "sourceUsername", "sourcePassword", "sourceSSLMode", "sourceTimeout", "sourceSSLRootCert",
      "sourceMaxPoolSize", "sourceIdleTimeout", "sourceMaxLifetime", "sourceTimezone"]) $(id).value = "";
    $("sourceSSL").checked = false;
  };
  const calls = [];
  const sources = existing.map((source) => structuredClone(source));
  let connectionResult = { status: "healthy", message: "Connection successful" };
  const jsonResponse = (body, status = 200) => ({
    ok: status >= 200 && status < 300,
    status,
    headers: { get() { return null; } },
    json: async () => body,
  });
  const fetch = async (url, options = {}) => {
    const request = { url, method: options.method || "GET", body: options.body ? JSON.parse(options.body) : undefined };
    calls.push(request);
    const route = new URL(url, "http://localhost");
    if (route.pathname === "/api/v1/database/types") return jsonResponse({ data: [
      { type: "mysql", driverName: "MySQL", registered: true, supported: true, recommended: true, defaultPort: 3306 },
      { type: "postgresql", driverName: "PostgreSQL", registered: true, supported: true, defaultPort: 5432 },
      { type: "file", driverName: "File", registered: true, supported: true },
    ] });
    if (route.pathname === "/api/v1/database/test-connection") return jsonResponse({ data: connectionResult });
    if (route.pathname === "/api/v1/datasources" && request.method === "POST") {
      if (!request.body.type) return jsonResponse({ success: false, error: { code: "VALIDATION_ERROR", message: "type is required" } }, 422);
      const source = { id: "new-source", status: "active", ...request.body };
      sources.push(source);
      return jsonResponse({ data: source }, 201);
    }
    if (route.pathname === "/api/v1/datasources" && request.method === "GET") {
      return jsonResponse({ data: { dataSources: sources, total: sources.length } });
    }
    const match = route.pathname.match(/^\/api\/v1\/datasources\/([^/]+)$/);
    if (match) {
      const source = sources.find((item) => item.id === decodeURIComponent(match[1]));
      if (!source) return jsonResponse({ success: false, error: { message: "not found" } }, 404);
      if (request.method === "PUT") {
        Object.assign(source, request.body);
        return jsonResponse({ data: source });
      }
      return jsonResponse({ data: source });
    }
    throw new Error("Unexpected request: " + request.method + " " + url);
  };
  const document = { getElementById: $, createElement: () => new Element(), body: new Element() };
  const context = { document, window: { addEventListener() {} }, fetch, URL, Node: Element, console, Intl,
    Event: class Event { constructor(type) { this.type = type; } } };
  vm.createContext(context);
  vm.runInContext(appSource, context);
  const flush = async () => { for (let i = 0; i < 5; i++) await new Promise((resolve) => setImmediate(resolve)); };
  const emitElement = async (el, type) => {
    for (const handler of el.listeners[type] || []) await handler({ type, preventDefault() {} });
    await flush();
  };
  await flush();
  return { $, calls, emit: (id, type) => emitElement($(id), type), emitElement,
    setResult: (result) => { connectionResult = result; } };
}

test("creating a source sends its selected type and refreshes the list", async () => {
  const app = await loadApp();
  await app.emit("addButton", "click");
  app.$("sourceType").value = "postgresql";
  await app.emit("sourceType", "change");
  app.$("sourceName").value = "orders";
  app.$("sourceHost").value = "db";
  app.$("sourceDatabase").value = "orders";
  await app.emit("sourceForm", "submit");
  const call = app.calls.find((item) => item.method === "POST" && item.url === "/api/v1/datasources");
  assert.equal(call.body.name, "orders");
  assert.equal(call.body.type, "postgresql");
  assert.equal(call.body.config.port, 5432);
  assert.equal(app.$("sourceRows").children.length, 1);
  assert.match(app.$("notice").textContent, /已创建/);
});

test("an empty connector selection gives an error and does not create a source", async () => {
  const app = await loadApp();
  await app.emit("addButton", "click");
  app.$("sourceType").value = "";
  app.$("sourceName").value = "orders";
  await app.emit("sourceForm", "submit");
  assert.equal(app.calls.some((item) => item.method === "POST" && item.url === "/api/v1/datasources"), false);
  assert.match(app.$("formError").textContent, /选择连接器/);
});

test("editing keeps the existing type and does not overwrite a saved password", async () => {
  const app = await loadApp([{ id: "saved", name: "old", type: "mysql", status: "active",
    config: { host: "db", port: 3307, database: "orders", additionalProps: {} } }]);
  const editButton = app.$("sourceRows").children[0].children[5].children[0].children[0];
  await app.emitElement(editButton, "click");
  assert.equal(app.$("sourceType").disabled, true);
  assert.equal(app.$("sourcePort").value, 3307);
  app.$("sourceName").value = "renamed";
  await app.emit("sourceForm", "submit");
  const call = app.calls.find((item) => item.method === "PUT");
  assert.equal(call.url, "/api/v1/datasources/saved");
  assert.equal(call.body.name, "renamed");
  assert.equal(Object.hasOwn(call.body, "type"), false);
  assert.equal(Object.hasOwn(call.body.config, "password"), false);
  assert.equal(call.body.config.port, 3307);
});

test("only healthy connection results are successful, and details are redacted", async () => {
  const app = await loadApp();
  await app.emit("addButton", "click");
  app.$("sourceUsername").value = "sample-user";
  app.$("sourcePassword").value = "sample-secret";
  for (const result of [
    { status: "unhealthy", message: "sample-user cannot connect with sample-secret" },
    { status: "error", message: "sample-secret failed" },
    { status: "unexpected", message: "unknown" },
    {},
    null,
  ]) {
    app.setResult(result);
    await app.emit("testButton", "click");
    assert.match(app.$("testResult").className, /error/);
    assert.doesNotMatch(app.$("testResult").textContent, /sample-user|sample-secret/);
  }
  app.setResult({ status: "healthy", message: "Connection successful" });
  await app.emit("testButton", "click");
  assert.match(app.$("testResult").className, /success/);
  assert.match(app.$("testResult").textContent, /连接测试成功/);
});

test("switching connectors updates automatic ports but preserves deliberate edits", async () => {
  const app = await loadApp();
  await app.emit("addButton", "click");
  assert.equal(String(app.$("sourcePort").value), "3306");
  app.$("sourceType").value = "postgresql";
  await app.emit("sourceType", "change");
  assert.equal(String(app.$("sourcePort").value), "5432");
  app.$("sourceType").value = "file";
  await app.emit("sourceType", "change");
  assert.equal(app.$("sourcePort").value, "");
  app.$("sourceType").value = "mysql";
  await app.emit("sourceType", "change");
  app.$("sourcePort").value = "3306";
  await app.emit("sourcePort", "input");
  app.$("sourceType").value = "postgresql";
  await app.emit("sourceType", "change");
  assert.equal(String(app.$("sourcePort").value), "3306", "an explicitly entered default is still a custom value");
  app.$("sourcePort").value = "6543";
  await app.emit("sourcePort", "input");
  app.$("sourceType").value = "mysql";
  await app.emit("sourceType", "change");
  assert.equal(app.$("sourcePort").value, "6543");
});
