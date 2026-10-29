import {readFile,writeFile} from 'node:fs/promises';
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {createServer} from '../../web/node_modules/vite/dist/node/index.js';
// Resolve Playwright from an explicit runner path or the installed package.
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE || 'playwright');

// Diagnostic runs own fresh browsers and never connect to an existing DSM tab.
if(process.env.LAYOUT_DIAGNOSTIC==='1') { await diagnose(); } else {
// Serve local source only; this harness does not publish or install an application artifact.
const server=await createServer({root:new URL('../../web',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});
await server.listen();const address=server.httpServer.address();
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH,headless:true,args:['--enable-precise-memory-info']});
const fixtures=JSON.parse(await readFile('/tmp/diskmap-layout-prepared.json','utf8'));const results=[];
try {
 for(const viewport of [{width:1024,height:700},{width:3840,height:2160}])for(const dpr of [1,2]){
  const context=await browser.newContext({viewport,deviceScaleFactor:dpr});const page=await context.newPage();const cdp=await context.newCDPSession(page);
  await cdp.send('Performance.enable');const cold=performance.now();await page.goto(`http://127.0.0.1:${address.port}`);console.log('loaded',viewport,dpr);
  await page.evaluate(()=>{document.body.innerHTML='<button id="switch">Treemap</button><canvas></canvas>';});
  for(const fixture of fixtures){
   console.log('measuring',fixture.shape);
   const measurement=await page.evaluate(async({fixtureJSON,viewport,dpr})=>{
    // Transport deep trees as JSON text to avoid the DevTools protocol nesting limit.
    const fixture=JSON.parse(fixtureJSON);
    const frames=()=>Promise.race([new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))),new Promise((_,reject)=>setTimeout(()=>reject(new Error('FRAME_TIMEOUT')),2000))]);
    const {draw}=await import('/src/layout/charts.ts');const canvas=document.querySelector('canvas');
    // Deliberate invalid render requests must fail before allocating canvas memory.
    let capRejected=false;try{draw(fixture.trees[1],'radial',canvas,viewport.width,viewport.height,dpr)}catch(e){capRejected=e.message==='RENDER_BUDGET'}
    if(!capRejected)throw new Error('CAP_NEGATIVE_TEST_FAILED');
    let canvasRejected=false;try{draw(fixture.trees[0],'radial',canvas,100000,100000,2)}catch(e){canvasRejected=e.message==='CANVAS_BUDGET'}
    if(!canvasRejected)throw new Error('CANVAS_NEGATIVE_TEST_FAILED');
    const start=performance.now();const treemap=draw(fixture.trees[1],'treemap',canvas,viewport.width,viewport.height,dpr);
    await frames();
    const initialMs=performance.now()-start;
    const reactionStart=performance.now();document.querySelector('button').textContent='Radial';
    await frames();
    const switchReactionMs=performance.now()-reactionStart;
    const radial=draw(fixture.trees[0],'radial',canvas,viewport.width,viewport.height,dpr);
    await frames();
    return {treemap,radial,initialMs,switchReactionMs,heap:performance.memory?.usedJSHeapSize??null};
   },{fixtureJSON:JSON.stringify(fixture),viewport,dpr});
   const metrics=(await cdp.send('Performance.getMetrics')).metrics;const heap=metrics.find(m=>m.name==='JSHeapUsedSize').value;
   // Sum Chromium descendants' resident sets as a conservative process-level memory trace.
   const processes=execFileSync('ps',['-axo','pid=,ppid=,rss=,comm='],{encoding:'utf8'}).trim().split('\n').map(line=>{const m=line.trim().match(/^(\d+)\s+(\d+)\s+(\d+)\s+(.*)$/);return m?{pid:+m[1],parent:+m[2],rss:+m[3],name:m[4]}:null}).filter(Boolean);
   const descendants=new Set([process.pid]);for(let pass=0;pass<8;pass++)for(const p of processes)if(descendants.has(p.parent))descendants.add(p.pid);
   const browserProcesses=processes.filter(p=>descendants.has(p.pid)&&/Chrome/.test(p.name));
   const row={shape:fixture.shape,viewport,dpr,...measurement,heapBytes:heap,browserProcessRSSBytes:browserProcesses.reduce((s,p)=>s+p.rss*1024,0),rendererRSSBytes:browserProcesses.filter(p=>p.name.includes('(Renderer)')).reduce((s,p)=>s+p.rss*1024,0),browserProcessCount:browserProcesses.length,coldElapsedMs:performance.now()-cold,browser:browser.version()};
   assert.equal(row.treemap.knownBytes,row.radial.knownBytes);assert.equal(row.treemap.unknownCount,row.radial.unknownCount);
   assert(row.treemap.nodes<=10000&&row.radial.nodes<=2000);results.push(row);
  }
  await context.close();
 }
 await writeFile(new URL('./measurements.json',import.meta.url),JSON.stringify(results,null,2)+'\n');
 console.log(JSON.stringify({cases:results.length,maxInitial:Math.max(...results.map(r=>r.initialMs)),maxReaction:Math.max(...results.map(r=>r.switchReactionMs)),maxLayout:Math.max(...results.flatMap(r=>[r.treemap.layoutAndDrawMs,r.radial.layoutAndDrawMs])),maxHeap:Math.max(...results.map(r=>r.heapBytes)),maxCanvas:Math.max(...results.map(r=>r.radial.canvasBytes))}));
}finally{await browser.close();await server.close();}

}

