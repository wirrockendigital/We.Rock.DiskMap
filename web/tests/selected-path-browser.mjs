import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// Check only the changed selection display, including an obsolete asynchronous response.
const server=await createServer({configFile:false,root:process.cwd(),server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
try{
 const page=await browser.newPage({viewport:{width:1500,height:950}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/design.html`);
 await page.evaluate(async()=>{(await import('/tests/fixtures/selected-path.jsx')).mount();});
 await page.locator('#select-a').click();await page.locator('#select-b').click();
 await page.waitForFunction(()=>document.querySelector('.disk-hover-path')?.textContent==='/volume1/video/nested/b.mov');
 await page.waitForTimeout(400);
 assert.equal(await page.locator('.disk-hover-path').textContent(),'/volume1/video/nested/b.mov');
 assert((await page.locator('.disk-selection-note').textContent()).includes('/volume1/video/nested/b.mov'));
 assert.equal(await page.locator('.disk-selection-size').textContent(),'100 B');
 assert(!(await page.locator('.disk-footer').textContent()).includes('gescannt'));
 assert.deepEqual(errors,[]);console.log('PASS selected path in footer and inspector; stale response discarded; no footer timestamp');
}finally{await browser.close();await server.close();}
