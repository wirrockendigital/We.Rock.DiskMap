import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
import {writeFile,rm} from 'node:fs/promises';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// This local browser fixture uses the real Workbench and controller; it is not a native DSM acceptance.
const entry=new URL('../.navigation-fixture.mjs',import.meta.url);
await writeFile(entry,"import'./src/style.css';import React from'react';import ReactDOM from'react-dom/client';import{Workbench}from'./src/Workbench.tsx';window.SYNO={SDS:{Session:{SynoToken:'test-only'}}};function Fixture(){const[state,set]=React.useState({selected:''});return React.createElement(Workbench,{language:'en',...state,onSelect:selected=>set({selected}),onAnalysis:(selected,pin,rememberedView)=>set({selected,pin,rememberedView}),denied:()=>{throw Error('denied')}})}ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(Fixture));");
const server=await createServer({root:new URL('../',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();const origin=`http://127.0.0.1:${server.httpServer.address().port}`;
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
const page=await browser.newPage();const errors=[],calls=[];page.on('pageerror',e=>errors.push(e.message));
const base={profileId:'p',snapshotId:'s',rootId:'root',selectedNodeId:null,map:'treemap',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'name',direction:'asc'};
let pref={version:2,display:{units:'decimal',colorMode:'folder',detail:'auto',labels:true,percentages:true,hierarchyBorders:'normal',reducedMotion:'auto'},dsmUserId:'admin',revision:1,lastView:base,language:'auto',theme:'auto',preferredProfileId:'p',map:'treemap',locale:null,timeZone:null};
const savedProfiles=new Map();
const summary={knownBytes:'12',unknownFileCount:'0',fileCount:'1',directoryCount:'1',specialCount:'0'};
const view={snapshotId:'s',state:'complete',startedAt:'2026-10-10T00:00:00Z',endedAt:'2026-10-10T00:01:00Z',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'Profile',scanBoundaries:[],scanBoundaryCount:'0',scanBoundariesTruncated:false,packageUsage:{logicalBytes:'0',allocatedBytes:'0',measuredAt:'2026-10-10T00:00:00Z',state:'complete',method:'package-state-stat'}};
const reader=(id,profile='p')=>({view:{...view,snapshotId:profile==='p'?'s':'s2'},lease:{id,snapshotId:profile==='p'?'s':'s2',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});
const nav=(rootId,selectedNodeId=null)=>({snapshotId:'s',rootId:rootId==='nas'?'root':rootId,parentId:rootId==='directory'?'root':null,selectedNodeId,reasons:[],breadcrumb:[{id:'root',name:'Profile root',kind:'nas'},...(rootId==='directory'?[{id:'directory',name:'Directory',kind:'directory'}]:[])]});
try{
 await page.route(origin+'/fixture',async route=>route.fulfill({contentType:'text/html',body:await server.transformIndexHtml('/fixture','<div id="root"></div><script type="module" src="/.navigation-fixture.mjs"></script>')}));
 await page.route('**/api.cgi?**',async route=>{const req=route.request(),action=new URL(req.url()).searchParams.get('action'),body=req.method()==='POST'?req.postDataJSON():null;calls.push({action,body});let data;
  if(action==='preferences'){if(body){pref={...pref,revision:pref.revision+1,lastView:body.view.value};}data=pref;}
  else if(action==='profiles')data={dsmBuild:'90080',profiles:[...new Set(['p','q',...savedProfiles.keys()])].map(id=>({profile:savedProfiles.get(id)??{id,name:'Profile '+id,revision:1,active:true,scope:{kind:'nas',targetIds:[]},exclusions:[],resourceMode:'balanced',limits:{workers:1,ioBytesPerSecond:null,cpuPercent:null},freshnessSeconds:3600},activeSnapshotId:id==='p'?'s':'s2',latestJobId:null}))};
  else if(action==='scope-inventory')data={volumes:[{id:'volume',name:'Volume',state:'reachable'}],shares:[],observedAt:'2026-10-10T00:00:00Z'};
  else if(action==='profile-action'){const profile={...body.configuration,id:body.profileId??'created',revision:body.operation==='create'?1:body.expectedRevision+1};savedProfiles.set(profile.id,profile);data={operation:body.operation,profileId:profile.id,profile,cleanupScheduled:false,tasksRequireReview:false};}
  else if(action==='view-restore')data={preferenceRevision:1,profileId:'p',resolvedView:base,reader:reader(body.viewId),navigation:nav('root'),reasons:[]};
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
  else if(action==='history')data={jobs:[],truncated:false};
  else if(action==='integrity-status')data={profileId:'p',supportedSchema:8,generations:[],truncated:false};
  else{await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({contractVersion:1,error:{code:'SERVICE_UNAVAILABLE'}})});return;}
  await route.fulfill({contentType:'application/json',body:JSON.stringify({contractVersion:1,data})});
 });
 await page.goto(origin+'/fixture');await page.getByRole('table',{name:'Recorded objects'}).getByRole('button',{name:'Directory',exact:true}).waitFor({timeout:5000});
 // The integrated view must own a painted canvas, not only the text fallback.
 await page.getByRole('img',{name:'Disk Map — hierarchical storage usage',exact:true}).waitFor();
 await page.waitForFunction(()=>{const c=document.querySelector('.map-viewport canvas');return c&&c.width>0&&c.height>0&&c.getContext('2d').getImageData(Math.floor(c.width/2),Math.floor(c.height/2),1,1).data[3]>0;});
 assert.equal(calls.filter(c=>c.action==='preferences'&&c.body).length,0);
 // Expert detail remains bounded even when the source contains many more nodes.
 const maximalRead=page.waitForResponse(r=>r.url().includes('action=hierarchy')&&r.request().postDataJSON()?.query.elementBudget===10000&&r.request().postDataJSON()?.query.depth===8);
 await page.getByRole('combobox',{name:'Detail level',exact:true}).selectOption('max');await maximalRead;
 await page.getByRole('table',{name:'Recorded objects'}).getByRole('button',{name:'Directory',exact:true}).waitFor();
 await page.locator('.map-viewport').screenshot({path:'/tmp/diskmap-host-treemap.png'});
 // Keyboard focus is local; a remainder-group inspection must never reach node APIs.
 const beforeGroupCalls=calls.length;
 const focus=page.getByRole('group',{name:'Map focus',exact:true});
 await focus.focus();await focus.press('End');await focus.press('Enter');
 await page.getByRole('region',{name:'Display group',exact:true}).waitFor();
 await page.getByRole('region',{name:'Display group',exact:true}).getByText('File.txt · 12 B',{exact:true}).waitFor();
 assert.ok(!(calls.slice(beforeGroupCalls).some(call=>JSON.stringify(call.body??{}).includes('remaining'))));
 assert.ok(await page.getByRole('table',{name:'Recorded objects'}).locator('tr[aria-selected="true"]').filter({hasText:'group'}).count()===1);
 await page.getByRole('button',{name:'Close group information',exact:true}).click();
 await focus.focus();await focus.press('Home');await focus.press('ArrowDown');await focus.press('Enter');
 await page.waitForFunction(()=>[...document.querySelectorAll('tr[aria-selected="true"]')].some(row=>row.textContent.includes('Directory')));
 // Presentation-only colors must preserve the current query, visible rows and byte totals.
 const priorHierarchyCalls=calls.filter(c=>c.action==='hierarchy').length;
 const beforeColorRows=await page.getByRole('table',{name:'Recorded objects'}).innerText();
 for(const mode of ['filetype','mtime','folder']){
  await page.getByRole('combobox',{name:'Color mode',exact:true}).selectOption(mode);
  await page.getByRole('region',{name:'Color legend',exact:true}).waitFor();
  // Age classification and its explanation must share the immutable snapshot reference.
  if(mode==='mtime'){const legend=await page.getByRole('region',{name:'Color legend'}).innerText();assert.ok(legend.includes('does not establish last use'));assert.ok(legend.includes('2026-10-10T00:01:00.000Z'));}
  assert.equal(await page.getByRole('table',{name:'Recorded objects'}).innerText(),beforeColorRows);
 }
 assert.equal(calls.filter(c=>c.action==='hierarchy').length,priorHierarchyCalls);
 await page.getByRole('table',{name:'Recorded objects'}).getByRole('row').filter({has:page.getByRole('button',{name:'Directory',exact:true})}).getByRole('button',{name:'Open here',exact:true}).click();await page.getByRole('button',{name:'File.txt',exact:true}).waitFor();
 await page.getByRole('button',{name:'File.txt',exact:true}).click();await page.waitForFunction(()=>[...document.querySelectorAll('tr')].some(x=>x.textContent.includes('File.txt')&&x.getAttribute('aria-selected')==='true'));
 const radialRead=page.waitForResponse(r=>r.url().includes('action=hierarchy')&&r.request().postDataJSON()?.query.elementBudget===2000);
 await page.getByRole('button',{name:'Radial map',exact:true}).click();await radialRead;await page.waitForFunction(()=>document.querySelector('button[aria-pressed="true"]')?.textContent==='Radial map');
 await page.getByRole('region',{name:'Radial map',exact:true}).waitFor();
 await page.waitForFunction(()=>{const c=document.querySelector('.map-viewport canvas');return c&&c.width>0&&c.height>0&&c.getContext('2d').getImageData(Math.floor(c.width/2),Math.floor(c.height/2),1,1).data[3]>0;});
 assert.ok(await page.getByRole('button',{name:'File.txt',exact:true}).isVisible());
 await page.locator('.map-viewport').screenshot({path:'/tmp/diskmap-host-radial.png'});
 // The radial center exposes a named parent action and retains normal view history.
 const centerParent=page.waitForResponse(r=>r.url().includes('action=hierarchy')&&r.request().postDataJSON()?.query.rootId==='root');
 await page.getByRole('button',{name:'Up one level',exact:true}).click();await centerParent;
 await page.getByRole('table',{name:'Recorded objects'}).getByRole('button',{name:'Directory',exact:true}).waitFor();
 const centerBack=page.waitForResponse(r=>r.url().includes('action=hierarchy')&&r.request().postDataJSON()?.query.rootId==='directory');
 await page.getByRole('button',{name:'Back',exact:true}).click();await centerBack;
 await page.getByRole('button',{name:'File.txt',exact:true}).waitFor();
 await page.getByRole('button',{name:'Back',exact:true}).click();await page.waitForFunction(()=>[...document.querySelectorAll('button')].some(x=>x.textContent==='Treemap'&&x.getAttribute('aria-pressed')==='true'));
 // Repeated map changes preserve the complete confirmed context and stored totals.
 for(let index=0;index<100;index++){
  const map=index%2===0?'radial':'treemap';
  const response=page.waitForResponse(r=>r.url().includes('action=hierarchy')&&r.request().postDataJSON()?.query.elementBudget===(map==='radial'?2000:10000));
  await page.getByRole('button',{name:map==='radial'?'Radial map':'Treemap',exact:true}).click();const got=await response,body=got.request().postDataJSON(),data=(await got.json()).data;
  assert.equal(body.query.snapshotId,'s');assert.equal(body.query.rootId,'directory');assert.equal(body.query.metric,'logical');assert.equal(body.query.mode,'highlight');assert.deepEqual(body.query.filter,{categories:[]});assert.equal(data.denominator.knownBytes,'12');
  await page.waitForFunction(()=>[...document.querySelectorAll('tr[aria-selected="true"]')].some(row=>row.textContent.includes('File.txt')));
 }
 const parentRead=page.waitForResponse(r=>r.url().includes('action=hierarchy')&&r.request().postDataJSON()?.query.rootId==='root');await page.getByRole('button',{name:'Up',exact:true}).click();await parentRead;await page.getByRole('table',{name:'Recorded objects'}).getByRole('button',{name:'Directory',exact:true}).waitFor();
 for(let n=0;n<18;n++){const id=n%2===0?'q':'p';const ready=page.waitForResponse(r=>r.url().includes('action=hierarchy')&&r.request().postDataJSON()?.profileId===id);await page.locator('.profile-select select').selectOption(id);await ready;await page.getByRole('table',{name:'Recorded objects'}).getByRole('button',{name:'Directory',exact:true}).waitFor();}
 assert.ok(calls.filter(c=>c.action==='view'&&c.body?.operation==='release').length>=18);
 await page.getByRole('button',{name:'Scan profiles',exact:true}).click();
 await page.getByRole('button',{name:'Edit',exact:true}).first().click();await page.getByRole('textbox',{name:'Name',exact:true}).fill('Edited profile');
 await page.locator('.profile-select select').selectOption('q');await page.getByText('Save or discard the profile draft before changing the analysis profile.',{exact:true}).waitFor();assert.equal(await page.getByRole('textbox',{name:'Name',exact:true}).inputValue(),'Edited profile');
 await page.getByRole('button',{name:'Save',exact:true}).click();await page.getByRole('heading',{name:'Edited profile',exact:true}).waitFor();
 await page.getByRole('button',{name:'Close',exact:true}).click();
 await page.getByRole('button',{name:'New profile',exact:true}).click();await page.getByRole('textbox',{name:'Name',exact:true}).fill('New profile saved');await page.getByRole('button',{name:'Refresh available scopes',exact:true}).click();await page.getByRole('checkbox',{name:'Entire NAS',exact:true}).check();
 await page.getByRole('button',{name:'Save',exact:true}).click();await page.getByRole('heading',{name:'New profile saved',exact:true}).waitFor();
 assert.equal(calls.filter(c=>c.action==='profile-action').length,2);
 await page.getByRole('textbox',{name:'Name',exact:true}).fill('Unsaved draft');await page.getByRole('button',{name:'Discard',exact:true}).click();assert.equal(await page.getByRole('textbox',{name:'Name',exact:true}).inputValue(),'New profile saved');
 assert.deepEqual(errors,[]);assert.ok(!calls.some(c=>c.action==='scan'));console.log(JSON.stringify({passed:true,restoreBeforeFallback:true,noBootstrapWrite:true,realHierarchyDrill:true,selection:true,mapPreservedRoot:true,colorPreservesQueryAndRows:true,paintedTreemapAndRadial:true,radialCenterParent:true,mapSwitches100:true,serverGroupChildren:true,maxDetailBounded:true,radialBudget2000:true,keyboardSelectionAndGroupIsolation:true,back:true,parent:true,noScan:true,requests:calls.length}));
}catch(error){console.error(JSON.stringify({errors,calls:calls.slice(-20),body:await page.locator('body').innerText()}));throw error;}finally{await browser.close();await server.close();await rm(entry,{force:true});}
