import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// Verify real pane components, inherited categories and empty-page continuation.
const server=await createServer({configFile:false,root:process.cwd(),server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
try{
 const page=await browser.newPage({viewport:{width:1584,height:992}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/design.html`);
 await page.evaluate(async()=>{(await import('/tests/fixtures/category-visibility.jsx')).mount();});
 const names=()=>page.locator('.disk-tree-name').allTextContents(),rows=()=>page.locator('.disk-row').allTextContents();
 await page.locator('.disk-row').filter({hasText:'visible.pdf'}).waitFor();
 assert.deepEqual(await names(),['NAS','Photos']);assert.equal((await rows()).length,1);
 assert.deepEqual(await page.evaluate(()=>window.visibilityLoads),['','1','2','3']);
 await page.getByRole('button',{name:'Papierkörbe',exact:true}).click();await page.locator('.disk-row').filter({hasText:'deleted.zip'}).waitFor();
 assert((await names()).includes('#recycle'));assert((await names()).includes('private'));assert(!(await names()).includes('@eaDir'));
 await page.getByRole('button',{name:'Systemdateien',exact:true}).click();await page.locator('.disk-row').filter({hasText:'.DS_Store'}).waitFor();
 assert((await names()).includes('thumbnails'));assert.equal((await rows()).length,4);
 await page.getByRole('button',{name:'Papierkörbe',exact:true}).click();await page.getByRole('button',{name:'Systemdateien',exact:true}).click();
 await page.locator('.disk-row').filter({hasText:'visible.pdf'}).waitFor();
 assert.deepEqual(await names(),['NAS','Photos']);assert.equal((await rows()).length,1);assert.deepEqual(errors,[]);
 // Future scan preferences are off, but the already loaded index remains toggleable.
 const toolbar=page.locator('.disk-category-toolbar'),size=toolbar.locator('strong');
 assert.equal(await size.textContent(),'1 GB');
 const boxes=await toolbar.locator('strong,button').evaluateAll(nodes=>nodes.map(n=>{const r=n.getBoundingClientRect();return {top:r.top,bottom:r.bottom,left:r.left};}));
 assert(boxes.every(b=>b.top<boxes[0].bottom&&b.bottom>boxes[0].top));assert(boxes[0].left<boxes[1].left&&boxes[1].left<boxes[2].left);
 await page.getByRole('button',{name:'Papierkörbe',exact:true}).click();assert.equal(await size.textContent(),'3 GB');
 await page.getByRole('button',{name:'Systemdateien',exact:true}).click();assert.equal(await size.textContent(),'4 GB');
 await page.locator('#availability').click();assert.equal(await toolbar.locator('button:disabled').count(),2);
 assert((await toolbar.locator('span[title]').first().getAttribute('title')).toLowerCase().includes('nicht im geladenen index'));

 // Recycle semantics remain visible even when the stored category is excluded.
 const bin=toolbar.locator('[data-recycle-state]'),button=bin.locator('button');
 await page.locator('#state-denied').click();
 assert.equal(await bin.getAttribute('data-recycle-state'),'denied');
 assert.equal(await button.evaluate(e=>getComputedStyle(e).color),'rgb(255, 98, 107)');
 assert.equal(await bin.evaluate(e=>getComputedStyle(e,'::after').content),'none');
 await page.locator('#state-missing').click();
 assert.equal(await button.isDisabled(),true);
 assert.equal(await button.evaluate(e=>getComputedStyle(e).opacity),'0.3');
 assert.equal(await bin.evaluate(e=>getComputedStyle(e,'::after').backgroundColor),'rgb(255, 98, 107)');
 await page.locator('#state-readable').click();
 assert.equal(await button.evaluate(e=>getComputedStyle(e).opacity),'1');
 assert.equal(await bin.evaluate(e=>getComputedStyle(e,'::after').content),'none');
 await page.locator('#state-unknown').click();
 assert.equal(await bin.evaluate(e=>getComputedStyle(e,'::after').content),'none');
 assert.deepEqual(errors,[]);
 console.log('PASS category visibility, cached tree toggles, inherited recursive rows, empty-page continuation');
}finally{await browser.close();await server.close();}
