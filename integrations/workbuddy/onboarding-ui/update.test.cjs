const vm=require('node:vm'),fs=require('node:fs'),assert=require('node:assert');
const src=fs.readFileSync(__dirname+'/onboarding.js','utf8');
const flow='A'.repeat(43), source='34ee7a59-e839-4de8-b278-afc920c54136';
const path='/onboarding/flows/'+flow+'/update/'+source;
function make(pathname, storage, authenticated){
 const nodes={};for(const k of ['loading','error','success','form','login','result','test','save','update-tls-note'])nodes[k]={textContent:'',hidden:false,disabled:false,events:{},addEventListener(k,v){this.events[k]=v}};
 const fields={};for(const k of ['name','type','host','port','database','schema','username','password','ssl','sslMode','sslRootCert','tlsCA'])fields[k]={value:'',checked:false,events:{},addEventListener(k,v){this.events[k]=v}};
 fields.type.options=[{}];fields.type.appendChild=x=>fields.type.options.push(x);nodes.form.elements=fields;nodes.form.reportValidity=()=>true;
 const calls=[],loc={pathname,replace(v){this.replaced=v}},ctx={document:{getElementById:id=>nodes[id],createElement:()=>({})},location:loc,sessionStorage:storage,encodeURIComponent,
 fetch:async(url,o)=>{calls.push([url,o]);if(url==='/onboarding/session')return authenticated?{ok:true,json:async()=>({authenticated:true,csrf_token:'csrf'})}:{ok:false,status:401};if(url==='/onboarding/source-types')return {ok:true,json:async()=>[{type:'mysql',name:'MySQL',enabled:true}]};return {ok:true,json:async()=>({connected:true,id:source})}}};
 vm.runInNewContext(src,ctx);return {nodes,fields,calls,loc};
}
const storage=()=>({v:{},setItem(k,v){this.v[k]=v},getItem(k){return this.v[k]||null},removeItem(k){delete this.v[k]}});
const tick=()=>new Promise(r=>setTimeout(r,10));
(async()=>{
 const st=storage(),first=make(path,st,false);await tick();
 assert.equal(first.nodes.login.href,'/onboarding/auth/login?flow='+flow);
 assert.equal(st.getItem('df-update-return'),path);
 const resumed=make('/onboarding/flows/'+flow,st,true);assert.equal(resumed.loc.replaced,path);assert.equal(st.getItem('df-update-return'),null);
 const f=make(path,st,true);await tick();assert.equal(f.nodes.form.hidden,false);assert.equal(f.fields.name.disabled,true);
 Object.assign(f.fields.type,{value:'mysql'});Object.assign(f.fields.host,{value:'db.example.test'});Object.assign(f.fields.port,{value:'3306'});Object.assign(f.fields.database,{value:'orders'});Object.assign(f.fields.username,{value:'reader'});Object.assign(f.fields.password,{value:'synthetic-secret'});
 f.nodes.test.events.click();await tick();
 assert.equal(f.calls[2][0],path+'/test');assert(!f.calls[2][0].includes('synthetic-secret'));assert.equal(JSON.parse(f.calls[2][1].body).name,'');
 f.nodes.form.events.submit({preventDefault(){}});await tick();assert.equal(f.calls[3][0],path+'/save');assert.equal(f.fields.password.value,'');assert.equal(st.getItem('df-update-return'),null);
 const unsafe=storage();unsafe.setItem('df-update-return','//evil.example/');const g=make('/onboarding/flows/'+flow,unsafe,true);await tick();assert.equal(g.loc.replaced,undefined);
 console.log('onboarding update UI checks passed');
})().catch(e=>{console.error(e);process.exit(1)});