// rss samples all Chrome descendants of this harness, including browser, GPU and utilities.
function rss(){
 const rows=execFileSync('ps',['-axo','pid=,ppid=,rss=,comm='],{encoding:'utf8'}).trim().split('\n').map(line=>{const m=line.trim().match(/^(\d+)\s+(\d+)\s+(\d+)\s+(.*)$/);return m?{pid:+m[1],parent:+m[2],rss:+m[3],name:m[4]}:null}).filter(Boolean);
 const descendants=new Set([process.pid]);for(let pass=0;pass<12;pass++)for(const p of rows)if(descendants.has(p.parent))descendants.add(p.pid);
 const processes=rows.filter(p=>descendants.has(p.pid)&&/Chrome/.test(p.name));
 // Darwin's kernel ledger includes charged physical memory, unlike summed shared RSS mappings.
 const physical=JSON.parse(execFileSync('python3',['-c',`import ctypes,json,sys
class Usage(ctypes.Structure):
 _fields_=[('uuid',ctypes.c_ubyte*16),('values',ctypes.c_uint64*35)]
lib=ctypes.CDLL('/usr/lib/libproc.dylib',use_errno=True)
result={}
for value in sys.argv[1:]:
 pid=int(value);usage=Usage()
 if lib.proc_pid_rusage(pid,4,ctypes.byref(usage))!=0: raise OSError(ctypes.get_errno(),'proc_pid_rusage failed')
 current=usage.values[7];maximum=usage.values[28]
 if maximum<current: raise ValueError('invalid footprint ledger')
 result[value]={'current':current,'maximum':maximum}
print(json.dumps(result))`,...processes.map(p=>String(p.pid))],{encoding:'utf8'}));
 for(const p of processes){p.physicalFootprintBytes=physical[p.pid].current;p.lifetimeMaxFootprintBytes=physical[p.pid].maximum;}
 return {sumLifetimeMaxFootprintBytes:processes.reduce((s,p)=>s+p.lifetimeMaxFootprintBytes,0),physicalFootprintBytes:processes.reduce((s,p)=>s+p.physicalFootprintBytes,0),totalBytes:processes.reduce((s,p)=>s+p.rss*1024,0),rendererBytes:processes.filter(p=>p.name.includes('(Renderer)')).reduce((s,p)=>s+p.rss*1024,0),processes:processes.map(p=>({name:p.name,rssBytes:p.rss*1024,physicalFootprintBytes:p.physicalFootprintBytes,lifetimeMaxFootprintBytes:p.lifetimeMaxFootprintBytes}))};
}

