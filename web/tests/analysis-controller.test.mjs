import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
// Exercise production transitions with delayed transports; no fake controller duplicates its logic.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/controller-'));let Controller,query,acquisitions,navigation;
try{writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const file of ['query-provenance.ts','reader-acquisition.ts','AnalysisController.ts','analysis-query.ts','analysis-state.ts','preference-client.ts','view-state-api.ts','NavigationBar.tsx','contracts.ts','session.ts','snapshot-reader.ts'])cpSync(join(web,'src',file),join(tmp,file));cpSync(join(web,'src/features/searchlist'),join(tmp,'features/searchlist'),{recursive:true});execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'AnalysisController.ts'),join(tmp,'analysis-query.ts'),join(tmp,'reader-acquisition.ts')],{stdio:'pipe'});const req=createRequire(import.meta.url);Controller=req(join(tmp,'out/AnalysisController.js'));query=req(join(tmp,'out/analysis-query.js'));acquisitions=req(join(tmp,'out/reader-acquisition.js'));navigation=req(join(tmp,'out/view-state-api.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
const response=data=>new Response(JSON.stringify({contractVersion:1,data}));
const view=()=>({...Controller.defaultView('p','s'),rootId:'root'});
const reader=(id='lease',snapshot='s')=>({lease:{id,snapshotId:snapshot,expiresAt:'2026-10-20T00:00:00Z'},view:{snapshotId:snapshot},selection:{nodeId:null,fallback:'none'},newCompleteSnapshotId:null});
const nav=(v=view())=>({snapshotId:v.snapshotId,rootId:v.rootId,parentId:null,selectedNodeId:v.selectedNodeId,reasons:[],breadcrumb:[{id:v.rootId,name:'Root',kind:'nas'}]});
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b;});return{promise,resolve,reject};};
const setup=()=>{globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'test-only'}}}}};};
test('restore publishes complete view and lease atomically without preference writes',()=>{setup();const states=[];const c=new Controller.AnalysisController(s=>states.push(s),()=>{});const v={...view(),map:'radial',filter:{categories:['image']},sort:'size'};c.restored(v,reader(),nav(v));assert.equal(states.length,1);assert.equal(states[0].pin.snapshotId,states[0].state.view.snapshotId);assert.deepEqual(states[0].state.view,v);assert.equal(states[0].state.back.length,0);});
test('late navigation success and failure do not overwrite a newer selection or session close',async()=>{setup();const old=globalThis.fetch;try{const events=[];const c=new Controller.AnalysisController(v=>events.push(v),()=>{});c.restored(view(),reader(),nav());const d=deferred();globalThis.fetch=()=>d.promise;const pending=c.move({...view(),rootId:'old'});c.clear();d.resolve(response(nav({...view(),rootId:'old'})));await pending;assert.equal(c.current.state.view,null);const n=events.length;const e=deferred();globalThis.fetch=()=>e.promise;c.restored(view(),reader(),nav());const fail=c.move({...view(),rootId:'old'});c.cancel();e.reject(Error('old rejected'));await fail;assert.equal(events.length,n+1);assert.equal(c.current.state.view.rootId,'root');}finally{globalThis.fetch=old;}});
test('history across profiles opens a newly qualified exact generation and releases old lease',async()=>{setup();const old=globalThis.fetch;try{const calls=[];globalThis.fetch=async(url,options)=>{const b=JSON.parse(options.body);calls.push(b);if(url.endsWith('view-navigation'))return response(nav({...view(),snapshotId:b.snapshotId,rootId:b.rootId,selectedNodeId:b.selectedNodeId}));if(b.operation==='release')return response({});return response(reader(b.viewId,b.snapshotId));};const c=new Controller.AnalysisController(()=>{},()=>{});c.restored(view(),reader(),nav());const next={...view(),profileId:'other',snapshotId:'otherSnapshot',rootId:'otherRoot',map:'radial'};await c.move(next);assert.equal(c.current.state.back.length,1);await c.move(c.current.state.back.at(-1),'back');assert.equal(c.current.state.view.profileId,'p');assert.equal(c.current.state.forward[0].profileId,'other');assert.equal(calls.filter(x=>x.operation==='open').length,2);assert.equal(calls.filter(x=>x.operation==='release').length,2);assert.notEqual(c.current.pin.viewId,'lease');}finally{globalThis.fetch=old;}});
test('explicit partial history opens through recorded job provenance',async()=>{setup();const old=globalThis.fetch;try{const calls=[];globalThis.fetch=async(url,options)=>{const b=JSON.parse(options.body);calls.push(b);return response(url.endsWith('view-navigation')?nav({...view(),snapshotId:'partial'}):reader(b.viewId,'partial'));};const c=new Controller.AnalysisController(()=>{},()=>{});c.rememberPartial('partial','partialJob');await c.move({...view(),snapshotId:'partial'});assert.equal(calls[0].jobId,'partialJob');assert.equal(calls[0].snapshotId,undefined);assert.equal(c.current.pin.snapshotId,'partial');}finally{globalThis.fetch=old;}});
test('map and selection transitions preserve semantic selectors and root',async()=>{setup();const old=globalThis.fetch;try{globalThis.fetch=async(_url,options)=>{const b=JSON.parse(options.body);return response(nav({...view(),rootId:b.rootId,selectedNodeId:b.selectedNodeId}));};const c=new Controller.AnalysisController(()=>{},()=>{});const v={...view(),filter:{categories:['image']},search:{text:'needle',field:'path',scope:'direct'},sort:'size',direction:'desc'};c.restored(v,reader(),nav(v));await c.move({...v,map:'radial',selectedNodeId:'node'});assert.deepEqual(c.current.state.view,{...v,map:'radial',selectedNodeId:'node'});assert.equal(c.current.pin.nodeId,'node');}finally{globalThis.fetch=old;}});
test('hierarchy ignores no bindings and fails closed before unknown allocated queries',async()=>{setup();const pin={profileId:'p',snapshotId:'s',viewId:'lease',nodeId:null};let calls=0;await assert.rejects(query.readAnalysis({...view(),metric:'allocated'},pin,new AbortController().signal,async()=>{calls++;return response({snapshotId:'s',rootId:'root',allocated:{selectable:false}});}),/METRIC_UNAVAILABLE/);assert.equal(calls,1);await assert.rejects(query.readAnalysis(view(),{...pin,snapshotId:'wrong'},new AbortController().signal,async()=>{assert.fail('network');}),/SNAPSHOT_MISMATCH/);});
// Separate list pages preserve cursor metadata and never masquerade as hierarchy totals.
test('restored search and sort selectors use the exact authenticated search contract',async()=>{setup();const pin={profileId:'p',snapshotId:'s',viewId:'lease',nodeId:null};const v={...view(),search:{text:'literal',field:'path',scope:'direct'},sort:'mtime',direction:'desc'};let body;const result=await query.readSearchList(v,pin,'continuation',new AbortController().signal,async(url,options)=>{assert.ok(url.endsWith('search'));body=JSON.parse(options.body);return response({snapshotId:'s',rootId:'root',metric:'logical',scope:'direct',selectionOrder:'mtime-desc-recorded-rowid',items:[],hasMore:true,nextCursor:'next',workLimited:true});});assert.equal(body.cursor,'continuation');assert.equal(body.query.text,'literal');assert.equal(body.query.sort,'mtime');assert.equal(body.query.direction,'desc');assert.equal(result.nextCursor,'next');assert.equal(result.workLimited,true);assert.equal(result.originalSummary,undefined);});
// Restored text conflicts need explicit user correction instead of silently changing the predicate.
test('hierarchy applies one literal predicate and rejects conflicting restored text before transport',async()=>{setup();const v={...view(),search:{text:'needle',field:'path',scope:'direct'}};assert.deepEqual(query.effectiveAnalysisFilter(v),{categories:[],text:'needle',field:'path'});assert.throws(()=>query.effectiveAnalysisFilter({...v,filter:{categories:[],text:'different',field:'name'}}),/FILTER_TEXT_CONFLICT/);const pin={profileId:'p',snapshotId:'s',viewId:'lease',nodeId:null};await assert.rejects(query.readAnalysis({...v,filter:{categories:[],text:'different'}},pin,new AbortController().signal,()=>assert.fail('network')),/FILTER_TEXT_CONFLICT/);});

