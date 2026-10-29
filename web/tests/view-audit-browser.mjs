import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// Instrument the base canvas independently from the interaction overlay.
const server=await createServer({configFile:false,root:process.cwd(),server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
try{
 const page=await browser.newPage({viewport:{width:1584,height:992}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(()=>{window.paintCounts=new WeakMap();const original=CanvasRenderingContext2D.prototype.fillRect;CanvasRenderingContext2D.prototype.fillRect=function(...args){window.paintCounts.set(this.canvas,(window.paintCounts.get(this.canvas)||0)+1);return original.apply(this,args);};});
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/design.html`);const started=Date.now();await page.evaluate(async()=>{(await import('/tests/fixtures/view-audit.jsx')).mount();});
 await page.waitForFunction(()=>document.querySelector('#view-audit .disk-folder-list')?.dataset.loadedCount==='10000');await page.waitForTimeout(200);const readyMs=Date.now()-started;
 const chart=page.locator('#view-audit .disk-chart-canvas'),box=await chart.boundingBox();const paints=await chart.evaluate(e=>window.paintCounts.get(e));assert(paints>0);
 const durations=[];for(let i=0;i<50;i++){const t=performance.now();await page.mouse.move(box.x+100+(i*17)%(box.width-150),box.y+100+(i*31)%(box.height-150));durations.push(performance.now()-t);}
 assert.equal(await chart.evaluate(e=>window.paintCounts.get(e)),paints);
 const list=page.locator('#view-audit .disk-folder-list');assert(await list.locator('.disk-row').count()<60);await list.evaluate(e=>{e.scrollTop=e.scrollHeight;});await list.getByText('file-9999.bin',{exact:true}).waitFor();const renderedRows=await list.locator('.disk-row').count();assert(renderedRows<60);
 await page.locator('#audit-mode').click();await page.locator('#view-audit .disk-chart-center').waitFor();assert.deepEqual(errors,[]);
 console.log(JSON.stringify({nodes:10000,readyMs,pointerP95Ms:Math.round(durations.sort((a,b)=>a-b)[47]),renderedRows,baseRepaintsOnHover:0,lastRowReachable:true}));
}finally{await browser.close();await server.close();}
