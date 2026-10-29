import {test} from 'node:test';
import {createHash} from 'node:crypto';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
// Compile only the real adapter and its public provenance validator into an isolated local fixture.
const root=fileURLToPath(new URL('../../',import.meta.url)),tmp=mkdtempSync(join(tmpdir(),'diskmap-map-adapter-'));let adaptHierarchy,groupDirectChildren,groupContinuation,colorForNode,palette,stateStyle,qualifyGeometry,hitTest,pointerPoint,keyboardTarget,dispatchTarget;
try{mkdirSync(join(tmp,'visualization/shared'),{recursive:true});writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const f of ['model','adapter','groups','colors','state-style','interaction'])cpSync(join(root,'web/src/visualization/shared',f+'.ts'),join(tmp,'visualization/shared',f+'.ts'));cpSync(join(root,'web/src/query-provenance.ts'),join(tmp,'query-provenance.ts'));execFileSync(join(root,'web/node_modules/.bin/tsc'),['--ignoreConfig','--strict','--skipLibCheck','--target','ES2022','--module','Node16','--moduleResolution','Node16','--outDir',join(tmp,'out'),join(tmp,'visualization/shared/adapter.ts'),join(tmp,'visualization/shared/groups.ts'),join(tmp,'visualization/shared/colors.ts'),join(tmp,'visualization/shared/state-style.ts'),join(tmp,'visualization/shared/interaction.ts')],{stdio:'pipe'});const require=createRequire(import.meta.url);({adaptHierarchy}=require(join(tmp,'out/visualization/shared/adapter.js')));({groupDirectChildren,groupContinuation}=require(join(tmp,'out/visualization/shared/groups.js')));;({colorForNode,palette}=require(join(tmp,'out/visualization/shared/colors.js')));({stateStyle}=require(join(tmp,'out/visualization/shared/state-style.js')));;({qualifyGeometry,hitTest,pointerPoint,keyboardTarget,dispatchTarget}=require(join(tmp,'out/visualization/shared/interaction.js')));}finally{rmSync(tmp,{recursive:true,force:true});}
const s=(bytes=0,files=0,dirs=0,unknown=0,special=0)=>({knownBytes:String(bytes),unknownFileCount:String(unknown),fileCount:String(files),directoryCount:String(dirs),specialCount:String(special)});
const provenance={version:1,authority:'native-inventory',sourceId:'share1',qualification:'qualified',state:'reachable'};
// Fixture keys represent server-qualified raw source branches, independent from their node IDs.
const branch=raw=>'cb1-'+createHash('sha256').update(raw).digest('hex');
const n=(id,parentId,kind,summary,extra={})=>({colorBranchKey:['directory','file','fifo'].includes(kind)?branch(kind==='directory'?id:parentId):null,id,parentId,kind,name:id,realNodeId:['nas','volume','share','other','group'].includes(kind)?null:id,virtual:['nas','volume','share','other','group'].includes(kind),summary,originalSummary:{...summary},matchSummary:{...summary},matchKind:'direct',hasMore:false,classification:null,...(['share','other'].includes(kind)?{provenance:{...provenance}}:{}),...extra});
const response=nodes=>({snapshotId:'snapshot',rootId:nodes[0].id,metric:'logical',mode:'highlight',state:'partial',selectionOrder:'recorded-rowid',nodes,truncated:false,filter:{categories:[]},matchState:'matches',originalSummary:{...nodes[0].originalSummary},matchSummary:{...nodes[0].matchSummary},denominator:{...nodes[0].summary},coverage:[{targetId:'source',state:'partial',errors:1}],sourceAvailability:{total:'1',truncated:false,records:[{name:'Locked',provenance:{...provenance,qualification:'unavailable',state:'locked'},reason:'source-unavailable'}]}});
const adapt=r=>adaptHierarchy(r,{snapshotId:r.snapshotId,rootId:r.rootId,metric:r.metric,mode:r.mode,maxNodes:10000});
function fixture(){return response([
 n('nas',null,'nas',s('9007199254740993',3,3,1,1),{hasMore:true}),
 n('vol1','nas','volume',s('9007199254740993',3,2,1,1),{hasMore:true,volumeId:'v1'}),
 n('vol2','nas','volume',s(0,0,1),{hasMore:true,volumeId:'v2'}),
 n('share','vol1','share',s('9007199254740993',2,1,1),{hasMore:true}),
 n('other','vol1','other',s(0,1,1,0,1),{hasMore:true}),
 n('dir','share','directory',s('9007199254740993',2,1,1),{hasMore:true}),
 n('large','dir','file',s('9007199254740993',1)),n('unknown','dir','file',s(0,1,0,1)),
 n('outside','other','directory',s(0,1,1,0,1),{hasMore:true}),n('zero','outside','file',s(0,1)),n('pipe','outside','fifo',s(0,0,0,0,1)),
 n('empty','vol2','directory',s(0,0,1))
]);}