// A cancelled open is still an owned server resource and must be released after its late response.
test('superseded acquisition releases the newly opened lease and aborted empty response cannot clear current view',async()=>{setup();const old=globalThis.fetch;try{const pending=deferred(),calls=[];globalThis.fetch=async(url,options)=>{const body=JSON.parse(options.body);calls.push(body);if(body.operation==='open'){await pending.promise;return response(reader(body.viewId,'other'));}if(body.operation==='release')return response({});return response(nav({...view(),snapshotId:'other'}));};const c=new Controller.AnalysisController(()=>{},()=>{});c.restored(view(),reader(),nav());const work=c.move({...view(),snapshotId:'other'});c.cancel();pending.resolve();await work;await new Promise(r=>setImmediate(r));assert.equal(c.current.state.view.snapshotId,'s');const opened=calls.find(x=>x.operation==='open');assert.ok(calls.some(x=>x.operation==='release'&&x.viewId===opened.viewId));const aborted=new AbortController();aborted.abort();assert.equal(await c.accept('p',{lease:null,view:{snapshotId:null}},aborted.signal),false);assert.equal(c.current.state.view.snapshotId,'s');}finally{globalThis.fetch=old;}});
// Snapshot adoption preserves semantic selectors while old generation-local identities are discarded.
test('new snapshot retains map filter search and sort but qualifies a fresh root and remapped selection',async()=>{setup();const old=globalThis.fetch;try{let sent;globalThis.fetch=async(_url,options)=>{const body=JSON.parse(options.body);if(body.operation==='release')return response({});sent=body;return response({...nav({...view(),snapshotId:'new',rootId:'newroot',selectedNodeId:'mapped'}),reasons:['ROOT_UNAVAILABLE']});};const c=new Controller.AnalysisController(()=>{},()=>{});const v={...view(),rootId:'oldroot',selectedNodeId:'oldnode',map:'radial',filter:{categories:['image']},search:{text:'literal',field:'name',scope:'direct'},sort:'mtime',direction:'desc'};c.restored(v,reader(),nav(v));const next={...reader('newLease','new'),selection:{nodeId:'mapped',fallback:'ancestor'}};await c.accept('p',next,new AbortController().signal,undefined,'navigate');assert.equal(sent.rootId,'nas');assert.equal(sent.selectedNodeId,'mapped');assert.deepEqual(c.current.state.view,{...v,snapshotId:'new',rootId:'newroot',selectedNodeId:'mapped'});}finally{globalThis.fetch=old;}});
// Echoed selectors are independently validated rather than trusting a matching snapshot alone.
test('hierarchy and pending responses reject mismatched filter mode and query budgets',async()=>{setup();const pin={profileId:'p',snapshotId:'s',viewId:'lease',nodeId:null};const summary={knownBytes:'0',unknownFileCount:'0',fileCount:'0',directoryCount:'0',specialCount:'0'};const good={snapshotId:'s',rootId:'root',metric:'logical',mode:'highlight',filter:{categories:[],field:'both'},depth:1,elementBudget:200,nodes:[],truncated:false,state:'complete',originalSummary:summary,matchSummary:summary,denominator:summary};assert.ok(await query.readAnalysis(view(),pin,new AbortController().signal,async()=>response(good)));for(const patch of [{mode:'hits-only'},{filter:{categories:['image']}},{depth:2},{elementBudget:100}])await assert.rejects(query.readAnalysis(view(),pin,new AbortController().signal,async()=>response({...good,...patch})),/INVALID_RESPONSE/);await assert.rejects(query.readAnalysis(view(),pin,new AbortController().signal,async()=>response({state:'pending',query:{...good,filter:{categories:['audio']}}})),/INVALID_RESPONSE/);});

