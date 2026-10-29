import type {StorageNode} from './model';

// Stable source identities, never file extensions, separate neighboring storage areas.
const hues = [268, 198, 334, 156, 32, 224, 288, 178, 12, 82, 312, 48];
function hash(value:string){let n=2166136261;for(const c of value)n=Math.imul(n^c.codePointAt(0)!,16777619);return n>>>0;}
export interface SurfaceColor {fill:string;light:string;shade:string;text:string}
// Explicit sRGB colors also work on the DSM embedded browser's Canvas implementation.
function rgb(h:number,s:number,l:number){
  const a=s*Math.min(l,1-l);const f=(n:number)=>{const k=(n+h/30)%12;return Math.round(255*(l-a*Math.max(-1,Math.min(k-3,9-k,1))));};
  return [f(0),f(8),f(4)];
}
// Reuse the small numeric palette without retaining source names or node identities.
const palette=new Map<string,SurfaceColor>();
function surface(h:number,s:number,l:number):SurfaceColor{
 const key=h+':'+s+':'+l,cached=palette.get(key);if(cached)return cached;
  const channels=rgb((h+360)%360,s,l),hex=(c:number[])=>'#'+c.map(v=>v.toString(16).padStart(2,'0')).join('');
  const luminance=channels.map(c=>{const v=c/255;return v<=.04045?v/12.92:((v+.055)/1.055)**2.4;}).reduce((sum,v,i)=>sum+v*[.2126,.7152,.0722][i],0);
  const result={fill:hex(channels),light:hex(rgb((h+360)%360,s*.88,Math.min(.82,l+.12))),shade:hex(rgb((h+360)%360,s,Math.max(.18,l-.14))),text:luminance>.179?'#101827':'#ffffff'};palette.set(key,result);if(palette.size>8192)palette.delete(palette.keys().next().value!);return result;
}
export function tileSurface(node:StorageNode):SurfaceColor{
  if(node.muted)return surface(0,0,.5+(hash(node.id)%20)/100);
  const identity=hash(node.id),family=hash(node.colorParentId??node.id);
  // Sibling files share a broad hue family, with enough variation to remain individually visible.
  const hue=hues[(node.kind==='file'?family:identity)%hues.length]+(node.kind==='file'?(identity%13)*5-30:0);
  // A finite palette prevents dense real-file maps from repeatedly recomputing sRGB gradients.
  return surface(hue,.66+(identity%4)*.03,.47+(identity%7)*.02);
}
export function storageColor(node:StorageNode){return tileSurface(node).fill;}
// Angular hue keeps radial branches related even when NAS has only one volume child.
export function radialSurface(start:number,end:number,depth:number,height:number):SurfaceColor{
  const hue=(280+(start+end)/2/(Math.PI*2)*360)%360;
  return surface(hue,.76,.59+Math.min(1,(depth-1)/Math.max(1,height-1))*.17);
}
