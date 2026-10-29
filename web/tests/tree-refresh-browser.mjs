import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
import react from '../node_modules/@vitejs/plugin-react/dist/index.js';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// A scan completion remount must preserve sibling shares after every supported saved source.
// This UI fixture needs no release metadata or native Go toolchain.
const server=await createServer({configFile:false,plugins:[react()],root:new URL('../',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
try{
 for(const source of ['nas','volumes','shares','folder']){
  const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/tests/fixtures/tree-refresh.html?source=${source}`);
  const shares=()=>page.locator('.disk-tree-name').allTextContents();
  await page.waitForFunction(()=>document.querySelectorAll('.disk-tree-name').length===6);
  const initial=await shares();
  await page.getByRole('button',{name:'Restore preferences'}).click();
  await page.waitForFunction(source=>document.querySelector('[data-selected]').dataset.selected===source,source);
  for(let i=0;i<2;i++){
   await page.getByRole('button',{name:'Scan completed'}).click();
   await page.waitForFunction(()=>['Share A','Share B','Share C'].every(name=>[...document.querySelectorAll('.disk-tree-name')].some(n=>n.textContent===name)),{},{timeout:3000});
   assert.deepEqual((await shares()).filter(n=>n.startsWith('Share')),initial.filter(n=>n.startsWith('Share')));
  }
  // Child folder loading must still work after inventory rows have been rebuilt twice.
  await page.getByRole('button',{name:'Expand: Share B',exact:true}).click();
  await page.getByRole('button',{name:'Child',exact:true}).last().waitFor();
  assert.deepEqual(errors,[]);console.log(source+': siblings retained after two scan completions; folders load');
  await page.close();
 }
}finally{await browser.close();await server.close();}
