import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
import {writeFile,rm} from 'node:fs/promises';
import {fixture} from './node-details-fixture.mjs';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// This synthetic local browser test exercises real hooks, requests and layout, not native DSM acceptance.
const entry=new URL('../.details-fixture.mjs',import.meta.url);
await writeFile(entry,`import React from 'react';import{createRoot}from'react-dom/client';import{HistoricalNodeDetails}from'./src/NodeDetails.tsx';import './src/details-panel.css';window.SYNO={SDS:{Session:{SynoToken:'fixture'}}};function Demo(){const[language,L]=React.useState('en');const[selection,S]=React.useState({profileId:'p',viewId:'v',snapshotId:'s',rootId:'root',nodeId:'n',metric:'logical',epoch:1});const[disabled,D]=React.useState(false);window.detailsFixture={S,D,L};return React.createElement('main',{style:{width:'320px',maxWidth:'100%',height:'660px',overflow:'auto'}},React.createElement(HistoricalNodeDetails,{language,selection,disabled,onSessionDenied:()=>{window.denied=true;D(true)}}))}createRoot(document.getElementById('root')).render(React.createElement(Demo));`);
const server=await createServer({root:new URL('../',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const origin=`http://127.0.0.1:${server.httpServer.address().port}`;
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
const page=await browser.newPage({viewport:{width:1024,height:700}}),errors=[],calls=[];page.on('pageerror',e=>{errors.push(e.message);console.error(e.message);});
let held,heldReply;
try{
 await page.route(origin+'/fixture',async route=>route.fulfill({contentType:'text/html',body:await server.transformIndexHtml('/fixture','<div id="root"></div><script type="module" src="/.details-fixture.mjs"></script>')}));
 await page.route('**/api.cgi?action=node-details',async route=>{
  const input=route.request().postDataJSON();calls.push(input);
  const v=fixture();v.detail.rootId=input.rootId;v.detail.nodeId=input.nodeId;v.detail.node.id=input.nodeId;v.paths.nodeId=input.nodeId;v.detail.name=input.nodeId+' '+('Longü & <text> '.repeat(35));
  v.detail.operation=input.operation;
  if(input.nodeId==='held-success'||input.nodeId==='held-error'){held=input.nodeId;await new Promise(r=>heldReply=r);if(input.nodeId==='held-error'){await route.fulfill({status:409,contentType:'application/json',body:JSON.stringify({contractVersion:1,error:{code:'VIEW_EXPIRED'}})}).catch(()=>{});return;}}
  if(input.nodeId==='denied'){await route.fulfill({status:401,body:'{}'});return;}
  if(input.operation!=='details'){const folder=input.operation==='direct-folders';v.detail.rankingSemantics=folder?'direct-real-folders-disjoint-subtrees':'recursive-file-path-entries-not-exclusive-physical-bytes';v.detail.ranking={snapshotId:'s',rootId:input.nodeId,metric:'logical',scope:folder?'direct':'recursive',selectionOrder:'size-desc-recorded-rowid',hasMore:!input.cursor,workLimited:!input.cursor,nextCursor:input.cursor?'':'next',items:input.cursor?[{snapshotId:'s',nodeId:'rank-file',name:'Final ranked item',path:'saved/path',kind:folder?'directory':'file',size:'9007199254740993',summary:v.detail.numerator}]:[]};}
  await route.fulfill({contentType:'application/json',body:JSON.stringify({contractVersion:1,data:v})}).catch(()=>{});
 });
 await page.goto(origin+'/fixture');await page.getByRole('heading',{name:'Selection details'}).waitFor();
 await page.getByRole('combobox',{name:'Display unit'}).selectOption('GB');assert.ok(await page.getByText('9007199254740993 B',{exact:false}).count()>0);
 const recursive=page.locator('section').filter({has:page.getByRole('heading',{name:'Largest recursively contained files',exact:true})}).last();
 await recursive.getByRole('button',{name:'Load ranking'}).focus();await page.keyboard.press('Enter');await recursive.getByText('Work budget reached',{exact:false}).waitFor();
 await recursive.getByRole('button',{name:'Next page'}).click();await recursive.getByText('Final ranked item',{exact:true}).waitFor();assert.equal(calls.at(-1).cursor,'next');
 for(const name of ['held-success','held-error']){
  held=null;await page.evaluate(nodeId=>window.detailsFixture.S(s=>({...s,nodeId,epoch:s.epoch+1})),name);await page.waitForFunction(()=>document.querySelector('[aria-busy="true"]'));
  for(let n=0;!held&&n<50;n++)await new Promise(r=>setTimeout(r,10));assert.equal(held,name);
  await page.evaluate(()=>window.detailsFixture.S(s=>({...s,nodeId:'fresh',epoch:s.epoch+1})));await page.getByRole('heading',{name:'Selection details'}).waitFor();heldReply();await page.waitForTimeout(60);
  assert.match(await page.getByRole('textbox',{name:'Full name',exact:true}).inputValue(),/^fresh /);assert.equal(await page.getByRole('alert').count(),0);
 }
 await page.evaluate(()=>window.detailsFixture.L('de'));await page.getByRole('heading',{name:'Details der Auswahl'}).waitFor();
 const layout=await page.evaluate(()=>({document:document.documentElement.scrollWidth,width:innerWidth,panel:document.querySelector('main').getBoundingClientRect().width}));assert.ok(layout.document<=layout.width);assert.equal(layout.panel,320);
 await page.getByRole('combobox',{name:'Anzeigeeinheit'}).focus();await page.keyboard.press('Tab');assert.ok(await page.evaluate(()=>document.activeElement.tagName!=='BODY'));
 await page.evaluate(()=>window.detailsFixture.S(s=>({...s,nodeId:'denied',epoch:s.epoch+1})));await page.waitForFunction(()=>window.denied===true);assert.equal(await page.getByRole('textbox').count(),0);
 assert.deepEqual(errors,[]);console.log(JSON.stringify({passed:true,viewport:'1024x700',panelWidth:320,languages:['de','en'],keyboard:true,exactBytes:true,rankWorkLimitedAndContinuation:true,staleSuccessAndError:true,sessionClear:true,requests:calls.length}));
}finally{await browser.close();await server.close();await rm(entry,{force:true});}
