import {decodeRecord,decodeBase64,visibleBytes,RECORDS_PER_BLOCK,RECORD_BYTES,type IndexRecord,type MapManifest} from './map-index-format';
import {tileSurface,radialSurface} from './chart-color';
import type {StorageNode} from './model';
import type {Hit,Geometry} from './chart-layout';

import {MapTaskQueue,type MapTask as Task,type RenderRecord} from './map-task-queue';
type FetchBlocks=(indexId:string,blocks:number[])=>Promise<{block:number;data:ArrayBuffer}[]>;
export interface RenderOptions {mode:'treemap'|'radial';box:{width:number;height:number};zoom?:{scale:number;x:number;y:number};recycle:boolean;system:boolean;dpr:number}

// Shared numeric readers avoid per-record DataView and closure allocation in dense maps.
function readAddress(v:DataView,offset:number,count:number){const n=v.getUint32(offset,true);if(v.getUint32(offset+4,true)||n>count)throw Error('INVALID_MAP_INDEX');return n;}
function readWeight(v:DataView,offset:number){return v.getUint32(offset,true)+v.getUint32(offset+4,true)*4294967296+v.getUint32(offset+8,true)*18446744073709551616+v.getUint32(offset+12,true)*79228162514264337593543950336;}

// One session-wide cache uses byte accounting rather than a global file-count ceiling.
export class MapIndexRenderer {
 private views=new WeakMap<ArrayBuffer,DataView>();
 // Consecutive records in the same block need only one LRU promotion.
 private touchedBlock='';
 private blocks=new Map<string,ArrayBuffer>();private bytes=0;private blockLimit=128*1024*1024;private epoch=0;
 private frames=new Map<string,{value:NonNullable<MapIndexRenderer['frame']>;bytes:number}>();private frameBytes=0;
 // Virtual identity lookup must stay constant-time in the per-pixel traversal.
 private virtuals=new WeakMap<MapManifest,Map<number,MapManifest['identities'][number]>>();
 private manifests=new Map<string,MapManifest>();private frame: {epoch:number;m:MapManifest;options:RenderOptions;canvas:OffscreenCanvas;grid:Uint32Array<ArrayBuffer>;pixels:Uint32Array<ArrayBuffer>|null;scale:number;labels:Map<number,Hit>;radius:number;inner:number;cx:number;cy:number}|null=null;
 constructor(private fetchBlocks:FetchBlocks){}
 // Opening another root preserves immutable blocks but invalidates all old visual work.
 open(m:MapManifest){this.cancel();this.virtuals.set(m,new Map(m.identities.map(n=>[n.slot,n])));this.manifests.delete(m.indexId+':'+m.rootId);this.manifests.set(m.indexId+':'+m.rootId,m);while(this.manifests.size>64)this.manifests.delete(this.manifests.keys().next().value!);}
 cancel(){this.epoch++;const old=this.frame;if(old){if(old.pixels?.byteLength)old.pixels.buffer.transfer(0);if(![...this.frames.values()].some(f=>f.value.grid===old.grid)){old.grid.buffer.transfer(0);old.canvas.width=1;old.canvas.height=1;}}this.frame=null;}
 // Read views are ephemeral; the LRU stores only transferred binary blocks.
 private record(m:MapManifest,slot:number):IndexRecord|null{if(slot===m.root)return decodeRecord(decodeBase64(m.record).buffer,0,m.count);const key=m.indexId+':'+Math.floor(slot/RECORDS_PER_BLOCK),raw=this.blocks.get(key);if(!raw)return null;this.blocks.delete(key);this.blocks.set(key,raw);return decodeRecord(raw,(slot%RECORDS_PER_BLOCK)*RECORD_BYTES,m.count);}
 private async load(m:MapManifest,blocks:number[]){this.touchedBlock='';const result=await this.fetchBlocks(m.indexId,blocks);for(const {block,data}of result){const key=m.indexId+':'+block,old=this.blocks.get(key);this.bytes-=old?.byteLength??0;if(old&&old!==data)old.transfer(0);this.blocks.delete(key);this.blocks.set(key,data);this.bytes+=data.byteLength;}this.trim();}
 // Reclaim old blocks immediately; raster accounting reserves transient bitmap and graphics overhead.
 private trim(){while(this.bytes>this.blockLimit){const key=this.blocks.keys().next().value!;const raw=this.blocks.get(key)!;this.bytes-=raw.byteLength;raw.transfer(0);this.blocks.delete(key);}}
 // Geometry reads numeric weights only; exact decimal metadata is decoded on interaction.
 private geometryRecord(m:MapManifest,slot:number,o:RenderOptions):RenderRecord|null{
  if(slot===m.root){const r=this.record(m,slot)!;return {...r,weight:Number(visibleBytes(r,o.recycle,o.system))};}
  const key=m.indexId+':'+Math.floor(slot/RECORDS_PER_BLOCK),raw=this.blocks.get(key);if(!raw)return null;
  if(this.touchedBlock!==key){this.blocks.delete(key);this.blocks.set(key,raw);this.touchedBlock=key;}let v=this.views.get(raw);if(!v){v=new DataView(raw);this.views.set(raw,v);}const at=(slot%RECORDS_PER_BLOCK)*RECORD_BYTES;
  return {source:v.getUint32(at+4,true)===0?v.getUint32(at,true):v.getBigUint64(at,true),parent:readAddress(v,at+8,m.count),left:readAddress(v,at+16,m.count),right:readAddress(v,at+24,m.count),kind:v.getUint8(at+32),flags:v.getUint8(at+33),depth:v.getUint16(at+34,true),maxDepth:v.getUint16(at+36,true),weight:readWeight(v,at+56)+(o.system?readWeight(v,at+72):0)+(o.recycle?readWeight(v,at+88):0)+(o.recycle&&o.system?readWeight(v,at+104):0)};
 }
 // Record lookup for a single interaction may reload an evicted block without rerendering the map.
 private async get(m:MapManifest,slot:number){let r=this.record(m,slot);if(!r){await this.load(m,[Math.floor(slot/RECORDS_PER_BLOCK)]);r=this.record(m,slot);}if(!r)throw Error('MAP_INDEX_UNAVAILABLE');return r;}
 private identity(m:MapManifest,slot:number,r:{source:bigint|number}){return this.virtuals.get(m)?.get(slot)?.id??r.source.toString();}
 private node(m:MapManifest,slot:number,r:IndexRecord,parent:string,aggregated=false,recycle=false,system=false):StorageNode{
  const v=this.virtuals.get(m)?.get(slot);const id=this.identity(m,slot,r);return {id,name:v?.name??'',kind:r.kind===1?'file':v?.kind==='volume'?'volume':v?.kind==='share'?'share':'folder',parentId:parent,colorParentId:parent,bytesExact:visibleBytes(r,recycle,system).toString(),normalBytes:(r.bytes[0]+r.bytes[1]).toString(),recycleBytes:(r.bytes[2]+r.bytes[3]).toString(),hiddenBytes:(r.bytes.reduce((a,b)=>a+b,0n)-visibleBytes(r,recycle,system)).toString(),fileCount:r.files.toString(),muted:r.flags!==0,aggregated,...(aggregated?{detailTarget:id}:{})};
 }
 // Layout uses exact precomputed weights; additional blocks refine pixels without moving parent areas.
 async render(indexId:string,root:string,o:RenderOptions,emit:(value:Geometry)=>void):Promise<Geometry>{
  const m=this.manifests.get(indexId+':'+root);if(!m)throw Error('MAP_NOT_READY');this.cancel();const epoch=this.epoch;
  // Warm navigation reuses a bounded finished raster and its hit map without a second tree walk.
  let visualRoot=m.root;
  // A share and its sole physical root draw identical treemap contents; share their finished raster.
  if(o.mode==='treemap'){const n=this.record(m,m.root)!;if(n.kind===4&&n.left&&!n.right)visualRoot=n.left;}
  const frameKey=JSON.stringify([indexId,o.mode==='treemap'?visualRoot:root,o]),cached=this.frames.get(frameKey);
  if(cached){this.frames.delete(frameKey);this.frames.set(frameKey,cached);const f={...cached.value,epoch,m,options:o};this.frame=f;const bitmap=await createImageBitmap(f.canvas);if(epoch!==this.epoch){bitmap.close();throw new DOMException('Superseded','AbortError');}return {bitmap,frameId:epoch,complete:true,hits:[...f.labels.values()],groups:[],cx:f.cx,cy:f.cy,radius:f.radius,inner:f.inner,cacheBytes:this.bytes,processed:0};}
  // Resolve the one physical share root without speculative transfers around the global virtual root.
  if(visualRoot!==m.root){await this.get(m,visualRoot);if(epoch!==this.epoch)throw new DOMException('Superseded','AbortError');}
  const scale=Math.min(Math.max(1,o.dpr),2,Math.sqrt(8388608/Math.max(1,o.box.width*o.box.height))),width=Math.max(1,Math.round(o.box.width*scale)),height=Math.max(1,Math.round(o.box.height*scale));
  this.blockLimit=Math.min(128*1024*1024,192*1024*1024-width*height*16);
  const canvas=new OffscreenCanvas(width,height),ctx=canvas.getContext('2d');if(!ctx)throw Error('MAP_RENDERER_UNAVAILABLE');ctx.scale(scale,scale);ctx.fillStyle='#111827';ctx.fillRect(0,0,o.box.width,o.box.height);
  const pixels=o.mode==='treemap'?new Uint32Array(width*height):null;if(pixels)pixels.fill(0xff271811);const imageData=pixels?new ImageData(new Uint8ClampedArray(pixels.buffer),width,height):null;
  const colors=new Map<string,number>(),pixelColors=new Map<string,Uint32Array>();
  const grid=new Uint32Array(width*height),labels=new Map<number,Hit>(),radius=Math.max(1,Math.min(o.box.width,o.box.height)/2-24),inner=radius*.23,cx=o.box.width/2,cy=o.box.height/2;
  this.frame={epoch,m,options:o,canvas,grid,pixels,scale,labels,radius,inner,cx,cy};const rr=this.record(m,m.root)!;const step=(radius-inner)/Math.max(1,rr.maxDepth-rr.depth);
  const zoom=o.zoom??{scale:1,x:0,y:0};const pending=new MapTaskQueue(rr.maxDepth-rr.depth<=2);pending.push({slot:m.root,x0:o.mode==='treemap'?zoom.x:0,y0:o.mode==='treemap'?zoom.y:0,x1:o.mode==='treemap'?zoom.x+o.box.width*zoom.scale:Math.PI*2,y1:o.mode==='treemap'?zoom.y+o.box.height*zoom.scale:0,depth:0,owner:m.rootId,parent:''});
  let lastFrame=0,processed=0;const check=()=>{if(epoch!==this.epoch)throw new DOMException('Superseded','AbortError');};
  const frame=async(complete:boolean)=>{check();
   // Pending geometry may outlive its LRU block; resolve bounded label metadata only after layout.
   if(complete){
    // Batch final labels so evicted metadata cannot create one network round trip per label.
    const entries=[...labels],batch=Math.max(1,Math.min(128,Math.floor(this.blockLimit/(262144*2))));
    for(let at=0;at<entries.length;at+=batch){const group=entries.slice(at,at+batch);let missing:number[];
     while((missing=[...new Set(group.filter(([slot])=>!this.record(m,slot)).map(([slot])=>Math.floor(slot/RECORDS_PER_BLOCK)))]).length){await this.load(m,missing);check();}
     for(const [slot,hit]of group){const exact=this.record(m,slot)!;hit.node={...this.node(m,slot,exact,hit.node.parentId??'',!!hit.node.aggregated,o.recycle,o.system),id:hit.node.id,...(hit.node.detailTarget?{detailTarget:hit.node.detailTarget}:{})};}
    }
   }

   // Dense tiles are one contiguous raster upload, not millions of Canvas draw commands.
   if(imageData){ctx.putImageData(imageData,0,0);for(const h of labels.values()){const gradient=ctx.createLinearGradient(h.x0,h.y0,h.x1,h.y1);gradient.addColorStop(0,h.surface.light);gradient.addColorStop(.25,h.surface.fill);gradient.addColorStop(1,h.surface.shade);ctx.fillStyle=gradient;ctx.fillRect(h.x0+.5,h.y0+.5,Math.max(0,h.x1-h.x0-1),Math.max(0,h.y1-h.y0-1));}}
   const bitmap=await createImageBitmap(canvas);if(epoch!==this.epoch){bitmap.close();check();}return {bitmap,frameId:epoch,complete,hits:[...labels.values()],groups:[],cx,cy,radius,inner,cacheBytes:this.bytes} as Geometry;};
  const paint=(task:Task,r:RenderRecord,placeholder:boolean)=>{
   // Pixel-sized compounds need only color and ownership; avoid allocating file metadata per pixel.
   if(o.mode==='treemap'&&r.kind===0){
    const a=Math.max(0,Math.round(task.x0*scale)),b=Math.max(0,Math.round(task.y0*scale)),c=Math.min(width,Math.round(task.x1*scale)),d=Math.min(height,Math.round(task.y1*scale));
    if(c-a<=1&&d-b<=1){
     if(c>a&&d>b){const key=task.parent+':'+r.flags;let palette=pixelColors.get(key);if(!palette){palette=new Uint32Array(64);pixelColors.set(key,palette);if(pixelColors.size>128)pixelColors.delete(pixelColors.keys().next().value!);}const n=task.slot%64;if(!palette[n]){const color=tileSurface({id:'pixel-'+n,name:'',kind:'file',colorParentId:task.parent,muted:r.flags!==0});const rgb=parseInt(color.fill.slice(1),16);palette[n]=(0xff000000|((rgb&255)<<16)|(rgb&65280)|(rgb>>>16))>>>0;}const at=b*width+a;grid[at]=task.slot;pixels![at]=palette[n];}return;
    }
   }
   const v=this.virtuals.get(m)?.get(task.slot);const node:StorageNode={id:v?.id??r.source.toString(),name:v?.name??'',kind:r.kind===1?'file':'folder',colorParentId:task.parent,parentId:task.parent,muted:r.flags!==0,aggregated:placeholder||r.kind===0};if(r.kind===0){node.id=task.owner;node.detailTarget=task.owner;node.kind='folder';}
   const color=o.mode==='radial'&&!node.muted?radialSurface(task.x0,task.x1,task.depth,Math.max(1,rr.maxDepth-rr.depth)):tileSurface(r.kind===0?{...node,id:'pixel-'+(task.slot%64),kind:'file'}:node);
   let {x0,y0,x1,y1}=task;if(o.mode==='radial'){y0=inner+Math.max(0,task.depth-1)*step;y1=placeholder?radius:inner+task.depth*step;if(task.depth===0)return;}
   if(o.mode==='treemap'){
    const a=Math.max(0,Math.round(x0*scale)),b=Math.max(0,Math.round(y0*scale)),c=Math.min(width,Math.round(x1*scale)),d=Math.min(height,Math.round(y1*scale));
    if(c<=a||d<=b)return;
    let rgba=colors.get(color.fill);if(rgba===undefined){const rgb=parseInt(color.fill.slice(1),16);rgba=(0xff000000|((rgb&255)<<16)|(rgb&65280)|(rgb>>>16))>>>0;colors.set(color.fill,rgba);if(colors.size>4096)colors.delete(colors.keys().next().value!);}
    for(let y=b;y<d;y++){grid.fill(task.slot,y*width+a,y*width+c);pixels!.fill(rgba,y*width+a,y*width+c);}
    if(c-a>110*scale&&d-b>52*scale){const hit:Hit={node,ancestors:new Set([node.id,task.parent]),surface:color,depth:task.depth,x0,y0,x1,y1};labels.set(task.slot,hit);if(labels.size>128)labels.delete(labels.keys().next().value!);}
   }else{
    // Treemap pixels bypass Canvas state entirely; only radial arcs need a fill style.
    ctx.fillStyle=color.fill;ctx.beginPath();ctx.arc(cx,cy,y1,x0-Math.PI/2,x1-Math.PI/2);ctx.arc(cx,cy,y0,x1-Math.PI/2,x0-Math.PI/2,true);ctx.closePath();ctx.fill();
    // Intersect each scanline with the annulus and the two angular half planes.
    // This avoids examining the whole enclosing circle for every small sector.
    const sectors=x1-x0>Math.PI?[[x0,x0+Math.PI],[x0+Math.PI,x1]]:[[x0,x1]];
    for(const [start,end] of sectors){
     const angles=[start,end];for(let q=0;q<=4;q++)if(q*Math.PI/2>start&&q*Math.PI/2<end)angles.push(q*Math.PI/2);
     const ys=angles.flatMap(a=>[-Math.cos(a)*y0,-Math.cos(a)*y1]);
     const ya=Math.max(0,Math.floor((cy+Math.min(...ys))*scale)),yb=Math.min(height,Math.ceil((cy+Math.max(...ys))*scale));
     const ax=Math.sin(start),ay=-Math.cos(start),bx=Math.sin(end),by=-Math.cos(end);
     for(let y=ya;y<yb;y++){
      const dy=(y+.5)/scale-cy;if(Math.abs(dy)>=y1)continue;
      const outer=Math.sqrt(y1*y1-dy*dy);let lo=-outer,hi=outer;
      // A*x+B >= 0 describes each side of a convex sector.
      for(const [A,B] of [[-ay,ax*dy],[by,-bx*dy]]){if(Math.abs(A)<1e-12){if(B<0){lo=1;hi=0;}}else if(A>0)lo=Math.max(lo,-B/A);else hi=Math.min(hi,-B/A);}
      if(hi<=lo)continue;const hole=Math.abs(dy)<y0?Math.sqrt(y0*y0-dy*dy):0;
      const spans=hole?[[lo,Math.min(hi,-hole)],[Math.max(lo,hole),hi]]:[[lo,hi]];
      for(const [left,right]of spans){const a=Math.max(0,Math.ceil((cx+left)*scale-.5)),b=Math.min(width,Math.ceil((cx+right)*scale-.5));if(b>a)grid.fill(task.slot,y*width+a,y*width+b);}
     }
    }
   }
  };
  while(pending.length){check();this.blockLimit=Math.max(8*1024*1024,Math.min(128*1024*1024,192*1024*1024-width*height*16-pending.bytes));this.trim();const waiting:Task[]=[],missing=new Set<number>();let slice=0;
   while(pending.length&&missing.size<256&&slice<32768){const task=pending.pop()!;slice++;processed++;
    if(o.mode==='treemap'&&(task.x1<=0||task.y1<=0||task.x0>=o.box.width||task.y0>=o.box.height||Math.round(task.x1*scale)<=Math.round(task.x0*scale)||Math.round(task.y1*scale)<=Math.round(task.y0*scale)))continue;
    const r=task.record??this.geometryRecord(m,task.slot,o);if(!r){waiting.push(task);missing.add(Math.floor(task.slot/RECORDS_PER_BLOCK));continue;}
    const weight=r.weight;if(weight===0)continue;
    const minimum=o.mode==='treemap'?Math.round(task.x1*scale)-Math.round(task.x0*scale)<=1&&Math.round(task.y1*scale)-Math.round(task.y0*scale)<=1:(task.x1-task.x0)*radius*scale<1;
    if(r.kind===1||!r.left||minimum){paint(task,r,r.kind!==1);continue;}
    const left=this.geometryRecord(m,r.left,o),right=r.right?this.geometryRecord(m,r.right,o):null;
    if(!left||r.right&&!right){paint(task,r,true);waiting.push({...task,record:r});if(!left)missing.add(Math.floor(r.left/RECORDS_PER_BLOCK));if(r.right&&!right)missing.add(Math.floor(r.right/RECORDS_PER_BLOCK));continue;}
    labels.delete(task.slot);
    const owner=r.kind===0?task.owner:this.identity(m,task.slot,r),parent=r.kind===0?task.parent:owner,depth=task.depth+(r.kind===0?0:1);
    if(o.mode==='radial'&&r.kind!==0)paint(task,r,false);
    if(!right){pending.push({...task,slot:r.left,record:left,owner,parent,depth});continue;}
    const lw=left.weight,rw=right.weight;if(Math.abs(lw+rw-weight)>Math.max(1,weight)*Number.EPSILON*4)throw Error('INVALID_MAP_TOTAL');const ratio=lw/weight;
    const horizontal=o.mode==='radial'||task.x1-task.x0>task.y1-task.y0;
    if(horizontal){const split=task.x0+(task.x1-task.x0)*ratio;if(lw)pending.push({slot:r.left,record:left,x0:task.x0,y0:task.y0,x1:split,y1:task.y1,owner,parent,depth});if(rw)pending.push({slot:r.right,record:right,x0:split,y0:task.y0,x1:task.x1,y1:task.y1,owner,parent,depth});}
    else{const split=task.y0+(task.y1-task.y0)*ratio;if(lw)pending.push({slot:r.left,record:left,x0:task.x0,y0:task.y0,x1:task.x1,y1:split,owner,parent,depth});if(rw)pending.push({slot:r.right,record:right,x0:task.x0,y0:split,x1:task.x1,y1:task.y1,owner,parent,depth});}
   }
   if(performance.now()-lastFrame>=100){emit(await frame(false));lastFrame=performance.now();}
   if(missing.size){
    // Packed subtrees place heavier branches first; forward prefetch follows their physical order.
    const wanted=[...missing];for(const block of wanted){for(let distance=1;distance<=128&&missing.size<256;distance++){for(const next of [block+distance])if(next>=0&&next<=Math.floor(m.count/RECORDS_PER_BLOCK)&&missing.size<256&&!this.blocks.has(m.indexId+':'+next))missing.add(next);}}
    const list=[...missing],batch=Math.max(1,Math.min(128,Math.floor(this.blockLimit/(262144*2))));await Promise.all([list.slice(0,batch),list.slice(batch,batch*2)].filter(v=>v.length).map(v=>this.load(m,v)));check();}
   // Complete blocked level entries before advancing the compact breadth-first frontier.
   pending.prepend(waiting);await new Promise<void>(resolve=>{const channel=new MessageChannel();channel.port1.onmessage=()=>{channel.port1.close();channel.port2.close();resolve();};channel.port2.postMessage(null);});
  }
  const result=await frame(true);const finished=this.frame!;const cost=width*height*8;
  if(cost<=16*1024*1024){const old=this.frames.get(frameKey);this.frameBytes-=old?.bytes??0;this.frames.delete(frameKey);this.frames.set(frameKey,{value:finished,bytes:cost});this.frameBytes+=cost;while(this.frameBytes>16*1024*1024){const key=this.frames.keys().next().value!;const retired=this.frames.get(key)!;this.frameBytes-=retired.bytes;retired.value.grid.buffer.transfer(0);retired.value.canvas.width=1;retired.value.canvas.height=1;this.frames.delete(key);}}
  // Pixel upload is complete; release transient backing storage without waiting for browser GC.
  pixels?.buffer.transfer(0);
  return {...result,processed};
 }
 // Interaction resolves one real record on demand; no per-file objects cross the worker boundary.
 async probe(frameId:number,x:number,y:number):Promise<Hit|null>{const f=this.frame;if(!f||f.epoch!==frameId)return null;const px=Math.floor(x*f.scale),py=Math.floor(y*f.scale);if(px<0||py<0||px>=f.canvas.width||py>=f.canvas.height)return null;let slot=f.grid[py*f.canvas.width+px];if(!slot)return null;const rendered=await this.get(f.m,slot);let r=rendered,aggregated=r.kind===0;if(aggregated){slot=r.parent;r=await this.get(f.m,slot);}const p=r.parent?await this.get(f.m,r.parent):null;const parent=p?this.identity(f.m,r.parent,p):'';if(this.frame!==f||f.epoch!==this.epoch)return null;const node=this.node(f.m,slot,r,parent,aggregated,f.options.recycle,f.options.system);if(aggregated)node.bytesExact=visibleBytes(rendered,f.options.recycle,f.options.system).toString();
  const known=f.labels.get(slot);if(known)return {...known,node};let a=px,b=px+1,c=py,d=py+1;const target=f.grid[py*f.canvas.width+px];while(a>0&&f.grid[py*f.canvas.width+a-1]===target)a--;while(b<f.canvas.width&&f.grid[py*f.canvas.width+b]===target)b++;while(c>0&&f.grid[(c-1)*f.canvas.width+px]===target)c--;while(d<f.canvas.height&&f.grid[d*f.canvas.width+px]===target)d++;
  if(this.frame!==f)return null;return {node,rasterBounds:true,ancestors:new Set([node.id,parent]),surface:tileSurface(node),depth:r.depth,x0:a/f.scale,x1:b/f.scale,y0:c/f.scale,y1:d/f.scale};
 }
}
