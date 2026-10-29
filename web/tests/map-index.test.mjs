import {test,after} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
import {gzipSync} from 'node:zlib';
import {createHash} from 'node:crypto';
import {rolldown} from 'rolldown';

// Execute the production binary reader and layout with a deterministic canvas adapter.
const dir=await mkdtemp(join(tmpdir(),'diskmap-index-test-'));
after(()=>rm(dir,{recursive:true,force:true}));
const bundle=await rolldown({cwd:new URL('../',import.meta.url).pathname,input:['src/design/map-index-renderer.ts','src/design/map-index-format.ts','src/design/map-index-api.ts','src/design/map-task-queue.ts','src/design/packed-map-client.ts','src/design/worker-runtime.ts'],platform:'node'});
await bundle.write({dir,format:'esm',entryFileNames:'[name].mjs'});await bundle.close();
const {MapIndexRenderer}=await import(pathToFileURL(join(dir,'map-index-renderer.mjs')));
const {decodeRecord,visibleBytes}=await import(pathToFileURL(join(dir,'map-index-format.mjs')));
globalThis.ImageData=class{constructor(data,width,height){this.data=data;this.width=width;this.height=height;}};
const context={putImageData(){},scale(){},fillRect(){},beginPath(){},arc(){},closePath(){},fill(){},createLinearGradient(){return {addColorStop(){}};}};
globalThis.OffscreenCanvas=class{constructor(width,height){this.width=width;this.height=height;}getContext(){return context;}};
globalThis.createImageBitmap=async()=>({close(){}});
// Real source IDs remain independent of compact addresses, including values beyond Number precision.
function fixture(){
 const raw=new ArrayBuffer(6*128),write=(slot,{source=0n,parent=0,left=0,right=0,kind=0,flags=0,depth=0,maxDepth=1,weights=[0n,0n,0n,0n],presence=0})=>{
  const v=new DataView(raw,slot*128,128);for(const [at,n]of [[0,source],[8,parent],[16,left],[24,right]])v.setBigUint64(at,BigInt(n),true);
  v.setUint8(32,kind);v.setUint8(33,flags);v.setUint8(38,presence);v.setUint16(34,depth,true);v.setUint16(36,maxDepth,true);
  weights.forEach((n,i)=>{v.setBigUint64(56+i*16,n&((1n<<64n)-1n),true);v.setBigUint64(64+i*16,n>>64n,true);});
 };
 write(1,{source:1n,kind:2,left:5,weights:[75n,0n,25n,0n],presence:2});
 write(2,{source:9007199254740993n,parent:1,kind:1,depth:1,weights:[75n,0n,0n,0n]});
 write(3,{source:3n,parent:1,kind:1,depth:1,flags:2,weights:[0n,0n,25n,0n],presence:2});
 write(5,{parent:1,left:2,right:3,weights:[75n,0n,25n,0n],presence:2});
 const manifest={version:1,status:'ready',snapshotId:'snapshot',indexId:'index',rootId:'1',root:1,count:5,blockBytes:262144,record:Buffer.from(raw.slice(128,256)).toString('base64'),identities:[]};
 return {raw,manifest};
}
const options={mode:'treemap',box:{width:100,height:100},recycle:true,system:false,dpr:1};
test('binary weights preserve 128-bit values and disjoint category masks',()=>{
 const raw=new ArrayBuffer(128),v=new DataView(raw);v.setUint8(32,1);v.setBigUint64(56,1n,true);v.setBigUint64(64,1n,true);v.setBigUint64(72,2n,true);v.setBigUint64(88,3n,true);v.setBigUint64(104,4n,true);
 const r=decodeRecord(raw);assert.equal(visibleBytes(r,false,false),(1n<<64n)+1n);assert.equal(visibleBytes(r,true,true),(1n<<64n)+10n);
 v.setUint8(39,1);assert.throws(()=>decodeRecord(raw),/INVALID_MAP_INDEX/);
});
test('refinement preserves exact file identity, category filtering and warm raster reuse',async()=>{
 const {raw,manifest}=fixture();let loads=0;const renderer=new MapIndexRenderer(async()=>{loads++;return [{block:0,data:raw.slice(0)}];});renderer.open(manifest);
 const g=await renderer.render('index','1',options,frame=>frame.bitmap.close());assert(g.complete);assert.equal((await renderer.probe(g.frameId,50,10)).node.id,'9007199254740993');assert.equal((await renderer.probe(g.frameId,50,90)).node.id,'3');
 const warm=await renderer.render('index','1',options,()=>assert.fail('warm map refined again'));assert.equal(warm.processed,0);assert.equal(loads,1);
 const filtered=await renderer.render('index','1',{...options,recycle:false},frame=>frame.bitmap.close());assert.equal((await renderer.probe(filtered.frameId,50,10)).node.id,'9007199254740993');assert.equal((await renderer.probe(filtered.frameId,50,90)).node.bytesExact,'75');
});
test('cancelled navigation never publishes a late bitmap or accepts its stale hit map',async()=>{
 const {raw,manifest}=fixture();let release;const renderer=new MapIndexRenderer(()=>new Promise(resolve=>{release=()=>resolve([{block:0,data:raw}]);}));renderer.open(manifest);
 const pending=renderer.render('index','1',options,frame=>frame.bitmap.close());await new Promise(resolve=>setImmediate(resolve));renderer.cancel();release();await assert.rejects(pending,{name:'AbortError'});assert.equal(await renderer.probe(2,10,10),null);
});
test('radial hit ownership agrees with angular sectors and rejects the center hole',async()=>{
 const {raw,manifest}=fixture(),renderer=new MapIndexRenderer(async()=>[{block:0,data:raw}]);renderer.open(manifest);
 const g=await renderer.render('index','1',{...options,mode:'radial',box:{width:400,height:400}},frame=>frame.bitmap.close());assert.equal(await renderer.probe(g.frameId,200,200),null);
 assert.equal((await renderer.probe(g.frameId,130,130)).node.id,'3');assert.equal((await renderer.probe(g.frameId,130,270)).node.id,'9007199254740993');
});

