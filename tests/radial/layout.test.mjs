import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
// Compile the production layout and adapter; fixture summaries are independent exact inputs.
const root=fileURLToPath(new URL('../../',import.meta.url)),tmp=mkdtempSync(join(tmpdir(),'diskmap-radial-'));let layoutRadial,adaptHierarchy,hitTest;
try{mkdirSync(join(tmp,'visualization'),{recursive:true});writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');cpSync(join(root,'web/src/visualization/shared'),join(tmp,'visualization/shared'),{recursive:true});cpSync(join(root,'web/src/visualization/radial'),join(tmp,'visualization/radial'),{recursive:true});cpSync(join(root,'web/src/query-provenance.ts'),join(tmp,'query-provenance.ts'));cpSync(join(root,'web/src/contracts.ts'),join(tmp,'contracts.ts'));cpSync(join(root,'web/src/size-metrics.ts'),join(tmp,'size-metrics.ts'));execFileSync(join(root,'web/node_modules/.bin/tsc'),['--ignoreConfig','--strict','--skipLibCheck','--target','ES2022','--module','Node16','--moduleResolution','Node16','--outDir',join(tmp,'out'),join(tmp,'visualization/radial/layout.ts'),join(tmp,'visualization/shared/adapter.ts')],{stdio:'pipe'});const require=createRequire(import.meta.url);({layoutRadial}=require(join(tmp,'out/visualization/radial/layout.js')));({adaptHierarchy}=require(join(tmp,'out/visualization/shared/adapter.js')));({hitTest}=require(join(tmp,'out/visualization/shared/interaction.js')));}finally{rmSync(tmp,{recursive:true,force:true});}
const summary=(bytes,files=0,dirs=0,unknown=0)=>({knownBytes:String(bytes),unknownFileCount:String(unknown),fileCount:String(files),directoryCount:String(dirs),specialCount:'0'});
const provenance={version:1,authority:'native-inventory',sourceId:'s',qualification:'qualified',state:'reachable'};
const node=(id,parentId,kind,s,extra={})=>({id,parentId,kind,name:id,virtual:['nas','volume','share'].includes(kind),realNodeId:['nas','volume','share'].includes(kind)?null:id,summary:s,originalSummary:s,matchSummary:s,matchKind:'direct',hasMore:false,classification:null,...(kind==='share'?{provenance}:{}),...extra});
function model(nodes){const r={snapshotId:'s',rootId:nodes[0].id,metric:'logical',mode:'highlight',state:'complete',selectionOrder:'recorded-rowid',nodes,truncated:false,filter:{categories:[]},matchState:'matches',originalSummary:nodes[0].summary,matchSummary:nodes[0].summary,denominator:nodes[0].summary,coverage:[],sourceAvailability:{total:'0',truncated:false,records:[]}};return adaptHierarchy(r,{snapshotId:'s',rootId:r.rootId,metric:'logical',mode:'highlight',maxNodes:10000});}
const options={width:800,height:600};
const arc=(l,id)=>l.geometry.find(g=>g.target.nodeId===id);
const close=(a,b)=>assert.ok(Math.abs(a-b)<1e-12,`${a} != ${b}`);
function tree(){return model([node('nas',null,'nas',summary(100,3,2)),node('v1','nas','volume',summary(75,2,1)),node('v2','nas','volume',summary(25,1,1)),node('share','v1','share',summary(75,2,1)),node('dir','share','directory',summary(75,2,1)),node('a','dir','file',summary(50,1)),node('b','dir','file',summary(25,1)),node('short','v2','directory',summary(25,1,1)),node('c','short','file',summary(25,1))]);}
test('NAS ring starts at volumes, children stay inside parents and short branches end',()=>{
 const m=tree(),before=JSON.stringify(m,(_,v)=>typeof v==='bigint'?String(v):v),l=layoutRadial(m,options);
 assert.deepEqual(l.geometry.filter(g=>g.depth===1).map(g=>g.target.nodeId),['v1','v2']);close(arc(l,'v1').endAngle,Math.PI*1.5);close(arc(l,'v2').endAngle,Math.PI*2);assert.equal(arc(l,'a').depth,4);assert.equal(arc(l,'c').depth,3);assert.equal(l.geometry.length,9);
 for(const g of l.geometry.slice(1)){const p=arc(l,m.nodes[g.target.nodeId].parentId);assert.ok(g.startAngle>=p.startAngle&&g.endAngle<=p.endAngle);close(g.innerRadius,p.outerRadius);}
 assert.equal(JSON.stringify(m,(_,v)=>typeof v==='bigint'?String(v):v),before);assert.deepEqual(layoutRadial(m,options),l);
});
test('drilled share has only its direct content in the first ring and preserves external parent',()=>{
 const m=model([node('share','outside','share',summary(6,2,0)),node('a','share','file',summary(4,1)),node('b','share','file',summary(2,1))]),l=layoutRadial(m,options);assert.equal(l.rootId,'share');assert.deepEqual(l.geometry.slice(1).map(g=>[g.target.nodeId,g.depth]),[['a',1],['b',1]]);close(arc(l,'a').endAngle,Math.PI*4/3);
});
test('huge exact weights and tiny zero unknown objects never receive invented positive angle',()=>{
 const huge=10n**100n,m=model([node('r',null,'directory',summary(huge+1n,4,1,1)),node('huge','r','file',summary(huge,1)),node('tiny','r','file',summary(1,1)),node('zero','r','file',summary(0,1)),node('unknown','r','file',summary(0,1,0,1))]),l=layoutRadial(m,options);
 assert.equal(m.nodes.r.weight,huge+1n);assert.deepEqual(l.listOnly.map(x=>[x.nodeId,x.reason]),[['tiny','precision'],['unknown','unknown'],['zero','zero']]);assert.equal(l.geometry.length,2);assert.equal(arc(l,'huge').endAngle,Math.PI*2);
});
test('zero and unknown root keep structural center without filling rings',()=>{
 for(const unknown of [0,1]){const l=layoutRadial(model([node('r',null,'directory',summary(0,1,1,unknown)),node('a','r','file',summary(0,1,0,unknown))]),options);assert.equal(l.geometry.length,1);assert.equal(l.listOnly[0].reason,unknown?'unknown':'zero');}
});
test('depth and segment budgets preserve every omitted ID and parent topology',()=>{
 const m=tree();for(const opts of [{maxDepth:1},{maxSegments:2}]){const l=layoutRadial(m,{...options,...opts});assert.ok(l.geometry.length<= (opts.maxSegments??2000));const ids=[...l.geometry.map(g=>g.target.nodeId),...l.listOnly.map(x=>x.nodeId)];assert.equal(new Set(ids).size,m.order.length);assert.deepEqual([...ids].sort(),[...m.order].sort());assert.ok(l.listOnly.every(x=>x.reason===(opts.maxDepth?'depth':'budget')));}
});
test('CSS arc hit tests use clockwise +x angles and deepest bounded ring',()=>{
 const l=layoutRadial(tree(),options),g=arc(l,'v2'),angle=(g.startAngle+g.endAngle)/2,r=(g.innerRadius+g.outerRadius)/2;assert.deepEqual(hitTest(l.geometry,{x:g.cx+Math.cos(angle)*r,y:g.cy+Math.sin(angle)*r}),{kind:'model',nodeId:'v2'});assert.deepEqual(hitTest(l.geometry,{x:400,y:300}),{kind:'model',nodeId:'nas'});
});
test('invalid viewport and budget settings fail closed',()=>{
 for(const change of [{width:0},{height:NaN},{width:Infinity},{maxDepth:0},{maxDepth:9},{maxSegments:0},{maxSegments:2001},{maxSegments:1.5}])assert.throws(()=>layoutRadial(tree(),{...options,...change}),/INVALID_RADIAL_LAYOUT/);
});
// The complete bounded model remains available when the radial segment budget is exhausted.
test('2000 segment limit includes center and exposes all 3001 omitted children',()=>{
 const nodes=[node('r',null,'directory',summary(5000,5000,1))];for(let i=0;i<5000;i++)nodes.push(node(`f${String(i).padStart(4,'0')}`,'r','file',summary(1,1)));
 const m=model(nodes),l=layoutRadial(m,options);assert.equal(l.geometry.length,2000);assert.equal(l.listOnly.length,3001);assert.ok(l.listOnly.every(x=>x.reason==='budget'));assert.equal(m.nodes.r.weight,5000n);close(arc(l,'f0000').endAngle,2*Math.PI/5000);
});
