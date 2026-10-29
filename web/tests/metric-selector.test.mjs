import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
// Compile the actual host and provider; synthetic transports never replace their identity or lifecycle logic.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/metric-'));let Workbench,query;
try{cpSync(join(web,'src'),join(tmp,'src'),{recursive:true});writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--resolveJsonModule','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'src/Workbench.tsx')],{stdio:'pipe'});const require=createRequire(import.meta.url);({Workbench}=require(join(tmp,'out/Workbench.js')));query=require(join(tmp,'out/analysis-query.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
const response=data=>new Response(JSON.stringify({contractVersion:1,data}));
const qualification='g06-dsm90080-kvmx64-st-blocks512-v1';
const sizes=(rootId='nas',allocated=true)=>({snapshotId:'s',rootId,defaultMetric:'logical',logical:{selectable:true,knownBytes:'18446744073709551617',unknownFileCount:'1',method:'st_size',qualification:null,reason:null},allocated:allocated?{selectable:true,knownBytes:'18446744073709551616',unknownFileCount:'2',method:'st_blocks*512',qualification,reason:null}:{selectable:false,knownBytes:null,unknownFileCount:null,method:null,qualification:null,reason:'METHOD_UNQUALIFIED'}});
const find=(tree,predicate)=>{if(Array.isArray(tree))return tree.map(x=>find(x,predicate)).find(Boolean);if(React.isValidElement(tree)){if(predicate(tree))return tree;return find(tree.props.children,predicate);}return null;};
const named=(tree,name)=>find(tree,e=>e.type?.name===name);
const selector=tree=>find(tree,e=>e.type==='select'&&e.props['aria-describedby']==='size-metric-explanation');
const tick=()=>new Promise(r=>setImmediate(r));
const summary={knownBytes:'0',unknownFileCount:'0',fileCount:'0',directoryCount:'0',specialCount:'0'};
// Hook scheduling retains real effects, cleanup and captured handlers while avoiding unrelated child requests.
const hooks=()=>({states:[],refs:[],effects:[],render(fn,props,run=true){let si=0,ri=0,ei=0;const scheduled=[];React.useState=initial=>{const i=si++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=initial=>this.refs[ri++]??={current:initial};React.useEffect=(effect,deps)=>{const i=ei++;if(run&&(!this.effects[i]||deps.some((v,j)=>v!==this.effects[i].deps[j])))scheduled.push(()=>{this.effects[i]?.cleanup?.();this.effects[i]={deps,cleanup:effect()};});};const tree=fn(props);scheduled.forEach(f=>f());return tree;},close(){this.effects.forEach(e=>e?.cleanup?.());}});
async function fixture(run,language='en'){
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window,document:globalThis.document,setInterval:globalThis.setInterval,clearInterval:globalThis.clearInterval,ResizeObserver:globalThis.ResizeObserver};const wb=hooks(),pv=hooks(),calls=[],writes=[];let denial=0;
 const f={allocated:true,pending:[],hold:false,fail:null,calls,writes};
 try{
 // The actual query lifecycle requires an attached, positively measured content box.
 globalThis.ResizeObserver=class{constructor(callback){this.callback=callback;}observe(target){this.callback([{target,contentRect:{width:900,height:500}}]);}disconnect(){}};
 globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'synthetic'}}}}};globalThis.document={visibilityState:'visible'};globalThis.setInterval=()=>1;globalThis.clearInterval=()=>{};
 globalThis.fetch=async(url,o)=>{const action=new URL(url,'https://test.invalid').searchParams.get('action'),b=JSON.parse(o.body??'{}');calls.push({action,b});
 if(action==='size-metrics'){if(f.hold)return new Promise((resolve,reject)=>f.pending.push({resolve,reject,b,signal:o.signal}));if(f.fail)return new Response(JSON.stringify({contractVersion:1,error:{code:f.fail===409?'VIEW_EXPIRED':'SERVICE_UNAVAILABLE'}}),{status:f.fail});return response(sizes(b.rootId,f.allocated));}
 if(action==='view')return response({view:{state:'complete',startedAt:'2026-10-10T00:00:00Z',endedAt:'2026-10-10T00:01:00Z',snapshotId:'s',nodes:[],truncated:false,scanBoundaries:[],scanBoundaryCount:'0',scanBoundariesTruncated:false,packageUsage:{logicalBytes:'0',allocatedBytes:'0',measuredAt:'2026-10-10T00:00:00Z',state:'complete',method:'package-state-stat'},profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'Synthetic'},lease:{id:b.viewId,snapshotId:'s',expiresAt:'2099-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});
 if(action==='view-navigation'&&f.holdNavigation)return new Promise(resolve=>{f.releaseNavigation=()=>resolve(response({snapshotId:b.snapshotId,rootId:b.rootId,parentId:null,selectedNodeId:b.selectedNodeId,reasons:[],breadcrumb:[{id:b.rootId,name:'Synthetic',kind:'nas'}]}));});
 if(action==='search')return response({snapshotId:b.query.snapshotId,rootId:b.query.rootId,metric:b.query.metric,scope:b.query.scope,selectionOrder:`${b.query.sort}-${b.query.direction}-recorded-rowid`,items:[],hasMore:false,nextCursor:'',workLimited:false});
 if(action==='view-navigation')return response({snapshotId:b.snapshotId,rootId:b.rootId,parentId:null,selectedNodeId:b.selectedNodeId,reasons:[],breadcrumb:[{id:b.rootId,name:'Synthetic',kind:'nas'}]});
 if(action==='hierarchy')return response({...b.query,state:'complete',truncated:false,selectionOrder:'recorded-rowid',coverage:[],sourceAvailability:{total:'0',truncated:false,records:[]},originalSummary:summary,matchSummary:summary,denominator:summary,matchState:'empty',nodes:[{id:b.query.rootId,parentId:null,realNodeId:null,name:'Synthetic',kind:'nas',virtual:true,summary,originalSummary:summary,matchSummary:summary,matchKind:'ancestor',classification:null,hasMore:false}]});
 throw Error('unexpected transport '+action);
 };
 const props={language,selected:'p',onSelect(){},denied(){denial++;}};wb.render(Workbench,props,false);wb.states[0]={dsmBuild:'90080',profiles:[{profile:{id:'p',name:'Synthetic',active:true,scope:{kind:'volumes',targetIds:['v']}},latestJobId:null}]};
 const viewport={};const element=()=>named(wb.render(Workbench,props,false),'ProfileView');f.render=()=>{const e=element();let tree;for(let i=0;i<3;i++){tree=pv.render(e.type,{...e.props,saveView:()=>writes.push(structuredClone(e.props.controller.current.state.view))});find(tree,node=>node.props.className==='map-viewport')?.props.ref(viewport);}return tree;};f.controller=element().props.controller;f.denials=()=>denial;f.close=()=>pv.close();
 // Flush the real hierarchy -> admission -> availability effects, including their microtasks.
 // A fixed bounded number of turns cannot resolve held transport promises or bypass a gate.
 f.flush=async()=>{let tree;for(let i=0;i<6;i++){tree=f.render();await tick();}return f.render();};
 await f.flush();await run(f);
 }finally{pv.close();wb.close();Object.assign(React,{useState:saved.useState,useRef:saved.useRef,useEffect:saved.useEffect});for(const k of ['fetch','window','document','setInterval','clearInterval','ResizeObserver'])globalThis[k]=saved[k];}
}
for(const language of ['de','en'])test(`actual ${language} selector preserves the complete view in both directions without scan`,()=>fixture(async f=>{
 const v={...f.controller.current.state.view,selectedNodeId:'selected',filter:{categories:['image']},search:{text:'literal',field:'name',scope:'direct'},sort:'mtime',direction:'asc',map:'radial'};
 await f.controller.move(v);let tree=await f.flush();const select=selector(tree);assert.ok(named(tree,'RadialCanvas')?.props.model,'qualified hierarchy must supply the real map');const callStart=f.calls.length;assert.ok(select);assert.equal(select.props.disabled,false);assert.equal(select.props.children[1].props.disabled,false);
 for(const metric of ['allocated','logical']){selector(tree).props.onChange({target:{value:metric}});tree=await f.flush();assert.deepEqual(f.controller.current.state.view,{...v,metric});assert.equal(named(tree,'SearchResultList').props.view.metric,metric);await named(tree,'SearchResultList').props.loadPage('',new AbortController().signal);assert.equal(f.calls.findLast(x=>x.action==='search').b.query.metric,metric);assert.equal(named(tree,'RadialCanvas').props.model.metric,metric);assert.equal(named(tree,'Legend').props.metric,metric);assert.equal(named(tree,'AnalysisWorkspace').props.details.props.selection.metric,metric);tree=await f.flush();}
 assert.equal(f.writes.length,2);assert.ok(f.calls.filter(x=>x.action==='hierarchy').some(x=>x.b.query.metric==='allocated'));assert.ok(f.calls.slice(callStart).every(x=>['view','view-navigation','hierarchy','size-metrics','search'].includes(x.action)));assert.equal(f.denials(),0);
},language));
test('loading and unqualified allocation block synthetic change events; logical stays selected',()=>fixture(async f=>{
 f.allocated=false;await f.controller.move({...f.controller.current.state.view,rootId:'next'});let tree=f.render();assert.equal(selector(tree).props.children[1].props.disabled,true);selector(tree).props.onChange({target:{value:'allocated'}});tree=await f.flush();assert.ok(f.calls.some(x=>x.action==='size-metrics'&&x.b.rootId==='next'),'unqualified response must actually be read');assert.ok(named(tree,'TreemapCanvas')?.props.model,'logical hierarchy remains available');assert.equal(selector(tree).props.children[1].props.disabled,true);assert.equal(f.controller.current.state.view.metric,'logical');assert.equal(f.writes.length,0);
}));
test('old availability success and late auth cannot cross a publication or session cleanup',()=>fixture(async f=>{
 f.hold=true;await f.controller.move({...f.controller.current.state.view,rootId:'old'});await f.flush();const old=f.pending.at(-1);const oldHandler=selector(f.render()).props.onChange;
 await f.controller.move({...f.controller.current.state.view,rootId:'new'});await f.flush();old.resolve(response(sizes('old')));await tick();oldHandler({target:{value:'allocated'}});assert.equal(f.controller.current.state.view.metric,'logical');assert.equal(selector(f.render()).props.children[1].props.disabled,true);
 const pending=f.pending.at(-1);f.close();pending.resolve(new Response('{}',{status:403}));await tick();assert.equal(f.denials(),0);assert.equal(f.writes.length,0);
}));
test('provider preserves unknown semantics, rejects foreign identities/methods and discards aborted auth',async()=>{
 const old=globalThis.window;globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'synthetic'}}}}};const v={profileId:'p',snapshotId:'s',rootId:'nas'},pin={profileId:'p',snapshotId:'s',viewId:'lease'};
 try{for(const enabled of [true,false])assert.equal((await query.readMetricAvailability(v,pin,new AbortController().signal,async()=>response(sizes('nas',enabled)))).allocated,enabled);
 for(const patch of [{rootId:'foreign'},{allocated:{...sizes().allocated,qualification:null}},{allocated:{...sizes('nas',false).allocated,knownBytes:'0'}}])await assert.rejects(query.readMetricAvailability(v,pin,new AbortController().signal,async()=>response({...sizes(),...patch})),/METRIC_UNAVAILABLE/);
 await assert.rejects(query.readMetricAvailability(v,{...pin,profileId:'foreign'},new AbortController().signal,async()=>assert.fail('no transport')),/SNAPSHOT_MISMATCH/);
 const c=new AbortController();await assert.rejects(query.readMetricAvailability(v,pin,c.signal,async()=>{c.abort();return new Response('{}',{status:403});}),e=>e.name==='AbortError');
 }finally{globalThis.window=old;}
});

// A pending owner blocks another metric intent; successful release restores the native select.
test('busy transition blocks repeated selection and expired availability disables the control',()=>fixture(async f=>{
 let tree=f.render();const initial=f.controller.current.state.view;f.holdNavigation=true;selector(tree).props.onChange({target:{value:'allocated'}});await tick();tree=f.render();assert.equal(selector(tree).props.disabled,true);selector(tree).props.onChange({target:{value:'logical'}});assert.deepEqual(f.controller.current.state.view,initial);f.holdNavigation=false;f.releaseNavigation();await f.flush();assert.equal(f.controller.current.state.view.metric,'allocated');assert.equal(f.writes.length,1);
 f.fail=409;await f.controller.move({...f.controller.current.state.view,rootId:'expired'});tree=await f.flush();assert.equal(selector(tree).props.disabled,true);assert.equal(f.writes.length,1);
}));
for(const status of [401,403])test(`current availability ${status} reaches the session boundary`,()=>fixture(async f=>{
 f.fail=status;await f.controller.move({...f.controller.current.state.view,rootId:'denied'});await f.flush();assert.equal(f.denials(),1);
}));
