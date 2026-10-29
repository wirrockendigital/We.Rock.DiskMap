import {hierarchy,partition,treemap} from 'd3-hierarchy';
import {radialSurface,tileSurface,type SurfaceColor} from './chart-color';
import {sizeOf,type StorageNode} from './model';
export type Hit={rasterBounds?:boolean;node:StorageNode;ancestors:ReadonlySet<string>;surface:SurfaceColor;depth:number;ownBytes?:string;residual?:boolean;x0:number;x1:number;y0:number;y1:number};
export interface Geometry {resolveLabels?:()=>Promise<Hit[]>;bitmap?:ImageBitmap;frameId?:number;complete?:boolean;cacheBytes?:number;processed?:number;probe?:(x:number,y:number)=>Promise<Hit|null>;raster?:Uint8ClampedArray;rasterWidth?:number;rasterHeight?:number;cells?:Map<string,number[]>;hits:Hit[];groups:{node:StorageNode;x0:number;x1:number;y0:number;y1:number;depth:number}[];cx:number;cy:number;radius:number;inner:number}
export type GeometryLoader=(root:string,mode:'radial'|'treemap',box:{width:number;height:number},zoom?:{scale:number;x:number;y:number},onFrame?:(frame:Geometry)=>void,signal?:AbortSignal)=>Promise<Geometry>;
// D3 geometry is calculated outside the main thread; no DOM or filesystem access occurs here.
export function layout(root:StorageNode,mode:'radial'|'treemap',box:{width:number;height:number}):Geometry {
 const tree=hierarchy(root).sum(n=>{const own=BigInt(n.bytesExact??Math.max(0,Math.trunc(sizeOf(n))))-(n.children??[]).reduce((sum,c)=>sum+BigInt(c.bytesExact??Math.max(0,Math.trunc(sizeOf(c)))),0n);return Number(own>0n?own:0n);}).sort((a,b)=>(b.value??0)-(a.value??0)||a.data.id.localeCompare(b.data.id));

    const radius=Math.max(1,Math.min(box.width,box.height)/2-24),inner=radius*.23;
    const step=(radius-inner)/Math.max(1,tree.height),cx=box.width/2,cy=box.height/2;
    const parents=new Map<string,string>();
    tree.eachBefore(node=>{if(node.parent)parents.set(node.data.id,node.parent.data.id);});
    const emptyAncestors=new Set<string>();
    let hits:Hit[];
    let groups:{node:StorageNode;x0:number;x1:number;y0:number;y1:number;depth:number}[]=[];
    if(mode==='radial'){
      hits=partition<StorageNode>().size([Math.PI*2,1])(tree.copy()).descendants().filter(n=>n.depth>0&&(n.value??0)>0).map(n=>({
        node:n.data,ancestors:emptyAncestors,depth:n.depth,surface:n.data.muted?tileSurface(n.data):radialSurface(n.x0,n.x1,n.depth,tree.height),
        x0:n.x0,x1:n.x1,y0:inner+(n.depth-1)*step+.65,y1:inner+n.depth*step-.65,
      })).filter(h=>h.y1>h.y0&&h.x1>h.x0);
    }else{
      // Residual weights retain their real folder identity and never invent actionable files.
      type Tile={node:StorageNode;children?:Tile[];weight:number;ownBytes?:string;residual?:boolean};
      const tiles=new Map<string,Tile>();
      const exact=(node:StorageNode)=>BigInt(node.bytesExact??Math.max(0,Math.trunc(sizeOf(node))));
      tree.eachAfter(node=>{
        const children=(node.children??[]).map(child=>tiles.get(child.data.id)!);
        const remaining=exact(node.data)-(node.children??[]).reduce((sum,child)=>sum+exact(child.data),0n),own=remaining>0n?remaining:0n;
        if(children.length&&own>0n)children.push({node:node.data,weight:Number(own),ownBytes:own.toString(),residual:true});
        tiles.set(node.data.id,{node:node.data,children:children.length?children:undefined,weight:children.length?0:Number(own),ownBytes:children.length?undefined:own.toString()});
      });
      const layout=hierarchy(tiles.get(root.id)!).sum(n=>n.weight).sort((a,b)=>(b.value??0)-(a.value??0)||a.data.node.id.localeCompare(b.data.node.id));
      // Zero outer padding prevents nested folders from accumulating wide gutters.
      const placed=treemap<Tile>().size([box.width,box.height]).paddingInner(1).paddingOuter(0).round(true)(layout);
      groups=placed.descendants().filter(n=>n.depth>0&&n.depth<=3&&n.children&&n.x1-n.x0>65&&n.y1-n.y0>30).map(n=>({node:n.data.node,x0:n.x0,x1:n.x1,y0:n.y0,y1:n.y1,depth:n.depth}));
      hits=placed.leaves().filter(n=>(n.value??0)>0).map(n=>({node:n.data.node,ancestors:emptyAncestors,depth:n.depth,surface:tileSurface(n.data.node),ownBytes:n.data.ownBytes,residual:n.data.residual,x0:n.x0,x1:n.x1,y0:n.y0,y1:n.y1}));
    }
    // Return only resolvable geometry; never clone nested directory trees with every hit.
    hits=hits.filter(h=>mode==='radial'?(h.x1-h.x0)*h.y1>=.2:h.x1-h.x0>=.2&&h.y1-h.y0>=.2);
    for(const hit of hits){const ids=new Set<string>();let id:string|undefined=hit.node.id;while(id){ids.add(id);id=parents.get(id);}hit.ancestors=ids;hit.node={...hit.node,children:undefined};}
    groups=groups.map(g=>({...g,node:{...g.node,children:undefined}}));
    const cells=new Map<string,number[]>();
    if(mode==='treemap')for(let i=0;i<hits.length;i++){const h=hits[i];for(let x=Math.floor(h.x0/64);x<=Math.floor((h.x1-.001)/64);x++)for(let y=Math.floor(h.y0/64);y<=Math.floor((h.y1-.001)/64);y++){const key=x+':'+y;let bucket=cells.get(key);if(!bucket){bucket=[];cells.set(key,bucket);}bucket.push(i);}}
    return {hits,groups,cells,cx,cy,radius,inner};

}
