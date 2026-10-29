import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {mkdtemp,readFile,writeFile,open,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {rolldown} from '../../web/node_modules/rolldown/dist/index.mjs';
const {chromium}=await import(process.env.DISKMAP_PLAYWRIGHT_MODULE??'playwright');

// This opt-in benchmark reads an immutable fixture; it never connects to DSM or modifies scan sources.
const fixture=resolve(process.env.DISKMAP_MAP_FIXTURE??''),runs=Number(process.env.DISKMAP_MAP_RUNS??20),delay=Number(process.env.DISKMAP_MAP_DELAY_MS??70);
assert(process.env.DISKMAP_MAP_FIXTURE&&Number.isInteger(runs)&&runs>0&&runs<=100&&delay>=0&&delay<=1000);
// Viewport and mode are explicit so 4K and radial runs use the same reproducible harness.
const width=Number(process.env.DISKMAP_MAP_WIDTH??1280),height=Number(process.env.DISKMAP_MAP_HEIGHT??800),mode=process.env.DISKMAP_MAP_MODE??'treemap';
assert(Number.isInteger(width)&&width>0&&width<=7680&&Number.isInteger(height)&&height>0&&height<=4320&&['treemap','radial'].includes(mode));
const output=process.env.DISKMAP_MAP_REPORT;assert(output,'DISKMAP_MAP_REPORT is required');
const manifest=JSON.parse(await readFile(join(fixture,'manifest.json'),'utf8'));manifest.indexId='a'.repeat(64);
const dir=await mkdtemp(join(tmpdir(),'diskmap-qualification-'));
const bundle=await rolldown({cwd:new URL('../../web/',import.meta.url).pathname,input:['src/design/packed-map.worker.ts','src/design/map-index-api.ts']});
await bundle.write({dir,format:'esm',entryFileNames:'[name].mjs',minify:true});await bundle.close();
const html=`<!doctype html><title>Immutable map qualification</title><canvas width="1280" height="800"></canvas><script type="module">
import {readMapManifest,readMapChunks,MapChunkCache} from './map-index-api.mjs';
window.SYNO={SDS:{Session:{SynoToken:'synthetic'}}};
const seed=await (await fetch('./manifest.json')).json(),context={view:{profileId:'p',snapshotId:seed.snapshotId,rootId:seed.rootId},pin:{profileId:'p',snapshotId:seed.snapshotId,viewId:'v'}},signal=new AbortController().signal;
const manifest=await readMapManifest(context,signal);let worker,cache,sequence=0,requests=0;const pending=new Map(),original=fetch;
window.fetch=(...args)=>{if(String(args[0]).includes('map-chunks'))requests++;return original(...args);};
window.fresh=()=>{worker?.terminate();cache=new MapChunkCache();worker=new Worker('./packed-map.worker.mjs',{type:'module'});worker.onmessage=async({data})=>{if(data.need){try{const blocks=await readMapChunks(context,manifest,data.blocks,signal,cache);worker.postMessage({loaded:data.need,blocks},blocks.map(b=>b.data));}catch(e){worker.postMessage({loaded:data.need,error:e.message});}return;}const call=pending.get(data.id);if(!call){data.value?.bitmap?.close();return;}if(data.progress){call.progress?.(data.value);return;}pending.delete(data.id);data.error?call.reject(Error(data.error)):call.resolve(data.value);};};
const call=(operation,data={},progress)=>new Promise((resolve,reject)=>{const id=++sequence;pending.set(id,{resolve,reject,progress});worker.postMessage({id,operation,...data});});
window.run=async()=>{await call('open',{manifest});const started=performance.now(),before=requests;let first;const frame=await call('layout',{indexId:manifest.indexId,root:manifest.rootId,options:{mode:window.mode??'treemap',box:window.box??{width:1280,height:800},recycle:false,system:false,dpr:1}},g=>{first??=performance.now()-started;g.bitmap.close();});const canvas=document.querySelector('canvas');canvas.width=frame.bitmap.width;canvas.height=frame.bitmap.height;canvas.getContext('2d').drawImage(frame.bitmap,0,0);frame.bitmap.close();return {first,elapsed:performance.now()-started,requests:requests-before,processed:frame.processed,cacheBytes:frame.cacheBytes,complete:frame.complete};};
fresh();</script>`;
// Frame responses obey the same prefix limit as the daemon, including incompressible blocks.
const offsets=await open(join(fixture,'offsets.bin'),'r'),packed=await open(join(fixture,'blocks.gz'),'r'),sums=await open(join(fixture,'blocks.sha256'),'r');
const server=createServer(async(req,res)=>{try{
 const path=new URL(req.url,'http://localhost').pathname;
 if(req.method==='POST'){
  let text='';for await(const part of req){text+=part;assert(text.length<=8192);}const request=JSON.parse(text);if(delay)await new Promise(r=>setTimeout(r,delay));
  if(req.url.endsWith('action=map-manifest')){res.setHeader('Content-Type','application/json');res.end(JSON.stringify({contractVersion:1,data:manifest}));return;}
  assert(req.url.endsWith('action=map-chunks')&&request.indexId===manifest.indexId&&request.blocks.length>0&&request.blocks.length<=128);
  const header=Buffer.alloc(44);header.write('WRDCH003');Buffer.from(manifest.indexId,'hex').copy(header,8);const parts=[header];let length=44,count=0;
  for(const block of request.blocks){assert(Number.isSafeInteger(block)&&block>=0&&block<=Math.floor(manifest.count/2048));const addresses=Buffer.alloc(16);await offsets.read(addresses,0,16,block*8);const start=Number(addresses.readBigUInt64LE()),end=Number(addresses.readBigUInt64LE(8));assert(end>start&&end-start<=263168);if(length+44+end-start>4194304)break;
   const record=Buffer.alloc(44),data=Buffer.alloc(end-start);record.writeBigUInt64LE(BigInt(block));record.writeUInt32LE(data.length,8);await sums.read(record,12,32,(block+1)*32);await packed.read(data,0,data.length,start);parts.push(record,data);length+=record.length+data.length;count++;
  }
  assert(count>0);header.writeUInt32LE(count,40);res.setHeader('Content-Type','application/octet-stream');res.end(Buffer.concat(parts));return;
 }
 if(path==='/'){res.setHeader('Content-Type','text/html');res.end(html);return;}
 if(path==='/manifest.json'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify(manifest));return;}
 assert(/^\/[a-zA-Z0-9_.-]+\.(?:mjs|js)$/.test(path));res.setHeader('Content-Type','text/javascript');res.end(await readFile(join(dir,path.slice(1))));
 }catch{res.statusCode=400;res.end('INVALID_FIXTURE_REQUEST');}});