// Independently encode byte planes for cross-contract decoder verification.
function wireGzip(raw){const rows=raw.length/128,planes=new Uint8Array(raw.length);for(let c=0;c<128;c++)for(let r=0;r<rows;r++)planes[c*rows+r]=raw[r*128+c];return gzipSync(planes);}
// Real gzip frames are verified after decompression, including corruption and identity mismatch.
test('chunk transport validates compression, index binding and final session identity',async()=>{
 const {readMapChunks,MapChunkCache}=await import(pathToFileURL(join(dir,'map-index-api.mjs')));
 const {raw,manifest}=fixture();manifest.indexId='a'.repeat(64);manifest.snapshotId='s';
 const compressed=wireGzip(new Uint8Array(raw)),frame=Buffer.alloc(88+compressed.length);
 frame.write('WRDCH003');Buffer.from(manifest.indexId,'hex').copy(frame,8);frame.writeUInt32LE(1,40);frame.writeBigUInt64LE(0n,44);frame.writeUInt32LE(compressed.length,52);createHash('sha256').update(new Uint8Array(raw)).digest().copy(frame,56);compressed.copy(frame,88);
 const token={SynoToken:'fixture-token'};globalThis.window={parent:{SYNO:{SDS:{Session:token}}}};
 let requests=0;globalThis.fetch=async()=>{requests++;return new Response(frame,{headers:{'Content-Type':'application/octet-stream'}});};
 const context={view:{profileId:'p',snapshotId:'s',rootId:'1'},pin:{profileId:'p',snapshotId:'s',viewId:'v'}};
 const cache=new MapChunkCache();
 const blocks=await readMapChunks(context,manifest,[0],new AbortController().signal,cache);assert.deepEqual(new Uint8Array(blocks[0].data),new Uint8Array(raw));
 await readMapChunks(context,manifest,[0],new AbortController().signal,cache);assert.equal(requests,1,'warm blocks must reuse bounded compressed data');
 await assert.rejects(readMapChunks({...context,view:{...context.view,snapshotId:'foreign'}},manifest,[0],new AbortController().signal,cache),/SNAPSHOT_MISMATCH/);
 token.SynoToken='renewed';await readMapChunks(context,manifest,[0],new AbortController().signal,cache);assert.equal(requests,2,'session changes discard compressed data');
 frame[56]^=1;await assert.rejects(readMapChunks(context,manifest,[0],new AbortController().signal),/INVALID_MAP_FRAME/);frame[56]^=1;
 globalThis.fetch=async()=>{token.SynoToken='changed';return new Response(frame,{headers:{'Content-Type':'application/octet-stream'}});};
 await assert.rejects(readMapChunks(context,manifest,[0],new AbortController().signal),/SESSION_CHANGED/);
});

