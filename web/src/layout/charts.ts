import { hierarchy, treemap, partition } from 'd3-hierarchy';
import { type MapNode, totals } from './model';

// Normalize huge decimal strings without overflowing or rounding tiny visible weights to integers.
function ratio(value:string,total:string):number {
  if(value==='0'||total==='0')return 0;
  const a=value.slice(0,16),b=total.slice(0,16);
  return Number(a)/Number(b)*10**((value.length-a.length)-(total.length-b.length));
}

// Use one semantic model, normalizing only the geometry weights to finite doubles.
export function draw(root:MapNode,mode:'treemap'|'radial',canvas:HTMLCanvasElement,width:number,height:number,dpr:number) {
  if(!Number.isFinite(width)||!Number.isFinite(height)||!Number.isFinite(dpr)||width<=0||height<=0||dpr<=0||width>3840||height>2160)throw new Error('CANVAS_BUDGET');
  // Bound raster allocation to physical 4K while retaining responsive CSS dimensions.
  const effectiveDpr=Math.min(dpr,Math.sqrt((3840*2160)/(width*height)));
  const rasterWidth=Math.max(1,Math.floor(width*effectiveDpr));
  const rasterHeight=Math.max(1,Math.floor(height*effectiveDpr));
  const summary=totals(root);const cap=mode==='treemap'?10000:2000;
  if(summary.nodes>cap)throw new Error('RENDER_BUDGET');
  const start=performance.now();
  const tree=hierarchy(root).sum(n=>n.children?0:ratio(n.knownBytes,summary.knownBytes));
  for(const node of tree.descendants()) {
    if(!Number.isFinite(node.value)||node.value!<0)throw new Error('INVALID_GEOMETRY_WEIGHT');
    if(node.children && Math.abs(node.children.reduce((s,c)=>s+c.value!,0)-node.value!)>1e-12)throw new Error('GEOMETRY_SUM_MISMATCH');
  }
  // Release an oversized intermediate raster before changing its aspect ratio.
  if(canvas.width!==rasterWidth&&rasterWidth*canvas.height>3840*2160)canvas.height=0;
  // Preserve the backing store across map switches instead of reallocating it.
  if(canvas.width!==rasterWidth)canvas.width=rasterWidth;
  if(canvas.height!==rasterHeight)canvas.height=rasterHeight;
  canvas.style.width=`${width}px`;canvas.style.height=`${height}px`;
  const ctx=canvas.getContext('2d')!;
  ctx.setTransform(rasterWidth/width,0,0,rasterHeight/height,0,0);
  ctx.clearRect(0,0,width,height);
  if(mode==='treemap') {
    const laid=treemap<MapNode>().size([width,height]).paddingInner(0)(tree);
    for(const n of laid.descendants()){if(n.depth===0)continue;ctx.fillStyle=`hsl(${n.depth*47%360} 55% 50%)`;ctx.fillRect(n.x0,n.y0,n.x1-n.x0,n.y1-n.y0);}
  }else{
    const radius=Math.min(width,height)/2;const laid=partition<MapNode>().size([Math.PI*2,radius])(tree);
    for(const n of laid.descendants()){if(n.depth===0||n.x1<=n.x0)continue;ctx.beginPath();ctx.arc(width/2,height/2,n.y1,n.x0,n.x1);ctx.arc(width/2,height/2,n.y0,n.x1,n.x0,true);ctx.closePath();ctx.fillStyle=`hsl(${n.depth*47%360} 55% 50%)`;ctx.fill();}
  }
  return {...summary,effectiveDpr,rasterWidth,rasterHeight,layoutAndDrawMs:performance.now()-start,canvasBytes:canvas.width*canvas.height*4};
}
