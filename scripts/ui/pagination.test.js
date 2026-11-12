// Regression test for the source list pagination in cmd/server/ui/app.js.
// Run with: node --test scripts/ui/pagination.test.js
"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const appSource = fs.readFileSync(path.join(__dirname, "..", "..", "cmd", "server", "ui", "app.js"), "utf8");

function makeElement(id) {
  return {
    id,
    value: "",
    textContent: "",
    className: "",
    hidden: false,
    disabled: false,
    type: "",
    children: [],
    listeners: {},
    addEventListener(type, handler) { (this.listeners[type] = this.listeners[type] || []).push(handler); },
    appendChild(child) { this.children.push(child); return child; },
    replaceChildren(...children) { this.children = children; },
    setAttribute() {},
    removeAttribute() {},
    focus() {},
    select() {},
    close() {},
    showModal() {},
    classList: { add() {}, remove() {}, toggle() {} },
  };
}

// Loads app.js against a fake DOM and a fake same-origin API holding `total` sources.
async function loadApp(total) {
  const elements = new Map();
  const $ = (id) => { if (!elements.has(id)) elements.set(id, makeElement(id)); return elements.get(id); };
  const requests = [];
  let sourceCount = total;
  const fetch = async (url) => {
    requests.push(url);
    const parsed = new URL(url, "http://localhost");
    let body;
    if (parsed.pathname === "/api/v1/database/types") {
      body = [];
    } else {
      const limit = Number(parsed.searchParams.get("limit"));
      const offset = Number(parsed.searchParams.get("offset"));
      const rows = [];
      for (let i = offset; i < Math.min(sourceCount, offset + limit); i++) {
        rows.push({ id: "src-" + i, name: "source " + i, type: "mysql", status: "active", config: { host: "db" } });
      }
      body = { dataSources: rows, total: sourceCount };
    }
    return { ok: true, status: 200, headers: { get() { return null; } }, json: async () => body };
  };
  const document = {
    getElementById: $,
    createElement: () => makeElement(""),
  };
  class Node {}
  const window = { addEventListener() {} };
  const context = { document, window, fetch, URL, Node, console, setTimeout, Intl };
  vm.createContext(context);
  vm.runInContext(appSource, context);
  const flush = async () => { for (let i = 0; i < 20; i++) await new Promise((resolve) => setTimeout(resolve, 0)); };
  await flush();
  const click = async (id) => {
    for (const handler of $(id).listeners.click || []) await handler();
    await flush();
  };
  return { $, requests, click, flush, setTotal: (n) => { sourceCount = n; } };
}

test("refresh on a page emptied by a shrink loads the last valid page", async () => {
  const app = await loadApp(21);
  await app.click("nextPage");
  assert.equal(app.$("pageSummary").textContent, "第 2 页 · 共 21 个");
  app.setTotal(20);
  await app.click("refreshButton");
  assert.equal(app.$("emptyState").hidden, true, "empty state shown: offset stuck at 20 after the list shrank");
  assert.equal(app.$("pageSummary").textContent, "第 1 页 · 共 20 个");
  assert.equal(app.$("sourceRows").children.length, 20);
  assert.equal(app.requests[app.requests.length - 1], "/api/v1/datasources?limit=20&offset=0");
});

test("a list spanning several pages keeps normal navigation", async () => {
  const app = await loadApp(45);
  await app.click("nextPage");
  await app.click("nextPage");
  assert.equal(app.$("pageSummary").textContent, "第 3 页 · 共 45 个");
  assert.equal(app.$("sourceRows").children.length, 5);
  await app.click("previousPage");
  assert.equal(app.$("pageSummary").textContent, "第 2 页 · 共 45 个");
  assert.equal(app.$("sourceRows").children.length, 20);
});

test("a truly empty list still shows the empty state", async () => {
  const app = await loadApp(0);
  assert.equal(app.$("emptyState").hidden, false);
  assert.equal(app.$("tableWrap").hidden, true);
  assert.equal(app.requests.length, 2);
  assert.equal(app.requests[1], "/api/v1/datasources?limit=20&offset=0");
});
