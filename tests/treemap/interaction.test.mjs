import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
// Compile the production layout and adapter; fixture summaries are independent exact inputs.
const root=fileURLToPath(new URL('../../',import.meta.url)),tmp=mkdtempSync(join(root,'web/.treemap-test-'));let treemapPresentation,activateTreemap,adaptHierarchy;
try{mkdirSync(join(tmp,'visualization'),{recursive:true});writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');cpSync(join(root,'web/src/visualization/shared'),join(tmp,'visualization/shared'),{recursive:true});cpSync(join(root,'web/src/visualization/treemap'),join(tmp,'visualization/treemap'),{recursive:true});cpSync(join(root,'web/src/query-provenance.ts'),join(tmp,'query-provenance.ts'));cpSync(join(root,'web/src/contracts.ts'),join(tmp,'contracts.ts'));cpSync(join(root,'web/src/size-metrics.ts'),join(tmp,'size-metrics.ts'));execFileSync(join(root,'web/node_modules/.bin/tsc'),['--ignoreConfig','--strict','--skipLibCheck','--target','ES2022','--module','Node16','--moduleResolution','Node16','--outDir',join(tmp,'out'),join(tmp,'visualization/treemap/presentation.ts'),join(tmp,'visualization/treemap/actions.ts'),join(tmp,'visualization/shared/adapter.ts')],{stdio:'pipe'});const require=createRequire(import.meta.url);({treemapPresentation}=require(join(tmp,'out/visualization/treemap/presentation.js')));({activateTreemap}=require(join(tmp,'out/visualization/treemap/actions.js')));({adaptHierarchy}=require(join(tmp,'out/visualization/shared/adapter.js')));}finally{rmSync(tmp,{recursive:true,force:true});}
const summary=(bytes,files=0,dirs=0,unknown=0)=>({knownBytes:String(bytes),unknownFileCount:String(unknown),fileCount:String(files),directoryCount:String(dirs),specialCount:'0'});
const provenance={version:1,authority:'native-inventory',sourceId:'s',qualification:'qualified',state:'reachable'};
const node=(id,parentId,kind,s,extra={})=>({id,parentId,kind,name:id,virtual:['nas','volume','share'].includes(kind),realNodeId:['nas','volume','share'].includes(kind)?null:id,summary:s,originalSummary:s,matchSummary:s,matchKind:'direct',hasMore:false,classification:null,...(kind==='share'?{provenance}:{}),...extra});
function model(nodes){const r={snapshotId:'s',rootId:nodes[0].id,metric:'logical',mode:'highlight',state:'complete',selectionOrder:'recorded-rowid',nodes,truncated:false,filter:{categories:[]},matchState:'matches',originalSummary:nodes[0].summary,matchSummary:nodes[0].summary,denominator:nodes[0].summary,coverage:[],sourceAvailability:{total:'0',truncated:false,records:[]}};return adaptHierarchy(r,{snapshotId:'s',rootId:r.rootId,metric:'logical',mode:'highlight',maxNodes:10000});}

// Tiny sibling grouping conserves all exact counters while preserving the original physical tree.
test('tiny direct siblings form a qualified local group without mutating source',()=>{
 const m=model([node('r',null,'directory',summary(10002,3,1)),node('large','r','file',summary(10000,1)),node('a','r','file',summary(1,1)),node('b','r','file',summary(1,1))]);
 const p=treemapPresentation(m,{width:320,height:240,decoration:true});assert.equal(p.groups.length,1);const g=p.groups[0];assert.deepEqual(g.memberIds,['a','b']);assert.equal(g.summary.knownBytes,2n);assert.equal(g.summary.fileCount,2n);assert.equal(g.canReveal,false);assert.equal(m.order.length,4);assert.equal(m.nodes.r.children.length,3);assert.equal(p.model.nodes.r.weight,10002n);assert.ok(Object.isFrozen(p.model));assert.equal(p.layout.rectangles.find(r=>r.nodeId===g.id).target.kind,'display-group');assert.deepEqual(p,treemapPresentation(m,{width:320,height:240,decoration:true}));
});
// Selection and drill dispatch use only original model identities, never display IDs.
test('real files select, real directories drill, and both group kinds only inspect',()=>{
 const m=model([node('r',null,'directory',summary(12,3,1)),node('a','r','file',summary(2,1)),node('rest','r','group',summary(10,2),{virtual:true,realNodeId:null,matchKind:'group',hasMore:true,groupReason:'time-budget',groupedChildren:'2',listRootId:'r',afterCursor:'cursor'})]);
 const calls=[],callbacks={onSelect:id=>calls.push(['select',id]),onFocus:id=>calls.push(['focus',id]),onOpen:id=>calls.push(['open',id]),onGroup:t=>calls.push(['group',t])};
 activateTreemap(m,{kind:'model',nodeId:'a'},true,callbacks);assert.deepEqual(calls,[['focus','a'],['select','a']]);calls.length=0;
 activateTreemap(m,{kind:'model',nodeId:'r'},true,callbacks);assert.deepEqual(calls,[['focus','r'],['open','r']]);calls.length=0;
 for(const target of [{kind:'model',nodeId:'rest'},{kind:'display-group',groupId:'local',parentId:'r',memberIds:['a','b']}]){activateTreemap(m,target,true,callbacks);assert.deepEqual(calls,[['group',target]]);calls.length=0;}
 activateTreemap(m,{kind:'model',nodeId:'foreign'},false,callbacks);assert.deepEqual(calls,[]);
});
