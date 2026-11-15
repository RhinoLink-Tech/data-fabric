// Regression test: out-of-order list responses must not overwrite the latest page.
// Run with: node --test scripts/ui/stale-list.test.js
"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const appSource = fs.readFileSync(path.join(__dirname, "..", "..", "cmd", "server", "ui", "app.js"), "utf8");

// Elements extend FakeNode so app.js's `instanceof Node` checks treat them as DOM nodes.
class FakeNode {}

function makeElement(id) {
  const element = Object.create(FakeNode.prototype);
  return Object.assign(element, {
    id, value: "", textContent: "", className: "", hidden: false, disabled: false, type: "",
    children: [], listeners: {},
    addEventListener(type, handler) { (this.listeners[type] = this.listeners[type] || []).push(handler); },
    appendChild(child) { this.children.push(child); return child; },
    replaceChildren(...children) { this.children = children; },
    setAttribute() {}, removeAttribute() {}, focus() {}, select() {}, close() {}, showModal() {},
    classList: { add() {}, remove() {}, toggle() {} },
  });
}

// Data-source list requests are held until the test resolves them, so completion order is controlled.
async function loadApp(total) {
  const elements = new Map();
  const $ = (id) => { if (!elements.has(id)) elements.set(id, makeElement(id)); return elements.get(id); };
  const pending = [];
  const jsonResponse = (body) => ({ ok: true, status: 200, headers: { get() { return null; } }, json: async () => body });
  const fetch = (url) => {
    const parsed = new URL(url, "http://localhost");
    if (parsed.pathname === "/api/v1/database/types") return Promise.resolve(jsonResponse([]));
    const offset = Number(parsed.searchParams.get("offset"));
    const limit = Number(parsed.searchParams.get("limit"));
    return new Promise((resolve, reject) => {
      pending.push({
        offset,
        respond() {
          const rows = [];
          for (let i = offset; i < Math.min(total, offset + limit); i++) {
            rows.push({ id: "src-" + i, name: "source " + i, type: "mysql", status: "active", config: { host: "db" } });
          }
          resolve(jsonResponse({ dataSources: rows, total }));
        },
        fail() { reject(new Error("network down")); },
      });
    });
  };
  const context = {
    document: { getElementById: $, createElement: () => makeElement("") },
    window: { addEventListener() {} }, fetch, URL, Node: FakeNode, console, setTimeout, Intl,
  };
  vm.createContext(context);
  vm.runInContext(appSource, context);
  const flush = async () => { for (let i = 0; i < 20; i++) await new Promise((resolve) => setTimeout(resolve, 0)); };
  const click = async (id) => {
    for (const handler of $(id).listeners.click || []) await handler();
    await flush();
  };
  // Takes the nth (in request order) pending list request for an offset out of the queue.
  const take = (offset, nth = 0) => {
    const matches = pending.filter((request) => request.offset === offset);
    assert.ok(matches[nth], "no pending list request #" + nth + " for offset " + offset);
    pending.splice(pending.indexOf(matches[nth]), 1);
    return matches[nth];
  };
  const respond = async (offset, nth = 0) => { take(offset, nth).respond(); await flush(); };
  const fail = async (offset, nth = 0) => { take(offset, nth).fail(); await flush(); };
  const firstName = (app) => app.$("sourceRows").children[0].children[0].children[0].textContent;
  return { $, click, respond, fail, firstName, flush, pending };
}

test("a slower earlier page response does not overwrite the latest page", async () => {
  const app = await loadApp(45);
  await app.respond(0);
  await app.click("nextPage"); // requests offset 20
  await app.click("nextPage"); // requests offset 40 before offset 20 completes
  await app.respond(40);
  await app.respond(20);
  assert.equal(app.$("pageSummary").textContent, "第 3 页 · 共 45 个");
  assert.equal(app.firstName(app), "source 40");
});

test("an earlier failure arriving after a newer success does not show a load error", async () => {
  const app = await loadApp(45);
  await app.respond(0);
  await app.click("nextPage"); // offset 20
  await app.click("nextPage"); // offset 40
  await app.respond(40);
  await app.fail(20);
  assert.equal(app.$("loadError").hidden, true);
  assert.equal(app.$("pageSummary").textContent, "第 3 页 · 共 45 个");
  assert.equal(app.firstName(app), "source 40");
});

test("requests started before clearing the token cannot overwrite the reload", async () => {
  const app = await loadApp(45);
  await app.respond(0);
  await app.click("nextPage"); // offset 20, becomes stale
  await app.click("previousPage"); // offset 0
  await app.click("clearToken"); // offset 0, newest request
  await app.respond(0, 1); // newest request (after clear) completes first
  await app.respond(0, 0); // the earlier offset-0 request completes next
  await app.respond(20); // the stale offset-20 request completes last
  assert.equal(app.$("pageSummary").textContent, "第 1 页 · 共 45 个");
  assert.equal(app.firstName(app), "source 0");
});
