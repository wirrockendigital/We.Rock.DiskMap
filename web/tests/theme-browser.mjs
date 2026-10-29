import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
import {writeFile,rm} from 'node:fs/promises';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// This local browser fixture uses the real Workbench and controller; it is not a native DSM acceptance.
const entry=new URL('../.theme-fixture.mjs',import.meta.url);
await writeFile(entry,"import'./src/style.css';import React from'react';import ReactDOM from'react-dom/client';import{Workbench}from'./src/Workbench.tsx';window.SYNO={SDS:{Session:{SynoToken:'test-only'}}};function Fixture(){const[state,set]=React.useState({selected:''});return React.createElement(Workbench,{language:new URLSearchParams(location.search).get('lang')==='de'?'de':'en',...state,onSelect:selected=>set({selected}),onAnalysis:(selected,pin,rememberedView)=>set({selected,pin,rememberedView}),denied:()=>{throw Error('denied')}})}ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(Fixture));");
const server=await createServer({root:new URL('../',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();const origin=`http://127.0.0.1:${server.httpServer.address().port}`;
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
const page=await browser.newPage({deviceScaleFactor:2});page.setDefaultTimeout(10000);const errors=[],calls=[];page.on('pageerror',e=>errors.push(e.message));
const base={profileId:'p',snapshotId:'s',rootId:'root',selectedNodeId:null,map:'treemap',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'name',direction:'asc'};
let pref={version:2,display:{units:'decimal',colorMode:'folder',detail:'auto',labels:true,percentages:true,hierarchyBorders:'normal',reducedMotion:'auto'},dsmUserId:'admin',revision:1,lastView:base,language:'auto',theme:'auto',preferredProfileId:'p',map:'treemap',locale:null,timeZone:null};
const savedProfiles=new Map();
const summary={knownBytes:'12',unknownFileCount:'0',fileCount:'1',directoryCount:'1',specialCount:'0'};
const view={snapshotId:'s',state:'complete',startedAt:'2026-10-10T00:00:00Z',endedAt:'2026-10-10T00:01:00Z',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'Profile',scanBoundaries:[],scanBoundaryCount:'0',scanBoundariesTruncated:false,packageUsage:{logicalBytes:'0',allocatedBytes:'0',measuredAt:'2026-10-10T00:00:00Z',state:'complete',method:'package-state-stat'}};
const reader=(id,profile='p')=>({view:{...view,snapshotId:profile==='p'?'s':'s2'},lease:{id,snapshotId:profile==='p'?'s':'s2',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});
const nav=(rootId,selectedNodeId=null)=>({snapshotId:'s',rootId:rootId==='nas'?'root':rootId,parentId:rootId==='directory'?'root':null,selectedNodeId,reasons:[],breadcrumb:[{id:'root',name:'Profile root',kind:'nas'},...(rootId==='directory'?[{id:'directory',name:'Directory',kind:'directory'}]:[])]});
try{
 await page.route(origin+'/fixture*',async route=>route.fulfill({contentType:'text/html',body:await server.transformIndexHtml('/fixture','<div id="root"></div><script type="module" src="/.theme-fixture.mjs"></script>')}));
 await page.route('**/api.cgi?**',async route=>{const req=route.request(),action=new URL(req.url()).searchParams.get('action'),body=req.method()==='POST'?req.postDataJSON():null;calls.push({action,body});let data;
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
  else if(action==='history')data={jobs:[],truncated:false};
  else if(action==='integrity-status')data={profileId:'p',supportedSchema:8,generations:[],truncated:false};
  else{await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({contractVersion:1,error:{code:'SERVICE_UNAVAILABLE'}})});return;}
  await route.fulfill({contentType:'application/json',body:JSON.stringify({contractVersion:1,data})});
 });
 // Verify actual Workbench CSS and both renderers at desktop, narrow and physical 4K dimensions.
 const evidence=[];
 for(const lang of ['de','en']){
  pref={...pref,theme:'auto',lastView:base,revision:1};await page.goto(origin+'/fixture?lang='+lang);await page.locator('.map-viewport canvas').waitFor();
  const settings=lang==='de'?'Einstellungen':'Settings',overview=lang==='de'?'Analyse':'Analysis';
  await page.getByRole('button',{name:settings,exact:true}).click();
  const theme=page.getByRole('combobox',{name:lang==='de'?'Farbschema':'Theme',exact:true});
  for(const choice of ['dark','light','auto']){
   await page.emulateMedia({colorScheme:choice==='light'?'dark':'light',reducedMotion:'reduce'});
   if(await theme.inputValue()!==choice){await theme.selectOption(choice);const before=await page.locator('html').getAttribute('data-theme');assert.notEqual(before,null);await page.getByRole('button',{name:lang==='de'?'Darstellung speichern':'Save appearance',exact:true}).click();}
   await page.waitForFunction(expected=>document.documentElement.dataset.theme===expected,choice==='dark'?'dark':'light');
   await page.waitForFunction(source=>document.querySelector('.theme-settings [role=status]')?.textContent.includes(source),choice==='auto'?(lang==='de'?'Browser-/System':'Browser/system'):(lang==='de'?'Persönliche':'Personal'));
   await page.waitForFunction(()=>document.querySelector('.theme-settings button').disabled===true);
  }
  await page.emulateMedia({colorScheme:'dark'});await page.waitForFunction(()=>document.documentElement.dataset.theme==='dark');
  await theme.selectOption('light');await page.getByRole('button',{name:lang==='de'?'Darstellung verwerfen':'Discard appearance',exact:true}).click();assert.equal(await theme.inputValue(),'auto');
  // Reload resolves confirmed personal settings, without issuing another appearance mutation.
  const saves=calls.filter(c=>c.action==='preferences'&&c.body?.group==='appearance').length;await page.reload();await page.locator('.map-viewport canvas').waitFor();assert.equal(calls.filter(c=>c.action==='preferences'&&c.body?.group==='appearance').length,saves);
  for(const scheme of ['light','dark'])for(const [width,height] of [[1024,700],[1280,800],[1920,1080],[640,700]]){
   await page.emulateMedia({colorScheme:scheme});await page.setViewportSize({width,height});await page.waitForTimeout(220);
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'horizontal overflow');
   const buttons=page.locator('.analysis-panel-controls button');for(let i=0;i<2;i++){await buttons.nth(i).click();assert.equal(await buttons.nth(i).getAttribute('aria-expanded'),'false');await buttons.nth(i).click();assert.equal(await buttons.nth(i).getAttribute('aria-expanded'),'true');}
   assert.equal(await page.evaluate(()=>getComputedStyle(document.body).animationName),'none');
   for(const map of ['radial','treemap']){
    await page.getByRole('button',{name:map==='radial'?(lang==='de'?'Radialkarte':'Radial map'):'Treemap',exact:true}).click();
    await page.waitForTimeout(220);const geometry=await page.locator('.map-viewport canvas').evaluate(c=>({width:c.width,height:c.height,cssWidth:c.getBoundingClientRect().width,cssHeight:c.getBoundingClientRect().height,dpr:devicePixelRatio}));
    assert.equal(geometry.dpr,2);assert.ok(Math.abs(geometry.width-geometry.cssWidth*2)<=2);assert.ok(geometry.height>0);
    if(width===1024)await page.locator('.map-viewport').screenshot({path:`/tmp/diskmap-wi096-${lang}-${scheme}-${map}.png`});
    evidence.push({lang,width,height,map,theme:await page.locator('html').getAttribute('data-theme'),geometry});
   }
  }
 }
 assert.deepEqual(errors,[]);assert.ok(!calls.some(c=>c.action==='scan'));
 console.log(JSON.stringify({passed:true,realWorkbench:true,evidence,manualBoth:true,systemChange:true,reducedMotion:true,discard:true,reopen:true,noScan:true}));
}catch(error){console.error(JSON.stringify({errors,body:await page.locator('body').innerText()}));throw error;}finally{await browser.close();await server.close();await rm(entry,{force:true});}
