const vm=require('node:vm'),fs=require('node:fs'),assert=require('node:assert');
const src=fs.readFileSync(__dirname+'/onboarding.js','utf8');
function make(){const nodes={};for(const k of ['loading','error','success','form','login','result','test','save','update-tls-note'])nodes[k]={textContent:'',hidden:false,disabled:false,events:{},addEventListener(k,v){this.events[k]=v}};const fields={};for(const k of ['name','type','host','port','database','schema','username','password','ssl','sslMode','sslRootCert','tlsCA'])fields[k]={value:'',checked:false,events:{},addEventListener(k,v){this.events[k]=v}};fields.type.options=[{}];fields.type.appendChild=x=>fields.type.options.push(x);nodes.form.elements=fields;nodes.form.reportValidity=()=>true;return {nodes,fields};}
async function tick(){await new Promise(r=>setTimeout(r,10))}
(async()=>{let f=make(),calls=[];const ctx={document:{getElementById:id=>f.nodes[id],createElement:()=>({})},location:{pathname:'/onboarding/flows/'+('A'.repeat(43))},fetch:async(url,o)=>{calls.push([url,o]);if(url==='/onboarding/session')return {ok:true,json:async()=>({authenticated:true,csrf_token:'csrf-test'})};if(url==='/onboarding/source-types')return {ok:true,json:async()=>[{type:'postgresql',name:'PostgreSQL',enabled:true}]};return {ok:true,json:async()=>({connected:true})}},encodeURIComponent};vm.runInNewContext(src,ctx);await tick();assert.equal(f.nodes.form.hidden,false);f.fields.name.value='test';f.fields.type.value='postgresql';f.fields.host.value='db.example.test?secret=bad';f.nodes.test.events.click();await tick();assert.equal(calls.length,2,'unsafe host must never be sent');assert.match(f.nodes.result.textContent,/主机/);f.fields.host.value='db.example.test';f.fields.port.value='5432';f.fields.password.value='synthetic-test';f.nodes.test.events.click();await tick();assert.equal(calls.length,3);assert.equal(calls[2][1].headers['X-CSRF-Token'],'csrf-test');assert.equal(calls[2][1].credentials,'same-origin');assert.equal(calls[2][1].referrerPolicy,'no-referrer');assert(!calls[2][0].includes('synthetic-test'));assert.equal(f.nodes.result.textContent,'连接测试成功。');for (const failure of [401, 403, 404]) {
  const g=make(), sent=[];
  const local={document:{getElementById:id=>g.nodes[id],createElement:()=>({})},location:{pathname:'/onboarding/flows/'+('B'.repeat(43))},fetch:async(url,o)=>{
    sent.push([url,o]);
    if(url==='/onboarding/session')return {ok:true,json:async()=>({authenticated:true,csrf_token:'csrf-test'})};
    if(url==='/onboarding/source-types')return {ok:true,json:async()=>[{type:'postgresql',enabled:true}]};
    return {ok:false,status:failure};
  },encodeURIComponent};
  vm.runInNewContext(src,local); await tick();
  g.fields.name.value='test';g.fields.type.value='postgresql';g.fields.host.value='db.example.test';g.fields.port.value='5432';g.fields.password.value='synthetic-test';
  g.nodes.test.events.click();await tick();
  assert.equal(g.fields.password.value,'','credentials cleared after authorization or expired-flow failure');
  assert.match(g.nodes.result.textContent,/WorkBuddy/);
  assert.match(g.nodes.result.textContent,/重新输入密码/);
  if(failure===404) {assert.equal(g.nodes.login.hidden,true);assert.match(g.nodes.result.textContent,/向导/);}
  else {assert.equal(g.nodes.login.hidden,false);assert.match(g.nodes.login.href,/^\/onboarding\/auth\/login\?flow=/);assert(!g.nodes.login.href.includes('synthetic-test'));}
  assert.equal(sent.length,3,'no automatic credential retry');
}
{
  const g=make();
  const local={document:{getElementById:id=>g.nodes[id],createElement:()=>({})},location:{pathname:'/onboarding/flows/'+('C'.repeat(43))},fetch:async()=>({ok:false,status:401}),encodeURIComponent};
  vm.runInNewContext(src,local);await tick();
  assert.match(g.nodes.error.textContent,/WorkBuddy/);
  assert.equal(g.nodes.login.hidden,false);
  assert.match(g.nodes.login.href,/^\/onboarding\/auth\/login\?flow=/);
}
console.log('onboarding UI checks passed');})().catch(e=>{console.error(e);process.exit(1)});
