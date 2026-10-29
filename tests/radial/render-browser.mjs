import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {createServer} from '../../web/node_modules/vite/dist/node/index.js';
import {radialFixture,deepRadialFixture} from './fixture.mjs';
// Opt-in real-browser visual qualification; screenshots and receipts stay outside source control.
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
const clippingOnly=process.env.DISKMAP_RADIAL_CLIPPING_ONLY==='1';
const output=process.env.DISKMAP_RADIAL_GOLDENS??'/tmp/diskmap-wi078-goldens';await mkdir(output,{recursive:true});
const source=`${clippingOnly?"import '/src/style.css';":''}import React from 'react';import{createRoot}from'react-dom/client';import{RadialCanvas}from'/src/visualization/radial/RadialCanvas.tsx';import{adaptHierarchy}from'/src/visualization/shared/adapter.ts';
const wire=${JSON.stringify(radialFixture())};const model=adaptHierarchy(wire,{snapshotId:wire.snapshotId,rootId:wire.rootId,metric:wire.metric,mode:wire.mode,maxNodes:2000});
const deepWire=${JSON.stringify(deepRadialFixture())};window.deepModel=adaptHierarchy(deepWire,{snapshotId:deepWire.snapshotId,rootId:deepWire.rootId,metric:deepWire.metric,mode:deepWire.mode,maxNodes:2000});window.model=model;window.receipts=[];window.actions=[];window.up=0;let props={model,width:1024,height:650,devicePixelRatio:2,colorMode:'folder',referenceTime:null,selectedNodeId:null,focusedNodeId:null,disabled:false,language:'de',theme:'light',canGoUp:true,onSelect:id=>window.actions.push(['select',id]),onFocus:id=>{window.actions.push(['focus',id]);window.update({focusedNodeId:id});},onOpen:id=>window.actions.push(['open',id]),onGroup:t=>window.actions.push(['group',t.kind]),onUp:()=>window.up++,onGeometry:g=>window.receipts.push(JSON.stringify(g))};
const root=createRoot(document.getElementById('app'));window.update=next=>{props={...props,...next};root.render(React.createElement(RadialCanvas,props));};window.update({});`;
let server,browser;
try{
 server=await createServer({...(clippingOnly?{configFile:false,esbuild:{jsx:'automatic'}}:{}),root:new URL('../../web/',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error',plugins:[{name:'radial-golden',resolveId(id){if(id==='virtual:radial-golden')return '\0virtual:radial-golden';},load(id){if(id==='\0virtual:radial-golden')return source;}}]});await server.listen();const origin=server.resolvedUrls.local[0];
 browser=await chromium.launch({headless:true,...(process.env.DISKMAP_BROWSER_EXECUTABLE?{executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE}:{})});// Load production global CSS and measure real rendered content and hit targets after resize.
 if(clippingOnly){
  const cases=[];
  for(const dpr of [1,2]){
   const context=await browser.newContext({viewport:{width:1024,height:700},deviceScaleFactor:dpr});const page=await context.newPage();
   await page.route(origin+'radial-clipping',async r=>r.fulfill({contentType:'text/html',body:await server.transformIndexHtml('/radial-clipping','<!doctype html><html><div id="app"></div><script type="module" src="/@id/__x00__virtual:radial-golden"></script></html>')}));
   await page.goto(origin+'radial-clipping');await page.waitForFunction(()=>window.receipts?.some(x=>x!=='[]'));
   for(const [width,height] of [[1024,700],[1280,800],[1440,921]]){
    await page.setViewportSize({width,height});await page.evaluate(p=>window.update(p),{width:width-462,height:350,devicePixelRatio:dpr});
    await page.waitForFunction(w=>document.querySelector('canvas')?.getBoundingClientRect().width===w,width-462);
    const result=await page.evaluate(async()=>{
     await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
     const canvas=document.querySelector('canvas'),section=canvas.closest('section'),c=canvas.getBoundingClientRect(),r=section.getBoundingClientRect(),style=getComputedStyle(section);
     const left=r.left+parseFloat(style.borderLeftWidth)+parseFloat(style.paddingLeft),right=r.right-parseFloat(style.borderRightWidth)-parseFloat(style.paddingRight),top=r.top+parseFloat(style.borderTopWidth)+parseFloat(style.paddingTop);
     const inset=1/devicePixelRatio,points=[[c.left+inset,c.top+inset],[c.right-inset,c.top+inset],[c.left+inset,c.bottom-inset],[c.right-inset,c.bottom-inset]];
     return{dpr:devicePixelRatio,canvas:[c.x,c.y,c.width,c.height],content:[left,top,right-left],contained:c.left>=left-.01&&c.right<=right+.01&&c.top>=top-.01&&c.bottom<=r.bottom+.01,hits:points.map(([x,y])=>document.elementFromPoint(x,y)===canvas),backing:[canvas.width,canvas.height],rootName:section.querySelector('strong')?.textContent,expectedRoot:window.model.nodes[window.model.rootId].name};
    });
    assert.equal(result.contained,true,JSON.stringify(result));assert.deepEqual(result.hits,[true,true,true,true],JSON.stringify(result));assert.equal(result.backing[0],(width-462)*dpr);assert.equal(result.backing[1],246*dpr);assert.equal(result.rootName,result.expectedRoot);cases.push({viewport:[width,height],...result});
   }
   await context.close();
  }
  console.log(JSON.stringify({passed:true,scope:'real global CSS radial clipping and resize',cases}));
 }else{
 const context=await browser.newContext({hasTouch:true,viewport:{width:1100,height:760},deviceScaleFactor:2});const page=await context.newPage(),errors=[];page.on('pageerror',e=>{errors.push(e.message);console.error(e.message);});
 await page.route(origin+'radial-golden',async r=>r.fulfill({contentType:'text/html',body:await server.transformIndexHtml('/radial-golden','<!doctype html><html><style>body{margin:0;font:14px sans-serif}button:focus-visible{outline:3px solid #005fcc}p{margin:6px 0}</style><div id="app"></div><script type="module" src="/@id/__x00__virtual:radial-golden"></script></html>')}));await page.goto(origin+'radial-golden');await page.waitForFunction(()=>window.receipts?.some(x=>x!=='[]'));
 for(const width of [1024,320,1920])for(const language of ['de','en'])for(const theme of ['light','dark']){
  await page.setViewportSize({width:Math.max(width,400),height:width===1920?1080:760});await page.evaluate(v=>window.update(v),{width,height:width===1920?1080:650,language,theme});await page.waitForFunction(()=>document.querySelector('section')?.getAttribute('aria-busy')==='false');await page.waitForTimeout(80);
  const dimensions=await page.locator('canvas').evaluate(c=>({width:c.width,height:c.height,cssWidth:c.getBoundingClientRect().width,cssHeight:c.getBoundingClientRect().height}));assert.equal(dimensions.width,width*2);assert.equal(dimensions.height,(width===1920?976:546)*2);
  const section=page.locator('section');assert.equal(await section.evaluate(e=>e.getBoundingClientRect().height),width===1920?1080:650);await page.getByRole('button',{name:language==='de'?'Eine Ebene hoch':'Up one level'}).focus();assert.equal(await page.evaluate(()=>document.activeElement?.tagName),'BUTTON');await page.keyboard.press('Enter');
  const first=await page.screenshot({path:output+'/'+width+'-'+language+'-'+theme+'.png'});assert.deepEqual(await page.screenshot(),first,'same-state pixels differ');
 }
 assert.equal(await page.evaluate(()=>window.up),12);
 // State repaint preserves hit geometry; disabled actions and paint failure never publish active hits.
 const before=await page.evaluate(()=>window.receipts.at(-1));await page.evaluate(()=>window.update({selectedNodeId:'large',focusedNodeId:'folder',disabled:true}));await page.waitForTimeout(80);assert.equal(await page.evaluate(()=>window.receipts.at(-1)),before);assert.equal(await page.getByRole('button',{name:'Up one level'}).isDisabled(),true);
 await page.evaluate(()=>{window.originalGetContext=HTMLCanvasElement.prototype.getContext;HTMLCanvasElement.prototype.getContext=function(){return null;};window.update({colorMode:'mtime'});});await page.waitForTimeout(80);assert.equal(await page.evaluate(()=>window.receipts.at(-1)),'[]');await page.getByText('Map unavailable. The object list remains available.').waitFor();
 await page.evaluate(()=>{HTMLCanvasElement.prototype.getContext=window.originalGetContext;window.update({colorMode:'folder'});});await page.waitForTimeout(80);assert.notEqual(await page.evaluate(()=>window.receipts.at(-1)),'[]');assert.equal(await page.getByText('Map unavailable. The object list remains available.').count(),0);assert.deepEqual(errors,[]);
 // The center remains bounded at eight actual levels, with the parent action in the caption.
 await page.setViewportSize({width:400,height:760});await page.evaluate(()=>window.update({model:window.deepModel,width:320,height:650,disabled:false,maxDepth:8}));await page.waitForTimeout(100);assert.equal(await page.getByRole('button',{name:'Up one level'}).evaluate(e=>e.closest('[tabindex=\"0\"]')!==null),true);await page.screenshot({path:output+'/320-en-dark-depth8.png'});
 // Real pointer and keyboard events share the painted targets; touch uses the same click path.
 await page.evaluate(()=>window.update({model:window.model,width:1024,height:650,disabled:false,maxDepth:8,selectedNodeId:null,focusedNodeId:null}));
 await page.setViewportSize({width:1100,height:760});await page.waitForTimeout(100);
 const targetPoint=async(id,kind='model')=>page.evaluate(({id,kind})=>{const g=JSON.parse(window.receipts.at(-1)).find(x=>x.target.kind===kind&&(kind!=='model'||x.target.nodeId===id));const r=(g.innerRadius+g.outerRadius)/2,a=(g.startAngle+g.endAngle)/2,b=document.querySelector('canvas').getBoundingClientRect();return{x:b.left+g.cx+Math.cos(a)*r,y:b.top+g.cy+Math.sin(a)*r};},{id,kind});
 let point=await targetPoint('large');await page.mouse.dblclick(point.x,point.y);assert.equal((await page.evaluate(()=>window.actions)).filter(x=>x[0]==='open').length,0);
 point=await targetPoint('folder');await page.mouse.dblclick(point.x,point.y);assert.deepEqual((await page.evaluate(()=>window.actions)).filter(x=>x[0]==='open'),[['open','folder']]);
 point=await targetPoint('', 'display-group');await page.touchscreen.tap(point.x,point.y);assert.deepEqual((await page.evaluate(()=>window.actions)).at(-1),['group','display-group']);
 await page.locator('canvas').focus();await page.keyboard.press('Home');await page.keyboard.press('ArrowDown');await page.keyboard.press('Enter');assert.ok((await page.evaluate(()=>window.actions)).some(x=>x[0]==='focus'));
 const actionCount=await page.evaluate(()=>window.actions.length);await page.evaluate(()=>window.update({disabled:true}));await page.waitForTimeout(30);await page.mouse.click(point.x,point.y);assert.equal(await page.evaluate(()=>window.actions.length),actionCount);
 console.log(JSON.stringify({passed:true,browser:browser.version(),cases:13,output,geometryStable:true,disabled:true,paintFailureRecovery:true,pointerKeyboardTouch:true,fixture:'synthetic-24nodes',memoryClaim:false}));
}
}finally{await browser?.close();await server?.close();}