// A fully abandoned coalesced acquisition releases once, only after the last transport settles.
test('shared acquisitions release only the last abandoned claim and never an adopted claim',()=>{
 const registry=new acquisitions.ReaderAcquisitions();let releases=0;
 const a=registry.begin('same',()=>releases++),b=registry.begin('same',()=>releases++);
 a(false);a(false);assert.equal(releases,0);b(false);assert.equal(releases,1);
 const c=registry.begin('next',()=>releases++),d=registry.begin('next',()=>releases++);
 c(true);d(false);assert.equal(releases,1);
 const e=registry.begin('third',()=>releases++),f=registry.begin('third',()=>releases++);
 e(false);f(true);assert.equal(releases,1);
});

// Real long recorded paths may exceed the preference transport cap while remaining valid query pages.
test('search accepts bounded long paths and rejects query payload overflow',async()=>{setup();const v=view(),pin={profileId:'p',snapshotId:'s',viewId:'lease',nodeId:null};const summary={knownBytes:'0',unknownFileCount:'0',fileCount:'1',directoryCount:'0',specialCount:'0'};const page={snapshotId:'s',rootId:'root',metric:'logical',scope:'recursive',selectionOrder:'name-asc-recorded-rowid',items:Array.from({length:50},(_,i)=>({snapshotId:'s',nodeId:String(i),parentId:null,name:'name',path:'x'.repeat(2000),kind:'file',volumeId:'v',shareId:null,modifiedAt:null,size:null,summary})),nextCursor:'',hasMore:false,workLimited:false};assert.ok(JSON.stringify(page).length>65536);assert.equal((await query.readSearchList(v,pin,'',new AbortController().signal,async()=>response(page))).items.length,50);await assert.rejects(query.readSearchList(v,pin,'',new AbortController().signal,async()=>response({...page,padding:'x'.repeat(4*1024*1024)})),/INVALID_RESPONSE/);});
// Cancellation during allocation qualification must prevent both a query and late auth side effects.
test('aborted allocated qualification cannot dispatch hierarchy or report a late denial',async()=>{setup();const abort=new AbortController(),pin={profileId:'p',snapshotId:'s',viewId:'lease',nodeId:null};let calls=0;await assert.rejects(query.readAnalysis({...view(),metric:'allocated'},pin,abort.signal,async(_url,options)=>{calls++;assert.ok(options.signal);abort.abort();return new Response('{}',{status:403});}),e=>e.name==='AbortError');assert.equal(calls,1);});

