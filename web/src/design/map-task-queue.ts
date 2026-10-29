// Compact breadth-first tasks follow index locality without retaining one JS object per visible pixel.
export type RenderRecord={source:bigint|number;parent:number;left:number;right:number;kind:number;flags:number;depth:number;maxDepth:number;weight:number};
export type MapTask={record?:RenderRecord;slot:number;x0:number;y0:number;x1:number;y1:number;depth:number;owner:string;parent:string};
const CAPACITY=64,STRIDE=72,LIMIT=64*1024*1024;
type Chunk={view:DataView<ArrayBuffer>;strings:string[];ids:Map<string,number>;read:number;written:number;bytes:number};
class BlockTasks {
 private chunks:Chunk[]=[];private head=0;private count=0;
 bytes=0;
 get length(){return this.count;}
 push(task:MapTask){
   let chunk=this.chunks.at(-1);
   if(!chunk||chunk.written===CAPACITY){chunk={view:new DataView(new ArrayBuffer(CAPACITY*STRIDE)),strings:[],ids:new Map(),read:0,written:0,bytes:CAPACITY*STRIDE};this.chunks.push(chunk);this.bytes+=chunk.bytes;}
   const stringId=(s:string)=>{const existing=chunk!.ids.get(s);if(existing!==undefined)return existing;const id=chunk!.strings.length;chunk!.strings.push(s);chunk!.ids.set(s,id);const bytes=128+s.length*2;chunk!.bytes+=bytes;this.bytes+=bytes;return id;};
   const at=chunk.written++*STRIDE,v=chunk.view,r=task.record;
   v.setUint32(at,task.slot,true);v.setUint16(at+4,task.depth,true);v.setUint8(at+6,r?.kind??255);v.setUint8(at+7,r?.flags??0);
   v.setFloat64(at+8,task.x0,true);v.setFloat64(at+16,task.y0,true);v.setFloat64(at+24,task.x1,true);v.setFloat64(at+32,task.y1,true);v.setFloat64(at+40,r?.weight??0,true);
   v.setUint32(at+48,r?.left??0,true);v.setUint32(at+52,r?.right??0,true);
   if(typeof r?.source==='bigint')v.setBigUint64(at+56,r.source,true);else {v.setUint32(at+56,r?.source??0,true);v.setUint32(at+60,0,true);}
   v.setUint32(at+64,stringId(task.owner),true);v.setUint32(at+68,stringId(task.parent),true);this.count++;
 }
 pop():MapTask{
  const chunk=this.chunks[this.head],at=chunk.read++*STRIDE,v=chunk.view,kind=v.getUint8(at+6),depth=v.getUint16(at+4,true);
  const task:MapTask={slot:v.getUint32(at,true),depth,x0:v.getFloat64(at+8,true),y0:v.getFloat64(at+16,true),x1:v.getFloat64(at+24,true),y1:v.getFloat64(at+32,true),owner:chunk.strings[v.getUint32(at+64,true)],parent:chunk.strings[v.getUint32(at+68,true)]};
  if(kind!==255)task.record={source:v.getUint32(at+60,true)?v.getBigUint64(at+56,true):v.getUint32(at+56,true),parent:0,left:v.getUint32(at+48,true),right:v.getUint32(at+52,true),kind,flags:v.getUint8(at+7),depth,maxDepth:0,weight:v.getFloat64(at+40,true)};
  this.count--;
  if(chunk.read===chunk.written){this.bytes-=chunk.bytes;chunk.view.buffer.transfer(0);this.chunks[this.head++]=undefined as unknown as Chunk;if(this.head>=64){this.chunks.splice(0,this.head);this.head=0;}}
  return task;
 }
}

// Prefer the lowest physical block across directory frontiers, keeping broad and nested trees sequential.
export class MapTaskQueue {
 private buckets=new Map<number,BlockTasks>();private heap:number[]=[];private retries:MapTask[]=[];private overflow:MapTask[]=[];private count=0;private local:MapTask[]=[];private activeBlock=-1;
 bytes=0;
 constructor(private breadth=true){}
 get length(){return this.count+this.retries.length+this.overflow.length+this.local.length;}
 prepend(tasks:MapTask[]){const target=this.breadth?this.retries:this.local;for(let n=tasks.length-1;n>=0;n--)target.push(tasks[n]);}
 push(task:MapTask){
  // Nested subtrees and resident branches use depth-first locality; broad flat levels use the block frontier.
  const key=Math.floor(task.slot/2048);if(!this.breadth||key===this.activeBlock){this.local.push(task);return;}
  if(this.overflow.length||this.bytes+CAPACITY*STRIDE+1024+2*(task.owner.length+task.parent.length)>LIMIT){this.overflow.push(task);return;}
  let bucket=this.buckets.get(key);
  if(!bucket){bucket=new BlockTasks();this.buckets.set(key,bucket);this.bytes+=256;let at=this.heap.length;this.heap.push(key);while(at){const parent=(at-1)>>>1;if(this.heap[parent]<=key)break;this.heap[at]=this.heap[parent];at=parent;}this.heap[at]=key;}
  const before=bucket.bytes;bucket.push(task);this.bytes+=bucket.bytes-before;this.count++;
 }
 private take(task:MapTask){this.activeBlock=Math.floor(task.slot/2048);return task;}
 pop():MapTask{
  const retry=this.retries.pop();if(retry)return this.take(retry);const local=this.local.pop();if(local)return this.take(local);const overflow=this.overflow.pop();if(overflow)return this.take(overflow);
  const key=this.heap[0],bucket=this.buckets.get(key)!,before=bucket.bytes,task=bucket.pop();this.bytes+=bucket.bytes-before;this.count--;
  if(!bucket.length){this.buckets.delete(key);this.bytes-=256;const last=this.heap.pop()!;
   if(this.heap.length){let at=0;while(at*2+1<this.heap.length){let child=at*2+1;if(child+1<this.heap.length&&this.heap[child+1]<this.heap[child])child++;if(this.heap[child]>=last)break;this.heap[at]=this.heap[child];at=child;}this.heap[at]=last;}
  }
  return this.take(task);
 }
}
