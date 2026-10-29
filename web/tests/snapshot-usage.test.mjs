import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import vm from 'node:vm';
// Validate only the read-only DSM report contract; credentials never enter stored results.
const source=stripTypeScriptTypes(readFileSync(new URL('../src/snapshot-usage.ts',import.meta.url),'utf8'),{mode:'transform'}).replace(/^import .*;$/gm,'').replace(/export /g,'');
function fixture(report={status:'done',estimated_in_bytes:811008,range_start:100,range_end:200,end_task:201},available=true){
 let token='session';const methods=[];
 const read=vm.runInNewContext(source+'\nreadSnapshotUsage',{currentToken:()=>token,sessionRequest:async()=>{},SessionDenied:Error,AbortSignal,TextDecoder,Uint8Array,URLSearchParams,fetch:async(url,options)=>{const method=options.body?.get('method');methods.push(method??'query');const data=!method?{'SYNO.Snap.Usage.Share':{path:'entry.cgi',minVersion:1,maxVersion:1}}:method==='status'?{share_list:available?[{share_name:'Anwendungsdaten',status:'done'}]:[]}:report;return new Response(JSON.stringify({success:true,data}));}});
 return {read,methods,revoke:()=>{token=null;}};
}
test('completed snapshot estimate preserves timeframe and calculation date without mutations',async()=>{const f=fixture();const r=await f.read('Anwendungsdaten',new AbortController().signal);assert.deepEqual(JSON.parse(JSON.stringify(r)),{bytes:'811008',from:100,to:200,calculatedAt:201});assert.deepEqual(f.methods,['query','status','get_report']);});
test('missing report stays unavailable rather than zero',async()=>{const f=fixture(undefined,false);assert.equal(await f.read('Anwendungsdaten',new AbortController().signal),null);assert.deepEqual(f.methods,['query','status']);});
test('invalid values and absent sessions fail closed',async()=>{for(const bytes of [-1,9007199254740992,'811008']){const f=fixture({status:'done',estimated_in_bytes:bytes,range_start:100,range_end:200,end_task:201});await assert.rejects(f.read('Anwendungsdaten',new AbortController().signal));}const f=fixture();f.revoke();await assert.rejects(f.read('Anwendungsdaten',new AbortController().signal));assert.equal(f.methods.length,0);});
// Exercise the verified Core.Share response independently of optional size reports.
function statusFixture(data,options={}){
 let token='session';const calls=[];
 const read=vm.runInNewContext(source+'\nreadSnapshotShareStatus',{currentToken:()=>token,sessionRequest:async()=>{},SessionDenied:class extends Error{},AbortSignal,TextDecoder,Uint8Array,URLSearchParams,fetch:async(url,init)=>{
  calls.push(init.body?Object.fromEntries(init.body):url);
  if(options.switchSession)token='other';
  if(options.abort)options.abort.abort();
  if(!init.body)return new Response(JSON.stringify({success:true,data:{'SYNO.Core.Share':{path:'entry.cgi',minVersion:1,maxVersion:1}}}));
  return new Response(JSON.stringify(options.denied?{success:false,error:{code:105}}:{success:true,data}),{status:options.http403?403:200});
 }});
 return {read,calls};
}
test('schedule and existing snapshots are independent of reports',async()=>{
 for(const [count,scheduled,state] of [[0,false,'missing'],[0,true,'readable'],[19,false,'readable'],[29,true,'readable']]){
  const f=statusFixture({name:'video',support_snapshot:true,snapshot_info:{snapshot_count:count,...(scheduled?{schedule:{hour:6}}:{})}});
  const result=await f.read('video',new AbortController().signal);
  assert.deepEqual(JSON.parse(JSON.stringify(result)),{state,supported:true,scheduled,count});
  assert.equal(f.calls.length,2);assert.equal(f.calls[1].method,'get');assert.equal(JSON.parse(f.calls[1].name),'video');
  assert.deepEqual(JSON.parse(f.calls[1].additional),['support_snapshot','snapshot_schedule','snapshot_count']);
 }
});
test('unsupported, denied and unknown remain separate observations',async()=>{
 const read=async(data,options)=>JSON.parse(JSON.stringify(await statusFixture(data,options).read('video',new AbortController().signal)));
 assert.equal((await read({name:'video',support_snapshot:false})).state,'missing');
 for(const options of [{denied:true},{http403:true}])assert.equal((await read({},options)).state,'denied');
 for(const data of [{},{name:'other',support_snapshot:false},{name:'video',support_snapshot:true},{name:'video',support_snapshot:true,snapshot_info:{snapshot_count:-1}},{name:'video',support_snapshot:true,snapshot_info:{snapshot_count:0,schedule:false}}]){
  assert.deepEqual(await read(data),{state:'unknown',supported:null,scheduled:null,count:null});
 }
});
test('cancelled requests and session changes cannot publish a share status',async()=>{
 const controller=new AbortController();await assert.rejects(statusFixture({}, {abort:controller}).read('video',controller.signal));
 await assert.rejects(statusFixture({}, {switchSession:true}).read('video',new AbortController().signal),/SESSION_CHANGED/);
});
