import type {PreferenceView} from '../contracts';
import type {ViewPin} from '../snapshot-reader';
import {currentToken,SessionDenied} from '../session';
import {queryEnvelope} from '../analysis-query';
import {BLOCK_BYTES,RECORD_BYTES,RECORDS_PER_BLOCK,decodeBase64,decodeRecord,type MapManifest} from './map-index-format';

const responseTokens=new WeakMap<Response,string>();
export interface MapContext {view:PreferenceView;pin:ViewPin}
// Network buffers are bounded before parsing; consumed stream buffers are released immediately.
async function bounded(response:Response,limit:number):Promise<ArrayBuffer>{const reader=response.body?.getReader();if(!reader)throw Error('INVALID_RESPONSE');const parts:Uint8Array[]=[];let length=0;try{for(;;){const {value,done}=await reader.read();if(done)break;length+=value.length;if(length>limit){await reader.cancel();throw Error('MAP_RESPONSE_TOO_LARGE');}parts.push(value);}const out=new Uint8Array(length);let at=0;for(const part of parts){out.set(part,at);at+=part.length;}for(const part of parts)if(part.buffer instanceof ArrayBuffer&&part.buffer.byteLength)part.buffer.transfer(0);const token=responseTokens.get(response);if(token&&currentToken()!==token)throw new SessionDenied('SESSION_CHANGED');return out.buffer;}finally{reader.releaseLock();}}
// All requests reuse the central view lease and the current DSM token without persisting either.
async function request(context:MapContext,action:string,extra:Record<string,unknown>,signal:AbortSignal):Promise<Response>{
 const {view,pin}=context,token=currentToken();if(!token)throw new SessionDenied('SESSION_REQUIRED');if(view.profileId!==pin.profileId||view.snapshotId!==pin.snapshotId)throw Error('SNAPSHOT_MISMATCH');
 const response=await fetch('./api.cgi?action='+action,{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal:AbortSignal.any([signal,AbortSignal.timeout(15000)]),headers:{'Content-Type':'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({contractVersion:1,profileId:pin.profileId,viewId:pin.viewId,query:{snapshotId:pin.snapshotId,rootId:view.rootId,metric:'logical',mode:'highlight',filter:{categories:[]},depth:1,elementBudget:1},...extra})});
 if(currentToken()!==token)throw new SessionDenied('SESSION_CHANGED');responseTokens.set(response,token);
 if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');if(!response.ok){const envelope=await queryEnvelope(response);throw Error(envelope.error?.code??'MAP_INDEX_UNAVAILABLE');}return response;
}
// Preparation is explicit; navigation cancellation also cancels its polling delay.
export async function readMapManifest(context:MapContext,signal:AbortSignal,onPreparing?:()=>void):Promise<MapManifest>{
 for(;;){const response=await request(context,'map-manifest',{},signal);const envelope=JSON.parse(new TextDecoder().decode(await bounded(response,4*1024*1024+4096)));const m=envelope.data as MapManifest;
 if(envelope.contractVersion!==1||m?.version!==1||m.snapshotId!==context.pin.snapshotId||! /^[a-f0-9]{64}$/.test(m.indexId)||!['ready','preparing'].includes(m.status))throw Error('INVALID_MAP_MANIFEST');
 if(m.status==='ready'){if(!Number.isSafeInteger(m.count)||m.count<1||m.count>=0xffffffff||!Number.isSafeInteger(m.root)||m.root<1||m.root>m.count||m.blockBytes!==BLOCK_BYTES||typeof m.rootId!=='string'||!Array.isArray(m.identities)||m.identities.length>10000)throw Error('INVALID_MAP_MANIFEST');const ids=new Set<string>(),slots=new Set<number>();for(const n of m.identities){if(!n||!Number.isSafeInteger(n.slot)||n.slot<1||n.slot>m.count||typeof n.id!=='string'||n.id.length>160||ids.has(n.id)||slots.has(n.slot)||typeof n.name!=='string'||n.name.length>4096||!['nas','volume','share','other'].includes(n.kind))throw Error('INVALID_MAP_MANIFEST');ids.add(n.id);slots.add(n.slot);}if(m.rootId!==context.view.rootId&&!(context.view.rootId==='nas'&&m.identities.some(n=>n.id===m.rootId&&n.kind==='nas')))throw Error('SNAPSHOT_MISMATCH');signal.throwIfAborted();const record=decodeBase64(m.record);if(record.length!==RECORD_BYTES)throw Error('INVALID_MAP_MANIFEST');decodeRecord(record.buffer,0,m.count);return m;}
 signal.throwIfAborted();onPreparing?.();await new Promise<void>((resolve,reject)=>{const abort=()=>{clearTimeout(timer);reject(new DOMException('Superseded','AbortError'));};const timer=setTimeout(()=>{signal.removeEventListener('abort',abort);resolve();},500);signal.addEventListener('abort',abort,{once:true});if(signal.aborted)abort();});}
}
// Compressed session data preserves warm navigation without retaining every decoded record.
export class MapChunkCache {
 private token='';private bytes=0;private values=new Map<string,{data:ArrayBuffer;digest:Uint8Array}>();
 private qualify(){const token=currentToken();if(!token)throw new SessionDenied('SESSION_REQUIRED');if(token!==this.token){this.clear();this.token=token;}}
 clear(){this.values.clear();this.bytes=0;this.token='';}
 get(index:string,block:number){this.qualify();const key=index+':'+block,value=this.values.get(key);if(value){this.values.delete(key);this.values.set(key,value);}return value;}
 put(index:string,block:number,value:{data:ArrayBuffer;digest:Uint8Array}){this.qualify();const key=index+':'+block;const previous=this.values.get(key);this.bytes-=previous?previous.data.byteLength+256:0;this.values.delete(key);this.values.set(key,value);this.bytes+=value.data.byteLength+256;while(this.bytes>48*1024*1024){const first=this.values.keys().next().value!;this.bytes-=this.values.get(first)!.data.byteLength+256;this.values.delete(first);}}
}
// Cached and network blocks share decompression bounds and checksum validation.
async function unpack(data:ArrayBuffer,digest:Uint8Array,expected:number){
 // Copy into the exact bounded output and immediately release each consumed inflater buffer.
 const raw=new Uint8Array(expected),reader=new Blob([data]).stream().pipeThrough(new DecompressionStream('gzip')).getReader();let length=0;
 try{for(;;){const {value,done}=await reader.read();if(done)break;if(length+value.length>expected){await reader.cancel();throw Error('INVALID_MAP_FRAME');}raw.set(value,length);length+=value.length;if(value.buffer instanceof ArrayBuffer)value.buffer.transfer(0);}}finally{reader.releaseLock();}
 // Restore fixed records from lossless byte planes before validating their canonical digest.
 const records=new Uint8Array(expected),rows=expected/RECORD_BYTES;if(!Number.isInteger(rows))throw Error('INVALID_MAP_FRAME');
 // Four byte planes form one explicitly little-endian word, reducing the decode loop count.
 const restored=new DataView(records.buffer);
 for(let column=0;column<RECORD_BYTES;column+=4){const a=column*rows,b=a+rows,c=b+rows,d=c+rows;
  for(let row=0;row<rows;row++)restored.setUint32(row*RECORD_BYTES+column,raw[a+row]|raw[b+row]<<8|raw[c+row]<<16|raw[d+row]<<24,true);
 }
 raw.buffer.transfer(0);const actual=new Uint8Array(await crypto.subtle.digest('SHA-256',records));if(length!==expected||digest.length!==actual.length||digest.some((v,i)=>v!==actual[i]))throw Error('INVALID_MAP_FRAME');return records.buffer;
}
// Binary frames repeat the immutable index identity and verify every independently fetched block.
export async function readMapChunks(context:MapContext,m:MapManifest,blocks:number[],signal:AbortSignal,cache?:MapChunkCache):Promise<{block:number;data:ArrayBuffer}[]>{
 if(!blocks.length||blocks.length>128||new Set(blocks).size!==blocks.length||blocks.some(n=>!Number.isSafeInteger(n)||n<0||n>Math.floor(m.count/RECORDS_PER_BLOCK)))throw Error('INVALID_MAP_REQUEST');
 signal.throwIfAborted();if(context.view.profileId!==context.pin.profileId||context.view.snapshotId!==context.pin.snapshotId||m.snapshotId!==context.pin.snapshotId)throw Error('SNAPSHOT_MISMATCH');
 const token=currentToken(),cached=blocks.map(block=>cache?.get(m.indexId,block));
 if(cached.every(Boolean)){const out=await Promise.all(blocks.map(async(block,i)=>({block,data:await unpack(cached[i]!.data,cached[i]!.digest,Math.min(BLOCK_BYTES,(m.count+1)*RECORD_BYTES-block*BLOCK_BYTES))})));signal.throwIfAborted();if(currentToken()!==token)throw new SessionDenied('SESSION_CHANGED');return out;}

 // Mixed batches transmit only missing blocks; cached members still undergo validation.
 const requested=blocks.filter((_,i)=>!cached[i]);
 // A corrupt derived artifact is rebuilt by the server; retry once against the same immutable identity.
 let response:Response;try{response=await request(context,'map-chunks',{indexId:m.indexId,blocks:requested},signal);}catch(error){if(!(error instanceof Error)||!['MAP_INDEX_UNAVAILABLE','MAP_PREPARING'].includes(error.message))throw error;const repaired=await readMapManifest(context,signal);if(repaired.indexId!==m.indexId||repaired.count!==m.count||repaired.root!==m.root||repaired.record!==m.record)throw Error('SNAPSHOT_MISMATCH');response=await request(context,'map-chunks',{indexId:m.indexId,blocks:requested},signal);}
 if(response.headers.get('Content-Type')!=='application/octet-stream')throw Error('INVALID_MAP_FRAME');const raw=await bounded(response,4*1024*1024),v=new DataView(raw),bytes=new Uint8Array(raw);
 const hex=(value:Uint8Array)=>Array.from(value,n=>n.toString(16).padStart(2,'0')).join('');if(raw.byteLength<44||new TextDecoder().decode(bytes.subarray(0,8))!=='WRDCH003'||hex(bytes.subarray(8,40))!==m.indexId||v.getUint32(40,true)<1||v.getUint32(40,true)>requested.length)throw Error('INVALID_MAP_FRAME');
 // A byte-bounded response may contain only a prefix; the worker requests missing blocks again.
 // Independent decompression and digests remain bounded by the admitted block count.
 const pending:(()=>Promise<{block:number;data:ArrayBuffer}>)[]=cached.flatMap((entry,i)=>entry?[async()=>({block:blocks[i],data:await unpack(entry.data,entry.digest,Math.min(BLOCK_BYTES,(m.count+1)*RECORD_BYTES-blocks[i]*BLOCK_BYTES))})]:[]);let at=44;
 for(const expectedBlock of requested.slice(0,v.getUint32(40,true))){
  if(at+44>raw.byteLength)throw Error('INVALID_MAP_FRAME');const block=Number(v.getBigUint64(at,true)),length=v.getUint32(at+8,true),expected=Math.min(BLOCK_BYTES,(m.count+1)*RECORD_BYTES-block*BLOCK_BYTES);
  if(block!==expectedBlock||length>BLOCK_BYTES+1024||length<1||at+44+length>raw.byteLength)throw Error('INVALID_MAP_FRAME');
  const checksum=bytes.slice(at+12,at+44),compressed=raw.slice(at+44,at+44+length);
  pending.push(async()=>{const data=await unpack(compressed,checksum,expected);signal.throwIfAborted();if(currentToken()!==token)throw new SessionDenied('SESSION_CHANGED');cache?.put(m.indexId,block,{data:compressed,digest:checksum});return {block,data};});at+=44+length;
 }
 if(at!==raw.byteLength)throw Error('INVALID_MAP_FRAME');raw.transfer(0);
 const out=await Promise.all(pending.map(load=>load()));signal.throwIfAborted();if(currentToken()!==responseTokens.get(response))throw new SessionDenied('SESSION_CHANGED');return out;
}
