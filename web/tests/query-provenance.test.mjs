import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import { fixture } from './node-details-fixture.mjs';
// Compile the real validators together so the same provenance gate protects all public surfaces.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/provenance-'));let p,nav,details,query,messages,notice;
try { writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');cpSync(join(web,'src'),join(tmp,'src'),{recursive:true});execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--resolveJsonModule','--skipLibCheck','--outDir',join(tmp,'out'),...['query-provenance.ts','view-state-api.ts','node-details-client.ts','analysis-query.ts','SourceAvailabilityNotice.tsx'].map(f=>join(tmp,'src',f))],{stdio:'pipe'});const require=createRequire(import.meta.url);p=require(join(tmp,'out/query-provenance.js'));nav=require(join(tmp,'out/view-state-api.js'));details=require(join(tmp,'out/node-details-client.js'));query=require(join(tmp,'out/analysis-query.js'));messages=require(join(tmp,'out/details-messages.js')).detailsMessages;notice=require(join(tmp,'out/SourceAvailabilityNotice.js')).SourceAvailabilityNotice;}finally{rmSync(tmp,{recursive:true,force:true});}
const provenance=()=>({version:1,authority:'native-inventory',sourceId:'recorded-share',qualification:'qualified',state:'reachable'});
test('provenance accepts only bounded public identity and consistent captured qualification',()=>{
 assert.equal(p.validQueryProvenance(provenance()),true);
 assert.equal(p.validQueryProvenance({...provenance(),sourceId:null,qualification:'unavailable',state:'locked'}),true);
 for(const authority of ['explicit-roots','synthetic-fixture'])assert.equal(p.validQueryProvenance({...provenance(),authority,qualification:'explicit'}),true);
 for(const delta of [{version:2},{sourceId:null},{sourceId:'../path'},{state:'complete'},{state:'locked'},{qualification:'explicit'},{authority:'synthetic-fixture'},{binding:{}},{path:'/private'}])assert.equal(p.validQueryProvenance({...provenance(),...delta}),false);
});
test('historical share and other breadcrumbs keep server labels without real-path authority',()=>{
 for(const kind of ['share','other']){const value={snapshotId:'s',rootId:'area',parentId:'volume',selectedNodeId:null,reasons:[],breadcrumb:[{id:'nas',kind:'nas',name:'NAS'},{id:'volume',kind:'volume',name:'Recorded volume'},{id:'area',kind,name:'Recorded <area>',provenance:provenance()}]};assert.equal(nav.checkedNavigation(value,'s','area'),value);delete value.breadcrumb[2].provenance;assert.throws(()=>nav.checkedNavigation(value,'s','area'));}
});
test('historical virtual detail accepts exact containers and rejects invented real metadata or paths',()=>{
 const selection={profileId:'p',viewId:'v',snapshotId:'s',rootId:'root',nodeId:'n',metric:'logical',epoch:1};
 for(const kind of ['share','other']){const v=fixture();Object.assign(v,{paths:null,pathProvenance:'not-applicable',fileStationReason:'VIRTUAL_NODE'});Object.assign(v.detail,{kind,virtual:true,node:null,classification:null,provenance:provenance()});v.detail.aggregate.direct=null;v.detail.aggregate.directSemantics='virtual-children-not-file-content';assert.equal(details.checkedHistoricalDetail(v,selection,'details'),v);for(const mutate of [x=>x.detail.node=fixture().detail.node,x=>x.paths=fixture().paths,x=>x.detail.virtual=false,x=>x.detail.classification=fixture().detail.classification,x=>delete x.detail.provenance]){const bad=structuredClone(v);mutate(bad);assert.throws(()=>details.checkedHistoricalDetail(bad,selection,'details'));}}
});
// Real transport decoding must preserve the container contract rather than trusting TypeScript casts.
test('hierarchy validates new containers and preserves existing real-node identities',async()=>{
 globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};
 const view={profileId:'p',snapshotId:'s',rootId:'root',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'}};
 const pin={profileId:'p',snapshotId:'s',viewId:'v'};
 const summary={knownBytes:'9007199254740993',unknownFileCount:'1',fileCount:'2',directoryCount:'1',specialCount:'0'};
 const node={id:'area',parentId:'root',realNodeId:null,name:'Captured area',kind:'share',virtual:true,classification:null,provenance:provenance(),summary,hasMore:true};
 const read=async n=>query.readAnalysis(view,pin,new AbortController().signal,async()=>new Response(JSON.stringify({contractVersion:1,data:{snapshotId:'s',rootId:'root',metric:'logical',mode:'highlight',depth:1,elementBudget:200,filter:{categories:[]},nodes:[n],originalSummary:summary,matchSummary:summary,denominator:summary,truncated:false,state:'complete'}})));
 assert.deepEqual((await read(node)).nodes[0],node);
 for(const delta of [{realNodeId:'area'},{virtual:false},{classification:{}},{provenance:undefined},{provenance:{...provenance(),binding:{}}}])await assert.rejects(read({...node,...delta}),/INVALID_RESPONSE/);
 const real={...node,kind:'directory',id:'real',realNodeId:'real',virtual:false};delete real.provenance;assert.equal((await read(real)).nodes[0].realNodeId,'real');
});