// Invalid rendering limits must fail before even allocation qualification can contact the service.
test('render budgets reject invalid depth and map-specific caps before network',async()=>{
 setup();const pin={profileId:'p',snapshotId:'s',viewId:'lease',nodeId:null};let calls=0;
 const transport=async()=>{calls++;assert.fail('invalid budget dispatched network');};
 for(const map of ['treemap','radial']){
  const cap=map==='radial'?2000:10000;
  for(const budget of [{depth:0,elementBudget:1},{depth:9,elementBudget:1},{depth:1.5,elementBudget:1},{depth:NaN,elementBudget:1},{depth:1,elementBudget:0},{depth:1,elementBudget:cap+1},{depth:1,elementBudget:1.5},{depth:1,elementBudget:Infinity}]){
   await assert.rejects(query.readAnalysis({...view(),map,metric:'allocated'},pin,new AbortController().signal,transport,budget),/INVALID_RENDER_BUDGET/);
  }
 }
 assert.equal(calls,0);
});

// Both complete and pending responses must echo the actual budget, including each map's allowed maximum.
test('render budget maxima bind outgoing query and complete or pending receipts',async()=>{
 setup();const pin={profileId:'p',snapshotId:'s',viewId:'lease',nodeId:null};
 const zero={knownBytes:'0',unknownFileCount:'0',fileCount:'0',directoryCount:'0',specialCount:'0'};
 for(const [map,elementBudget] of [['radial',2000],['treemap',10000]]){
  const budget={depth:8,elementBudget},v={...view(),map};let sent;
  const invoke=reply=>query.readAnalysis(v,pin,new AbortController().signal,async(url,options)=>{
   assert.ok(url.endsWith('action=hierarchy'));sent=JSON.parse(options.body);
   assert.equal(sent.profileId,'p');assert.equal(sent.viewId,'lease');assert.equal(sent.query.depth,8);assert.equal(sent.query.elementBudget,elementBudget);
   return response(reply(sent.query));
  },budget);
  const complete=q=>({...q,nodes:[],truncated:false,state:'complete',originalSummary:zero,matchSummary:zero,denominator:zero});
  assert.equal((await invoke(complete)).elementBudget,elementBudget);
  assert.equal(await invoke(q=>({state:'pending',query:q})),null);
  for(const patch of [{depth:7},{elementBudget:elementBudget-1},{depth:undefined}]){
   await assert.rejects(invoke(q=>({...complete(q),...patch})),/INVALID_RESPONSE/);
   await assert.rejects(invoke(q=>({state:'pending',query:{...q,...patch}})),/INVALID_RESPONSE/);
  }
 }
});

