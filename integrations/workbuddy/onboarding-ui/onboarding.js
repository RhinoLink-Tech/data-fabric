(() => {
  'use strict';
  const el = (id) => document.getElementById(id);
  const form = el('form');
  const fields = form.elements;
  const flowMatch = location.pathname.match(/^\/onboarding\/flows\/([A-Za-z0-9_-]{43})\/?$/);
  let csrf = '';
  const message = (id, text) => { const node = el(id); node.textContent = text; node.hidden = !text; };
  function fail(text) { message('loading', ''); message('error', text); }
  async function request(path, options = {}) {
    const headers = {'Accept':'application/json'};
    if (options.method === 'POST') { headers['Content-Type'] = 'application/json'; headers['X-CSRF-Token'] = csrf; }
    return fetch(path, {...options, headers, credentials:'same-origin', cache:'no-store', redirect:'error', referrerPolicy:'no-referrer'});
  }
  function body() {
    if (!form.reportValidity()) throw new Error('请检查必填项。');
    const host = fields.host.value.trim();
    if (/[\x00-\x20\x7f@?#]/.test(host)) throw new Error('主机不能包含空格、用户名、密码、查询参数或片段。');
    const portText = fields.port.value;
    const port = portText ? Number(portText) : 0;
    if (portText && (!Number.isInteger(port) || port < 1 || port > 65535)) throw new Error('端口必须在 1 到 65535 之间。');
    return {name:fields.name.value.trim(), type:fields.type.value, config:{host,port,database:fields.database.value.trim(),schema:fields.schema.value.trim(),username:fields.username.value.trim(),password:fields.password.value}};
  }
  function status(text) { el('result').textContent = text; }
  function busy(v) { el('test').disabled = v; el('save').disabled = v; }
  async function action(kind) {
    let payload;
    try { payload = body(); } catch(e) { status(e.message); return; }
    busy(true); status(kind === 'test' ? '正在测试连接…' : '正在保存…');
    try {
      const endpoint = '/onboarding/flows/' + flowMatch[1] + (kind === 'test' ? '/test' : '/sources');
      const response = await request(endpoint,{method:'POST',body:JSON.stringify(payload)});
      if (!response.ok) { status(response.status === 401 || response.status === 403 ? '登录或权限已失效，请重新登录并检查授权。' : response.status === 422 ? '当前向导暂不支持 TLS 连接。' : '操作未完成，请检查连接配置后重试。'); return; }
      const result = await response.json();
      if (kind === 'test') { status(result && result.connected === true ? '连接测试成功。' : '连接测试未成功，请检查连接配置。'); return; }
      if (!result || typeof result.id !== 'string' || !/^[0-9a-fA-F-]{36}$/.test(result.id)) { status('未收到有效的保存结果，请重新打开此流程检查状态。'); return; }
      fields.password.value = ''; form.hidden = true; message('success','数据源已创建，可以返回 WorkBuddy 使用。');
    } catch(_) { status('无法连接服务，请检查网络后重试。'); }
    finally { payload = null; busy(false); }
  }
  if (!flowMatch) { fail('接入链接无效，请从 WorkBuddy 重新开始。'); return; }
  form.addEventListener('submit', e => { e.preventDefault(); action('create'); });
  el('test').addEventListener('click', () => action('test'));
  (async () => {
    try {
      const sessionResponse = await request('/onboarding/session');
      if (sessionResponse.status === 401) { fail('请先登录 Data Fabric 并确认管理权限。'); el('login').href = '/onboarding/auth/login?flow=' + encodeURIComponent(flowMatch[1]); el('login').hidden = false; return; }
      if (!sessionResponse.ok) { fail('无法验证登录状态，请重试。'); return; }
      const session = await sessionResponse.json();
      if (!session || session.authenticated !== true || typeof session.csrf_token !== 'string' || !session.csrf_token) { fail('登录会话不可用，请重新登录。'); return; }
      csrf = session.csrf_token;
      const catalogResponse = await request('/onboarding/source-types');
      if (!catalogResponse.ok) { fail('无法读取当前服务支持的数据源类型。'); return; }
      const catalog = await catalogResponse.json();
      if (!Array.isArray(catalog)) { fail('数据源类型目录不可用。'); return; }
      for (const item of catalog) {
        if (!item || typeof item.type !== 'string' || !/^[a-zA-Z0-9_-]{1,64}$/.test(item.type) || item.enabled !== true) continue;
        const opt = document.createElement('option'); opt.value = item.type; opt.textContent = typeof item.name === 'string' ? item.name : item.type; fields.type.appendChild(opt);
      }
      if (fields.type.options.length === 1) { fail('当前服务没有可通过此向导添加的数据源类型。'); return; }
      message('loading',''); form.hidden = false;
    } catch(_) { fail('无法连接 Data Fabric 服务，请检查网络后重试。'); }
  })();
})();
