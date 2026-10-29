import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
// Compile only the real adapter and its public provenance validator into an isolated local fixture.
const root=fileURLToPath(new URL('../../',import.meta.url)),tmp=mkdtempSync(join(tmpdir(),'diskmap-map-adapter-'));let adaptHierarchy,groupDirectChildren,groupContinuation;
try{mkdirSync(join(tmp,'visualization/shared'),{recursive:true});writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const f of ['model','adapter','groups'])cpSync(join(root,'web/src/visualization/shared',f+'.ts'),join(tmp,'visualization/shared',f+'.ts'));cpSync(join(root,'web/src/query-provenance.ts'),join(tmp,'query-provenance.ts'));execFileSync(join(root,'web/node_modules/.bin/tsc'),['--ignoreConfig','--strict','--skipLibCheck','--target','ES2022','--module','Node16','--moduleResolution','Node16','--outDir',join(tmp,'out'),join(tmp,'visualization/shared/adapter.ts'),join(tmp,'visualization/shared/groups.ts')],{stdio:'pipe'});const require=createRequire(import.meta.url);({adaptHierarchy}=require(join(tmp,'out/visualization/shared/adapter.js')));({groupDirectChildren,groupContinuation}=require(join(tmp,'out/visualization/shared/groups.js')));}finally{rmSync(tmp,{recursive:true,force:true});}
const s=(bytes=0,files=0,dirs=0,unknown=0,special=0)=>({knownBytes:String(bytes),unknownFileCount:String(unknown),fileCount:String(files),directoryCount:String(dirs),specialCount:String(special)});
const provenance={version:1,authority:'native-inventory',sourceId:'share1',qualification:'qualified',state:'reachable'};
const n=(id,parentId,kind,summary,extra={})=>({id,parentId,kind,name:id,realNodeId:['nas','volume','share','other','group'].includes(kind)?null:id,virtual:['nas','volume','share','other','group'].includes(kind),summary,originalSummary:{...summary},matchSummary:{...summary},matchKind:'direct',hasMore:false,classification:null,...(['share','other'].includes(kind)?{provenance:{...provenance}}:{}),...extra});
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

test('multi-volume qualified share/other topology conserves five counters without folder double counting',()=>{
 const r=fixture(),m=adapt(r);assert.equal(m.denominator.knownBytes,9007199254740993n);assert.equal(m.nodes.dir.summary.knownBytes,m.nodes.large.weight);assert.equal(m.nodes.nas.summary.directoryCount,3n);assert.equal(m.nodes.other.provenance.sourceId,'share1');assert.equal(m.nodes.other.canReveal,false);assert.equal(m.nodes.pipe.canReveal,false);assert.equal(m.nodes.unknown.unknown,true);assert.equal(m.nodes.unknown.weight,0n);assert.equal(m.nodes.zero.unknown,false);assert.equal(m.nodes.zero.weight,0n);assert.equal(m.nodes.empty.weight,0n);assert.deepEqual(m.nodes.vol1.children,['share','other']);assert.equal(m.coverage[0].errors,1);assert.equal(m.sourceAvailability.records[0].provenance.state,'locked');
 assert.ok(Object.isFrozen(m.nodes.dir.children));assert.ok(Object.isFrozen(m.coverage[0]));assert.throws(()=>m.nodes.dir.summary.knownBytes=1n);r.nodes[3].name='renamed today';r.nodes[3].provenance.sourceId='moved';assert.equal(m.nodes.share.name,'share');assert.equal(m.nodes.share.provenance.sourceId,'share1');
});

test('every parent counter detects a forged subtree sum',()=>{for(const field of ['knownBytes','unknownFileCount','fileCount','directoryCount','specialCount']){const r=fixture();r.nodes[0].summary[field]=String(BigInt(r.nodes[0].summary[field])+1n);r.nodes[0].originalSummary={...r.nodes[0].summary};assert.throws(()=>adapt(r));}});

test('selected subtree keeps external parent context and only subtracts its real directory self',()=>{const r=response([n('root','external','directory',s(9,1,1),{hasMore:true}),n('file','root','file',s(9,1))]);assert.equal(adapt(r).nodes.root.parentId,'external');});

