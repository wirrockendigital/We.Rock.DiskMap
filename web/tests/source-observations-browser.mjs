import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// Drive the real source-navigation hook with controlled concurrent observations; never touch DSM.
const server=await createServer({root:new URL('../',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
try{
 const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/tests/fixtures/source-observations.html`);
 const count=()=>page.evaluate(()=>window.observationRequests.length);
 const wait=n=>page.waitForFunction(n=>window.observationRequests.length>=n,n,{timeout:5000});
 const values=()=>page.locator('[data-bytes]').evaluateAll(cells=>cells.slice(0,4).map(c=>c.dataset.bytes));
 await wait(1);await page.evaluate(()=>reply(0));await wait(2);const partial=await values();
 await page.evaluate(()=>reply(1));await page.waitForTimeout(50);const initial=await values();
 await page.evaluate(()=>{void startChild();});await wait(3);
 await page.getByRole('button',{name:'Scan completed'}).click();await wait(4);await page.evaluate(()=>reply(3,'3000000000000','2026-10-16T00:00:00Z'));await wait(5);await page.evaluate(()=>reply(4,'3000000000000','2026-10-16T00:00:00Z'));await page.waitForTimeout(50);const fresh=await values();
 await page.evaluate(()=>reply(2));await page.waitForTimeout(50);const afterStale=await values();
 const sameEpoch=await count();await page.evaluate(()=>{void startChild();});await wait(sameEpoch+1);await page.evaluate(i=>reply(i),sameEpoch);await page.waitForTimeout(50);const afterOlderSnapshot=await values();
 const before=await count();await page.getByRole('button',{name:'Scan completed'}).click();await wait(before+1);await page.evaluate(i=>reply(i,'3200000000000','2026-10-17T00:00:00Z',500),before);
 let recovered=false;try{await page.waitForFunction(n=>observationRequests.length>n,before+1,{timeout:4000});const i=await count()-1;await page.evaluate(i=>reply(i,'3200000000000','2026-10-17T00:00:00Z'),i);await wait(i+2);await page.evaluate(i=>reply(i,'3200000000000','2026-10-17T00:00:00Z'),i+1);await page.waitForTimeout(50);recovered=(await values())[3]==='3200000000000';}catch{}
 const beforeUnits=await count(),unitValues=await values();for(let i=0;i<5;i++){await page.getByRole('button',{name:/^Unit /}).click();assert.deepEqual(await values(),unitValues);}assert.equal(await count(),beforeUnits);
 console.log(JSON.stringify({partial,initial,fresh,afterStale,afterOlderSnapshot,recovered,unitRequests:0,errors}));
 {assert.equal(partial[0],'unknown');assert.equal(partial[1],'unknown');assert.deepEqual(fresh,['3032000000000','3031000000000','1000000000','3000000000000']);assert.deepEqual(afterStale,fresh);assert.deepEqual(afterOlderSnapshot,fresh);assert(recovered);assert.deepEqual(errors,[]);}
}finally{await browser.close();await server.close();}