const target=nodeId=>({kind:'model',nodeId});
// Adjacent half-open edges and nested depth use the same CSS geometry as drawing.
test('rectangle deepest hits, shared boundaries, zero areas and DPR coordinates',()=>{const m=adapt(fixture());const geometry=qualifyGeometry(m,[{shape:'rect',target:target('dir'),depth:0,x:0,y:0,width:100,height:100},{shape:'rect',target:target('large'),depth:1,x:0,y:0,width:50,height:100},{shape:'rect',target:target('unknown'),depth:1,x:50,y:0,width:50,height:100},{shape:'rect',target:target('zero'),depth:3,x:0,y:0,width:0,height:0}]);assert.equal(hitTest(geometry,{x:49.99,y:1}).nodeId,'large');assert.equal(hitTest(geometry,{x:50,y:1}).nodeId,'unknown');assert.equal(hitTest(geometry,{x:100,y:1}),null);assert.deepEqual(pointerPoint(150,75,{left:100,top:50,width:200,height:100},100,50),{x:25,y:12.5});assert.equal(pointerPoint(0,0,{left:0,top:0,width:0,height:0},0,0),null);});
test('arc angle and radius boundaries resolve without DPR or area fabrication',()=>{const m=adapt(fixture()),geometry=qualifyGeometry(m,[{shape:'arc',target:target('large'),depth:1,cx:0,cy:0,innerRadius:10,outerRadius:20,startAngle:0,endAngle:Math.PI},{shape:'arc',target:target('unknown'),depth:1,cx:0,cy:0,innerRadius:10,outerRadius:20,startAngle:Math.PI,endAngle:2*Math.PI}]);assert.equal(hitTest(geometry,{x:10,y:0}).nodeId,'large');assert.equal(hitTest(geometry,{x:-10,y:0}).nodeId,'unknown');assert.equal(hitTest(geometry,{x:20,y:0}),null);assert.equal(hitTest(geometry,{x:0,y:0}),null);});
test('local aggregate dispatch never invokes model-node navigation',()=>{const m=adapt(fixture()),t={kind:'display-group',groupId:'local-dir',parentId:'dir',memberIds:['large','unknown']};const g=qualifyGeometry(m,[{shape:'rect',target:t,depth:1,x:0,y:0,width:1,height:1}]);let opened;dispatchTarget(hitTest(g,{x:.5,y:.5}),()=>assert.fail('local ID reached backend callback'),group=>opened=group);assert.deepEqual(opened.memberIds,['large','unknown']);assert.ok(Object.isFrozen(g[0].target.memberIds));assert.throws(()=>qualifyGeometry(m,[{...g[0],target:{...t,memberIds:['large','zero']}}]));});
test('keyboard focus is controlled, zero nodes reachable, and no geometry mutation',()=>{const m=adapt(fixture());assert.equal(keyboardTarget(m,'dir','ArrowRight'),'large');assert.equal(keyboardTarget(m,'large','ArrowLeft'),'dir');assert.equal(keyboardTarget(m,'outside','ArrowDown'),'zero');assert.equal(keyboardTarget(m,null,'Home'),'nas');assert.equal(keyboardTarget(m,'nas','End'),'empty');assert.equal(keyboardTarget(m,'large','Enter'),'large');assert.equal(m.nodes.large.parentId,'dir');assert.equal(m.nodes.zero.weight,0n);});