// Preparation is authorized only by an existing lease and is never a navigation publication.
for(const outcome of ['confirm','fallback','error','cancel','new-lease'])test(`same-lease preparation ownership: ${outcome}`,async()=>{
 setup();const old=globalThis.fetch,pending=deferred(),events=[];try{
  globalThis.fetch=async(_url,options)=>{const b=JSON.parse(options.body);if(b.operation==='open')return response(reader(b.viewId,'other'));if(b.operation==='release')return response({});events.push('navigation');await pending.promise;if(outcome==='error')throw Error('transport');return response({...nav({...view(),snapshotId:b.snapshotId,rootId:outcome==='fallback'?'nas':b.rootId}),reasons:outcome==='fallback'?['ROOT_UNAVAILABLE']:[]});};
  const c=new Controller.AnalysisController(()=>events.push('publish'),()=>{});c.restored(view(),reader(),nav());events.length=0;
  let signal;const work=c.move({...view(),rootId:'child',...(outcome==='new-lease'?{snapshotId:'other'}:{})},'navigate',(target,pin,s)=>{assert.equal(target.rootId,'child');assert.equal(pin.viewId,'lease');signal=s;events.push('prepare');return{confirm:()=>events.push('confirm'),discard:()=>events.push('discard')};});
  await new Promise(r=>setImmediate(r));assert.equal(c.current.state.view.rootId,'root');assert.deepEqual(events,outcome==='new-lease'?['navigation']:['prepare','navigation']);
  if(outcome==='cancel'){c.cancel();assert.equal(signal.aborted,true);}pending.resolve();if(outcome==='error')await assert.rejects(work,/transport/);else await work;
  if(outcome==='confirm')assert.deepEqual(events,['prepare','navigation','confirm','publish']);
  if(outcome==='fallback')assert.deepEqual(events,['prepare','navigation','discard','publish']);
  if(['error','cancel'].includes(outcome)){assert.deepEqual(events,['prepare','navigation','discard']);assert.equal(c.current.state.view.rootId,'root');}
  if(outcome==='new-lease')assert.deepEqual(events,['navigation','publish']);
 }finally{globalThis.fetch=old;}
});

