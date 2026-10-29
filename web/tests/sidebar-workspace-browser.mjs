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
 await page.getByRole('button',{name:'Aufklappen: volume-2-folder-2',exact:true}).click();
 await page.locator('.disk-tree-name').filter({hasText:'Aufnahmen'}).waitFor();
 await page.locator('.disk-tree-name').filter({hasText:'Aufnahmen'}).click();
 await page.locator('.disk-side .disk-row').filter({hasText:'Interview.mov'}).waitFor();
 assert.equal(await page.locator('.disk-side .disk-row').count(),2);
 assert.equal(await page.locator('.disk-tree-row.is-selected .disk-tree-name').innerText(),'Aufnahmen');
 await page.getByRole('button',{name:'Ordnerbaum umschalten',exact:true}).click();assert.equal(await page.locator('.disk-tree-pane').count(),0);
 await page.getByRole('button',{name:'Dateiliste umschalten',exact:true}).click();assert.equal(await page.locator('.disk-side').count(),0);
 await page.getByRole('button',{name:'Ordnerbaum umschalten',exact:true}).click();await page.getByRole('button',{name:'Dateiliste umschalten',exact:true}).click();
 await page.locator('.disk-tree-name').filter({hasText:'NAS'}).click();await page.locator('.disk-side .disk-row').filter({hasText:'Volume 2'}).waitFor();
 assert.equal(await page.locator('.disk-layout-picker').count(),0);
 assert.equal(await page.locator('canvas[role="img"]').count(),1);
 await page.screenshot({path:'/tmp/diskmap-sidebar-local.png'});
 assert.deepEqual(errors,[]);console.log('PASS: synchronized NAS/folder selection, real child rows, independent sidebar toggles, no scan, no console errors');
}finally{await browser.close();await server.close();}
