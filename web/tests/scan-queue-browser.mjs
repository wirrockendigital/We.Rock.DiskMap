import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// Check queue dialog interaction without starting filesystem scans.
const server=await createServer({configFile:false,root:process.cwd(),server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
try{
 const page=await browser.newPage({viewport:{width:1500,height:950}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/design.html`);
 await page.evaluate(async()=>{(await import('/tests/fixtures/scan-queue.jsx')).mount();});
 await page.getByRole('button',{name:'Scanwarteschlange',exact:true}).click();
 assert.equal(await page.getByRole('dialog').count(),1);
 assert.equal(await page.locator('.disk-queue-active').textContent(),'Wird gerade gescannthomes');
 assert.equal(await page.getByRole('button',{name:/Aus Warteschlange entfernen:/}).count(),2);
 await page.getByRole('button',{name:'Aus Warteschlange entfernen: Videos'}).click();
 assert.equal(await page.getByRole('button',{name:/Aus Warteschlange entfernen:/}).count(),1);
 assert.equal(await page.locator('.disk-queue-toggle>span').textContent(),'1');
 await page.getByRole('button',{name:'Aus Warteschlange entfernen: Fotos'}).click();
 assert.equal(await page.locator('.disk-queue-toggle>span').count(),0);
 assert.equal(await page.locator('.disk-queue-active strong').textContent(),'homes');
 await page.getByRole('button',{name:'Schließen',exact:true}).click();
 assert.equal(await page.getByRole('dialog').count(),0);
 assert.deepEqual(errors,[]);console.log('PASS queue dialog, order, removal, count, active job preserved');
}finally{await browser.close();await server.close();}
