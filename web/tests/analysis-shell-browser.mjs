import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
import {mkdir,writeFile,rm} from 'node:fs/promises';
// Resolve Playwright from an explicit runner path or the installed package.
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
// This bounded local presentation fixture uses the actual Workbench, never a NAS or a real scan.
const started=performance.now();
const entryPath=new URL('../.shell-fixture.mjs',import.meta.url);
await writeFile(entryPath,"   import React from 'react';import ReactDOM from'react-dom/client';import{Workbench}from'./src/Workbench.tsx';import'./src/style.css';\n   window.SYNO={SDS:{Session:{SynoToken:'local-fixture-only'}}};function Fixture(){const[selected,onSelect]=React.useState('deleted-profile');return React.createElement(Workbench,{language:'de',selected,onSelect,denied:()=>{throw Error('unexpected denial')}});}ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(Fixture));\n");
const server=await createServer({root:new URL('../',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});
await server.listen();const origin=`http://127.0.0.1:${server.httpServer.address().port}`;
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_PATH});
const results=[];
try{
 await mkdir(new URL('../../output/playwright/',import.meta.url),{recursive:true});
 for(const viewport of [{width:1024,height:700},{width:1280,height:800}]){
  const context=await browser.newContext({viewport});const page=await context.newPage();const mutations=[];const readerProfiles=[];const errors=[];let empty=false;
  page.on('pageerror',e=>{errors.push(e.message);console.error('local browser error:',e.message);});
  await page.route(origin+'/shell-fixture',async route=>route.fulfill({contentType:'text/html',body:await server.transformIndexHtml('/shell-fixture',`<!doctype html><html lang="de"><head><meta charset="utf-8"></head><body><main><h1>We.Rock.DiskMap</h1><div id="root"></div></main><script type="module" src="/.shell-fixture.mjs"></script></body></html>`)}));
  await page.route('**/api.cgi?**',async route=>{
   const req=route.request(),action=new URL(req.url()).searchParams.get('action');let data;
   if(req.method()==='POST'&&!['view','view-restore','view-navigation'].includes(action))mutations.push(action);
   if(action==='preferences')data={version:2,display:{units:'decimal',colorMode:'folder',detail:'auto',labels:true,percentages:true,hierarchyBorders:'normal',reducedMotion:'auto'},dsmUserId:'admin',revision:0,lastView:null,language:'auto',theme:'auto',preferredProfileId:null,map:'treemap',locale:null,timeZone:null};
   else if(action==='view-restore')data={preferenceRevision:0,profileId:empty?null:'actual-profile',resolvedView:null,reader:null,navigation:null,reasons:['PROFILE_UNAVAILABLE','NO_SNAPSHOT']};
   else if(action==='profiles')data={dsmBuild:'90080',profiles:empty?[]:[{profile:{id:'actual-profile',name:'Langfristige Datensicherungen und gemeinschaftliche Medienverwaltung – Grüße & <Test> '.repeat(3),revision:3,active:true,scope:{kind:'shares',targetIds:['share']},exclusions:[],resourceMode:'balanced',limits:{workers:1,ioBytesPerSecond:null,cpuPercent:null},freshnessSeconds:3600},activeSnapshotId:null,latestJobId:null}]};
   else if(action==='view'){readerProfiles.push(req.postDataJSON().profileId);data={view:{snapshotId:null,state:null,startedAt:null,endedAt:null,nodes:[],truncated:false,profileRevision:null,currentProfileRevision:3,profileChanged:false,capturedProfileName:null,scanBoundaries:[],scanBoundaryCount:'0',scanBoundariesTruncated:false,packageUsage:{logicalBytes:'0',allocatedBytes:'0',measuredAt:'2026-10-10T00:00:00Z',state:'complete',method:'package-state-stat'}},lease:null,newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}};}
   else if(action==='history')data={jobs:[],truncated:false};
   else if(action==='integrity-status')data={profileId:'actual-profile',supportedSchema:7,generations:[],truncated:false};
   else{await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({contractVersion:1,error:{code:'SERVICE_UNAVAILABLE'}})});return;}
   await route.fulfill({contentType:'application/json',body:JSON.stringify({contractVersion:1,data})});
  });
  await page.goto(origin+'/shell-fixture');await page.getByRole('heading',{name:'Noch kein Datenstand',exact:true}).waitFor();
  assert.equal(await page.locator('canvas,svg').count(),0);assert.equal(await page.locator('.profile-select select').inputValue(),'actual-profile');
  assert.ok(await page.getByText('Das zuvor gewählte Profil ist nicht mehr verfügbar.',{exact:false}).isVisible());
  const opens=readerProfiles.length;
  await page.getByRole('button',{name:'Details',exact:true}).click();assert.equal(await page.locator('#analysis-details').isVisible(),false);
  // Tab through actual browser focus targets after each area change; hidden owners must be skipped.
  for(const name of ['Scan-Profile','Scan-Status','Einstellungen','Übersicht / Karte']){
   await page.getByRole('navigation').getByRole('button',{name,exact:true}).click();
   for(let n=0;n<18;n++){await page.keyboard.press('Tab');assert.equal(await page.evaluate(()=>!!document.activeElement?.closest('[hidden]')),false);}
  }
  assert.equal(await page.locator('#analysis-details').isVisible(),false);assert.equal(readerProfiles.length,opens);
  await page.getByRole('button',{name:'Details',exact:true}).click();assert.ok(await page.locator('#analysis-details').isVisible());
  await page.getByRole('button',{name:'Bereiche',exact:true}).click();assert.equal(await page.locator('#analysis-orientation').isVisible(),false);
  await page.getByRole('button',{name:'Bereiche',exact:true}).click();
  await page.getByText('Hilfe zu diesem Bereich',{exact:true}).click();assert.ok(await page.getByText('Wähle ein Profil und lade seinen gespeicherten Datenstand.',{exact:false}).isVisible());
  const dimensions=await page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth,main:document.querySelector('.analysis-main').getBoundingClientRect().width}));
  assert.ok(dimensions.scroll<=dimensions.width,JSON.stringify(dimensions));assert.ok(dimensions.main>=350,JSON.stringify(dimensions));
  assert.deepEqual(mutations,[]);assert.ok(readerProfiles.every(id=>id==='actual-profile'));assert.deepEqual(errors,[]);
  await page.screenshot({path:new URL(`../../output/playwright/analysis-shell-${viewport.width}.png`,import.meta.url).pathname,fullPage:true});
  empty=true;await page.reload();await page.getByRole('heading',{name:'Einrichtung erforderlich'}).waitFor();assert.equal(await page.locator('.analysis-main').count(),0);assert.deepEqual(mutations,[]);
  results.push({viewport,dimensions,noSnapshot:true,invalidSelection:true,tabStatePreserved:true,hiddenFocusSkipped:true,noImplicitScan:true,emptyCatalogue:true});await context.close();
 }
 console.log(JSON.stringify({results,elapsedSeconds:(performance.now()-started)/1000}));
}finally{await browser.close();await server.close();await rm(entryPath,{force:true});}
