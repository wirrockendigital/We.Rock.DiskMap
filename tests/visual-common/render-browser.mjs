import assert from 'node:assert/strict';
import {readFile,stat} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createServer} from '../../web/node_modules/vite/dist/node/index.js';

// Explicit modes distinguish the historical infrastructure probe from the real production React renderers.
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
const rendererMode=process.env.DISKMAP_RENDER_MODE??'infrastructure';assert.ok(['infrastructure','production'].includes(rendererMode));
const fixturePath=process.env.DISKMAP_RENDER_HIERARCHY;
const mapKind=process.env.DISKMAP_RENDER_KIND??'treemap';assert.ok(['treemap','radial'].includes(mapKind));
let wire,fixtureSHA=null;
if(fixturePath){
 assert.ok((await stat(fixturePath)).size<=4*1024*1024,'hierarchy response exceeds wire budget');
 const raw=await readFile(fixturePath);fixtureSHA=createHash('sha256').update(raw).digest('hex');
 const parsed=JSON.parse(raw);wire=parsed.data??parsed;
}else{
 // The fallback is deliberately labelled synthetic and cannot qualify the ten-million-entry case.
 const summary=(files)=>({knownBytes:String(files),unknownFileCount:'0',fileCount:String(files),directoryCount:'0',specialCount:'0'});
 const root=summary(1999),node=(id,parentId,kind,s)=>({id,parentId,realNodeId:kind==='nas'?null:id,name:id,kind,virtual:kind==='nas',summary:s,originalSummary:s,matchSummary:s,matchKind:kind==='nas'?'ancestor':'direct',hasMore:false,classification:null,modifiedAt:null});
 wire={snapshotId:'synthetic-render',rootId:'root',metric:'logical',mode:'highlight',state:'complete',selectionOrder:'recorded-rowid',truncated:false,depth:8,elementBudget:10000,filter:{categories:[]},originalSummary:root,matchSummary:root,denominator:root,matchState:'matches',coverage:[],sourceAvailability:{total:'0',truncated:false,records:[]},nodes:[node('root',null,'nas',root),...Array.from({length:1999},(_,i)=>node('file-'+i,'root','file',summary(1)))]};
}
assert.ok(Buffer.byteLength(JSON.stringify(wire))<=4*1024*1024,'fixture exceeds the actual response byte budget');
const run=promisify(execFile),samples=[];let sampling=false,browserServer,browser,server,timer;
// RSS is a sampled OS process-tree sum, with shared pages potentially counted more than once.
async function sample(){
 if(sampling||!browserServer)return;sampling=true;
 try{
  const {stdout}=await run('ps',['-axo','pid=,ppid=,rss=']);
  const rows=stdout.trim().split('\n').map(s=>s.trim().split(/\s+/).map(Number));
  const ids=new Set([browserServer.process().pid]);let changed=true;
  while(changed){changed=false;for(const [pid,parent] of rows)if(ids.has(parent)&&!ids.has(pid)){ids.add(pid);changed=true;}}
  const present=rows.filter(([pid])=>ids.has(pid));
  if(present.length)samples.push({at:Date.now(),bytes:present.reduce((sum,r)=>sum+r[2]*1024,0),processes:present.length,perProcess:present.map(([pid,parent,rss])=>({pid,parent,bytes:rss*1024}))});
 }finally{sampling=false;}
}
// vmmap physical footprint excludes clean shared mappings; report unavailable observations explicitly.
async function footprint(){
 await sample();const tree=samples.at(-1)?.perProcess??[],out=[];
 for(const entry of tree){
  let kind=entry.pid===browserServer.process().pid?'browser':'helper';
  try{const {stdout}=await run('ps',['-p',String(entry.pid),'-o','args=']);kind=stdout.match(/--type=([^ ]+)/)?.[1]??kind;}catch{}
  if(process.platform!=='darwin'){out.push({...entry,kind,physicalBytes:null});continue;}
  try{
   const {stdout}=await run('/usr/bin/vmmap',['-summary',String(entry.pid)],{timeout:3000,maxBuffer:1024*1024});
   const peak=stdout.match(/^Physical footprint \(peak\):\s*([\d.]+)([KMGT]?)\s*$/m);
   const found=stdout.match(/^Physical footprint:\s*([\d.]+)([KMGT]?)\s*$/m);
   out.push({...entry,kind,physicalPeakBytes:peak?Math.round(Number(peak[1])*1024**(' KMGT'.indexOf(peak[2])<0?0:' KMGT'.indexOf(peak[2]))):null,physicalBytes:found?Math.round(Number(found[1])*1024**(' KMGT'.indexOf(found[2])<0?0:' KMGT'.indexOf(found[2]))):null});
  }catch{out.push({...entry,kind,physicalBytes:null});}
 }
 return out;
}
// Paint stripes only to exercise the real backing store; this is not treemap/radial geometry.
const moduleSource=`
import {adaptHierarchy} from '/src/visualization/shared/adapter.ts';
import {configureCanvas} from '/src/visualization/shared/canvas-surface.ts';
import {renderBudget,animationDuration} from '/src/visualization/shared/render-budget.ts';
import {LatestRenderCoordinator} from '/src/visualization/shared/render-coordinator.ts';
const wire=await(await fetch('/fixture.json')).json();
const model=adaptHierarchy(wire,{snapshotId:wire.snapshotId,rootId:wire.rootId,metric:wire.metric,mode:wire.mode,maxNodes:10000});
const canvas=document.querySelector('canvas');
window.probe={nodes:model.order.length,weight:model.denominator.knownBytes.toString(),outputs:[],failures:[],started:[],aborted:0,active:0,maxActive:0};
const p=window.probe;
const coordinator=new LatestRenderCoordinator(async(input,signal)=>{
 p.active++;p.maxActive=Math.max(p.maxActive,p.active);p.started.push(input.id);
 try{
  const g=configureCanvas(canvas,input.width,input.height,devicePixelRatio);
  const context=canvas.getContext('2d');context.fillStyle='#164e63';
  for(let i=0;i<model.order.length;i++){if(signal.aborted)break;context.fillRect(i%Math.max(1,input.width),i%Math.max(1,input.height),1,1);}
  if(input.hold)await new Promise(resolve=>{p.heldId=input.id;p.settle=resolve;});
  if(signal.aborted)p.aborted++;
  if(input.reject)throw Error('late-synthetic-error');
  return {id:input.id,geometry:g,budget:renderBudget({kind:${JSON.stringify(mapKind)},detail:'max',width:input.width,height:input.height})};
 }finally{p.active--;}
},value=>p.outputs.push(value),error=>p.failures.push(String(error)));
p.request=(id,width,height,hold=false,reject=false)=>coordinator.request({id,width,height,hold,reject});
p.dispose=()=>coordinator.dispose();p.reduced=animationDuration(matchMedia('(prefers-reduced-motion: reduce)').matches);p.ready=true;
`;
// The opt-in production path mounts the same React components as the application host.
const productionModule=`
import React from '/node_modules/.vite/deps/react.js';
import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';
import {RadialCanvas} from '/src/visualization/radial/RadialCanvas.tsx';
import {TreemapCanvas} from '/src/visualization/treemap/TreemapCanvas.tsx';
import {adaptHierarchy} from '/src/visualization/shared/adapter.ts';
import {canvasGeometry} from '/src/visualization/shared/canvas-surface.ts';
import {renderBudget,animationDuration} from '/src/visualization/shared/render-budget.ts';
import {LatestRenderCoordinator} from '/src/visualization/shared/render-coordinator.ts';
const wire=await(await fetch('/fixture.json')).json();
const initializationStarted=performance.now();
const model=adaptHierarchy(wire,{snapshotId:wire.snapshotId,rootId:wire.rootId,metric:wire.metric,mode:wire.mode,maxNodes:10000});
const drillID=model.order.find(id=>id!==model.rootId&&model.nodes[id].kind==='directory'&&model.nodes[id].children.length);
let drillModel=model;if(drillID){const ids=new Set([drillID]),queue=[drillID];for(let i=0;i<queue.length;i++)for(const id of model.nodes[queue[i]].children){ids.add(id);queue.push(id);}const node=model.nodes[drillID];drillModel=adaptHierarchy({...wire,rootId:drillID,nodes:wire.nodes.filter(n=>ids.has(n.id)),originalSummary:wire.nodes.find(n=>n.id===drillID).originalSummary,matchSummary:wire.nodes.find(n=>n.id===drillID).matchSummary,denominator:wire.nodes.find(n=>n.id===drillID)[wire.mode==='hits-only'?'matchSummary':'originalSummary']},{snapshotId:wire.snapshotId,rootId:drillID,metric:wire.metric,mode:wire.mode,maxNodes:10000});}
let painted=null;
const root=ReactDOM.createRoot(document.getElementById('app'));
window.probe={nodes:model.order.length,weight:model.denominator.knownBytes.toString(),outputs:[],failures:[],started:[],aborted:0,active:0,maxActive:0};const p=window.probe;
const coordinator=new LatestRenderCoordinator(async(input,signal)=>{
 p.active++;p.maxActive=Math.max(p.maxActive,p.active);p.started.push(input.id);
 try{
  const viewModel=input.drill?drillModel:model;
  const kind=input.kind??${JSON.stringify(mapKind)},chartHeight=kind==='radial'?Math.max(1,input.height-Math.min(104,input.height/3)):input.height;
  const geometry=canvasGeometry(input.width,chartHeight,devicePixelRatio);
  let segments=0;
  await new Promise((resolve,reject)=>{let settled=false;const timer=setTimeout(()=>{if(!settled){settled=true;reject(Error('PRODUCTION_PAINT_TIMEOUT'));}},5000);
   const onGeometry=g=>{if(!g.length||settled)return;segments=g.length;painted={kind,model:viewModel,width:input.width,height:input.height,geometry:g};requestAnimationFrame(()=>requestAnimationFrame(()=>{if(!settled){settled=true;clearTimeout(timer);resolve();}}));};
   root.render(React.createElement(kind==='radial'?RadialCanvas:TreemapCanvas,{model:viewModel,width:input.width,height:input.height,devicePixelRatio:devicePixelRatio,colorMode:'filetype',referenceTime:null,selectedNodeId:null,focusedNodeId:null,disabled:false,language:'en',theme:'light',maxDepth:8,maxSegments:2000,maxElements:10000,canGoUp:false,onUp:()=>{},onGeometry}));
   if(painted?.kind===kind&&painted.model===viewModel&&painted.width===input.width&&painted.height===input.height)onGeometry(painted.geometry);
  });
  const canvas=document.querySelector('canvas');if(!canvas||canvas.width!==geometry.pixelWidth||canvas.height!==geometry.pixelHeight)throw Error('WRONG_CANVAS_GEOMETRY');
  if(segments>(kind==='radial'?2000:10000))throw Error('SEGMENT_BUDGET');
  const elapsed=performance.now()-input.requestedAt;p.firstMapMillis??=performance.now()-initializationStarted;
  if(input.hold)await new Promise(resolve=>{p.heldId=input.id;p.settle=resolve;});
  if(signal.aborted)p.aborted++;if(input.reject)throw Error('late-synthetic-error');
  return{id:input.id,kind,geometry,budget:renderBudget({kind,detail:'max',width:input.width,height:input.height}),segments,visibleMillis:elapsed,weight:viewModel.denominator.knownBytes.toString(),rootId:viewModel.rootId,snapshotId:model.snapshotId};
 }finally{p.active--;}
},value=>p.outputs.push(value),error=>p.failures.push(String(error)));
p.request=(id,width,height,hold=false,reject=false,kind,drill=false)=>coordinator.request({id,width,height,hold,reject,kind,drill,requestedAt:performance.now()});
p.dispose=()=>{coordinator.dispose();root.unmount();};p.reduced=animationDuration(matchMedia('(prefers-reduced-motion: reduce)').matches);p.drillAvailable=!!drillID;p.ready=true;
`;

