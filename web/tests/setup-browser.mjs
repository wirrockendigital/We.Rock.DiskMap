import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// Verify state transitions, errors and small windows using production components.
const server=await createServer({configFile:false,root:process.cwd(),server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const browser=await chromium.launch({headless:true});
try{
 const page=await browser.newPage({viewport:{width:1400,height:900}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/design.html`);
 await page.evaluate(async()=>{(await import('/tests/fixtures/setup-workspace.jsx')).mount();});
 await page.getByRole('heading',{name:'Gib deinem Speicher eine Karte'}).waitFor();
 await page.screenshot({path:'/tmp/diskmap-setup-permissions.png'});
 await page.evaluate(()=>{window.setupFail=true;});
 await page.getByRole('button',{name:'Freigaben erneut prüfen'}).click();
 await page.getByRole('alert').waitFor();assert.equal(await page.locator('.disk-setup-steps li').count(),3);
 await page.evaluate(()=>{window.setupFail=false;});
 await page.getByRole('button',{name:'Freigaben erneut prüfen'}).click();
 await page.getByRole('heading',{name:'Bereit für deinen ersten Scan'}).waitFor();
 assert.equal(await page.evaluate(()=>window.setupChecks),2);
 await page.getByRole('button',{name:'Fotos',exact:true}).click();assert.equal(await page.evaluate(()=>window.setupSelected),'Fotos');
 await page.screenshot({path:'/tmp/diskmap-setup-ready.png'});
 await page.setViewportSize({width:800,height:600});await page.evaluate(()=>window.setupSetMode('permissions'));
 await page.getByRole('heading',{name:'Gib deinem Speicher eine Karte'}).waitFor();
 await page.getByRole('button',{name:'Freigaben erneut prüfen'}).scrollIntoViewIfNeeded();
 assert(await page.getByRole('button',{name:'Freigaben erneut prüfen'}).isVisible());assert.deepEqual(errors,[]);
 console.log('PASS setup, inventory retry/failure, share selection, small window; no scans');
}finally{await browser.close();await server.close();}
