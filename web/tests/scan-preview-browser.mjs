import assert from 'node:assert/strict';
import {createServer} from '../node_modules/vite/dist/node/index.js';
// A real local HTTP endpoint and unmodified Chromium fetch expose illegal receiver bindings.
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');
let server,browser;const requests=[];
try {
 server=await createServer({configFile:false,root:new URL('../',import.meta.url).pathname,logLevel:'error',server:{host:'127.0.0.1',port:0},plugins:[{name:'scan-preview-default-transport',configureServer(s){s.middlewares.use((req,res,next)=>{
  const url=new URL(req.url,'http://localhost');
  if(url.pathname==='/preview-fixture'){res.setHeader('Content-Type','text/html');res.end('<!doctype html><html><body>Local preview transport fixture</body></html>');return;}
  if(url.pathname!='/api.cgi')return next();
  requests.push({action:url.searchParams.get('action'),profile:url.searchParams.get('profileId'),job:url.searchParams.get('jobId'),method:req.method,authenticated:req.headers['x-syno-token']==='local-fixture'});
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({contractVersion:1,data:{profileId:'p',jobId:'j',preview:{sequence:1,capturedAt:'2026-10-14T12:00:00Z',entries:'1',knownLogicalBytes:'42',segments:[{id:'preview-1',name:'Local file',path:'target/file',kind:'file',bytes:'42'}],truncated:false}}}));
 });}}]});await server.listen();
 browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined});
 const page=await browser.newPage();page.setDefaultTimeout(10000);
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/preview-fixture`);
 const result=await page.evaluate(async()=>{
  const native=Function.prototype.toString.call(window.fetch).includes('[native code]');
  // Only the session adapter is populated; the client gets no injected transport or token callback.
  window.SYNO={SDS:{Session:{SynoToken:'local-fixture'}}};
  const {ScanPreviewClient}=await import('/src/design/scan-preview.ts');const client=new ScanPreviewClient();
  try {const value=await client.read('p','j',AbortSignal.timeout(5000));return {native,bytes:value.preview.knownLogicalBytes,job:value.jobId};}
  catch(error){return {native,errorName:error.name,errorMessage:error.message};}
  finally {client.dispose();delete window.SYNO;}
 });
 assert.equal(result.native,true);assert.equal(result.errorName,undefined,JSON.stringify(result));
 assert.deepEqual(result,{native:true,bytes:'42',job:'j'});
 assert.deepEqual(requests,[{action:'scan-preview',profile:'p',job:'j',method:'GET',authenticated:true}]);
 console.log(JSON.stringify({qualified:true,nativeFetch:true,injectedTransport:false,requests:requests.length,knownLogicalBytes:result.bytes}));
} finally {await browser?.close();await server?.close();}