// Captured virtual kinds have explicit bilingual labels without relabelling real files.
test('detail labels distinguish historical share and other containers in both languages',()=>{assert.equal(messages.de.kinds.share,'Freigabebereich');assert.equal(messages.en.kinds.share,'Shared-folder area');assert.equal(messages.de.kinds.other,'Weitere erfasste Volume-Bereiche');assert.equal(messages.en.kinds.other,'Other recorded volume areas');assert.equal(messages.en.kinds.directory,'Directory');});

// Byte budgets count UTF-8 JSON, not JavaScript characters, and never silently truncate records.
test('availability counts and exact Unicode JSON byte boundary fail closed',()=>{
 const record={name:'Ü <area>',provenance:provenance(),reason:'content-not-recorded'};
 const value={total:'1',truncated:false,records:[record]};assert.equal(p.validSourceAvailability(value),true);
 assert.equal(p.validSourceAvailability({total:'0',truncated:false,records:[]}),true);
 assert.equal(p.validSourceAvailability({...value,total:'9007199254740993',truncated:true}),true);
 for(const delta of [{total:'01'},{total:'0'},{total:1},{truncated:true},{records:Array(33).fill(record),total:'33'},{records:[{...record,path:'/private'}]},{records:[{...record,reason:'invented'}]}])assert.equal(p.validSourceAvailability({...value,...delta}),false);
 const base={...value,records:[{...record,name:''}]};const overhead=Buffer.byteLength(JSON.stringify(base));const remaining=16384-overhead;
 base.records[0].name='ü'.repeat(Math.floor(remaining/2))+'x'.repeat(remaining%2);assert.equal(Buffer.byteLength(JSON.stringify(base)),16384);assert.equal(p.validSourceAvailability(base),true);base.records[0].name+='x';assert.equal(p.validSourceAvailability(base),false);
});
test('detail availability is strictly decoded and rendered as escaped historical evidence',()=>{
 const v=fixture(),selection={profileId:'p',viewId:'v',snapshotId:'s',rootId:'root',nodeId:'n',metric:'logical',epoch:1};
 v.detail.sourceAvailability={total:'2',truncated:true,records:[{name:'<private-looking-label>',provenance:{...provenance(),qualification:'unavailable',state:'locked'},reason:'source-unavailable'}]};
 details.checkedHistoricalDetail(v,selection,'details');
 for(const language of ['de','en']){const html=renderToStaticMarkup(React.createElement(notice,{value:v.detail.sourceAvailability,language}));assert.match(html,/&lt;private-looking-label&gt;/);assert.match(html,language==='de'?/keine Prüfung der aktuellen/:/not a check of current/);assert.match(html,language==='de'?/Begrenzter Ausschnitt/:/Bounded excerpt/);assert.doesNotMatch(html,/<button|\b0 B\b/);}
 v.detail.sourceAvailability.records[0].provenance.binding={};assert.throws(()=>details.checkedHistoricalDetail(v,selection,'details'));
});
