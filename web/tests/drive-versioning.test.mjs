import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import vm from 'node:vm';
// Execute the production parser against captured response shapes and bounded synthetic pages.
const source=stripTypeScriptTypes(readFileSync(new URL('../src/drive-versioning.ts',import.meta.url),'utf8'),{mode:'transform'}).replace(/^import .*;$/gm,'').replace(/export /g,'');
class Denied extends Error{}class SessionDenied extends Error{}
function fixture(pages,options={}){
 const calls=[];let page=0;
 const read=vm.runInNewContext(source+'\nreadDriveVersioning',{Map,fetch,AbortSignal,URLSearchParams,SessionDenied,SnapshotAccessDenied:Denied,snapshotClient:async()=>async(url,body)=>{
  calls.push(body?Object.fromEntries(body):url);
  if(options.fail)throw options.fail;
  if(!body)return options.noApi?{}:{'SYNO.SynologyDrive.Share':{path:'entry.cgi',minVersion:1,maxVersion:2}};
  return pages[page++];
 }});
 return {read,calls};
}
const row=(name,enabled,count)=>({share_name:name,share_enable:enabled,rotate_cnt:count});
test('Drive status distinguishes enabled, disabled and non-team-folder shares',async()=>{
 const f=fixture([{total:3,items:[row('Docs',true,8),row('dev',true,0),row('Archive',false,'-')]}]);
 const result=await f.read(new AbortController().signal);
 assert.equal(result.state,'readable');
 assert.equal(result.shares.get('Docs').state,'readable');assert.equal(result.shares.get('Docs').maxVersions,8);
 assert.equal(result.shares.get('dev').state,'missing');assert.equal(result.shares.get('dev').teamFolder,true);
 assert.equal(result.shares.get('Archive').teamFolder,false);
 assert.deepEqual(f.calls.slice(1).map(x=>x.method),['list']);
 assert.equal(JSON.parse(f.calls[1].sort_by),'share_name');
});
test('My Drive is qualified as personal directories rather than the entire homes share',async()=>{
 const r=await fixture([{total:2,items:[row('homes/mydrive',true,8),row('homes',false,'-')]}]).read(new AbortController().signal);
 assert.equal(r.shares.get('homes').personal,true);assert.equal(r.shares.get('homes').state,'readable');
});
test('Drive pagination consumes each page exactly once',async()=>{
 const f=fixture([{total:101,items:Array.from({length:100},(_,i)=>row('s'+i,true,8))},{total:101,items:[row('last',true,0)]}]);
 const r=await f.read(new AbortController().signal);assert.equal(r.shares.size,101);assert.equal(f.calls[2].offset,'100');
});
test('invalid limits, incomplete pages, duplicates and changing inventories stay unknown',async()=>{
 const cases=[[{total:1025,items:[]}],[{total:1,items:[]}],[{total:0,items:[row('a',true,8)]}],[{total:2,items:[row('a',true,8),row('a',true,8)]}],[{total:2,items:[row('a',true,8)]},{total:3,items:[row('b',true,8)]}]];
 for(const pages of cases){const r=await fixture(pages).read(new AbortController().signal);assert.equal(r.state,'unknown');assert.equal(r.shares.size,0);}
 for(const count of ['8',-1,33,null]){const r=await fixture([{total:1,items:[row('a',true,count)]}]).read(new AbortController().signal);assert.equal(r.shares.get('a').state,'unknown');}
});
test('API absence, access denial, cancellation and expired sessions cannot imply disabled versioning',async()=>{
 assert.equal((await fixture([],{noApi:true}).read(new AbortController().signal)).state,'unknown');
 assert.equal((await fixture([],{fail:new Denied()}).read(new AbortController().signal)).state,'denied');
 await assert.rejects(fixture([],{fail:new SessionDenied('SESSION_CHANGED')}).read(new AbortController().signal),/SESSION_CHANGED/);
 const controller=new AbortController();controller.abort();await assert.rejects(fixture([],{fail:Error()}).read(controller.signal));
});
