import assert from 'node:assert/strict';
import {createServer} from '../../web/node_modules/vite/dist/node/index.js';

// Exercise production components and CSS with bounded synthetic data, never a DSM endpoint.
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
const moduleSource=`
import React from 'react';import{createRoot}from'react-dom/client';
import '/src/style.css';
import{AnalysisNodeList}from'/src/features/analysisinteraction/AnalysisNodeList';
import{SearchResultList}from'/src/features/searchlist/SearchResultList';
const params=new URLSearchParams(location.search),language=params.get('lang'),kind=params.get('kind'),shape=params.get('shape')??'long';
const summary={knownBytes:'42',unknownFileCount:'0',fileCount:'1',directoryCount:'0',specialCount:'0'};
const name=i=>shape!=='long'?'File '+i:(language==='de'?'Erfasste Datei mit langem Namen Grüße ':'Recorded file with long name Unicode Grüße ')+String(i).padStart(3,'0')+' '+('Unicode_Ä_長_' .repeat(12));
const total=shape==='empty'?0:shape==='short'?1:kind==='analysis'?101:51;
window.probe={calls:[],pages:[],total,name,ready:false};
const view={profileId:'p',snapshotId:'s',rootId:'r',selectedNodeId:null,map:'treemap',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'name',direction:'asc'};
const nodes=Array.from({length:total},(_,i)=>({id:'n'+i,realNodeId:'n'+i,parentId:'r',name:name(i),kind:'file',virtual:false,summary,hasMore:false}));
function App(){const[selected,set]=React.useState(null),[disabled,setDisabled]=React.useState(false),[root,setRoot]=React.useState('r');
 const callbacks={onSelect:n=>{probe.calls.push(['select',n.id]);set(n.id);},onDetails:n=>probe.calls.push(['details',n.id]),onOpen:n=>probe.calls.push(['open',n.id]),onFileStation:n=>probe.calls.push(['fs',n.id]),onSessionDenied:()=>probe.calls.push(['denied'])};
 probe.externalSelect=set;probe.disable=setDisabled;probe.resetRoot=setRoot;
 const loadPage=async cursor=>{probe.pages.push(cursor);const start=cursor==='next'?50:0,items=nodes.slice(start,start+50).map(n=>({snapshotId:'s',nodeId:n.id,parentId:'r',name:n.name,path:(shape==='long'?'Long directory / '.repeat(30):'Folder / ')+n.name,kind:'file',volumeId:'v',shareId:null,modifiedAt:null,size:'42',summary}));return{snapshotId:'s',rootId:root,metric:'logical',scope:'recursive',selectionOrder:'name-asc-recorded-rowid',items,hasMore:total>50&&start===0,nextCursor:total>50&&start===0?'next':'',workLimited:false};};
 React.useEffect(()=>{probe.ready=true;},[]);
 return React.createElement('main',{className:'analysis-main'},React.createElement('button',{id:'start'},'Start'),React.createElement(kind==='analysis'?AnalysisNodeList:SearchResultList,{...callbacks,language,disabled,fileStationDisabled:true,selectedNodeId:selected,...(kind==='analysis'?{identity:{profileId:'p',viewId:'lease',snapshotId:'s',rootId:root,metric:'logical',epoch:1},nodes}:{view:{...view,rootId:root},pin:{profileId:'p',viewId:'lease',snapshotId:'s'},loadPage})}),React.createElement('button',{id:'end'},'End'));
}
createRoot(document.getElementById('root')).render(React.createElement(App));`;
let browser,server;
const results=[],errors=[];
try{
 server=await createServer({configFile:false,plugins:[{name:'ui-scroll-fixture',resolveId(id){if(id==='/scroll-fixture.mjs')return '\0ui-scroll-fixture';},load(id){if(id==='\0ui-scroll-fixture')return moduleSource;}}],esbuild:{jsx:'automatic'},root:new URL('../../web/',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
 const origin=`http://127.0.0.1:${server.httpServer.address().port}`;
 browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
 const page=await browser.newPage();page.setDefaultTimeout(10000);page.on('pageerror',e=>errors.push(e.message));
 await page.route(origin+'/scroll-fixture?*',async route=>route.fulfill({contentType:'text/html',body:await server.transformIndexHtml('/scroll-fixture','<div id="root"></div><script type="module" src="/scroll-fixture.mjs"></script>')}));
 // Any attempted remote/API access fails rather than fabricating a successful production response.
 await page.route('**/api.cgi?**',route=>{errors.push('UNEXPECTED_API');return route.abort();});
 const tabTo=async target=>{for(let i=0;i<650;i++){if(await target.evaluate(e=>e===document.activeElement))return;await page.keyboard.press('Tab');}throw Error('KEYBOARD_TARGET_UNREACHABLE');};
 const activate=async target=>{await tabTo(target);await page.keyboard.press('Enter');};
 const open=async(lang,kind,shape='long')=>{await page.goto(`${origin}/scroll-fixture?lang=${lang}&kind=${kind}&shape=${shape}`);await page.waitForFunction(()=>window.probe?.ready);await page.locator('table').waitFor();};
 for(const [width,height]of [[1024,700],[1280,800],[1440,921]])for(const lang of ['de','en'])for(const kind of ['analysis','search']){
  await page.setViewportSize({width,height});await open(lang,kind);
  const region=page.locator('.table-scroll'),table=region.locator('table'),rows=table.locator('tbody tr');
  const count=kind==='analysis'?100:50;assert.equal(await rows.count(),count);
  const geometry=await region.evaluate(e=>({height:e.clientHeight,content:e.scrollHeight,overflow:getComputedStyle(e).overflowY,max:getComputedStyle(e).maxHeight,wide:document.documentElement.scrollWidth>innerWidth+1,pagingInside:e.querySelector('nav')!==null,tabIndex:e.tabIndex,role:e.getAttribute('role'),label:e.getAttribute('aria-label')}));
  assert.ok(geometry.height<=height*.42+1);assert.ok(geometry.content>geometry.height);assert.equal(geometry.overflow,'auto');assert.equal(geometry.wide,false);assert.equal(geometry.pagingInside,false);assert.equal(geometry.tabIndex,0);assert.equal(geometry.role,'region');assert.ok(geometry.label);
  // Real keyboard input must scroll the inner viewport while keeping document position stable.
  await tabTo(region);const before=await region.evaluate(e=>({inner:e.scrollTop,outer:scrollY}));await page.keyboard.press('PageDown');await page.waitForFunction(v=>document.querySelector('.table-scroll').scrollTop>v,before.inner);
  assert.ok(Math.abs(await page.evaluate(()=>scrollY)-before.outer)<=1);
  const last=rows.last().locator('td button').first();await activate(last);assert.deepEqual(await page.evaluate(()=>probe.calls.at(-1)),['select','n'+(count-1)]);
  const nav=page.locator('nav'),buttons=nav.getByRole('button'),next=buttons.last(),previous=kind==='analysis'?buttons.first():buttons.nth(1);
  const selectedCalls=await page.evaluate(()=>probe.calls.length);await activate(next);await page.waitForFunction(()=>document.querySelectorAll('tbody tr').length===1);assert.ok((await rows.first().innerText()).includes(String(count).padStart(3,'0')));
  assert.equal(await page.evaluate(()=>probe.calls.length),selectedCalls);await activate(previous);await page.waitForFunction(n=>document.querySelectorAll('tbody tr').length===n,count);
  if(kind==='search')assert.deepEqual(await page.evaluate(()=>probe.pages),['','next','']);
  else{await page.evaluate(()=>probe.externalSelect('n100'));await page.waitForFunction(()=>document.querySelectorAll('tbody tr').length===1);await page.evaluate(()=>{probe.externalSelect(null);probe.resetRoot('r2');});await page.waitForFunction(()=>document.querySelectorAll('tbody tr').length===100);}
  await page.evaluate(()=>probe.disable(true));await page.waitForFunction(()=>document.querySelector('.table-scroll').tabIndex===-1);assert.equal(await next.isEnabled(),false);
  results.push({width,height,lang,kind,rows:count,keyboard:true,paging:true,geometry});
 }
 // Short and empty lists must shrink naturally; paging and the explicit empty message remain available.
 for(const lang of ['de','en'])for(const kind of ['analysis','search'])for(const shape of ['short','empty']){
  await page.setViewportSize({width:1024,height:700});await open(lang,kind,shape);
  const region=page.locator('.table-scroll');assert.equal(await region.locator('tbody tr').count(),shape==='short'?1:0);
  const g=await region.evaluate(e=>({height:e.clientHeight,content:e.scrollHeight}));assert.ok(g.height<294);assert.ok(g.content<=g.height+1);
  assert.equal(await page.locator('nav button:enabled').count(),0);
  if(shape==='empty'&&kind==='search')assert.ok((await page.locator('section > [role="status"]').innerText()).length>0);
  results.push({lang,kind,shape,geometry:g});
 }
 assert.deepEqual(errors,[]);console.log(JSON.stringify({scope:'local production list components and CSS; synthetic records; no DSM or performance acceptance',passed:results.length,results}));
}finally{await browser?.close();await server?.close();}
