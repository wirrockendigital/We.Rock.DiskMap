import assert from 'node:assert/strict';
import {fixture as detailFixture} from './node-details-fixture.mjs';
import {createServer} from '../node_modules/vite/dist/node/index.js';
import {writeFile,rm} from 'node:fs/promises';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// This local browser fixture uses the real Workbench and controller; it is not a native DSM acceptance.
const entry=new URL('../.accessibility-fixture.mjs',import.meta.url);
await writeFile(entry,"import'./src/style.css';import React from'react';import ReactDOM from'react-dom/client';import{Workbench}from'./src/Workbench.tsx';window.SYNO={SDS:{Session:{SynoToken:'test-only'}}};function Fixture(){const[state,set]=React.useState({selected:''});return React.createElement(Workbench,{language:new URLSearchParams(location.search).get('lang')==='de'?'de':'en',...state,onSelect:selected=>set({selected}),onAnalysis:(selected,pin,rememberedView)=>set({selected,pin,rememberedView}),denied:()=>{throw Error('denied')}})}ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(Fixture));");
const server=await createServer({root:new URL('../',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();const origin=`http://127.0.0.1:${server.httpServer.address().port}`;
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
const page=await browser.newPage({deviceScaleFactor:2,hasTouch:true});page.setDefaultTimeout(10000);const errors=[],calls=[];let failScan=true;page.on('pageerror',e=>errors.push(e.message));
const base={profileId:'p',snapshotId:'s',rootId:'root',selectedNodeId:null,map:'treemap',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'name',direction:'asc'};
let pref={version:2,display:{units:'decimal',colorMode:'folder',detail:'auto',labels:true,percentages:true,hierarchyBorders:'normal',reducedMotion:'auto'},dsmUserId:'admin',revision:1,lastView:base,language:'auto',theme:'auto',preferredProfileId:'p',map:'treemap',locale:null,timeZone:null};
const savedProfiles=new Map();
const summary={knownBytes:'12',unknownFileCount:'0',fileCount:'1',directoryCount:'1',specialCount:'0'};
const view={snapshotId:'s',state:'complete',startedAt:'2026-10-10T00:00:00Z',endedAt:'2026-10-10T00:01:00Z',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'Profile',scanBoundaries:[],scanBoundaryCount:'0',scanBoundariesTruncated:false,packageUsage:{logicalBytes:'0',allocatedBytes:'0',measuredAt:'2026-10-10T00:00:00Z',state:'complete',method:'package-state-stat'}};
const reader=(id,profile='p')=>({view:{...view,snapshotId:profile==='p'?'s':'s2'},lease:{id,snapshotId:profile==='p'?'s':'s2',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});
const nav=(rootId,selectedNodeId=null)=>({snapshotId:'s',rootId:rootId==='nas'?'root':rootId,parentId:rootId==='directory'?'root':null,selectedNodeId,reasons:[],breadcrumb:[{id:'root',name:'Profile root',kind:'nas'},...(rootId==='directory'?[{id:'directory',name:'Directory',kind:'directory'}]:[])]});
try{
 await page.route(origin+'/host',route=>route.fulfill({contentType:'text/html',body:'<script>window.launches=[];window.SYNO={SDS:{Session:{SynoToken:"test-only"},AppLaunch:(name,parameters)=>launches.push({name,parameters})}};</script><iframe title="DiskMap application" style="width:100%;height:950px;border:0" src="/fixture"></iframe>'}));
 await page.route(origin+'/fixture*',async route=>route.fulfill({contentType:'text/html',body:await server.transformIndexHtml('/fixture','<div id="root"></div><script type="module" src="/.accessibility-fixture.mjs"></script>')}));
 await page.route('**/api.cgi?**',async route=>{const req=route.request(),action=new URL(req.url()).searchParams.get('action'),body=req.method()==='POST'?req.postDataJSON():null;calls.push({action,body,key:req.headers()['idempotency-key']});let data;
  if(action==='preferences'){if(body){pref={...pref,revision:pref.revision+1,...(body.group==='appearance'?body.appearance:{lastView:body.view.value})};}data=pref;}
  else if(action==='regional-context')data={version:1,nasTimeZone:'Europe/Amsterdam',timeZoneSource:'system-zoneinfo'};
  else if(action==='profiles')data={dsmBuild:'90080',profiles:[...new Set(['p','q',...savedProfiles.keys()])].map(id=>({profile:savedProfiles.get(id)??{id,name:'Profile '+id,revision:1,active:true,scope:{kind:'nas',targetIds:[]},exclusions:[],resourceMode:'balanced',limits:{workers:1,ioBytesPerSecond:null,cpuPercent:null},freshnessSeconds:3600},activeSnapshotId:id==='p'?'s':'s2',latestJobId:null}))};
  else if(action==='scope-inventory')data={volumes:[{id:'volume',name:'Volume',state:'reachable'}],shares:[],observedAt:'2026-10-10T00:00:00Z'};
  else if(action==='profile-action'){const profile={...body.configuration,id:body.profileId??'created',revision:body.operation==='create'?1:body.expectedRevision+1};savedProfiles.set(profile.id,profile);data={operation:body.operation,profileId:profile.id,profile,cleanupScheduled:false,tasksRequireReview:false};}
  else if(action==='view-restore')data={preferenceRevision:pref.revision,profileId:'p',resolvedView:base,reader:reader(body.viewId),navigation:nav('root'),reasons:[]};
  else if(action==='view')data=reader(body.viewId,body.profileId);
  else if(action==='view-navigation')data={...nav(body.rootId,body.selectedNodeId),snapshotId:body.snapshotId};
  // The real hierarchy response includes its root and depth-limit residual groups.
  else if(action==='hierarchy'){
   const root=body.query.rootId,fileSummary={...summary,directoryCount:'0'};
   const node=(id,parentId,name,kind,total,hasMore=false)=>({id,parentId,realNodeId:kind==='nas'||kind==='group'?null:id,name,kind,virtual:kind==='nas'||kind==='group',classification:null,summary:total,originalSummary:total,matchSummary:total,matchKind:kind==='group'?'group':kind==='nas'?'ancestor':'direct',hasMore});
   const nodes=root==='root'?[node('root',null,'Profile root','nas',summary),node('directory','root','Directory','directory',summary,true),{...node('remaining','directory','','group',fileSummary,true),groupReason:'depth-limit',groupedChildren:'1',listRootId:'directory'}]:[node('directory',null,'Directory','directory',summary),node('file','directory','File.txt','file',fileSummary)];
   data={...body.query,filter:{...body.query.filter,field:'both'},state:'complete',selectionOrder:'recorded-rowid',coverage:[],sourceAvailability:{total:'0',truncated:false,records:[]},originalSummary:summary,matchSummary:summary,denominator:summary,matchState:'matches',truncated:root==='root',nodes};
  }
  else if(action==='children'){
   const fileSummary={...summary,directoryCount:'0'};
   const child={id:'file',parentId:body.query.rootId,realNodeId:'file',name:'File.txt',kind:'file',virtual:false,classification:null,summary:fileSummary,originalSummary:fileSummary,matchSummary:fileSummary,matchKind:'direct',hasMore:false};
   data={...body.query,selectionOrder:'recorded-rowid',originalSummary:summary,matchSummary:summary,denominator:summary,matchState:'matches',hasMore:false,nextCursor:'',nodes:[child]};
  }
  else if(action==='node-details'){
   data=detailFixture();data.profileId=body.profileId;data.viewId=body.viewId;
   Object.assign(data.detail,{snapshotId:body.snapshotId,rootId:body.rootId,nodeId:body.nodeId,metric:body.metric,snapshotState:'complete',name:'File.txt',kind:'file',aggregate:null,classification:null,coverage:[],boundaryCount:'0'});
   Object.assign(data.detail.node,{id:body.nodeId,snapshotId:body.snapshotId,kind:'file',originalName:'File.txt',logicalBytes:'12',allocatedBytes:'4096'});
   Object.assign(data.paths,{snapshotId:body.snapshotId,nodeId:body.nodeId,name:{text:'File.txt',encoding:'utf8'},nasPath:{text:'/volume1/Test/File.txt',encoding:'utf8'}});
  }
  else if(action==='search'){data={snapshotId:body.query.snapshotId,rootId:body.query.rootId,metric:body.query.metric,scope:body.query.scope,selectionOrder:body.query.sort+'-'+body.query.direction+'-recorded-rowid',hasMore:false,nextCursor:'',workLimited:false,items:[{snapshotId:body.query.snapshotId,nodeId:'file',parentId:'directory',name:'File.txt',path:'Directory/File.txt',volumeId:'volume',shareId:null,kind:'file',size:'12',modifiedAt:null,classification:null,summary:{...summary,directoryCount:'0'}}]};}
  else if(action==='filestation')data={kind:'file',fileStationPath:'/Test/File.txt',parentPath:'/Test'};
  else if(action==='session')data={authenticated:true,administrator:true};
  else if(action==='scan'){if(failScan){failScan=false;await route.abort('failed');return;}data={job:{id:'job',profileId:body.profileId,scanMode:body.mode,profileRevision:1},deduplicated:false};}
  else if(action==='profile-summaries')data={observedAt:'2026-10-10T00:02:00Z',profiles:body.profileIds.map(profileId=>({profileId,currentRevision:1,snapshotId:'s',capturedRevision:1,endedAt:view.endedAt,quality:'complete',freshness:'current',ageSeconds:'60',integrityQuality:'unverified',integrityCheckedAt:null,lastAttempt:null}))};
  else if(action==='history')data={jobs:[],truncated:false};
  else if(action==='integrity-status')data={profileId:'p',supportedSchema:8,generations:[],truncated:false};
  else{await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({contractVersion:1,error:{code:'SERVICE_UNAVAILABLE'}})});return;}
  await route.fulfill({contentType:'application/json',body:JSON.stringify({contractVersion:1,data})});
 });
 // Navigate by Tab and activate with Enter: no click/focus shortcut drives the core workflow.
 await page.goto(origin+'/host');const app=page.frames().find(f=>f.url().includes('/fixture'));assert.ok(app);
 await app.locator('.map-viewport canvas').waitFor();
 let tabs=0;const reach=async locator=>{await locator.waitFor();for(let i=0;i<200&&!(await locator.isEnabled());i++)await page.waitForTimeout(10);assert.ok(await locator.isEnabled(),'keyboard target enabled');for(let i=0;i<550;i++){if(await locator.evaluate(e=>e===document.activeElement))return;await page.keyboard.press('Tab');tabs++;}throw Error('Keyboard target unreachable: '+await locator.textContent());};
 const activate=async locator=>{await reach(locator);await page.keyboard.press('Enter');};
 const button=name=>app.getByRole('button',{name,exact:true});
 const group=app.getByRole('group',{name:'Map focus',exact:true});
 for(const map of ['Treemap','Radial map']){
  await activate(button(map));await app.locator('.map-viewport canvas').waitFor();
  await reach(group);await page.keyboard.press('End');await page.keyboard.press('Enter');
  const inspection=app.getByRole('region',{name:'Display group',exact:true});await inspection.waitFor();
  await app.getByRole('button',{name:'Close group information',exact:true}).waitFor();
  await page.keyboard.press('Escape');await inspection.waitFor({state:'hidden'});
  assert.ok(await group.evaluate(e=>e===document.activeElement),'group returns focus');
  await page.keyboard.press('Home');await page.keyboard.press('ArrowDown');await page.keyboard.press('Enter');
  await app.waitForFunction(()=>document.querySelector('tr[aria-selected=true]')?.textContent.includes('Directory'));
  const row=app.getByRole('table',{name:'Recorded objects'}).getByRole('row').filter({has:button('Directory')});
  await activate(row.getByRole('button',{name:'Open here',exact:true}));await app.getByRole('table',{name:'Recorded objects'}).getByRole('button',{name:'File.txt',exact:true}).waitFor();
  await activate(app.getByRole('table',{name:'Recorded objects'}).getByRole('button',{name:'File.txt',exact:true}));await app.waitForFunction(()=>document.querySelector('tr[aria-selected=true]')?.textContent.includes('File.txt'));
  const fileRow=app.getByRole('table',{name:'Recorded objects'}).getByRole('row').filter({has:button('File.txt')});const actions=fileRow.getByRole('button',{name:'Actions',exact:true});
  await activate(actions);const menu=app.getByRole('menu',{name:'Actions: File.txt',exact:true});await menu.waitFor();await page.keyboard.press('End');await page.keyboard.press('ArrowUp');await page.keyboard.press('Enter');
  const copy=app.getByRole('dialog',{name:'Quick preview',exact:true});await copy.waitFor();await page.keyboard.press('Escape');await copy.waitFor({state:'hidden'});assert.ok(await actions.evaluate(e=>e===document.activeElement));
  const fs=app.getByRole('button',{name:'Open in File Station',exact:true}).last();await fs.waitFor();
  await activate(fs);await page.waitForFunction(n=>window.launches.length===n,map==='Treemap'?1:2);
  await activate(button('Profile root').first());await app.getByRole('table',{name:'Recorded objects'}).getByRole('button',{name:'Directory',exact:true}).waitFor();
 }
 // Search uses the real form and list; Enter submits and selection preserves the central identity.
 const search=app.getByRole('searchbox');await reach(search);await page.keyboard.type('File');const searched=page.waitForResponse(r=>r.url().includes('action=search')&&r.request().postDataJSON()?.query.text==='File');await page.keyboard.press('Enter');await searched;
 await app.getByRole('table',{name:'Result list',exact:true}).waitFor();
 assert.ok(calls.some(c=>c.action==='search'&&c.body.query.text==='File'));
 const result=app.getByRole('table',{name:'Result list',exact:true}).getByRole('button',{name:'File.txt',exact:true});await activate(result);
 await activate(button('Remove all filters'));
 // Canvas failure preserves the synchronized named list; touch reaches the same explicit actions without hover.
 for(const map of ['Treemap','Radial map']){
  await activate(button(map));const recorded=app.getByRole('table',{name:'Recorded objects'});await recorded.waitFor();
  await app.evaluate(()=>{window.savedCanvasContext=HTMLCanvasElement.prototype.getContext;HTMLCanvasElement.prototype.getContext=function(){return null;};});
  const color=app.getByRole('combobox',{name:'Color mode',exact:true});await reach(color);await page.keyboard.press('Home');await page.keyboard.press('ArrowDown');
  await app.waitForFunction(()=>[...document.querySelectorAll('.map-viewport [role=status]')].some(e=>e.textContent.includes('Map unavailable')));
  assert.ok(await recorded.isVisible());assert.equal(await app.locator('.map-viewport canvas').getAttribute('aria-disabled'),'true');assert.equal(await app.locator('.map-viewport canvas').getAttribute('tabindex'),'-1');
  const directory=recorded.getByRole('button',{name:'Directory',exact:true});await directory.scrollIntoViewIfNeeded();const bounds=await directory.boundingBox();assert.ok(bounds);await page.touchscreen.tap(bounds.x+bounds.width/2,bounds.y+bounds.height/2);
  await app.waitForFunction(()=>document.querySelector('tr[aria-selected=true]')?.textContent.includes('Directory'));
  await app.evaluate(()=>{HTMLCanvasElement.prototype.getContext=window.savedCanvasContext;});await reach(color);await page.keyboard.press('Home');await page.keyboard.press('Enter');await page.setViewportSize({width:map==='Treemap'?1279:1280,height:1000});
  await app.waitForFunction(()=>document.querySelector('.map-viewport canvas')?.getAttribute('aria-disabled')==='false');
 }
 // Real profile confirmation is modal, named, Escape-safe and returns focus; an uncertain write keeps its exact retry reachable.
 await activate(button('Scan profiles'));const card=app.getByRole('article',{name:'Profile p',exact:true});const refresh=card.getByRole('button',{name:'Refresh now',exact:true});
 await activate(refresh);const dialog=app.getByRole('dialog',{name:'Refresh now: Profile p',exact:true});await dialog.waitFor();
 for(let i=0;i<8;i++){await page.keyboard.press('Tab');assert.ok(await dialog.evaluate(e=>e.contains(document.activeElement)),'focus stays in modal');}
 await page.keyboard.press('Escape');await dialog.waitFor({state:'hidden'});assert.ok(await refresh.evaluate(e=>e===document.activeElement));
 await page.keyboard.press('Enter');await dialog.waitFor();await activate(dialog.getByRole('button',{name:'Confirm',exact:true}));
 const retry=dialog.getByRole('button',{name:'Confirm the same action again',exact:true});await retry.waitFor();
 await page.keyboard.press('Escape');assert.ok(await dialog.isVisible(),'uncertain mutation cannot be dismissed');
 await activate(retry);await dialog.waitFor({state:'hidden'});
 const scans=calls.filter(c=>c.action==='scan');assert.equal(scans.length,2);assert.deepEqual(scans[0].body,scans[1].body);assert.equal(scans[0].key,scans[1].key);
 assert.deepEqual(await page.evaluate(()=>window.launches.map(x=>x.parameters)),[{opendir:'/Test'},{opendir:'/Test'}]);
 assert.deepEqual(errors,[]);console.log(JSON.stringify({passed:true,realWorkbench:true,keyboardTabs:tabs,bothMaps:true,search:true,selection:true,fileStationAdapterRequests:2,modalContainment:true,escapeReturn:true,uncertainExactRetry:true,canvasFailureBoth:true,textAlternativeTouch:true,scope:'localhost synthetic API; no native DSM or assistive-technology certification'}));
}catch(error){console.error(JSON.stringify({errors,body:await page.locator('body').innerText(),frames:await Promise.all(page.frames().map(async f=>({url:f.url(),text:await f.locator('body').innerText().catch(()=>'' )})))}));throw error;}finally{await browser.close();await server.close();await rm(entry,{force:true});}