// Byte-limited replies must advance a requested prefix without inventing missing blocks.
test('chunk batches accept a complete prefix and reject unbounded or empty replies',async()=>{
 const {readMapChunks}=await import(pathToFileURL(join(dir,'map-index-api.mjs')));
 const {raw,manifest}=fixture();manifest.indexId='b'.repeat(64);manifest.snapshotId='s';manifest.count=2048;
 const full=Buffer.alloc(262144);Buffer.from(raw).copy(full);
 const compressed=wireGzip(full),frame=Buffer.alloc(88+compressed.length);
 frame.write('WRDCH003');Buffer.from(manifest.indexId,'hex').copy(frame,8);frame.writeUInt32LE(1,40);frame.writeBigUInt64LE(0n,44);frame.writeUInt32LE(compressed.length,52);createHash('sha256').update(full).digest().copy(frame,56);compressed.copy(frame,88);
 globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'batch-token'}}}}};
 globalThis.fetch=async()=>new Response(frame,{headers:{'Content-Type':'application/octet-stream'}});
 const context={view:{profileId:'p',snapshotId:'s',rootId:'1'},pin:{profileId:'p',snapshotId:'s',viewId:'v'}},signal=new AbortController().signal;
 const result=await readMapChunks(context,manifest,[0,1],signal);assert.deepEqual(result.map(x=>x.block),[0]);assert.equal(result[0].data.byteLength,262144);
 frame.writeUInt32LE(0,40);await assert.rejects(readMapChunks(context,manifest,[0,1],signal),/INVALID_MAP_FRAME/);
 frame.writeUInt32LE(3,40);await assert.rejects(readMapChunks(context,manifest,[0,1],signal),/INVALID_MAP_FRAME/);
 globalThis.fetch=async()=>new Response(new Uint8Array(4*1024*1024+1),{headers:{'Content-Type':'application/octet-stream'}});
 await assert.rejects(readMapChunks(context,manifest,[0,1],signal),/MAP_RESPONSE_TOO_LARGE/);
 await assert.rejects(readMapChunks(context,{...manifest,count:2048*129},Array.from({length:129},(_,i)=>i),signal),/INVALID_MAP_REQUEST/);
});

// Compact frontiers preserve geometry and large source identities across chunk recycling.
test('compact task queue preserves level order, retries, identity and bounded frontier storage',async()=>{
 const {MapTaskQueue}=await import(pathToFileURL(join(dir,'map-task-queue.mjs')));
 const q=new MapTaskQueue(),make=slot=>({slot,depth:500,x0:.125,y0:-7.75,x1:1280.5,y1:800.25,owner:'directory',parent:'share',record:{source:9007199254740993n,parent:0,left:2,right:3,kind:0,flags:3,depth:500,maxDepth:501,weight:128.25}});
 for(let n=1;n<=5000;n++)q.push(make(n));q.prepend([make(6001),make(6002)]);assert.equal(q.pop().slot,6001);assert.equal(q.pop().slot,6002);
 for(let n=1;n<=5000;n++){const t=q.pop();assert.equal(t.slot,n);assert.equal(t.record.source,9007199254740993n);assert.equal(t.record.weight,128.25);assert.equal(t.owner,'directory');assert.equal(t.x0,.125);assert.equal(t.depth,500);}
 assert.equal(q.length,0);assert.equal(q.bytes,0);q.push(make(7));assert.equal(q.pop().slot,7);
 // The renderer expands at most two children for each consumed entry, so overflow stays depth-first.
 const wide=new MapTaskQueue(); let count=0;while(wide.bytes+64*72+2048<64*1024*1024){wide.push(make(++count));}while(wide.bytes+64*72+1024+30<=64*1024*1024){wide.push(make(++count));}wide.push(make(++count));const bound=wide.bytes;assert.equal(wide.pop().slot,count);assert(bound<=64*1024*1024);
 const depthFirst=new MapTaskQueue(false);depthFirst.prepend([make(1),make(2)]);assert.equal(depthFirst.pop().slot,1);depthFirst.push(make(3));assert.equal(depthFirst.pop().slot,3);assert.equal(depthFirst.pop().slot,2);
 const ordered=new MapTaskQueue();for(const n of [10000,1,5000,2])ordered.push(make(n));assert.deepEqual([ordered.pop().slot,ordered.pop().slot,ordered.pop().slot,ordered.pop().slot],[1,2,5000,10000]);
});

