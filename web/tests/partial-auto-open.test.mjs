import assert from 'node:assert/strict';
import {readFile,mkdir,mkdtemp,rm} from 'node:fs/promises';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
import {rolldown} from 'rolldown';
// Exercise the production owner transition with controlled read-only API responses.
// Absolute fixture paths keep the test independent of the runner working directory.
const testRoot=new URL('../.test-build/',import.meta.url).pathname;
await mkdir(testRoot,{recursive:true});
const dir=await mkdtemp(join(testRoot,'partial-auto-'));
try {
 const bundle=await rolldown({cwd:new URL('../',import.meta.url).pathname,input:'src/design/useNativeDiskMap.ts',platform:'node',plugins:[{
  name:'owner-test',
  async transform(code,id){if(id.endsWith('/design/useNativeDiskMap.ts'))return code+'\nexport {NativeOwner};';},
  load(id){if(id.endsWith('/Workbench.tsx'))return 'export const readView=(r,s)=>globalThis.partialRead(r,s); export const checkedScanBoundaryReport=x=>x; export const checkedReader=x=>x; export const readLeasedPaths=()=>{};';}
 }]});
 await bundle.write({file:join(dir,'owner.mjs'),format:'esm'});await bundle.close();
 const {NativeOwner}=await import(pathToFileURL(join(dir,'owner.mjs')));
 const result={job:{id:'job',profileId:'profile',state:'completed',profileRevision:1,resultSnapshotId:'partial'},snapshotState:'partial',snapshotEndedAt:'2026-10-13T21:43:00Z',coverageErrors:1};
 globalThis.partialRead=async r=>r.action==='job'?result:{truncated:false,boundaries:[{reasonCode:'UNREADABLE'}]};
 const owner=new NativeOwner(()=>{},()=>{});owner.state.profileId='profile';owner.state.profiles=[{profile:{id:'profile'},latestJobId:'job'}];
 const opens=[];owner.openProfile=async(...args)=>{opens.push(args);};
 await owner.readLatestResult('profile',0,new AbortController().signal,owner.navigationVersion);
 assert.equal(opens.length,1);assert.equal(opens[0][0],'profile');assert.equal(opens[0][1].snapshotId,'partial');assert.deepEqual(opens[0][1].reasons,{UNREADABLE:1});assert.equal(owner.state.terminalCode,'INCOMPLETE_COVERAGE');
 // A source switch during the request must not open a result in the new source.
 opens.length=0;globalThis.partialRead=async()=>{owner.epoch++;return result;};await owner.readLatestResult('profile',0,new AbortController().signal,owner.navigationVersion);assert.equal(opens.length,0);
 // A source intent owns the view even before its asynchronous reader changes the view epoch.
 owner.epoch=0;opens.length=0;globalThis.partialRead=async r=>{if(r.action==='job')owner.navigationVersion++;return r.action==='job'?result:{truncated:false,boundaries:[]};};
 await owner.readLatestResult('profile',0,new AbortController().signal,owner.navigationVersion);assert.equal(opens.length,0);
 // Re-selecting the same folder preserves its pending map request.
 const originalController=owner.controller,currentView={rootId:'same-folder',selectedNodeId:null};
 owner.controller={current:{state:{view:currentView}}};owner.query=new AbortController();
 await owner.move({...currentView});assert.equal(owner.query.signal.aborted,false);
 owner.controller=originalController;
 owner.dispose();delete globalThis.partialRead;
 console.log('PASS: partial auto-open with coverage warning, stale-source response rejected');
}finally{await rm(dir,{recursive:true,force:true});}
