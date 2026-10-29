import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
// Exercise actual chart pixels, mode changes and pointer work without a NAS scan.
const server=await createServer({configFile:false,root:process.cwd(),server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
try{
 const page=await browser.newPage({viewport:{width:1280,height:800}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/design.html`);
 await page.locator('canvas[role="img"]').waitFor();await page.waitForTimeout(300);
 const pixels=()=>page.locator('canvas[role="img"]').evaluate(c=>{const p=c.getContext('2d').getImageData(0,0,c.width,c.height).data;const colors=new Set();for(let i=0;i<p.length;i+=112)if(p[i+3]>200)colors.add(`${p[i]>>4},${p[i+1]>>4},${p[i+2]>>4}`);return colors.size;});
 assert(await pixels()>50,'Treemap needs distinguishable colored surfaces');
 const before=await page.locator('canvas[role="img"]').evaluate(c=>c.toDataURL());
 const b=await page.locator('canvas[role="img"]').boundingBox();await page.mouse.move(b.x+b.width*.3,b.y+b.height*.3);await page.waitForTimeout(80);
 assert.equal(await page.locator('canvas[role="img"]').evaluate(c=>c.toDataURL()),before,'Hover must not repaint base map');
 await page.mouse.move(2,2);await page.screenshot({path:'/tmp/diskmap-colors-local-treemap.png'});
 await page.getByRole('button',{name:'Radial',exact:true}).click();await page.waitForTimeout(200);assert(await pixels()>35,'Sunburst needs multiple hue families');
 await page.screenshot({path:'/tmp/diskmap-colors-local-radial.png'});
 // Resize must preserve a usable drawing surface and accurate aspect ratio.
 await page.setViewportSize({width:1024,height:700});await page.waitForTimeout(200);
 const shape=await page.locator('canvas[role="img"]').evaluate(c=>({w:c.width,h:c.height,rectW:c.getBoundingClientRect().width,rectH:c.getBoundingClientRect().height}));assert(Math.abs(shape.w/shape.h-shape.rectW/shape.rectH)<.01);
 assert.deepEqual(errors,[]);console.log('PASS: multicolor treemap/radial pixels, cached base on hover, responsive geometry, no browser errors');
}finally{await browser.close();await server.close();}
