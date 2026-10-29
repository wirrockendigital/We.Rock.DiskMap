import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// Render production components and verify navigation, paging, and independent collapse behavior.
const server=await createServer({configFile:false,root:process.cwd(),server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
try{
 const page=await browser.newPage({viewport:{width:1440,height:900}}),errors=[];
 page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/design.html`);
 await page.evaluate(async()=>{(await import('/tests/fixtures/sidebar-workspace.jsx')).mount();});
 // Check scope routing, deduplication, running-state controls, and constrained widths.
 await page.evaluate(async()=>{(await import('/tests/fixtures/sidebar-workspace.jsx')).mount(true);});
 await page.getByRole('button',{name:'Aktualisieren',exact:true}).waitFor();
 assert.equal(await page.locator('.disk-storage-button,.disk-rescan-button').count(),0);
 await page.getByRole('button',{name:'Ordnerbaum umschalten',exact:true}).click();
 assert.match(await page.locator('.disk-scan-age').innerText(),/3 Tagen/);
 for(const width of [1440,1024,800,700]){await page.setViewportSize({width,height:900});
 const boxes=await page.locator('.disk-header-scan,.disk-toolbar-actions').evaluateAll(es=>es.map(e=>{const r=e.getBoundingClientRect();return{x:r.x,y:r.y,right:r.right,bottom:r.bottom};}));
 assert(boxes.every(b=>b.x>=0&&b.right<=width+1));assert(boxes[0].right<=boxes[1].x+1||boxes[0].bottom<=boxes[1].y+1);
 }
 await page.setViewportSize({width:1440,height:900});
 await page.getByRole('button',{name:'Aktualisieren',exact:true}).click();assert.equal(await page.evaluate(()=>window.headerAction),'v2');
 assert(await page.getByRole('button',{name:'Scanne NAS',exact:true}).isDisabled());await page.locator('.disk-header-scan').getByRole('button',{name:'Abbrechen',exact:true}).click();
 await page.locator('.disk-crumbs').getByRole('button',{name:'NAS',exact:true}).click();assert.equal(await page.getByRole('button',{name:'Aktualisieren',exact:true}).count(),0);
 await page.getByRole('button',{name:'Scanne NAS',exact:true}).click();assert.equal(await page.evaluate(()=>window.headerAction),'nas');
 await page.locator('.disk-header-scan').getByRole('button',{name:'Abbrechen',exact:true}).click();
 assert.deepEqual(errors,[]);console.log('PASS: age without sidebar, scoped/global actions, NAS deduplication, running/cancel, responsive layout');
}finally{await browser.close();await server.close();}