// A probe completing after navigation must not publish cached-label metadata from the retired frame.
test('late label probes are discarded after navigation and raw eviction releases backing storage',async()=>{
 const {raw,manifest}=fixture();let deferred=false,release;
 const renderer=new MapIndexRenderer(async()=>{if(deferred)await new Promise(resolve=>release=resolve);return [{block:0,data:raw.slice(0)}];});renderer.open(manifest);
 const g=await renderer.render('index','1',{...options,box:{width:400,height:400}},frame=>frame.bitmap.close());
 const held=renderer.blocks.get('index:0');renderer.blockLimit=0;renderer.trim();assert.equal(held.byteLength,0);renderer.blockLimit=96*1024*1024;deferred=true;
 const probe=renderer.probe(g.frameId,20,20);await new Promise(resolve=>setImmediate(resolve));renderer.cancel();release();assert.equal(await probe,null);
});

// Aborting while the worker acknowledges an open is navigation cancellation, not a map-readiness error.
test('late worker acknowledgements and late manifests cannot complete superseded opens',async()=>{
 const {PackedMapClient}=await import(pathToFileURL(join(dir,'packed-map-client.mjs'))),{configureMapWorkers}=await import(pathToFileURL(join(dir,'worker-runtime.mjs')));
 const {manifest}=fixture();manifest.indexId='a'.repeat(64);const context={view:{profileId:'p',snapshotId:'snapshot',rootId:'1'},pin:{profileId:'p',snapshotId:'snapshot',viewId:'v'}};
 const savedFetch=globalThis.fetch,savedWindow=globalThis.window,messages=[];
 const worker={postMessage(message){messages.push(message);},terminate(){}};configureMapWorkers({packed:()=>worker,layout:()=>{throw Error('unexpected layout worker');}});
 globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'test'}}}}};globalThis.fetch=async()=>new Response(JSON.stringify({contractVersion:1,data:manifest}),{headers:{'Content-Type':'application/json'}});
 const client=new PackedMapClient();
 try{
  const controller=new AbortController(),pending=client.open(context,controller.signal);while(!messages.length)await new Promise(r=>setImmediate(r));controller.abort();worker.onmessage({data:{id:messages.shift().id,value:null}});await assert.rejects(pending,{name:'AbortError'});await assert.rejects(client.layout('other',options,undefined,controller.signal),{name:'AbortError'});
  const responses=[];globalThis.fetch=()=>new Promise(resolve=>responses.push(resolve));const older=client.open(context,new AbortController().signal),newer=client.open(context,new AbortController().signal);
  responses[1](new Response(JSON.stringify({contractVersion:1,data:manifest})));while(!messages.length)await new Promise(r=>setImmediate(r));worker.onmessage({data:{id:messages.shift().id,value:null}});await newer;
  responses[0](new Response(JSON.stringify({contractVersion:1,data:manifest})));await assert.rejects(older,{name:'AbortError'});assert.equal(messages.length,0);
 }finally{client.dispose();globalThis.fetch=savedFetch;globalThis.window=savedWindow;}
});
