import assert from 'node:assert/strict';
import {readFile,writeFile,mkdir} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {join} from 'node:path';
import {createHash} from 'node:crypto';
import {createServer} from '../../web/node_modules/vite/dist/node/index.js';
// Golden files are updated only explicitly, then independently inspected before acceptance.
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
const dir=fileURLToPath(new URL('./golden/',import.meta.url));
const source=`
import React from '/node_modules/.vite/deps/react.js';
import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';
const {createRoot}=ReactDOM;
import {TreemapCanvas} from '/src/visualization/treemap/TreemapCanvas.tsx';
import {adaptHierarchy} from '/src/visualization/shared/adapter.ts';
import {treemapPresentation} from '/src/visualization/treemap/presentation.ts';
import {paintTreemap} from '/src/visualization/treemap/paint.ts';
const s=(b,f=0,d=0,u=0)=>({knownBytes:String(b),fileCount:String(f),directoryCount:String(d),unknownFileCount:String(u),specialCount:'0'});
const n=(id,parentId,kind,summary,name=id)=>({id,parentId,kind,name,realNodeId:['nas','volume','share'].includes(kind)?null:id,virtual:['nas','volume','share'].includes(kind),summary,originalSummary:summary,matchSummary:summary,matchKind:'direct',hasMore:false,classification:kind==='file'?{category:'document'}:null,modifiedAt:'2026-10-01T00:00:00Z',...(kind==='share'?{provenance:{version:1,authority:'native-inventory',sourceId:'share',qualification:'qualified',state:'reachable'}}:{})});
const total=s(3600,1004,3,1),nodes=[n('nas',null,'nas',total,'Archive NAS'),n('volume','nas','volume',total,'Volume 1'),n('share','volume','share',total,'Documents'),n('root','share','directory',total,'Working files'),n('d','root','directory',s(2000,2,1),'Long project title with an extensive descriptive subtitle that must be shortened'),n('a','d','file',s(1200,1),'Project documentation.pdf'),n('b','d','file',s(800,1),'Source archive.zip'),n('direct','root','file',s(600,1),'Direct file.txt'),n('tiny','root','directory',s(1000,1001,1,1),'Many tiny files'),n('unknown','tiny','file',s(0,1,0,1),'Unknown')];for(let i=0;i<1000;i++)nodes.push(n('f'+i,'tiny','file',s(1,1),'Tiny '+i));
const wire={snapshotId:'synthetic-golden',rootId:'nas',metric:'logical',mode:'highlight',state:'partial',truncated:false,selectionOrder:'recorded-rowid',nodes,originalSummary:total,matchSummary:total,denominator:total,matchState:'matches',filter:{},coverage:[],sourceAvailability:{total:'0',records:[],truncated:false}};
const model=adaptHierarchy(wire,{snapshotId:wire.snapshotId,rootId:wire.rootId,metric:'logical',mode:'highlight',maxNodes:10000});
const getContext=HTMLCanvasElement.prototype.getContext;window.failPaint=false;HTMLCanvasElement.prototype.getContext=function(...args){return window.failPaint&&this.isConnected?null:getContext.apply(this,args);};
window.actions=[];window.geometry=[];const root=createRoot(document.getElementById('app'));window.renderTest=async(config)=>{window.geometryCalls=[];window.ready=false;document.body.style.background=config.theme==='dark'?'#111827':'#f3f4f6';const options={...config,colorMode:'filetype',referenceTime:'2026-10-10T00:00:00Z',selectedNodeId:null,focusedNodeId:config.focusedNodeId??null};root.render(React.createElement(TreemapCanvas,{...options,model,devicePixelRatio:config.dpr??1,disabled:config.disabled??false,onSelect:id=>window.actions.push(['select',id]),onFocus:id=>window.actions.push(['focus',id]),onOpen:id=>window.actions.push(['open',id]),onGroup:target=>window.actions.push(['group',target]),maxDepth:8,maxElements:10000,onGeometry:g=>{window.geometryCalls.push(g.length);window.geometry=g;if(g.length)window.ready=true;}}));const presentation=treemapPresentation(model,{width:config.width,height:config.height,maxDepth:8,maxElements:10000,decoration:true});const layout=presentation.layout;const canvas=document.createElement('canvas');window.labels=paintTreemap(canvas.getContext('2d'),presentation.model,layout,options);window.report={nodes:layout.rectangles.length,zeroIds:layout.zeroIds,omitted:layout.omitted.length};};
`;
let server,browser;
try{
 server=await createServer({root:fileURLToPath(new URL('../../web/',import.meta.url)),server:{host:'127.0.0.1',port:0},plugins:[{name:'treemap-golden-fixture',resolveId(id){if(id==='/fixture.js')return '\0treemap-fixture';},load(id){if(id==='\0treemap-fixture')return source;},configureServer(s){s.middlewares.use(async(req,res,next)=>{if(req.url==='/fixture.js'){res.setHeader('Content-Type','text/javascript');res.end(source);}else if(req.url==='/golden'){res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml('/golden','<!doctype html><html><body style="margin:0"><main id="app"></main><script type="module" src="/fixture.js"></script></body></html>'));}else next();});}}]});await server.listen();
 browser=await chromium.launch({headless:true,...(process.env.DISKMAP_CHROMIUM_PATH?{executablePath:process.env.DISKMAP_CHROMIUM_PATH}:{})});let page;const errors=[],receipts=[];await mkdir(dir,{recursive:true});
 for(const [width,height,dpr] of [[1024,700,1],[320,480,1],[1920,1080,2]]){
  await page?.close();page=await browser.newPage({viewport:{width,height},deviceScaleFactor:dpr});page.on('pageerror',e=>{errors.push(e.message);console.error(e.message);});await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/golden`);await page.waitForFunction(()=>typeof window.renderTest==='function');assert.equal(await page.evaluate(()=>window.devicePixelRatio),dpr);
  for(const language of ['de','en'])for(const theme of ['light','dark']){
   await page.evaluate(c=>window.renderTest(c),{width,height,dpr,language,theme});await page.waitForFunction(()=>window.ready);await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
   const result=await page.evaluate(()=>({labels:window.labels,report:window.report}));assert.ok(result.report.nodes>=10&&result.report.nodes<=1009);assert.deepEqual(result.report.zeroIds,['unknown']);assert.deepEqual(await page.locator('canvas').evaluate(c=>[c.width,c.height]),[width*dpr,height*dpr]);
   for(let i=0;i<result.labels.length;i++)for(let j=i+1;j<result.labels.length;j++){const a=result.labels[i].bounds,b=result.labels[j].bounds;assert.ok(!(a.x<b.x+b.width&&b.x<a.x+a.width&&a.y<b.y+b.height&&b.y<a.y+a.height),'overlapping labels');}
   const name=`${width}${dpr===2?'-dpr2':''}-${language}-${theme}.png`,actual=await page.locator('canvas').screenshot(),second=await page.locator('canvas').screenshot();assert.deepEqual(actual,second,'same-state pixels differ');if(process.env.DISKMAP_UPDATE_GOLDEN==='1')await writeFile(join(dir,name),actual);else assert.deepEqual(actual,await readFile(join(dir,name)),name+' differs from reviewed golden');receipts.push({name,labels:result.labels.length,...result.report,sha256:createHash('sha256').update(actual).digest('hex')});
  }
 }
 // Real pointer, keyboard and touch events reuse painted CSS geometry without re-layout.
 await page.evaluate(()=>window.renderTest({width:320,height:480,language:'en',theme:'light'}));await page.waitForFunction(()=>window.ready);
 const before=await page.evaluate(()=>JSON.stringify(window.geometry,(_,v)=>typeof v==='bigint'?String(v):v));
 const location=await page.evaluate(()=>{const r=window.geometry.find(x=>x.target.kind==='model'&&x.target.nodeId==='direct');return {x:r.x+r.width/2,y:r.y+r.height/2};});
 await page.mouse.move(location.x,location.y);await page.mouse.click(location.x,location.y);
 assert.ok((await page.evaluate(()=>window.actions)).some(a=>a[0]==='select'&&a[1]==='direct'));
 assert.equal(await page.evaluate(()=>JSON.stringify(window.geometry,(_,v)=>typeof v==='bigint'?String(v):v)),before);
 await page.evaluate(()=>{window.actions=[];return window.renderTest({width:320,height:480,language:'en',theme:'light',focusedNodeId:'b'});});await page.waitForFunction(()=>window.ready);await page.locator('canvas').focus();await page.keyboard.press('Enter');assert.ok((await page.evaluate(()=>window.actions)).some(a=>a[0]==='select'&&a[1]==='b'));

 await page.locator('canvas').focus();await page.keyboard.press('Home');await page.keyboard.press('Enter');assert.ok((await page.evaluate(()=>window.actions)).some(a=>a[0]==='select'&&a[1]==='nas'));
 const groupPoint=await page.evaluate(()=>{const r=window.geometry.find(x=>x.target.kind==='display-group');return {x:r.x+r.width/2,y:r.y+r.height/2};});
 await page.evaluate(()=>window.actions=[]);await page.mouse.click(groupPoint.x,groupPoint.y);const grouped=await page.evaluate(()=>window.actions);assert.equal(grouped.length,1);assert.equal(grouped[0][0],'group');assert.equal(grouped[0][1].kind,'display-group');
 await page.locator('canvas').dispatchEvent('pointermove',{pointerType:'touch',clientX:location.x,clientY:location.y});await page.locator('canvas').dispatchEvent('click',{clientX:location.x,clientY:location.y});assert.ok((await page.evaluate(()=>window.actions)).some(a=>a[0]==='select'&&a[1]==='direct'));
 await page.evaluate(()=>{window.actions=[];return window.renderTest({width:320,height:480,language:'en',theme:'light',disabled:true});});await page.waitForFunction(()=>window.ready);await page.mouse.click(location.x,location.y);assert.deepEqual(await page.evaluate(()=>window.actions),[]);
 // A failed draw must publish no hit geometry, and a later valid draw must recover.
 await page.evaluate(()=>{window.failPaint=true;return window.renderTest({width:320,height:480,language:'en',theme:'light'});});await page.getByRole('status').waitFor();assert.ok((await page.evaluate(()=>window.geometryCalls)).every(n=>n===0));assert.equal(await page.locator('canvas').isVisible(),false);
 await page.evaluate(()=>{window.failPaint=false;return window.renderTest({width:320,height:480,language:'en',theme:'dark'});});await page.waitForFunction(()=>window.ready);assert.equal(await page.locator('canvas').isVisible(),true);
 assert.deepEqual(errors,[]);console.log(JSON.stringify({ok:true,method:'real Chromium production component, synthetic hierarchy, exact reviewed PNG comparison',receipts}));
}finally{await browser?.close();await server?.close();}
