import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, mkdirSync, mkdtempSync, writeFileSync, rmSync, cpSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { createRequire } from 'node:module';

// Compile real request and session adapters without building a deployment artifact.
const web = fileURLToPath(new URL('../', import.meta.url));
mkdirSync(join(web, '.test-build'), { recursive: true });
const output = mkdtempSync(join(web, '.test-build/privacy-'));
let session, actions, monitorSession;
try {
 writeFileSync(join(output,'package.json'),'{"type":"commonjs"}');
 for(const name of ['session.ts','session-monitor.ts','actions.ts']) cpSync(join(web,'src',name),join(output,name));
 execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--strict','--skipLibCheck','--outDir',join(output,'compiled'),join(output,'session-monitor.ts'),join(output,'actions.ts')],{stdio:'pipe'});
 const require=createRequire(import.meta.url);session=require(join(output,'compiled/session.js'));actions=require(join(output,'compiled/actions.js'));({monitorSession}=require(join(output,'compiled/session-monitor.js')));
}finally{rmSync(output,{recursive:true,force:true});}

// External network is unavailable in this harness; only the explicitly local endpoint can respond.
test('requests work without internet and never persist tokens or metadata',async()=>{
 const calls=[];let storageTouches=0;
 const properties=['localStorage','sessionStorage','indexedDB','caches'];
 const before=new Map(properties.map(key=>[key,Object.getOwnPropertyDescriptor(globalThis,key)]));
 for(const key of properties) Object.defineProperty(globalThis,key,{configurable:true,get(){storageTouches++;throw new Error('OFFLINE_STORAGE_FORBIDDEN');}});
 try {
  const token='synthetic-token-must-stay-in-header';
  const request=async(url,options)=>{
   assert.match(url,/^\.\/api\.cgi\?action=(session|health|scan)$/);
   assert.ok(!url.includes(token));assert.equal(options.headers['X-SYNO-TOKEN'],token);
   assert.equal(options.cache,'no-store');assert.equal(options.redirect,'error');assert.equal(options.credentials,'same-origin');
   assert.ok(!String(options.body??'').includes(token));calls.push(url);
   return new Response(JSON.stringify({contractVersion:1,data:{authenticated:true}}));
  };
  const signal=new AbortController().signal;
  await session.sessionRequest('session',signal,token,request);
  await session.sessionRequest('health',signal,token,request);
  await actions.requestScan({contractVersion:1,profileId:'fixture',mode:'full'},'fixture-key',signal,request,token);
  assert.equal(calls.length,3);assert.equal(storageTouches,0);
  await assert.rejects(session.sessionRequest('session',signal,null,request),session.SessionDenied);assert.equal(calls.length,3);
 }finally{for(const key of properties){const descriptor=before.get(key);if(descriptor)Object.defineProperty(globalThis,key,descriptor);else delete globalThis[key];}}
});

// A hidden view cancels its request; late authenticated completion cannot reopen private content.
test('hidden or expired session cannot restore private views from delayed results',async()=>{
 let visible=true,resolve;const states=[];const timers=new Map();let timer=0,signal;
 const monitor=monitorSession({visible:()=>visible,changed:state=>states.push(state),request:received=>{signal=received;return new Promise(done=>{resolve=done;});},schedule:(callback,delay)=>{timers.set(++timer,{callback,delay});return timer;},cancel:id=>timers.delete(id)});
 visible=false;monitor.visibility();assert.equal(signal.aborted,true);resolve({privateName:'stale'});await new Promise(done=>setImmediate(done));
 assert.equal(states.at(-1),'checking');assert.equal(timers.size,0);monitor.stop();
 const deniedStates=[];
 const denied=monitorSession({visible:()=>true,changed:state=>deniedStates.push(state),request:async()=>{throw new session.SessionDenied('SESSION_REQUIRED');},schedule:()=>1,cancel:()=>{}});
 await new Promise(done=>setImmediate(done));assert.equal(deniedStates.at(-1),'denied');denied.stop();
});

// Only the reviewed UI-preference adapters use local storage; metadata and session adapters never do.
test('static assets prohibit remote loading and application code has no persistent browser store',()=>{
 const html=readFileSync(join(web,'index.html'),'utf8');
 for(const directive of ["default-src 'none'","script-src 'self'","connect-src 'self'","font-src 'self'","base-uri 'none'","form-action 'none'"])assert.ok(html.includes(directive));
 assert.match(html,/name="referrer" content="no-referrer"/);
 const inspect=directory=>{for(const entry of readdirSync(directory,{withFileTypes:true})){const path=join(directory,entry.name);if(entry.isDirectory()){inspect(path);continue;}if(!/\.(tsx?|css)$/.test(entry.name)||entry.name.endsWith('.test.ts'))continue;const source=readFileSync(path,'utf8');assert.doesNotMatch(source,/\b(?:sessionStorage|indexedDB|serviceWorker|sendBeacon)\b/);if(!['diagnostics.ts','useNativeDiskMap.ts'].includes(entry.name))assert.doesNotMatch(source,/\blocalStorage\b/);assert.doesNotMatch(source,/\b(?:caches\.(?:open|put)|new\s+(?:WebSocket|EventSource))\b/);assert.doesNotMatch(source,/(?:src|href)\s*=\s*["']https?:\/\/|@import\s+["']https?:\/\//);}};
 inspect(join(web,'src'));
});

// The account can change after headers while JSON is still in transit.
test('late response body cannot authenticate a replacement DSM session',async()=>{
 const before=Object.getOwnPropertyDescriptor(globalThis,'window');let resolve;
 const host={parent:{SYNO:{SDS:{Session:{SynoToken:'first'}}}}};
 Object.defineProperty(globalThis,'window',{configurable:true,value:host});
 try{
  const pending=session.sessionRequest('session',new AbortController().signal,'first',async()=>({ok:true,status:200,json:()=>new Promise(done=>resolve=done)}));
  await new Promise(done=>setImmediate(done));host.parent.SYNO.SDS.Session.SynoToken='second';
  resolve({contractVersion:1,data:{authenticated:true,principal:'old-owner'}});
  await assert.rejects(pending,session.SessionDenied);
 }finally{if(before)Object.defineProperty(globalThis,'window',before);else delete globalThis.window;}
});

// A stale authorization must neither render nor poison the next confirmed owner identity.
test('owner changes clear the view and stale generations cannot replace the owner',async()=>{
 let visible=true;const pending=[],changes=[],timers=new Map();let seq=0;
 const monitor=monitorSession({visible:()=>visible,changed:(state,principal)=>changes.push([state,principal]),request:()=>new Promise(done=>pending.push(done)),schedule:(callback,delay)=>{timers.set(++seq,{callback,delay});return seq;},cancel:id=>timers.delete(id)});
 pending.shift()({data:{principal:'owner-a'}});await new Promise(done=>setImmediate(done));
 monitor.focus();const stale=pending.shift();visible=false;monitor.visibility();visible=true;monitor.visibility();
 pending.shift()({data:{principal:'owner-b'}});await new Promise(done=>setImmediate(done));const count=changes.length;
 stale({data:{principal:'owner-a'}});await new Promise(done=>setImmediate(done));
 assert.equal(changes.length,count);assert.deepEqual(changes.at(-1),['authenticated','owner-b']);
 assert.ok(changes.some(([state])=>state==='checking'));monitor.stop();assert.equal(timers.size,0);
});