// diagnose separates fresh-case allocation, repeated map switches and explicit canvas release.
async function diagnose(){
 const server=await createServer({root:new URL('../../web',import.meta.url).pathname,server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();
 const address=server.httpServer.address();const origin=`http://127.0.0.1:${address.port}`;
 const fixtures=JSON.parse(await readFile('/tmp/diskmap-layout-prepared.json','utf8'));const selected=process.env.LAYOUT_DIAGNOSTIC_ALL==='1'?fixtures:[fixtures.find(f=>f.shape==='wide')??fixtures[0]];const results=[];
 try{
  for(const fixture of selected)for(const viewport of [{width:1024,height:700},{width:1920,height:1080},{width:3840,height:2160}]){
   const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH,headless:true,args:['--enable-precise-memory-info']});
   try{
    const context=await browser.newContext({viewport,deviceScaleFactor:2});const page=await context.newPage();
    await page.route(`${origin}/__layout_probe__`,route=>route.fulfill({contentType:'text/html',body:'<!doctype html><html><body style="margin:0"><canvas></canvas></body></html>'}));
    await page.goto(`${origin}/__layout_probe__`);await page.waitForTimeout(300);const baseline=rss();
    await page.evaluate(async text=>{window.fixture=JSON.parse(text);window.draw=(await import('/src/layout/charts.ts')).draw;window.allocations=0;const canvas=document.querySelector('canvas');for(const key of ['width','height']){const property=Object.getOwnPropertyDescriptor(HTMLCanvasElement.prototype,key);Object.defineProperty(canvas,key,{get(){return property.get.call(this)},set(value){window.allocations++;property.set.call(this,value);if(this.width*this.height>3840*2160)throw new Error('TRANSIENT_CANVAS_BUDGET')}});}},JSON.stringify(fixture));
    const stages=[];
    for(let i=0;i<20;i++){
     const mode=i%2===0?'treemap':'radial';const measured=await page.evaluate(async({mode,viewport})=>{const start=performance.now();const value=window.draw(window.fixture.trees[mode==='treemap'?1:0],mode,document.querySelector('canvas'),viewport.width,viewport.height,2);await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));return {...value,allocations:window.allocations,frameElapsedMs:performance.now()-start,heapBytes:performance.memory?.usedJSHeapSize??null};},{mode,viewport});
     assert(measured.canvasBytes<=3840*2160*4);assert.equal(measured.allocations,2);assert(measured.nodes<=(mode==='treemap'?10000:2000));if(stages.length){assert.equal(measured.knownBytes,stages[0].knownBytes);assert.equal(measured.unknownCount,stages[0].unknownCount);}
     await page.waitForTimeout(100);stages.push({iteration:i,mode,...measured,rss:rss()});
    }
    // Exercise aspect-ratio changes that could otherwise allocate an oversized intermediate raster.
    await page.evaluate(()=>{const c=document.querySelector('canvas');for(const [w,h] of [[1024,2160],[3840,700],[1024,700]])window.draw(window.fixture.trees[0],'radial',c,w,h,2);});
    await page.evaluate(()=>{const c=document.querySelector('canvas');c.width=0;c.height=0;window.fixture=null;});await page.waitForTimeout(1000);const released=rss();
    assert(released.sumLifetimeMaxFootprintBytes<=512*1024*1024,'FULL_BROWSER_MEMORY_BUDGET');
    const row={viewport,dpr:2,browser:browser.version(),shape:fixture.shape,baseline,stages,released};results.push(row);console.log(JSON.stringify({shape:fixture.shape,viewport,baselineFootprintBytes:baseline.physicalFootprintBytes,sumLifetimeMaxFootprintBytes:released.sumLifetimeMaxFootprintBytes,peakFootprintBytes:Math.max(...stages.map(s=>s.rss.physicalFootprintBytes)),releasedFootprintBytes:released.physicalFootprintBytes,baselineBytes:baseline.totalBytes,peakBytes:Math.max(...stages.map(s=>s.rss.totalBytes)),releasedBytes:released.totalBytes,maxCanvasBytes:Math.max(...stages.map(s=>s.canvasBytes))}));await context.close();
   }finally{await browser.close()}
  }
  const target=process.env.LAYOUT_DIAGNOSTIC_OUTPUT||'measurements-diagnostic.json';await writeFile(new URL(target,import.meta.url),JSON.stringify(results,null,2)+'\n');
 }finally{await server.close()}
}
