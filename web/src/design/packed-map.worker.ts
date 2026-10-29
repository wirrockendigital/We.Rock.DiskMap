import {MapIndexRenderer} from './map-index-renderer';
import type {Geometry} from './chart-layout';

// Fetching stays on the authenticated UI boundary; only numeric immutable blocks enter this worker.
let sequence=0;
const waiting=new Map<number,{resolve:(v:{block:number;data:ArrayBuffer}[])=>void;reject:(e:Error)=>void}>();
const renderer=new MapIndexRenderer((indexId,blocks)=>new Promise((resolve,reject)=>{const request=++sequence;waiting.set(request,{resolve,reject});self.postMessage({need:request,indexId,blocks});}));
const send=(id:number,value:unknown,progress=false)=>{const bitmap=(value as Geometry|null)?.bitmap;(self as unknown as {postMessage(v:unknown,t:Transferable[]):void}).postMessage({id,value,progress},bitmap?[bitmap]:[]);};
// Async rendering yields between bounded batches so newer navigation can cancel old work promptly.
self.onmessage=async event=>{
 const data=event.data;if(data.loaded){const pending=waiting.get(data.loaded);waiting.delete(data.loaded);if(data.error)pending?.reject(Error(data.error));else pending?.resolve(data.blocks);return;}
 const {id,operation}=data;
 // Superseded render requests must not keep queued authenticated transfers alive.
 if(operation==='open'||operation==='cancel'||operation==='layout'){for(const p of waiting.values())p.reject(new DOMException('Superseded','AbortError'));waiting.clear();self.postMessage({cancelLoads:true});}

 try{let value:unknown=null;
  if(operation==='open')renderer.open(data.manifest);
  else if(operation==='cancel')renderer.cancel();
  else if(operation==='layout')value=await renderer.render(data.indexId,data.root,data.options,v=>send(id,v,true));
  else if(operation==='probe')value=await renderer.probe(data.frameId,data.x,data.y);
  else throw Error('INVALID_MAP_OPERATION');send(id,value);
 }catch(error){self.postMessage({id,error:error instanceof Error?error.message:'MAP_RENDER_FAILED',aborted:error instanceof DOMException&&error.name==='AbortError'});}
};
