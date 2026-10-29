import assert from 'node:assert/strict';
import {createServer} from '../../web/node_modules/vite/dist/node/index.js';
import {writeFile,rm} from 'node:fs/promises';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// This local browser fixture uses the real Workbench and controller; it is not a native DSM acceptance.
const entry=new URL('../../web/.scroll-navigation-fixture.mjs',import.meta.url);
await writeFile(entry,"import'./src/style.css';import React from'react';import ReactDOM from'react-dom/client';import{Workbench}from'./src/Workbench.tsx';window.SYNO={SDS:{Session:{SynoToken:'test-only'}}};function Fixture(){const[state,set]=React.useState({selected:''});return React.createElement(Workbench,{language:'en',...state,onSelect:selected=>set({selected}),onAnalysis:(selected,pin,rememberedView)=>set({selected,pin,rememberedView}),denied:()=>{throw Error('denied')}})}ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(Fixture));");
const server=await createServer({configFile:false,esbuild:{jsx:'automatic'},root:new URL('../../web/',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();const origin=`http://127.0.0.1:${server.httpServer.address().port}`;
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
const page=await browser.newPage({viewport:{width:1440,height:921}});page.setDefaultTimeout(10000);let releaseHierarchy=null,holdRoot=null;const results=[];const errors=[],calls=[];page.on('pageerror',e=>errors.push(e.message));
const base={profileId:'p',snapshotId:'s',rootId:'root',selectedNodeId:null,map:'treemap',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'name',direction:'asc'};
let pref={version:2,display:{units:'decimal',colorMode:'folder',detail:'auto',labels:true,percentages:true,hierarchyBorders:'normal',reducedMotion:'auto'},dsmUserId:'admin',revision:1,lastView:base,language:'auto',theme:'auto',preferredProfileId:'p',map:'treemap',locale:null,timeZone:null};
const savedProfiles=new Map();
const summary={knownBytes:'12',unknownFileCount:'0',fileCount:'1',directoryCount:'1',specialCount:'0'};
const view={snapshotId:'s',state:'complete',startedAt:'2026-10-10T00:00:00Z',endedAt:'2026-10-10T00:01:00Z',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'Profile',scanBoundaries:[],scanBoundaryCount:'0',scanBoundariesTruncated:false,packageUsage:{logicalBytes:'0',allocatedBytes:'0',measuredAt:'2026-10-10T00:00:00Z',state:'complete',method:'package-state-stat'}};
const reader=(id,profile='p')=>({view:{...view,snapshotId:profile==='p'?'s':'s2'},lease:{id,snapshotId:profile==='p'?'s':'s2',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});
const nav=(rootId,selectedNodeId=null)=>({snapshotId:'s',rootId:rootId==='nas'?'root':rootId,parentId:rootId==='directory'?'root':null,selectedNodeId,reasons:[],breadcrumb:[{id:'root',name:'Profile root',kind:'nas'},...(rootId==='directory'?[{id:'directory',name:'Directory',kind:'directory'}]:[])]});
try{
 await page.route(origin+'/fixture',async route=>route.fulfill({contentType:'text/html',body:await server.transformIndexHtml('/fixture','<div id="root"></div><script type="module" src="/.scroll-navigation-fixture.mjs"></script>')}));
 await page.route('**/api.cgi?**',async route=>{const req=route.request(),action=new URL(req.url()).searchParams.get('action'),body=req.method()==='POST'?req.postDataJSON():null;calls.push({action,body});let data;
  if(action==='preferences'){if(body){pref={...pref,revision:pref.revision+1,lastView:body.view.value};}data=pref;}
  else if(action==='profiles')data={dsmBuild:'90080',profiles:[...new Set(['p','q',...savedProfiles.keys()])].map(id=>({profile:savedProfiles.get(id)??{id,name:'Profile '+id,revision:1,active:true,scope:{kind:'nas',targetIds:[]},exclusions:[],resourceMode:'balanced',limits:{workers:1,ioBytesPerSecond:null,cpuPercent:null},freshnessSeconds:3600},activeSnapshotId:id==='p'?'s':'s2',latestJobId:null}))};
  else if(action==='scope-inventory')data={volumes:[{id:'volume',name:'Volume',state:'reachable'}],shares:[],observedAt:'2026-10-10T00:00:00Z'};
  else if(action==='profile-action'){const profile={...body.configuration,id:body.profileId??'created',revision:body.operation==='create'?1:body.expectedRevision+1};savedProfiles.set(profile.id,profile);data={operation:body.operation,profileId:profile.id,profile,cleanupScheduled:false,tasksRequireReview:false};}
  // Restore the scenario's selected renderer instead of silently forcing Treemap.
  else if(action==='view-restore')data={preferenceRevision:1,profileId:'p',resolvedView:pref.lastView,reader:reader(body.viewId),navigation:nav('root'),reasons:[]};
  else if(action==='view')data=reader(body.viewId,body.profileId);
  else if(action==='view-navigation')data={...nav(body.rootId,body.selectedNodeId),snapshotId:body.snapshotId};
  // The real hierarchy response includes its root and depth-limit residual groups.
  else if(action==='hierarchy'){
   const root=body.query.rootId,leaf={knownBytes:'12',unknownFileCount:'0',fileCount:'1',directoryCount:'0',specialCount:'0'};
   const total={...leaf,knownBytes:'1200',fileCount:'100',directoryCount:'1'};
   const node=(id,parentId,name,kind,total,hasMore=false)=>({id,parentId,realNodeId:kind==='nas'?null:id,name,kind,virtual:kind==='nas',classification:null,summary:total,originalSummary:total,matchSummary:total,matchKind:kind==='nas'?'ancestor':'direct',hasMore});
   const files=Array.from({length:100},(_,i)=>node('f'+i,root==='root'?'directory':'directory','Recorded file '+i+' long Unicode name '.repeat(8),'file',leaf));
   const nodes=root==='root'?[node('root',null,'Profile root','nas',total),node('directory','root','Directory','directory',total),...files]:[node('directory','root','Directory','directory',total),...files];
   data={...body.query,filter:{...body.query.filter,field:'both'},state:'complete',selectionOrder:'recorded-rowid',coverage:[],sourceAvailability:{total:'0',truncated:false,records:[]},originalSummary:total,matchSummary:total,denominator:total,matchState:'matches',truncated:false,nodes};
   if(holdRoot===root)await new Promise(resolve=>{releaseHierarchy=resolve;});
  }
  else if(action==='size-metrics')data={snapshotId:'s',rootId:body.rootId,defaultMetric:'logical',logical:{selectable:true,knownBytes:'1200',unknownFileCount:'0',method:'st_size',qualification:null,reason:null},allocated:{selectable:false,knownBytes:null,unknownFileCount:null,method:null,qualification:null,reason:'METHOD_UNQUALIFIED'}};
  else if(action==='search')data={snapshotId:'s',rootId:body.query.rootId,metric:body.query.metric,scope:body.query.scope,selectionOrder:'name-asc-recorded-rowid',hasMore:false,nextCursor:'',workLimited:false,items:Array.from({length:50},(_,i)=>({snapshotId:'s',nodeId:'f'+i,parentId:'directory',name:'Recorded '+i,path:'Long path segment / '.repeat(30)+i,kind:'file',volumeId:'v',shareId:null,modifiedAt:null,size:'12'}))};
  else if(action==='children'){
   const fileSummary={...summary,directoryCount:'0'};
   const child={id:'file',parentId:body.query.rootId,realNodeId:'file',name:'File.txt',kind:'file',virtual:false,classification:null,summary:fileSummary,originalSummary:fileSummary,matchSummary:fileSummary,matchKind:'direct',hasMore:false};
   data={...body.query,selectionOrder:'recorded-rowid',originalSummary:summary,matchSummary:summary,denominator:summary,matchState:'matches',hasMore:false,nextCursor:'',nodes:[child]};
  }
  else if(action==='history')data={jobs:[],truncated:false};
  else if(action==='integrity-status')data={profileId:'p',supportedSchema:8,generations:[],truncated:false};
  else{await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({contractVersion:1,error:{code:'SERVICE_UNAVAILABLE'}})});return;}
  await route.fulfill({contentType:'application/json',body:JSON.stringify({contractVersion:1,data})});
 });

 // Use the real Workbench publication lifecycle; hold only fixture responses, never alter product handlers.
 const activate=async target=>{if(mapInput==='mouse')return target.click();for(let n=0;n<500;n++){if(await target.evaluate(e=>document.activeElement===e)){await page.keyboard.press('Enter');return;}await page.keyboard.press('Tab');}throw Error('KEYBOARD_TARGET_UNREACHABLE');};
 let mapInput='mouse';
 for(const [width,height] of [[1024,700],[1280,800],[1440,921]])for(const map of ['treemap','radial']){
  await page.setViewportSize({width,height});mapInput=map==='radial'?'keyboard':'mouse';
  pref={...pref,revision:1,lastView:{...base,map},map};holdRoot=null;
  const openingCallStart=calls.length;
  await page.goto(origin+'/fixture');await page.locator('.map-viewport canvas').waitFor();
  await page.getByRole('table',{name:'Recorded objects',exact:true}).waitFor();
  assert.equal(await page.locator('.map-viewport canvas').evaluate(e=>e.closest('.treemap-canvas')?'treemap':'radial'),map,'restored renderer matches the scenario');
  // A real content-box measurement must produce one initial query, not a placeholder replacement.
  await page.waitForTimeout(220);
  const openingQueries=calls.slice(openingCallStart).filter(x=>x.action==='hierarchy');
  const measured=await page.locator('.map-viewport').evaluate(e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return{width:Math.round(r.width-parseFloat(s.paddingLeft)-parseFloat(s.paddingRight)-parseFloat(s.borderLeftWidth)-parseFloat(s.borderRightWidth)),height:Math.round(r.height-parseFloat(s.paddingTop)-parseFloat(s.paddingBottom)-parseFloat(s.borderTopWidth)-parseFloat(s.borderBottomWidth))};});
  assert.equal(openingQueries.length,1,'one first query from the measured viewport');
  assert.equal(openingQueries[0].body.query.elementBudget,Math.min(map==='treemap'?10000:2000,Math.max(1,Math.floor(measured.width*measured.height/(map==='treemap'?160:600)))),'first request matches actual CSS content box');
  const target=page.getByRole('table',{name:'Recorded objects',exact:true}).getByRole('row').filter({has:page.getByRole('button',{name:'Directory',exact:true})}).getByRole('button',{name:'Open here',exact:true});
  for(const [root,click]of [['directory',()=>activate(target)],['root',()=>activate(page.getByRole('navigation',{name:'Analysis navigation',exact:true}).getByRole('button',{name:'Up',exact:true}))]]){
   holdRoot=root;releaseHierarchy=null;await click();
   const end=Date.now()+10000;while(!releaseHierarchy&&Date.now()<end)await new Promise(r=>setTimeout(r,10));assert.ok(releaseHierarchy);
   await page.locator('.map-viewport').scrollIntoViewIfNeeded();
   const before=await page.locator('.map-viewport').evaluate(e=>({y:e.getBoundingClientRect().y,scroll:scrollY}));
   assert.equal(await page.locator('.map-viewport canvas').count(),0,'stale map must not survive pending identity');
   holdRoot=null;releaseHierarchy();
   await page.locator('.map-viewport canvas').waitFor();await page.getByRole('table',{name:'Recorded objects',exact:true}).waitFor();
   await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const after=await page.locator('.map-viewport').evaluate(e=>({y:e.getBoundingClientRect().y,bottom:e.getBoundingClientRect().bottom,scroll:scrollY,h:innerHeight,active:document.activeElement.tagName}));
   results.push({width,height,map,input:mapInput,root,before,after});assert.ok(after.y>=-1&&after.bottom<=after.h+1,JSON.stringify(results.at(-1)));
  }
 }
 assert.ok(calls.filter(x=>x.body).every(x=>['preferences','view-restore','view','view-navigation','hierarchy','search','size-metrics','profile-summaries'].includes(x.action)),'no domain mutation: '+JSON.stringify([...new Set(calls.filter(x=>x.body).map(x=>x.action))]));
 assert.ok(calls.filter(x=>x.action==='preferences'&&x.body).every(x=>x.body.group==='lastView'),'only routine view persistence');
 assert.deepEqual(errors,[]);console.log(JSON.stringify({scope:'local real Workbench async publication, synthetic DTOs',results}));
}finally{await browser.close();await server.close();await rm(entry,{force:true});}
