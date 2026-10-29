import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// Verify the production empty surface and category toggles in both map modes.
const server=await createServer({configFile:false,root:process.cwd(),server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
try{
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/design.html`);
 await page.evaluate(async()=>{(await import('/tests/fixtures/hidden-map-workspace.jsx')).mount();});
 for(const mode of ['Disk Map','Radial']){
  await page.getByRole('button',{name:mode,exact:true}).click();
  await page.getByRole('heading',{name:'Keine sichtbaren Dateien',exact:true}).waitFor();
  assert.equal(await page.locator('.disk-chart-canvas').count(),0);
  await page.getByRole('button',{name:'Systemdateien',exact:true}).click();
  await page.locator('.disk-chart-canvas').waitFor();
  assert.equal(await page.getByRole('heading',{name:'Keine sichtbaren Dateien',exact:true}).count(),0);
  await page.getByRole('button',{name:'Systemdateien',exact:true}).click();
 }
 assert.deepEqual(errors,[]);console.log('PASS hidden-only map explains category filtering; both map modes restore after visibility toggle');
}finally{await browser.close();await server.close();}