test('server budget groups preserve exact remainder and opaque cursors without reveal',()=>{for(const reason of ['element-budget','depth-limit','byte-budget','time-budget']){const r=response([n('root',null,'directory',s(12,3,1),{hasMore:true}),n('one','root','file',s(2,1)),n('rest','root','group',s(10,2),{matchKind:'group',hasMore:true,groupReason:reason,groupedChildren:'2',listRootId:'root',afterCursor:'opaque-negative-row-cursor'})]);r.truncated=true;const m=adapt(r);assert.equal(m.nodes.rest.canReveal,false);assert.deepEqual(groupContinuation(m,'rest'),{rootId:'root',cursor:'opaque-negative-row-cursor',reason,directChildren:2n});}});

test('unexpanded parents keep authoritative weight without invented children',()=>{const r=response([n('root',null,'directory',s(20,4,2,1),{hasMore:true})]);r.truncated=true;const m=adapt(r);assert.equal(m.nodes.root.opaque,true);assert.equal(m.nodes.root.weight,20n);assert.deepEqual(m.nodes.root.children,[]);});

test('local grouping is only direct siblings and never changes source parent relations',()=>{const m=adapt(fixture()),g=groupDirectChildren(m,'dir',['unknown','large']);assert.deepEqual(g.memberIds,['large','unknown']);assert.equal(g.summary.knownBytes,9007199254740993n);assert.equal(g.summary.unknownFileCount,1n);assert.equal(g.canReveal,false);assert.equal(m.nodes.large.parentId,'dir');assert.throws(()=>groupDirectChildren(m,'dir',['large','zero']));assert.throws(()=>groupDirectChildren(m,'dir',['large','large']));});

test('hit-only pruned originals remain distinct from matching geometry and denominator',()=>{const root=n('root',null,'directory',s(3,1,1),{originalSummary:s(20,4,1),matchSummary:s(3,1,1),hasMore:true});const r=response([root,n('file','root','file',s(3,1))]);r.mode='hits-only';const m=adapt(r);assert.equal(m.original.knownBytes,20n);assert.equal(m.match.knownBytes,3n);assert.equal(m.denominator.knownBytes,3n);});

test('no-match differs from truly empty without positive filler weights',()=>{for(const state of ['empty','no-match']){const r=response([n('root',null,'directory',s(),{originalSummary:s(0,0,1),matchSummary:s(),matchKind:'none'})]);r.mode='hits-only';r.matchState=state;const m=adapt(r);assert.equal(m.matchState,state);assert.equal(m.nodes.root.weight,0n);}});

test('mixed identities, cycles, invalid groups, and unqualified source wrappers fail closed',()=>{
 for(const mutate of [r=>r.nodes.push({...r.nodes[1]}),r=>r.nodes[1].parentId='absent',r=>r.nodes[1].parentId='share',r=>r.nodes[3].provenance=undefined,r=>r.nodes[3].realNodeId='dir',r=>r.denominator.knownBytes='0',r=>r.nodes[3].summary.knownBytes='01',r=>r.sourceAvailability.records[0].provenance.path='/private']){const r=fixture();mutate(r);assert.throws(()=>adapt(r));}
 const r=fixture();assert.throws(()=>adaptHierarchy(r,{snapshotId:'foreign',rootId:'nas',metric:'logical',mode:'highlight',maxNodes:10000}));assert.throws(()=>adaptHierarchy(r,{snapshotId:'snapshot',rootId:'nas',metric:'logical',mode:'highlight',maxNodes:2}));
});

test('deep bounded nodes use iterative traversal and oversized wire trees are rejected',()=>{const nodes=[];for(let i=0;i<5000;i++)nodes.push(n('n'+i,i?'n'+(i-1):null,'directory',s(0,0,5000-i),{hasMore:i<4999}));const m=adapt(response(nodes));assert.equal(m.order.length,5000);assert.equal(m.nodes.n4999.summary.directoryCount,1n);for(let i=5000;i<10000;i++)nodes.push(n('n'+i,'n'+(i-1),'directory',s()));assert.throws(()=>adapt(response(nodes)));});

// Production responses clear hasMore after all direct children have been emitted.
test('complete emitted parents keep their children when hasMore is false',()=>{const r=fixture();for(const node of r.nodes)node.hasMore=false;const m=adapt(r);assert.deepEqual(m.nodes.nas.children,['vol1','vol2']);assert.deepEqual(m.nodes.dir.children,['large','unknown']);assert.equal(m.nodes.dir.opaque,false);assert.equal(m.nodes.nas.hasMore,false);});
