import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Execute compiled production controllers with real asynchronous promise ordering.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/analysis-'));let state,client,nav,api;
try{writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const file of ['view-state-api.ts','analysis-state.ts','preference-client.ts','query-provenance.ts','NavigationBar.tsx','contracts.ts','session.ts'])cpSync(join(web,'src',file),join(tmp,file));execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'view-state-api.ts'),join(tmp,'analysis-state.ts'),join(tmp,'preference-client.ts'),join(tmp,'NavigationBar.tsx')],{stdio:'pipe'});const require=createRequire(import.meta.url);api=require(join(tmp,'out/view-state-api.js'));state=require(join(tmp,'out/analysis-state.js'));client=require(join(tmp,'out/preference-client.js'));nav=require(join(tmp,'out/NavigationBar.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
const view=()=>({profileId:'p',snapshotId:'s',rootId:'nas',selectedNodeId:null,map:'treemap',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'name',direction:'asc'});
const pref=revision=>({version:2,display:{units:'decimal',colorMode:'folder',detail:'auto',labels:true,percentages:true,hierarchyBorders:'normal',reducedMotion:'auto'},dsmUserId:'admin',revision,lastView:view(),language:'auto',theme:'auto',preferredProfileId:null,map:'treemap',locale:null,timeZone:null});
const response=(data,status=200)=>new Response(JSON.stringify({contractVersion:1,data}),{status});
const defer=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b;});return{promise,resolve,reject};};
test('atomic bounded history preserves all fields and clones nested values',()=>{
 let s=state.initialAnalysisState(),v=view();s=state.analysisReducer(s,{type:'navigate',view:v});v.filter.categories.push('image');assert.deepEqual(s.view.filter.categories,[]);
 for(let i=0;i<70;i++)s=state.analysisReducer(s,{type:'navigate',view:{...view(),rootId:`n${i}`,metric:'allocated'}});
 assert.equal(s.back.length,64);const last=s.view;s=state.analysisReducer(s,{type:'back'});assert.equal(s.view.rootId,'n68');s=state.analysisReducer(s,{type:'forward'});assert.deepEqual(s.view,last);
 s=state.analysisReducer(s,{type:'back'});s=state.analysisReducer(s,{type:'navigate',view:view()});assert.equal(s.forward.length,0);
});
test('stale success and rejection cannot alter a newer state',async()=>{
 let s=state.analysisReducer(state.initialAnalysisState(),{type:'replace',view:view()});const ticket=state.analysisTicket(s),a=defer(),b=defer();let events=[];
 const pa=state.settleAnalysis(a.promise,ticket,()=>s,x=>events.push(x),e=>events.push(e));const pb=state.settleAnalysis(b.promise,ticket,()=>s,x=>events.push(x),e=>events.push(e));
 s=state.analysisReducer(s,{type:'navigate',view:{...view(),rootId:'other'}});a.resolve('old');b.reject(Error('old'));await Promise.all([pa,pb]);assert.deepEqual(events,[]);
 await state.settleAnalysis(Promise.reject('current'),state.analysisTicket(s),()=>s,x=>events.push(x),e=>events.push(e));assert.deepEqual(events,['current']);
});
test('uncertain save serializes CAS and replays exact frozen bytes and key',async()=>{
 const calls=[];let n=0;const c=new client.PreferenceClient(async(u,o)=>{calls.push(o);if(n++===0)throw Error('network');return response(pref(2));},()=> 'token',()=> 'stable-key');c.adopt(pref(1));const v=view();
 await assert.rejects(c.writeLastView(v));v.rootId='mutated';assert.equal(c.status,'uncertain');await assert.rejects(c.writeLastView(v),/PREFERENCE_PENDING/);assert.throws(()=>c.adopt(pref(3)),/PREFERENCE_PENDING/);
 await c.retry();assert.equal(calls.length,2);assert.equal(calls[0].body,calls[1].body);assert.equal(calls[0].headers['Idempotency-Key'],calls[1].headers['Idempotency-Key']);assert.equal(JSON.parse(calls[1].body).view.value.rootId,'nas');assert.equal(c.preference.revision,2);
});
test('conflict requires explicit reload and does not automatically overwrite',async()=>{
 let count=0;const c=new client.PreferenceClient(async()=>++count===1?new Response(JSON.stringify({contractVersion:1,error:{code:'PREFERENCE_CONFLICT'}}),{status:409}):response(pref(8)),()=> 'token');c.adopt(pref(1));
 await assert.rejects(c.writeLastView(view()),/PREFERENCE_CONFLICT/);assert.equal(c.status,'conflict');await assert.rejects(c.writeLastView(view()),/RELOAD_REQUIRED/);assert.equal(count,1);await c.read();assert.equal(c.preference.revision,8);
});
test('concurrent writes and late disposed completion are rejected',async()=>{
 const d=defer();const c=new client.PreferenceClient(()=>d.promise,()=> 'token');c.adopt(pref(1));const p=c.writeLastView(view());await assert.rejects(c.writeLastView(view()),/PREFERENCE_PENDING/);c.dispose();d.resolve(response(pref(2)));await assert.rejects(p,/DISPOSED/);assert.equal(c.preference.revision,1);
});
test('toolbar binds breadcrumb snapshot and escapes names',()=>{
 const p={language:'de',view:view(),navigation:{snapshotId:'s',rootId:'nas',parentId:null,selectedNodeId:null,reasons:[],breadcrumb:[{id:'nas',name:'<private>',kind:'nas'}]},canBack:false,canForward:false,onBack(){},onForward(){},onParent(){},onRoot(){},onBreadcrumb(){},onMap(){}};
 const html=renderToStaticMarkup(React.createElement(nav.NavigationBar,p));assert.ok(html.includes('&lt;private&gt;'));assert.ok(html.includes('aria-pressed="true"'));
 assert.ok(!renderToStaticMarkup(React.createElement(nav.NavigationBar,{...p,navigation:{...p.navigation,snapshotId:'wrong'}})).includes('private'));
});
// Malformed or excessive replies leave the original intent replayable instead of asserting success.
test('invalid owner, revision and oversized response retain uncertain intent',async()=>{
 for(const data of [{...pref(2),dsmUserId:'foreign'},pref(9),{...pref(2),lastView:{}}]){
  const c=new client.PreferenceClient(async()=>response(data),()=> 'token');c.adopt(pref(1));await assert.rejects(c.writeLastView(view()),/INVALID_PREFERENCE_RESPONSE/);assert.equal(c.status,'uncertain');assert.equal(c.preference.revision,1);
 }
 const c=new client.PreferenceClient(async()=>new Response('x'.repeat(65537)),()=> 'token');c.adopt(pref(1));await assert.rejects(c.writeLastView(view()),/INVALID_PREFERENCE_RESPONSE/);assert.equal(c.status,'uncertain');
});
// Restore is a read operation with exact owner-qualified lease and snapshot bindings.
test('restore transport preserves reader and rejects foreign lease, snapshot and stale preference',async()=>{
 globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'test-token'}}}}};
 const navigation={snapshotId:'s',rootId:'root',parentId:null,selectedNodeId:null,reasons:[],breadcrumb:[{id:'root',name:'Root',kind:'nas'}]};
 const good={preferenceRevision:3,profileId:'p',resolvedView:{...view(),rootId:'root'},navigation,reasons:[],reader:{view:{snapshotId:'s',state:'complete',nodes:[],truncated:false},lease:{id:'lease',snapshotId:'s',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}}};
 const calls=[];const transport=async(u,o)=>{calls.push([u,o]);return response(good);};
 assert.equal((await api.requestRestore('lease',3,new AbortController().signal,transport)).profileId,'p');assert.equal(calls[0][0],'./api.cgi?action=view-restore');assert.equal(calls[0][1].credentials,'same-origin');assert.equal(calls[0][1].headers['X-SYNO-TOKEN'],'test-token');assert.equal(calls[0][1].headers['Idempotency-Key'],'lease');
 for(const mutate of [x=>x.preferenceRevision=4,x=>x.reader.lease.id='foreign',x=>x.reader.view.snapshotId='foreign',x=>x.navigation.rootId='foreign']){const bad=structuredClone(good);mutate(bad);await assert.rejects(api.requestRestore('lease',3,new AbortController().signal,async()=>response(bad)),/VIEW_RESPONSE_INVALID/);}
 const abort=new AbortController();await assert.rejects(api.requestRestore('lease',3,abort.signal,async()=>{abort.abort();return response(good);}),/ABORTED/);
 delete globalThis.window;
});
test('navigation rejects unexplained root substitutions and mismatched trails',()=>{
 const n={snapshotId:'s',rootId:'r',parentId:'p',selectedNodeId:null,reasons:[],breadcrumb:[{id:'p',name:'P',kind:'nas'},{id:'r',name:'R',kind:'directory'}]};
 assert.equal(api.checkedNavigation(n,'s','r'),n);assert.throws(()=>api.checkedNavigation(n,'s','other'));assert.throws(()=>api.checkedNavigation({...n,parentId:'wrong'},'s','r'));
});
// Obsolete authentication failures must not invalidate the current authenticated interface.
test('disposed preference reads reject stale authentication and transport failures',async()=>{
 for(const reject of [false,true]){const d=defer(),c=new client.PreferenceClient(()=>d.promise,()=> 'token');const pending=c.read();c.dispose();if(reject)d.reject(Error('old transport'));else d.resolve(new Response('',{status:401}));await assert.rejects(pending,/PREFERENCE_DISPOSED/);}
});
test('aborted restore and navigation mask late auth and transport failures',async()=>{
 globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'test-token'}}}}};
 try{for(const kind of ['restore','navigation'])for(const reject of [false,true]){const d=defer(),c=new AbortController();const work=kind==='restore'?api.requestRestore('lease',0,c.signal,()=>d.promise):api.requestNavigation('p','lease','s','nas',null,c.signal,()=>d.promise);c.abort();if(reject)d.reject(Error('old transport'));else d.resolve(new Response('',{status:403}));await assert.rejects(work,/VIEW_REQUEST_ABORTED/);}}finally{delete globalThis.window;}
});
test('navigation binds selected identity and only permits an explained clear',async()=>{
 globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'test-token'}}}}};
 const n={snapshotId:'s',rootId:'r',parentId:null,selectedNodeId:'selected',reasons:[],breadcrumb:[{id:'r',name:'Root',kind:'nas'}]};
 try{for(const bad of [{...n,selectedNodeId:'foreign'},{...n,selectedNodeId:null},{...n,breadcrumb:[{id:'p',name:'Parent',kind:'nas'},{id:'r',name:'Root',kind:'directory'}]}])await assert.rejects(api.requestNavigation('p','lease','s','r','selected',new AbortController().signal,async()=>response(bad)),/VIEW_RESPONSE_INVALID/);
 const cleared={...n,selectedNodeId:null,reasons:['SELECTION_UNAVAILABLE']};assert.equal((await api.requestNavigation('p','lease','s','r','selected',new AbortController().signal,async()=>response(cleared))).selectedNodeId,null);
 }finally{delete globalThis.window;}
});
// Successful receipts must attest the complete frozen last-view intent, not merely a new revision.
test('mismatched last-view receipts remain uncertain and optional omissions compare equally',async()=>{
 for(const mutate of [v=>v.rootId='wrong',v=>v.selectedNodeId='wrong',v=>v.search.text='wrong',v=>v.map='radial',v=>v.filter.categories=['image'],v=>v.direction='desc']){
  const data=pref(2);mutate(data.lastView);const c=new client.PreferenceClient(async()=>response(data),()=> 'token');c.adopt(pref(1));await assert.rejects(c.writeLastView(view()),/INVALID_PREFERENCE_RESPONSE/);assert.equal(c.status,'uncertain');assert.equal(c.preference.revision,1);
 }
 const c=new client.PreferenceClient(async()=>response(pref(2)),()=> 'token');c.adopt(pref(1));const v=view();v.filter.objectKinds=[];v.filter.text='';await c.writeLastView(v);assert.equal(c.status,'idle');
});
// Browser native fetch rejects a client object as its receiver; the default wrapper preserves a free call.
test('default preference transport does not bind native fetch to the client',async()=>{
 const original=globalThis.fetch;let calls=0;
 globalThis.fetch=function(){assert.equal(this,undefined);calls++;return Promise.resolve(response(pref(1)));};
 try{const c=new client.PreferenceClient(undefined,()=> 'token');await c.read();assert.equal(calls,1);}finally{globalThis.fetch=original;}
});
// Wizard language writes retain unrelated appearance settings and exact uncertain retries.
test('language appearance CAS preserves fields and replays only the same language',async()=>{
 const original={...pref(4),theme:'dark',map:'radial',locale:'de-DE',timeZone:'Europe/Berlin'};
 const calls=[];let count=0;const c=new client.PreferenceClient(async(u,o)=>{calls.push(o);if(count++===0)throw Error('lost');return response({...original,revision:5,language:'en'});},()=> 'token',()=> 'language-key');c.adopt(original);
 await assert.rejects(c.writeLanguage('en'));await assert.rejects(c.writeLanguage('de'),/PREFERENCE_PENDING/);
 const saved=await c.writeLanguage('en');assert.equal(saved.language,'en');assert.equal(calls[0].body,calls[1].body);assert.equal(calls[0].headers['Idempotency-Key'],calls[1].headers['Idempotency-Key']);
 assert.deepEqual(JSON.parse(calls[0].body),{contractVersion:2,expectedRevision:4,group:'appearance',appearance:{language:'en',theme:'dark',map:'radial',locale:'de-DE',timeZone:'Europe/Berlin'}});assert.deepEqual(saved.lastView,original.lastView);
});
// A valid envelope with a foreign appearance receipt must remain unresolved.
test('language appearance rejects mismatched receipts and preserves conflict state',async()=>{
 const c=new client.PreferenceClient(async()=>response({...pref(2),language:'de'}),()=> 'token');c.adopt(pref(1));await assert.rejects(c.writeLanguage('en'),/INVALID_PREFERENCE_RESPONSE/);assert.equal(c.status,'uncertain');
 const conflict=new client.PreferenceClient(async()=>new Response(JSON.stringify({contractVersion:1,error:{code:'PREFERENCE_CONFLICT'}}),{status:409}),()=> 'token');conflict.adopt(pref(1));await assert.rejects(conflict.writeLanguage('en'),/PREFERENCE_CONFLICT/);await assert.rejects(conflict.writeLanguage('de'),/PREFERENCE_RELOAD_REQUIRED/);
});

