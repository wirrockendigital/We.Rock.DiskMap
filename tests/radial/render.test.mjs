import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import {radialFixture} from './fixture.mjs';
// Compile real canvas, presentation, painter and hierarchy adapter with installed React types.
const root=fileURLToPath(new URL('../../',import.meta.url)),tmp=mkdtempSync(join(root,'web/.radial-test-'));let layoutRadial,radialPresentation,paintRadial,adaptHierarchy;
try{mkdirSync(join(tmp,'visualization'),{recursive:true});mkdirSync(join(tmp,'features/settings'),{recursive:true});cpSync(join(root,'web/src/features/settings/display.tsx'),join(tmp,'features/settings/display.tsx'));writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const folder of ['shared','radial'])cpSync(join(root,'web/src/visualization',folder),join(tmp,'visualization',folder),{recursive:true});cpSync(join(root,'web/src/query-provenance.ts'),join(tmp,'query-provenance.ts'));cpSync(join(root,'web/src/contracts.ts'),join(tmp,'contracts.ts'));cpSync(join(root,'web/src/size-metrics.ts'),join(tmp,'size-metrics.ts'));for(const f of ['regional-format.tsx','size-metrics.ts'])cpSync(join(root,'web/src',f),join(tmp,f));execFileSync(join(root,'web/node_modules/.bin/tsc'),['--ignoreConfig','--strict','--skipLibCheck','--jsx','react-jsx','--target','ES2022','--module','Node16','--moduleResolution','Node16','--outDir',join(tmp,'out'),join(tmp,'visualization/radial/RadialCanvas.tsx'),join(tmp,'visualization/shared/adapter.ts')],{stdio:'pipe'});const require=createRequire(import.meta.url);({layoutRadial}=require(join(tmp,'out/visualization/radial/layout.js')));({radialPresentation}=require(join(tmp,'out/visualization/radial/presentation.js')));({paintRadial}=require(join(tmp,'out/visualization/radial/paint.js')));({adaptHierarchy}=require(join(tmp,'out/visualization/shared/adapter.js')));}finally{rmSync(tmp,{recursive:true,force:true});}
const model=()=>adaptHierarchy(radialFixture(),{snapshotId:'synthetic',rootId:'root',metric:'logical',mode:'highlight',maxNodes:2000});
const layout=m=>layoutRadial(m,{width:800,height:600});
// Record canvas operations rather than mirroring the implementation's angle calculation.
function context(){const calls=[];const ctx={calls,measureText:t=>({width:t.length*7})};for(const method of ['clip','beginPath','arc','lineTo','closePath','fill','stroke','setLineDash','save','translate','rotate','fillText','restore'])ctx[method]=(...args)=>calls.push([method,...args]);return ctx;}
test('tiny sibling grouping conserves exact bytes/counts without crossing parents',()=>{
 const m=model(),l=layout(m),p=radialPresentation(m,l);assert.equal(p.groups.length,1);const g=p.groups[0];assert.equal(g.parentId,'root');assert.equal(g.directChildren,20n);assert.equal(g.weight,20n);assert.equal(g.summary.fileCount,20n);assert.equal(g.canReveal,false);assert.ok(g.memberIds.every(id=>m.nodes[id].parentId==='root'));
 const old=l.geometry.filter(x=>x.target.kind==='model'&&g.memberIds.includes(x.target.nodeId)),merged=p.geometry.find(x=>x.target.kind==='display-group');assert.equal(merged.startAngle,old[0].startAngle);assert.equal(merged.endAngle,old.at(-1).endAngle);assert.equal(p.geometry.length,5);assert.equal(m.nodes.root.children.length,22);
});
test('light/dark and selection/focus repaint identical geometry with non-color cues',()=>{
 const m=model(),p=radialPresentation(m,layout(m)),before=JSON.stringify(p.geometry);for(const theme of ['light','dark']){const c=context();paintRadial(c,m,p,{colorMode:'folder',referenceTime:null,selectedNodeId:'large',focusedNodeId:'folder',theme,language:'de'});assert.ok(c.calls.some(x=>x[0]==='setLineDash'&&x[1].length===2));assert.ok(c.calls.some(x=>x[0]==='fillText'&&x[1]==='large'));assert.equal(JSON.stringify(p.geometry),before);}
});
test('unknown reference time stays neutral and overlong/tiny labels are not forced into arcs',()=>{
 const m=model(),p=radialPresentation(m,layout(m)),c=context();paintRadial(c,m,p,{colorMode:'mtime',referenceTime:null,selectedNodeId:null,focusedNodeId:null,theme:'light',language:'en'});assert.ok(!c.calls.some(x=>x[0]==='fillText'&&String(x[1]).startsWith('small-')));assert.ok(!c.calls.some(x=>x[0]==='fillText'&&String(x[1]).startsWith('Recorded root')));
});
// Measure the painter's actual fill/stroke pairs rather than assuming theme colors.
test('hierarchy boundaries contrast with every branch fill and retain clipped CSS width',()=>{
 const luminance=hex=>{const channels=hex.slice(1).match(/../g).map(x=>parseInt(x,16)/255).map(x=>x<=0.04045?x/12.92:((x+0.055)/1.055)**2.4);return channels.reduce((sum,x,i)=>sum+x*[0.2126,0.7152,0.0722][i],0);};
 const fills=new Set();
 for(const theme of ['light','dark'])for(const hierarchyBorders of ['normal','subtle'])for(let key=0;key<32;key++){
  const original=model(),m={...original,nodes:Object.fromEntries(Object.entries(original.nodes).map(([id,n])=>[id,{...n,colorBranchKey:`contrast-${key}`}]))},p=radialPresentation(m,layout(m));
  const before=JSON.stringify(p.geometry),c=context(),strokes=[];let clipped=false,fill=null;
  c.save=()=>{clipped=false;};c.clip=()=>{clipped=true;};c.fill=()=>{fill=c.fillStyle;};
  c.stroke=()=>{strokes.push({fill,border:c.strokeStyle,width:c.lineWidth,clipped});};
  paintRadial(c,m,p,{colorMode:'folder',referenceTime:null,selectedNodeId:null,focusedNodeId:null,theme,language:'en',display:{hierarchyBorders,labels:false}});
  assert.equal(JSON.stringify(p.geometry),before,'painting must not change geometry');
  assert.equal(strokes.length,p.geometry.length,'one hierarchy boundary per sector');
  for(const s of strokes){
   assert.equal(s.clipped,true,'stroke must remain inside its original sector');
   assert.equal(s.width/2,hierarchyBorders==='subtle'?1:1.5,'centered stroke loses its outer half to clipping');
   if(s.fill==='#f8fafc'||s.fill==='#111827')continue; // The center is not a categorical hierarchy band.
   fills.add(s.fill);const a=luminance(s.fill),b=luminance(s.border);
   assert.ok((Math.max(a,b)+0.05)/(Math.min(a,b)+0.05)>=3,`insufficient hierarchy contrast ${s.fill}/${s.border}`);
  }
 }
 assert.equal(fills.size,8,'exercise the complete categorical palette');
});
