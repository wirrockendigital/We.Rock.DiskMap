import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Compile the actual independent feature, leaving integration ownership with the central controller.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});
const tmp=mkdtempSync(join(web,'.test-build/status-'));let model,component;
try{writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');
 cpSync(join(web,'src'),join(tmp,'src'),{recursive:true});
 execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'src/features/snapshotstatus/SnapshotStatus.tsx')],{stdio:'pipe'});
 const require=createRequire(import.meta.url);model=require(join(tmp,'out/features/snapshotstatus/snapshot-status.js'));component=require(join(tmp,'out/features/snapshotstatus/SnapshotStatus.js'));
}finally{rmSync(tmp,{recursive:true,force:true});}
const snapshot={snapshotId:'old',state:'complete',startedAt:'2026-01-01T00:00:00Z',endedAt:'2026-01-01T00:01:00Z',profileRevision:1};
const props=()=>({language:'de',snapshot,currentRevision:1,freshnessSeconds:60,newCompleteSnapshotId:null,expired:false,selectionFallback:'none',contentState:'data',job:null,sourceGap:false,partialAvailable:false,partialUnqualified:false,completeAvailable:true,pendingSwitch:false,disabled:false,onAdopt(){throw Error('unsolicited adoption');},onOpenPartial(){throw Error('unsolicited partial');},onBackComplete(){throw Error('unsolicited complete');}});
const render=p=>renderToStaticMarkup(React.createElement(component.SnapshotStatus,p));
test('selected capture age has exact target boundary, unknown future and no duration overflow',()=>{
 const end=Date.parse(snapshot.endedAt);assert.equal(model.selectedFreshness(snapshot,60,end+60000),'current');assert.equal(model.selectedFreshness(snapshot,60,end+60001),'stale');
 assert.equal(model.selectedFreshness(snapshot,60,end-1),'unknown');assert.equal(model.selectedFreshness({...snapshot,endedAt:'bad'},60,end),'unknown');
 assert.equal(model.selectedFreshness(snapshot,Number.MAX_SAFE_INTEGER,end+100000000),'current');assert.equal(model.selectedFreshness(null,60,end),'unknown');assert.equal(model.selectedFreshness(snapshot,0,end),'unknown');
});
test('new complete and running or failed job do not replace older selected capture',()=>{
 for(const state of ['scanning','failed','interrupted']){const p={...props(),job:{state},newCompleteSnapshotId:'new',currentRevision:2};
  assert.equal(model.snapshotStatus(p,Date.now()).profileChanged,true);const html=render(p);assert.ok(html.includes(snapshot.endedAt));assert.ok(html.includes('weiterhin die aufgezeichnete Konfiguration'));assert.ok(html.includes('bis zur bewussten Übernahme'));assert.equal(p.snapshot.snapshotId,'old');}
});
test('first partial requires explicit button and selected partial retains warning after complete publication',()=>{
 let html=render({...props(),snapshot:null,contentState:'no-snapshot',partialAvailable:true,completeAvailable:false});assert.ok(html.includes('Teilweisen Datenstand bewusst öffnen'));assert.ok(!html.includes('Vollständigen Datenstand öffnen'));
 html=render({...props(),snapshot:{...snapshot,state:'partial'},newCompleteSnapshotId:'new'});assert.ok(html.includes('Lücken bleiben unbekannt'));assert.ok(html.includes('Vollständigen Datenstand öffnen'));assert.ok(html.includes('Neuen Datenstand übernehmen'));
});
test('empty, no hits, unavailable and no snapshot remain distinct in both languages',()=>{
 for(const language of ['de','en']){const outputs=['empty','no-hits','bounded-empty','unavailable','no-snapshot'].map(contentState=>render({...props(),language,snapshot:null,contentState}));assert.equal(new Set(outputs).size,5);}
});
test('source gaps are recorded evidence, parent fallback and expired retry remain explicit',()=>{
 const html=render({...props(),sourceGap:true,selectionFallback:'ancestor',expired:true,disabled:true,pendingSwitch:true});assert.ok(html.includes('Aktueller Quellenstatus nicht geprüft'));assert.ok(html.includes('keine leeren Ordner'));assert.ok(html.includes('Elternordner'));assert.ok(html.includes('disabled=""'));assert.ok(html.includes('denselben Auftrag'));
});
test('unknown job strings are not rendered as diagnostics and historical partial cannot be fabricated',()=>{
 for(const language of ['de','en']){const html=render({...props(),language,job:{state:'<private-source-path>'},partialUnqualified:true,partialAvailable:false});assert.ok(!html.includes('private-source-path'));assert.ok(!html.includes(language==='de'?'Teilweisen Datenstand bewusst öffnen':'Explicitly open partial snapshot'));}
});

// Resetting the first partial view must not claim that a complete generation exists.
test('first partial reset and bounded search use honest explicit labels',()=>{
 for(const language of ['de','en']){const html=render({...props(),language,snapshot:{...snapshot,state:'partial'},completeAvailable:false,contentState:'bounded-empty'});assert.ok(html.includes(language==='de'?'Zur Standardansicht ohne Datenstand':'Return to default view without a snapshot'));assert.ok(html.includes(language==='de'?'kein abschließendes Suchergebnis':'not a final search result'));}
});