// Native return navigation retains only bounded live leases and still validates every target remotely.
for(const failure of ['none','expired','denied'])test(`retained reader return remains qualified: ${failure}`,async()=>{
 setup();const old=globalThis.fetch,calls=[];let fail=false;
 try{
  globalThis.fetch=async(url,options)=>{const b=JSON.parse(options.body);calls.push(b);
   if(url.endsWith('view-navigation')){if(fail&&b.profileId==='p'){fail=false;if(failure==='denied')return new Response('{}',{status:403});if(failure==='expired')return new Response(JSON.stringify({contractVersion:1,error:{code:'VIEW_EXPIRED'}}),{status:409});}return response(nav({...view(),...b}));}
   if(b.operation==='release')return response({});
   return response({...reader(b.viewId,b.snapshotId),lease:{id:b.viewId,snapshotId:b.snapshotId,expiresAt:new Date(Date.now()+120000).toISOString()}});
  };
  const c=new Controller.AnalysisController(()=>{},()=>{},2),first={...reader(),lease:{id:'lease',snapshotId:'s',expiresAt:new Date(Date.now()+120000).toISOString()}};
  c.restored(view(),first,nav());await c.move({...view(),profileId:'other',snapshotId:'other-snapshot'});
  assert.equal(calls.filter(x=>x.operation==='release').length,0);fail=failure!=='none';let prepared=0,confirmed=0,discarded=0;
  const work=c.move(view(),'replace',()=>{prepared++;return {confirm:()=>confirmed++,discard:()=>discarded++};});
  if(failure==='denied'){await assert.rejects(work,/SESSION_REQUIRED/);assert.equal(c.current.pin.profileId,'other');}
  else{await work;assert.equal(c.current.pin.profileId,'p');}
  assert.equal(prepared,1);assert.equal(confirmed,failure==='none'?1:0);assert.equal(discarded,failure==='none'?0:1);
  assert.equal(calls.filter(x=>x.operation==='open').length,failure==='expired'?2:1,'only expired retained readers reacquire; denial never retries');
  for(let i=0;i<4;i++)await c.move({...view(),profileId:'p'+i,snapshotId:'s'+i});
  assert.ok(c.retained.size<=2);const before=calls.filter(x=>x.operation==='release').length,retained=c.retained.size;c.releaseRetained();assert.equal(c.retained.size,0);assert.equal(calls.filter(x=>x.operation==='release').length,before+retained);
 }finally{globalThis.fetch=old;}
});

// An aborted acquisition may already exist server-side even if its response never reaches the caller.
test('cancelled reader acquisition releases its intent before any reader response is adopted',async()=>{
 setup();const old=globalThis.fetch,pending=deferred(),calls=[];
 try{
  globalThis.fetch=async(_url,options)=>{const b=JSON.parse(options.body);calls.push(b);if(b.operation==='open'){await pending.promise;return response(reader(b.viewId,b.snapshotId));}return response({});};
  const c=new Controller.AnalysisController(()=>{},()=>{},3);c.restored(view(),reader(),nav());
  const work=c.move({...view(),profileId:'other',snapshotId:'other'});await new Promise(r=>setImmediate(r));c.cancel();pending.resolve();await work;
  assert.equal(c.current.pin.profileId,'p');const open=calls.find(x=>x.operation==='open'),releases=calls.filter(x=>x.operation==='release');assert.equal(releases.length,1);assert.equal(releases[0].viewId,open.viewId);assert.equal(releases[0].profileId,'other');
 }finally{globalThis.fetch=old;}
});

// Transient navigation retries are bounded and never apply to denial or a superseded selection.
test('navigation retries 503 with the same qualified identity and stops after three attempts',async()=>{
 setup();let calls=0;const sent=[];
 const fetcher=async(_url,options)=>{sent.push(options.body);return ++calls<3?new Response('',{status:503}):response(nav());};
 await navigation.requestNavigation('p','lease','s','root',null,new AbortController().signal,fetcher);
 assert.equal(calls,3);assert.equal(new Set(sent).size,1);
 calls=0;await assert.rejects(navigation.requestNavigation('p','lease','s','root',null,new AbortController().signal,async()=>{calls++;return new Response(JSON.stringify({contractVersion:1,error:{code:'QUERY_BUDGET_EXCEEDED'}}),{status:503});}),/QUERY_BUDGET_EXCEEDED/);assert.equal(calls,3);
});
test('navigation backoff cancels and authorization denial never retries',async()=>{
 setup();let calls=0;const abort=new AbortController();
 const pending=navigation.requestNavigation('p','lease','s','root',null,abort.signal,async()=>{calls++;setTimeout(()=>abort.abort(),10);return new Response('',{status:503});});
 await assert.rejects(pending,/VIEW_REQUEST_ABORTED/);assert.equal(calls,1);
 calls=0;await assert.rejects(navigation.requestNavigation('p','lease','s','root',null,new AbortController().signal,async()=>{calls++;return new Response('',{status:403});}),/SESSION_REQUIRED/);assert.equal(calls,1);
});