await new Promise(r=>server.listen(0,'127.0.0.1',r));
const browser=await chromium.launch({headless:true,executablePath:process.env.DISKMAP_BROWSER_EXECUTABLE||undefined}),context=await browser.newContext(),page=await context.newPage(),errors=[];
page.on('pageerror',e=>errors.push(e.message));
// CDP reports JS, embedder and backing storage; OS/GPU allocations are outside this measurement.
const cdp=await browser.newBrowserCDPSession(),mainCDP=await context.newCDPSession(page),pendingMemory=new Map();let memorySequence=0;
const memory={workerPeakBytes:0,mainPeakBytes:0,combinedPeakBytes:0,samples:0};
cdp.on('Target.receivedMessageFromTarget',({message})=>{const data=JSON.parse(message),call=pendingMemory.get(data.id);if(call){pendingMemory.delete(data.id);data.error?call.reject(Error(data.error.message)):call.resolve(data.result);}});
const memoryCommand=(sessionId)=>new Promise((resolve,reject)=>{const id=++memorySequence;pendingMemory.set(id,{resolve,reject});cdp.send('Target.sendMessageToTarget',{sessionId,message:JSON.stringify({id,method:'Runtime.getHeapUsage'})}).catch(reject);});

try{
 await page.goto('http://127.0.0.1:'+server.address().port+'/');await page.waitForFunction(()=>typeof window.run==='function');await page.evaluate(({width,height,mode})=>{window.box={width,height};window.mode=mode;},{width,height,mode});
 const measurements=[];for(let n=0;n<runs;n++){
  await page.evaluate(()=>fresh());let target;
  for(let retry=0;retry<20&&!target;retry++){await page.waitForTimeout(10);target=(await cdp.send('Target.getTargets')).targetInfos.find(t=>t.type==='worker'&&t.url.endsWith('/packed-map.worker.mjs'));}
  assert(target,'Worker target missing from resource qualification');
  const {sessionId}=await cdp.send('Target.attachToTarget',{targetId:target.targetId,flatten:false});let sampling=false;
  const sample=async()=>{if(sampling)return;sampling=true;try{const [main,worker]=await Promise.all([mainCDP.send('Runtime.getHeapUsage'),memoryCommand(sessionId)]);const total=v=>v.usedSize+v.embedderHeapUsedSize+v.backingStorageSize,w=total(worker),m=total(main);memory.workerPeakBytes=Math.max(memory.workerPeakBytes,w);memory.mainPeakBytes=Math.max(memory.mainPeakBytes,m);memory.combinedPeakBytes=Math.max(memory.combinedPeakBytes,w+m);memory.samples++;}finally{sampling=false;}};
  const timer=setInterval(()=>void sample().catch(()=>{}),100);let value;
  try{value=await page.evaluate(()=>run());await sample();}finally{clearInterval(timer);await cdp.send('Target.detachFromTarget',{sessionId}).catch(()=>{});}
  assert(value.complete);measurements.push(value);console.log(JSON.stringify({run:n+1,...value,memory}));
 }
 const sorted=measurements.map(r=>r.elapsed).sort((a,b)=>a-b),report={browser:browser.version(),fixtureRecords:manifest.count,viewport:{width,height},mode,additionalRequestDelayMs:delay,runs,measurements,p95:sorted[Math.ceil(runs*.95)-1],memory,errors};
 assert.deepEqual(errors,[]);await writeFile(output,JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify({complete:true,p95:report.p95}));
}finally{await browser.close();await new Promise(r=>server.close(r));await offsets.close();await packed.close();await sums.close();await rm(dir,{recursive:true,force:true});}
