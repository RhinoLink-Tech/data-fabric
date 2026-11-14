(() => {
  "use strict";
  const pageSize = 20;
  const state = { catalog: [], sources: [], total: 0, offset: 0, editingID: "", currentConfig: null, portManuallyEdited: false, testRun: 0 };
  const $ = (id) => document.getElementById(id);
  const tokenInput = $("bearerToken");
  const rows = $("sourceRows");
  const dialog = $("editorDialog");
  const form = $("sourceForm");

  class ApiError extends Error {
    constructor(message, code, correlationID, status) {
      super(message); this.name = "ApiError"; this.code = code || ""; this.correlationID = correlationID || ""; this.status = status || 0;
    }
  }

  function secretValues(config) {
    const values = [];
    if (tokenInput.value.trim()) values.push(tokenInput.value.trim());
    if (config && config.password) values.push(String(config.password));
    if (config && config.username) values.push(String(config.username));
    const sensitive = /password|passwd|token|secret|credential|authorization|private.?key|access.?key|api.?key|signature|sig|sas/i;
    if (config && config.host) {
      try {
        const url = new URL(config.host);
        if (url.username) values.push(decodeURIComponent(url.username));
        if (url.password) values.push(decodeURIComponent(url.password));
        if (url.hash) values.push(url.hash.slice(1));
        url.searchParams.forEach((value, key) => { if (sensitive.test(key) && value) values.push(value); });
      } catch (_) {}
    }
    const visit = (value, key) => {
      if (key && sensitive.test(key) && (typeof value === "string" || typeof value === "number") && String(value)) values.push(String(value));
      if (value && typeof value === "object") Object.keys(value).forEach((child) => visit(value[child], child));
    };
    if (config && config.additionalProps) visit(config.additionalProps, "");
    return [...new Set(values)].sort((a, b) => b.length - a.length);
  }
  function redact(message, config) {
    let safe = String(message || "");
    secretValues(config).forEach((secret) => { safe = safe.split(secret).join("[已隐藏]"); });
    return safe;
  }
  function errorText(error) {
    if (!(error instanceof ApiError)) return error && error.message ? String(error.message) : "请求失败，请检查服务状态后重试。";
    const parts = [];
    if (error.code) parts.push(error.code);
    parts.push(error.message || ("请求失败（HTTP " + error.status + "）"));
    if (error.correlationID) parts.push("请求 ID：" + error.correlationID);
    return redact(parts.join(" · "), state.currentConfig);
  }
  async function api(path, options = {}) {
    const headers = { Accept: "application/json" };
    const request = { method: options.method || "GET", headers, cache: "no-store", credentials: "same-origin" };
    if (options.body !== undefined) { headers["Content-Type"] = "application/json"; request.body = JSON.stringify(options.body); }
    const token = tokenInput.value.trim();
    if (options.auth !== false && token) headers.Authorization = "Bearer " + token;
    let response;
    try { response = await fetch(path, request); }
    catch (_) { throw new ApiError("无法连接到同源 Data Fabric API。请确认服务运行状态和当前页面地址。", "", "", 0); }
    let payload = null;
    try { payload = await response.json(); } catch (_) { payload = null; }
    if (!response.ok || (payload && payload.success === false)) {
      const error = payload && payload.error;
      const message = (error && typeof error === "object" && error.message) || (typeof error === "string" && error) || (payload && payload.message) || ("请求失败（HTTP " + response.status + "）");
      throw new ApiError(redact(message, options.sanitizeWith), error && typeof error === "object" ? error.code : "", (payload && payload.correlationId) || response.headers.get("X-Correlation-ID"), response.status);
    }
    return payload && Object.prototype.hasOwnProperty.call(payload, "data") ? payload.data : payload;
  }
  function showNotice(message, kind = "") {
    const box = $("notice"); box.textContent = message; box.className = "notice" + (kind ? " " + kind : ""); box.hidden = !message;
  }
  function makeButton(label, className, handler, ariaLabel) {
    const button = document.createElement("button"); button.type = "button"; button.className = "button " + className; button.textContent = label;
    if (ariaLabel) button.setAttribute("aria-label", ariaLabel); button.addEventListener("click", handler); return button;
  }
  function cell(row, content) {
    const td = document.createElement("td");
    if (content instanceof Node) td.appendChild(content); else td.textContent = content == null || content === "" ? "—" : String(content);
    row.appendChild(td); return td;
  }
  function displayHost(value) {
    const raw = String(value || "");
    try {
      const url = new URL(raw);
      if (url.host) return url.protocol + "//" + url.host + url.pathname;
    } catch (_) {}
    return raw.replace(/(password|passwd|token|secret|credential|signature|sig|sas|access[_-]?key|api[_-]?key)=([^&\s#]+)/gi, "$1=[已隐藏]")
      .replace(/\/\/[^/@\s]+@/g, "//[已隐藏]@");
  }
  function typeName(type) {
    const item = state.catalog.find((entry) => entry.type === type); return item && item.driverName ? item.driverName : String(type || "未知类型");
  }
  function formatDate(value) {
    const date = value ? new Date(value) : null;
    return date && !Number.isNaN(date.getTime()) ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(date) : "—";
  }
  function renderSources() {
    rows.replaceChildren();
    $("loadError").hidden = true;
    $("emptyState").hidden = state.sources.length !== 0;
    $("tableWrap").hidden = state.sources.length === 0;
    $("sourceSummary").textContent = state.total + " 个数据源";
    $("pagination").hidden = state.total <= pageSize;
    $("pageSummary").textContent = "第 " + (Math.floor(state.offset / pageSize) + 1) + " 页 · 共 " + state.total + " 个";
    $("previousPage").disabled = state.offset <= 0; $("nextPage").disabled = state.offset + pageSize >= state.total;
    state.sources.forEach((source) => {
      const row = document.createElement("tr");
      const name = document.createElement("span"); name.className = "source-name"; name.textContent = source.name || "未命名数据源";
      const endpoint = document.createElement("span"); endpoint.className = "source-detail";
      endpoint.textContent = source.config && source.config.host ? displayHost(source.config.host) + (source.config.port ? ":" + source.config.port : "") : source.id;
      const nameCell = cell(row, name); nameCell.appendChild(endpoint);
      const type = document.createElement("span"); type.className = "type-label"; type.textContent = typeName(source.type);
      const typeDetail = document.createElement("span"); typeDetail.className = "source-detail"; typeDetail.textContent = source.type || "";
      const typeCell = cell(row, type); typeCell.appendChild(typeDetail);
      cell(row, source.config && (source.config.database || source.config.schema));
      const status = String(source.status || "inactive").toLowerCase();
      const badge = document.createElement("span"); badge.className = "status-pill status-" + (["active", "inactive", "error"].includes(status) ? status : "inactive");
      badge.textContent = ({ active: "已启用", inactive: "已停用", error: "异常" })[status] || status; cell(row, badge);
      cell(row, formatDate(source.updated_at || source.updatedAt));
      const actions = document.createElement("div"); actions.className = "row-actions";
      actions.appendChild(makeButton("编辑", "quiet", () => editSource(source.id), "编辑 " + (source.name || "数据源")));
      const toggle = status === "active" ? "停用" : "启用";
      actions.appendChild(makeButton(toggle, "quiet", () => toggleSource(source), toggle + " " + (source.name || "数据源")));
      actions.appendChild(makeButton("删除", "quiet danger", () => deleteSource(source), "删除 " + (source.name || "数据源")));
      cell(row, actions); rows.appendChild(row);
    });
  }
  function renderLoadError(error) {
    $("sourceSummary").textContent = "数据源暂时无法读取"; $("tableWrap").hidden = true; $("emptyState").hidden = true; $("pagination").hidden = true;
    $("loadError").textContent = errorText(error); $("loadError").hidden = false;
  }
  async function loadCatalog() {
    const result = await api("/api/v1/database/types", { auth: false });
    const entries = Array.isArray(result) ? result : Object.values(result || {});
    state.catalog = entries.filter((item) => item && item.registered && item.supported && item.type)
      .sort((a, b) => Number(Boolean(b.recommended)) - Number(Boolean(a.recommended)) || String(a.type).localeCompare(String(b.type)));
    if (state.sources.length) renderSources();
  }
  async function loadSources() {
    try {
      const result = await api("/api/v1/datasources?limit=" + pageSize + "&offset=" + state.offset);
      const total = Number(result && result.total);
      if (state.offset > 0 && Number.isFinite(total) && state.offset >= total) {
        // The list shrank past the current page (e.g. another operator removed sources): load the last valid page.
        state.offset = total > 0 ? Math.floor((total - 1) / pageSize) * pageSize : 0;
        return loadSources();
      }
      state.sources = Array.isArray(result && result.dataSources) ? result.dataSources : [];
      state.total = total || state.sources.length; renderSources();
    } catch (error) { renderLoadError(error); }
  }
  function addTypeOption(select, item, suffix = "") {
    const option = document.createElement("option"); option.value = item.type;
    option.textContent = (item.driverName || item.type) + " (" + item.type + ")" + suffix; select.appendChild(option);
  }
  function populateTypes(currentType = "") {
    const select = $("sourceType"); select.replaceChildren();
    const placeholder = document.createElement("option"); placeholder.value = ""; placeholder.textContent = "选择连接器类型"; select.appendChild(placeholder);
    state.catalog.forEach((item) => addTypeOption(select, item, item.endToEndVerified ? " · 已完成端到端验证" : ""));
    if (currentType && !state.catalog.some((item) => item.type === currentType)) addTypeOption(select, { type: currentType }, " · 当前配置类型");
    select.value = currentType; select.disabled = Boolean(state.editingID);
  }
  function setFormError(message) { const box = $("formError"); box.textContent = message; box.hidden = !message; }
  function setTestResult(message, kind = "") { const box = $("testResult"); box.textContent = message; box.className = "test-result" + (kind ? " " + kind : ""); box.hidden = !message; }
  // Any change to the form (or switching to another source) makes the shown result and any pending test stale.
  function invalidateTestResult() {
    state.testRun++; setTestResult("");
    $("testButton").disabled = false; $("testButton").textContent = "测试连接";
  }
  function resetEditor() {
    form.reset(); state.editingID = ""; state.currentConfig = null; state.portManuallyEdited = false; populateTypes();
    $("editorTitle").textContent = "添加数据源"; $("saveButton").textContent = "保存数据源";
    $("passwordHint").textContent = "新建时只在保存请求中提交；不会写入浏览器存储。"; $("sourceAdditionalProps").value = "{}";
    setFormError(""); invalidateTestResult();
  }
  function openCreateDialog() {
    if (!state.catalog.length) { showNotice("无法读取可用连接器目录，请先检查服务状态。", "error"); return; }
    resetEditor();
    $("sourceType").value = (state.catalog.find((item) => item.recommended) || state.catalog[0]).type;
    $("sourceType").dispatchEvent(new Event("change"));
    dialog.showModal(); $("sourceName").focus();
  }
  async function editSource(id) {
    try {
      const source = await api("/api/v1/datasources/" + encodeURIComponent(id));
      state.editingID = source.id; state.currentConfig = source.config || {}; populateTypes(source.type || "");
      $("editorTitle").textContent = "编辑数据源"; $("saveButton").textContent = "保存更改";
      $("passwordHint").textContent = "服务器不会返回已保存密码。留空会保留原密码；填写新值可替换。";
      $("sourceName").value = source.name || ""; $("sourceHost").value = source.config && displayHost(source.config.host) || "";
      $("sourcePort").value = source.config && source.config.port || ""; $("sourceDatabase").value = source.config && source.config.database || "";
      $("sourceSchema").value = source.config && source.config.schema || ""; $("sourceUsername").value = "";
      $("sourcePassword").value = ""; $("sourceSSL").checked = Boolean(source.config && source.config.ssl);
      $("sourceSSLMode").value = source.config && source.config.sslMode || ""; $("sourceTimeout").value = source.config && source.config.timeout || "";
      $("sourceSSLRootCert").value = source.config && source.config.sslRootCert || "";
      $("sourceMaxPoolSize").value = source.config && source.config.maxPoolSize || "";
      $("sourceIdleTimeout").value = source.config && source.config.idleTimeout || "";
      $("sourceMaxLifetime").value = source.config && source.config.maxLifetime || "";
      $("sourceTimezone").value = source.config && source.config.timezone || "";
      $("sourceAdditionalProps").value = JSON.stringify((source.config && source.config.additionalProps) || {}, null, 2);
      setFormError(""); invalidateTestResult(); dialog.showModal(); $("sourceName").focus();
    } catch (error) { showNotice("无法打开数据源：" + errorText(error), "error"); }
  }
  function hasRedactedHost(host) {
    return Boolean(host && (host.includes("[redacted]") || /(?:^|\/\/)redacted@|[?#]redacted(?:[&#]|$)/i.test(host)));
  }
  function readConfig() {
    let additionalProps;
    try { additionalProps = JSON.parse($("sourceAdditionalProps").value || "{}"); }
    catch (_) { throw new Error("额外参数不是有效 JSON，请检查括号和引号。"); }
    if (!additionalProps || typeof additionalProps !== "object" || Array.isArray(additionalProps)) throw new Error("额外参数必须是 JSON 对象。");
    const config = {
      host: $("sourceHost").value.trim(), port: Number($("sourcePort").value) || 0,
      database: $("sourceDatabase").value.trim(), schema: $("sourceSchema").value.trim(),
      ssl: $("sourceSSL").checked,
      sslMode: $("sourceSSLMode").value.trim(), sslRootCert: $("sourceSSLRootCert").value,
      timeout: Number($("sourceTimeout").value) || 0, maxPoolSize: Number($("sourceMaxPoolSize").value) || 0,
      idleTimeout: Number($("sourceIdleTimeout").value) || 0, maxLifetime: Number($("sourceMaxLifetime").value) || 0,
      timezone: $("sourceTimezone").value.trim(), additionalProps,
    };
    config.username = $("sourceUsername").value.trim() || (state.currentConfig && state.currentConfig.username) || "";
    const password = $("sourcePassword").value; if (password) config.password = password;
    const existingHost = state.currentConfig && state.currentConfig.host;
    if (existingHost && config.host === displayHost(existingHost)) config.host = existingHost;
    else if (hasRedactedHost(existingHost)) {
      throw new Error("当前地址包含隐藏的凭据或参数，不能在此直接修改主机；请使用完整的新连接配置创建数据源。");
    }
    return config;
  }
  async function testConnection() {
    const run = ++state.testRun; // a later edit or test makes this run stale, so its response is ignored
    setFormError(""); setTestResult("");
    try {
      const config = readConfig(); state.currentConfig = config;
      if (state.editingID && hasRedactedHost(config.host)) {
        throw new Error("该连接包含未回显的凭据或参数，页面无法直接测试；请使用支持该类型的已配置数据源连接测试工具。");
      }
      const type = $("sourceType").value; if (!type) throw new Error("请先选择连接器类型。");
      $("testButton").disabled = true; $("testButton").textContent = "正在测试…";
      const result = await api("/api/v1/database/test-connection?type=" + encodeURIComponent(type), { method: "POST", body: config, sanitizeWith: config });
      if (run !== state.testRun) return;
      const status = result && typeof result.status === "string" ? result.status.trim().toLowerCase() : "";
      if (status === "healthy") {
        setTestResult("连接测试成功。", "success");
      } else {
        const detail = result && typeof result.message === "string" ? redact(result.message, config) : "";
        setTestResult(detail ? "连接测试失败：" + detail : "连接测试失败，请检查主机、端口、TLS 和凭据。", "error");
      }
    } catch (error) { if (run === state.testRun) setTestResult(redact(errorText(error), state.currentConfig), "error"); }
    finally { if (run === state.testRun) { $("testButton").disabled = false; $("testButton").textContent = "测试连接"; } }
  }
  async function saveSource(event) {
    event.preventDefault(); setFormError("");
    const type = $("sourceType").value;
    if (!type) { setFormError("请先选择连接器类型。"); return; }
    if (!form.reportValidity()) return;
    let config; try { config = readConfig(); } catch (error) { setFormError(error.message); return; }
    state.currentConfig = config; const body = { name: $("sourceName").value.trim(), config };
    if (!state.editingID) body.type = type;
    $("saveButton").disabled = true; $("saveButton").textContent = "正在保存…";
    try {
      if (state.editingID) await api("/api/v1/datasources/" + encodeURIComponent(state.editingID), { method: "PUT", body, sanitizeWith: config });
      else await api("/api/v1/datasources", { method: "POST", body, sanitizeWith: config });
      const created = !state.editingID; $("sourcePassword").value = ""; $("sourceAdditionalProps").value = "{}"; dialog.close();
      state.offset = 0; await loadSources(); showNotice(created ? "数据源已创建。" : "数据源已更新。", "success"); resetEditor();
    } catch (error) { setFormError(errorText(error)); }
    finally { $("saveButton").disabled = false; $("saveButton").textContent = state.editingID ? "保存更改" : "保存数据源"; }
  }
  async function toggleSource(source) {
    const active = String(source.status || "").toLowerCase() === "active"; const action = active ? "deactivate" : "activate";
    try {
      await api("/api/v1/datasources/" + encodeURIComponent(source.id) + "/" + action, { method: "POST" });
      showNotice(active ? "数据源已停用。" : "数据源已启用。", "success"); await loadSources();
    } catch (error) { showNotice("状态变更失败：" + errorText(error), "error"); }
  }
  async function deleteSource(source) {
    if (!window.confirm("确定删除“" + (source.name || "此数据源") + "”吗？此操作无法撤销。")) return;
    try {
      await api("/api/v1/datasources/" + encodeURIComponent(source.id), { method: "DELETE" });
      if (state.offset > 0 && state.sources.length === 1) state.offset = Math.max(0, state.offset - pageSize);
      await loadSources(); showNotice("数据源已删除。", "success");
    } catch (error) { showNotice("删除失败：" + errorText(error), "error"); }
  }
  async function copyGuideText(elementID, successMessage) {
    const value = $(elementID).textContent || "";
    let copied = false;
    if (window.isSecureContext && navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
      try { await navigator.clipboard.writeText(value); copied = true; } catch (_) {}
    }
    if (!copied && document.execCommand) {
      const temporary = document.createElement("textarea");
      temporary.value = value; temporary.setAttribute("readonly", ""); temporary.style.position = "fixed"; temporary.style.opacity = "0";
      document.body.appendChild(temporary); temporary.select();
      try { copied = document.execCommand("copy"); } catch (_) {}
      temporary.remove();
    }
    showNotice(copied ? successMessage : "无法访问剪贴板，请手动选择示例文本复制。", copied ? "success" : "error");
  }
  $("copyMCPCommand").addEventListener("click", () => { void copyGuideText("mcpClientCommand", "MCP 连接命令已复制。请替换主机名并使用独立的 OIDC token。"); });
  $("copyFirstTaskPrompt").addEventListener("click", () => { void copyGuideText("firstQueryPrompt", "首次查询任务提示已复制。请在已连接的 MCP 客户端中使用。"); });
  $("addButton").addEventListener("click", openCreateDialog); $("emptyAddButton").addEventListener("click", openCreateDialog);
  $("refreshButton").addEventListener("click", loadSources);
  $("clearToken").addEventListener("click", () => { tokenInput.value = ""; showNotice("当前页面中的访问令牌已清除。"); loadSources(); });
  $("closeDialog").addEventListener("click", () => dialog.close()); $("cancelButton").addEventListener("click", () => dialog.close());
  $("testButton").addEventListener("click", testConnection); form.addEventListener("submit", saveSource);
  $("sourcePort").addEventListener("input", () => { if (!state.editingID) state.portManuallyEdited = true; });
  $("sourceType").addEventListener("change", () => {
    if (!state.editingID && !state.portManuallyEdited) {
      const item = state.catalog.find((entry) => entry.type === $("sourceType").value);
      $("sourcePort").value = item && item.defaultPort ? item.defaultPort : "";
    }
  });
  $("previousPage").addEventListener("click", () => { state.offset = Math.max(0, state.offset - pageSize); loadSources(); });
  $("nextPage").addEventListener("click", () => { if (state.offset + pageSize < state.total) { state.offset += pageSize; loadSources(); } });
  // The display name is not part of the connection config, so editing it keeps the result.
  const onFormChange = (event) => { if (event.target !== $("sourceName")) invalidateTestResult(); };
  form.addEventListener("input", onFormChange); form.addEventListener("change", onFormChange);
  dialog.addEventListener("close", () => { $("sourcePassword").value = ""; $("sourceAdditionalProps").value = "{}"; state.currentConfig = null; invalidateTestResult(); });
  window.addEventListener("pagehide", () => { tokenInput.value = ""; $("sourcePassword").value = ""; $("sourceAdditionalProps").value = "{}"; });
  (async () => {
    const results = await Promise.allSettled([loadCatalog(), loadSources()]);
    if (results[0].status === "rejected") showNotice("连接器支持目录无法读取：" + errorText(results[0].reason), "error");
  })();
})();