try{
 server=await createServer({root:new URL('../../web/',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});
 await server.listen();
 browserServer=await chromium.launchServer({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
 browser=await chromium.connect(browserServer.wsEndpoint());
 timer=setInterval(()=>{void sample().catch(()=>{});},100);
 const context=await browser.newContext({viewport:{width:1920,height:1080},deviceScaleFactor:2,reducedMotion:'reduce'});
 // Let the isolated blank browser finish its initial helper handoff before the baseline observation.
 const page=await context.newPage(),errors=[];await page.waitForTimeout(500);await sample();const baselineRSS=samples.at(-1)?.bytes??null;const baselineFootprints=await footprint();page.on('pageerror',error=>errors.push(error.message));
 // Intercept only fixture resources; production TypeScript modules are served by Vite.
 const origin='http://127.0.0.1:'+server.httpServer.address().port;
 await page.route(origin+'/render-harness.mjs',route=>route.fulfill({contentType:'application/javascript',body:rendererMode==='production'?productionModule:moduleSource}));
 await page.route(origin+'/fixture.json',route=>route.fulfill({contentType:'application/json',body:JSON.stringify(wire)}));
 await page.route(origin+'/render-fixture',async route=>route.fulfill({contentType:'text/html',body:await server.transformIndexHtml('/render-fixture','<!doctype html><html><body style="margin:0"><main id="app"></main>'+(rendererMode==='infrastructure'?'<canvas aria-label="Infrastructure probe"></canvas>':'')+'<script type="module" src="/render-harness.mjs"></script></body></html>')}));
 await page.goto(origin+'/render-fixture');
 try{await page.waitForFunction(()=>window.probe?.ready,{},{timeout:10000});}catch(error){throw Error('Browser initialization failed: '+errors.join('; ')+'; '+error.message);}
 await page.evaluate(()=>probe.request('native-4k',1920,1080));
 await page.waitForFunction(()=>probe.outputs.some(o=>o.id==='native-4k'));
 const native4K=await page.evaluate(()=>probe.outputs.find(o=>o.id==='native-4k'));
 assert.equal(native4K.geometry.pixelWidth,3840);assert.equal(native4K.geometry.pixelHeight,rendererMode==='production'&&mapKind==='radial'?1952:2160);assert.equal(native4K.geometry.limited,false);
 const native4KFootprints=await footprint();
 await page.setViewportSize({width:3840,height:2160});
 await page.evaluate(()=>probe.request('4k',3840,2160));
 await page.waitForFunction(()=>probe.outputs.some(o=>o.id==='4k'));
 await sample();
 const highDPIFootprints=await footprint();
 const highDPI=await page.evaluate(()=>probe.outputs.find(o=>o.id==='4k'));
 assert.ok(highDPI.geometry.estimatedBytes<=64*1024*1024);assert.equal(highDPI.geometry.limited,true);
 await page.setViewportSize({width:320,height:700});
 await page.evaluate(()=>probe.request('narrow',320,700));await page.waitForFunction(()=>probe.outputs.some(o=>o.id==='narrow'));
 await page.evaluate(()=>{for(let i=0;i<1000;i++)probe.request('storm-'+i,320+i%400,700);});
 await page.waitForFunction(()=>probe.outputs.some(o=>o.id==='storm-999'));
 assert.equal(await page.evaluate(()=>probe.started.filter(x=>x.startsWith('storm-')).length),1);
 const stormFootprints=await footprint();
 for(const reject of [false,true]){
  await page.evaluate(reject=>probe.request('obsolete-'+reject,320,700,true,reject),reject);
  await page.waitForFunction(reject=>probe.heldId==='obsolete-'+reject,reject);
  await page.evaluate(reject=>{probe.request('latest-'+reject,400,700);probe.settle();},reject);
  await page.waitForFunction(reject=>probe.outputs.some(o=>o.id==='latest-'+reject),reject);
 }
 // Twenty actual component switches per direction measure request-to-visible-paint, including frame scheduling.
 let productionTiming=null,drillTiming=null;
 if(rendererMode==='production'){
  const target=mapKind,other=target==='radial'?'treemap':'radial';
  for(let i=0;i<20;i++)for(const kind of [other,target]){
   const id='switch-'+kind+'-'+i;await page.evaluate(({id,kind})=>probe.request(id,1024,700,false,false,kind),{id,kind});
   await page.waitForFunction(id=>probe.outputs.some(o=>o.id===id),id);
  }
  const values=await page.evaluate(()=>probe.outputs.filter(o=>o.id.startsWith('switch-')));
  productionTiming=Object.fromEntries(['treemap','radial'].map(kind=>{const samples=values.filter(x=>x.kind===kind).map(x=>x.visibleMillis).sort((a,b)=>a-b);return[kind,{samples:samples.length,p95:samples[Math.ceil(samples.length*.95)-1],max:samples.at(-1)}];}));
  assert.ok(values.every(v=>v.weight===String(wire.denominator?.knownBytes??wire.originalSummary.knownBytes)&&v.snapshotId===wire.snapshotId&&v.rootId===wire.rootId),'renderer identity or exact sums changed');
  if(await page.evaluate(()=>probe.drillAvailable)){
   for(let i=0;i<20;i++){const id='drill-'+i;await page.evaluate(({id,kind,drill})=>probe.request(id,1024,700,false,false,kind,drill),{id,kind:mapKind,drill:i%2===0});await page.waitForFunction(id=>probe.outputs.some(o=>o.id===id),id);}
   const times=await page.evaluate(()=>probe.outputs.filter(o=>o.id.startsWith('drill-')).map(o=>o.visibleMillis).sort((a,b)=>a-b));drillTiming={samples:times.length,p95:times[Math.ceil(times.length*.95)-1],max:times.at(-1),scope:'already delivered qualified DTO, frontend root/layout/paint only;20alternations(10drill,10back)'};
  }

 }
 await page.evaluate(()=>probe.request('disposed',400,700,true,true));await page.waitForFunction(()=>probe.heldId==='disposed');
 await page.evaluate(()=>{probe.dispose();probe.settle();probe.request('forbidden',400,700);});
 await page.waitForFunction(()=>probe.active===0);await sample();
 const evidence=await page.evaluate(()=>({nodes:probe.nodes,weight:probe.weight,outputs:probe.outputs,started:probe.started,aborted:probe.aborted,maxActive:probe.maxActive,failures:probe.failures,reduced:probe.reduced,firstMapMillis:probe.firstMapMillis??null,heapBytes:performance.memory?.usedJSHeapSize??null}));
 assert.equal(evidence.maxActive,1);assert.equal(evidence.reduced,0);assert.deepEqual(evidence.failures,[]);assert.deepEqual(errors,[]);
 assert.ok(!evidence.outputs.some(o=>o.id.startsWith('obsolete')||o.id==='disposed'));assert.ok(!evidence.started.includes('forbidden'));
 assert.ok(samples.length>0,'OS memory observation unavailable');
 const finalFootprints=await footprint();
 const footprintStages=[baselineFootprints,native4KFootprints,highDPIFootprints,stormFootprints,finalFootprints];
 const footprintAvailable=footprintStages.every(stage=>stage.length>0&&stage.every(p=>p.physicalBytes!==null&&p.physicalPeakBytes!==null));
 const sampledPeakPhysical=footprintAvailable?Math.max(...footprintStages.map(stage=>stage.reduce((sum,p)=>sum+p.physicalBytes,0))):null;
 // Sum process lifetime peaks conservatively; they need not have occurred simultaneously.
 const processPeaks=new Map();for(const stage of footprintStages)for(const p of stage)if(p.physicalPeakBytes!==null)processPeaks.set(p.pid,Math.max(processPeaks.get(p.pid)??0,p.physicalPeakBytes));
 const physicalPeakUpperSum=footprintAvailable?[...processPeaks.values()].reduce((a,b)=>a+b,0):null;
 const sampledPeakRSS=Math.max(...samples.map(s=>s.bytes));
 console.log(JSON.stringify({fixture:fixturePath?'external-qualified-response':'synthetic-2000-infrastructure-only',fixtureSHA,mapKind,rendererMode,productionTiming,drillTiming,platform:process.platform,baselineFootprints,native4KFootprints,highDPIFootprints,stormFootprints,finalFootprints,browser:browser.version(),dpr:2,native4K,stress8K:highDPI,narrow:true,resizeStorm:1000,staleSuccessAndErrorDiscarded:true,disposed:true,evidence,sampledPeakRSS,baselineRSS,peakProcesses:samples.find(s=>s.bytes===sampledPeakRSS).perProcess,sampleCount:samples.length,rssMethod:'OS ps RSS KiB converted to bytes, isolated browser process tree; sampled, shared pages may be double-counted',rawRSSGate:sampledPeakRSS<512*1024*1024,sampledPeakPhysical,physicalPeakUpperSum,memoryGate:physicalPeakUpperSum!==null&&physicalPeakUpperSum<512*1024*1024,memoryMethod:'macOS vmmap physical footprint, entire isolated process tree including baseline; conservative sum of lifetime process peaks, unavailable fails closed',scope:rendererMode==='production'?'actual React map renderer with recorded 10M query DTO when supplied; local browser only, no native DSM performance claim':'shared canvas/coordinator/adapter only; no finished map renderer or native DSM performance claim'}));
 if(rendererMode==='production')assert.ok(Number.isFinite(evidence.firstMapMillis)&&evidence.firstMapMillis<3000,'first frontend map exceeds3s or has no timing');
 if(drillTiming)assert.ok(drillTiming.p95<1000,'frontend drill p95 exceeds1s');
 if(productionTiming)for(const value of Object.values(productionTiming)){assert.equal(value.samples,20);assert.ok(value.p95<=200,'production switch p95 exceeds200ms');assert.ok(value.max<1000,'production layout/paint exceeds1s');}
 assert.ok(physicalPeakUpperSum!==null&&physicalPeakUpperSum<512*1024*1024,'OS physical-footprint evidence unavailable or exceeds 512 MiB');
}finally{
 clearInterval(timer);await browser?.close();await browserServer?.close();await server?.close();
}
