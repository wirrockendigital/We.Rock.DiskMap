import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// Exercise the real list disclosure, recursive matching and map path affordance.
const server=await createServer({configFile:false,root:process.cwd(),server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
try{
 const page=await browser.newPage({viewport:{width:1584,height:992}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/design.html`);
 await page.evaluate(async()=>{(await import('/tests/fixtures/sidebar-workspace.jsx')).mount();});
 await page.locator('.disk-tree-name').filter({hasText:'volume-2-folder-2'}).click();
 await page.locator('.disk-side .disk-row').filter({hasText:'Aufnahmen'}).waitFor();
 const toggle=page.getByRole('button',{name:'Filtern und Sortieren',exact:true});await toggle.click();
 await page.getByLabel('Unterordner einbeziehen').check();
 await page.getByLabel('Name, Pfad oder Dateiendung suchen').fill('*.mov');
 await page.locator('.disk-side .disk-row').filter({hasText:'Interview.mov'}).waitFor();
 assert.equal(await page.locator('.disk-side .disk-row').count(),1);
 await page.getByLabel('Reihenfolge',{exact:true}).selectOption('asc');
 await page.getByLabel('Ordner zuerst',{exact:true}).uncheck();
 await toggle.click();assert.equal(await toggle.getAttribute('aria-expanded'),'false');
 await toggle.click();assert.equal(await page.getByLabel('Reihenfolge',{exact:true}).inputValue(),'asc');assert.equal(await page.getByLabel('Ordner zuerst',{exact:true}).isChecked(),false);
 await page.getByLabel('Name, Pfad oder Dateiendung suchen').fill('');
 await page.getByLabel('Anzeigen',{exact:true}).selectOption('directory');
 await page.locator('.disk-side .disk-row').filter({hasText:'Aufnahmen'}).waitFor();assert.equal(await page.locator('.disk-side .disk-row').count(),1);
 await page.setViewportSize({width:1024,height:700});await page.waitForTimeout(250);
 assert.deepEqual(errors,[]);await page.screenshot({path:'/tmp/diskmap-list-local.png'});
 // Check three appended batches and a subsequent filter reset.
 await page.evaluate(async()=>{(await import('/tests/fixtures/list-scroll.jsx')).mount();});
 const scrollRows=page.locator('#scroll-test .disk-row'),scrollList=page.locator('#scroll-test .disk-folder-list');
 await page.waitForFunction(()=>Number(document.querySelector('#scroll-test .disk-folder-list')?.dataset.loadedCount)>=50);
 await scrollList.evaluate(e=>{e.scrollTop=e.scrollHeight;});await page.waitForFunction(()=>Number(document.querySelector('#scroll-test .disk-folder-list')?.dataset.loadedCount)>=100);
 await scrollList.evaluate(e=>{e.scrollTop=e.scrollHeight;});await page.waitForFunction(()=>Number(document.querySelector('#scroll-test .disk-folder-list')?.dataset.loadedCount)===120);
 assert(await scrollRows.count()<40);assert.equal(await page.locator('.disk-list-paging').count(),0);
 await page.locator('#scroll-test .disk-search input').fill('file-119');
 await page.waitForTimeout(350);assert.equal(await scrollRows.count(),1);
 // Switch scope while a continuation is in flight; no prior-scope row may survive.
 await page.evaluate(async()=>{(await import('/tests/fixtures/list-scope-race.jsx')).mount();});
 const scopeList=page.locator('#scope-race .disk-folder-list');await page.locator('#scope-race .disk-row').first().waitFor();
 await scopeList.evaluate(e=>{e.scrollTop=e.scrollHeight;});await page.locator('#scope-switch').click();
 await page.waitForTimeout(300);assert((await page.locator('#scope-race .disk-row').allTextContents()).every(t=>t.startsWith('B-')));
 await scopeList.evaluate(e=>{e.scrollTop=e.scrollHeight;});await page.waitForTimeout(300);assert.equal(await scopeList.getAttribute('data-loaded-count'),'100');assert((await page.locator('#scope-race .disk-row').allTextContents()).every(t=>t.startsWith('B-')));
 // An unscanned scope never queries; real failures still expose retry after readiness.
 await page.evaluate(async()=>{(await import('/tests/fixtures/list-empty.jsx')).mount();});
 const empty=page.locator('#empty-test');
 await empty.getByText('Noch kein Scan vorhanden.',{exact:false}).waitFor();
 assert.equal(await page.evaluate(()=>window.emptyListCalls),0);
 await empty.locator('#ready-toggle').click();await empty.locator('[data-error="LIST_UNAVAILABLE"]').waitFor();
 assert.equal(await page.evaluate(()=>window.emptyListCalls),1);
 await empty.locator('[data-error]').click();await page.waitForFunction(()=>window.emptyListCalls===2);
 await empty.locator('#ready-toggle').click();await empty.getByText('Noch kein Scan vorhanden.',{exact:false}).waitFor();
 assert.equal(await empty.locator('[data-error]').count(),0);
 console.log('PASS inline disclosure, wildcard, recursive paths, type filter, retained selectors, narrow layout');
}finally{await browser.close();await server.close();}