// Version two requires every display field and never reconstructs defaults on the client.
test('preference v2 rejects missing or malformed display and old DTOs',()=>{
 const c=new client.PreferenceClient(async()=>response(pref(1)),()=> 'token');
 const invalid=[{...pref(1),version:1},{...pref(1),display:null}];
 for(const key of Object.keys(pref(1).display)){const p=pref(1);delete p.display[key];invalid.push(p);}
 for(const [key,value] of [['detail','2'],['detail',0],['detail',9],['labels',null],['percentages',0],['units','GB'],['colorMode','type'],['hierarchyBorders','off'],['reducedMotion','off']])invalid.push({...pref(1),display:{...pref(1).display,[key]:value}});
 for(const p of invalid)assert.throws(()=>c.adopt(p),/INVALID_PREFERENCE_RESPONSE/);
 for(const detail of ['auto','max',1,8]){c.adopt({...pref(1),display:{...pref(1).display,detail}});assert.equal(c.preference.display.detail,detail);}
});
// Existing appearance and view writes retain the authoritative display group exactly.
test('v2 appearance and view transport preserve nondefault display through exact retry',async()=>{
 const initial={...pref(1),display:{units:'binary',colorMode:'mtime',detail:8,labels:false,percentages:false,hierarchyBorders:'subtle',reducedMotion:'reduce'}};
 let value=structuredClone(initial),fail=true;const calls=[];
 const c=new client.PreferenceClient(async(u,o)=>{calls.push(o);const b=JSON.parse(o.body);assert.equal(b.contractVersion,2);assert.equal(b.expectedRevision,value.revision);if(fail){fail=false;throw Error('lost');}value={...value,revision:value.revision+1,...b.appearance,...(b.group==='lastView'?{lastView:b.view.value}:{})};return response(value);},()=> 'token',()=> 'key');
 c.adopt(initial);await assert.rejects(c.writeTheme('dark'));await c.retry();assert.equal(calls[0].body,calls[1].body);assert.deepEqual(c.preference.display,initial.display);
 await c.writeLastView(null);assert.deepEqual(c.preference.display,initial.display);assert.equal(c.preference.lastView,null);const copy=c.preference;copy.display.labels=true;assert.equal(c.preference.display.labels,false);
});

// Go and JavaScript may serialize nested workspace fields in different orders.
test('workspace receipts compare values independent of object member ordering',async()=>{
 const workspace={source:null,scanRecycle:true,scanSystem:true,jobs:[],zoom:{root:'n',scale:1,x:0,y:0,snapshotId:'s'},left:true,right:true,leftWidth:322,rightWidth:400,expanded:[],treeSort:'name',treeDirection:'asc',treeScroll:0,listScroll:0,filterOpen:false,list:{text:'',recursive:false,kind:'all',category:'',sort:'size',direction:'desc',foldersFirst:true},showRecycle:false,showSystem:false};
 const reordered={...workspace,zoom:{snapshotId:'s',root:'n',scale:1,x:0,y:0},list:Object.fromEntries(Object.entries(workspace.list).reverse())};delete reordered.source;
 const c=new client.PreferenceClient(async()=>response({...pref(2),workspace:reordered}),()=> 'token',()=> 'workspace-key');c.adopt(pref(1));
 const result=await c.writeWorkspace(workspace);assert.equal(c.status,'idle');assert.equal(result.workspace.leftWidth,322);
});
