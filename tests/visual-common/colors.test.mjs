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
const root=fileURLToPath(new URL('../../',import.meta.url)),tmp=mkdtempSync(join(tmpdir(),'diskmap-map-adapter-'));let adaptHierarchy,groupDirectChildren,groupContinuation,colorForNode,palette,stateStyle;
try{mkdirSync(join(tmp,'visualization/shared'),{recursive:true});writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const f of ['model','adapter','groups','colors','state-style'])cpSync(join(root,'web/src/visualization/shared',f+'.ts'),join(tmp,'visualization/shared',f+'.ts'));cpSync(join(root,'web/src/query-provenance.ts'),join(tmp,'query-provenance.ts'));execFileSync(join(root,'web/node_modules/.bin/tsc'),['--ignoreConfig','--strict','--skipLibCheck','--target','ES2022','--module','Node16','--moduleResolution','Node16','--outDir',join(tmp,'out'),join(tmp,'visualization/shared/adapter.ts'),join(tmp,'visualization/shared/groups.ts'),join(tmp,'visualization/shared/colors.ts'),join(tmp,'visualization/shared/state-style.ts')],{stdio:'pipe'});const require=createRequire(import.meta.url);({adaptHierarchy}=require(join(tmp,'out/visualization/shared/adapter.js')));({groupDirectChildren,groupContinuation}=require(join(tmp,'out/visualization/shared/groups.js')));;({colorForNode,palette}=require(join(tmp,'out/visualization/shared/colors.js')));({stateStyle}=require(join(tmp,'out/visualization/shared/state-style.js')));}finally{rmSync(tmp,{recursive:true,force:true});}
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


// Color transforms never mutate the authoritative geometry or depend on display order.
test('deterministic branch colors preserve hierarchy and all weights',()=>{const r=fixture(),m=adapt(r);const before=m.nodes.large.summary;const a=colorForNode(m,'large','folder','2026-10-10T00:00:00Z');assert.deepEqual(a,colorForNode(m,'large','folder','2026-10-10T00:00:00Z'));assert.equal(a.fill,colorForNode(m,'unknown','folder','2026-10-10T00:00:00Z').fill);for(const mode of ['folder','filetype','mtime'])colorForNode(m,'large',mode,'2026-10-10T00:00:00Z');assert.equal(m.nodes.large.summary,before);assert.equal(m.nodes.large.parentId,'dir');});
test('mixed folders and unclassified specials remain explicit',()=>{const r=fixture();r.nodes.find(n=>n.id==='dir').classification={folderComposition:'mixed',folderCategories:['image','document']};r.nodes.find(n=>n.id==='large').classification={category:'image'};const m=adapt(r);assert.equal(colorForNode(m,'dir','filetype','').pattern,'mixed');assert.equal(colorForNode(m,'large','filetype','').key,'image');assert.equal(colorForNode(m,'pipe','filetype','').key,'unknown');});
test('age buckets have exact boundaries and never imply last use',()=>{const ref='2026-10-10T00:00:00Z';for(const [days,key] of [[-1,'future'],[0,'recent'],[29,'recent'],[30,'year'],[364,'year'],[365,'older']]){const r=fixture();r.nodes.find(n=>n.id==='large').modifiedAt=new Date(Date.parse(ref)-days*86400000).toISOString();assert.equal(colorForNode(adapt(r),'large','mtime',ref).key,key);}assert.equal(colorForNode(adapt(fixture()),'large','mtime',ref).key,'unknown');assert.throws(()=>colorForNode(adapt(fixture()),'large','mtime','invalid'));});
test('all fills retain WCAG normal-text contrast against white',()=>{for(const hex of palette){const rgb=hex.slice(1).match(/../g).map(x=>parseInt(x,16)/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4);const luminance=.2126*rgb[0]+.7152*rgb[1]+.0722*rgb[2];assert.ok(1.05/(luminance+.05)>=4.5,hex);}});
test('selection focus and warning have concurrent noncolor signals',()=>{const state=stateStyle({selected:true,focused:true,warning:true});assert.equal(state.outline,'3px double currentColor');assert.equal(state.icon,'⚠');assert.equal(state.pattern,'diagonal-lines');assert.equal(state.selected,true);assert.equal(state.focused,true);});

// Drill changes visibility, not the recorded folder identity used as the color key.
test('folder color survives drill to the same recorded directory',()=>{const full=fixture(),m=adapt(full);const subset=response(full.nodes.filter(n=>['dir','large','unknown'].includes(n.id)));const drilled=adapt(subset);assert.equal(colorForNode(m,'large','folder','').fill,colorForNode(drilled,'large','folder','').fill);assert.equal(colorForNode(m,'dir','folder','').fill,colorForNode(drilled,'dir','folder','').fill);});

// Snapshot-qualified navigation IDs may change while the server-qualified visual key remains stable.
test('folder colors survive new snapshots without changing navigation or five counters',()=>{
 const first=fixture(),before=adapt(first),second=structuredClone(first);second.snapshotId='new-snapshot';
 for(const node of second.nodes){node.id='new-'+node.id;if(node.parentId!==null)node.parentId='new-'+node.parentId;if(node.realNodeId!==null)node.realNodeId='new-'+node.realNodeId;}
 second.rootId='new-nas';const after=adapt(second);
 assert.deepEqual(colorForNode(before,'large','folder',''),colorForNode(after,'new-large','folder',''));
 assert.deepEqual(before.nodes.large.summary,after.nodes['new-large'].summary);
 assert.equal(after.nodes['new-large'].parentId,'new-dir');assert.notEqual(after.nodes['new-large'].id,before.nodes.large.id);
 assert.notEqual(before.nodes.dir.colorBranchKey,before.nodes.outside.colorBranchKey);
});
// Absent qualification is explicit unknown, not a path/parent/snapshot-ID fallback.
test('missing and malformed color keys cannot fabricate branch authority',()=>{
 const value=fixture();delete value.nodes.find(n=>n.id==='large').colorBranchKey;
 assert.equal(colorForNode(adapt(value),'large','folder','').key,'unknown');
 assert.equal(colorForNode(adapt(value),'nas','folder','').pattern,'unknown');
 for(const key of ['raw/path','cb1-'+ 'a'.repeat(63),17]){const invalid=fixture();invalid.nodes[0].colorBranchKey=key;assert.throws(()=>adapt(invalid),/INVALID_MAP/);}
});
