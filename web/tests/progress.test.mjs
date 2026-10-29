import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
// Compile the actual runtime validator rather than copying its decision rules.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/progress-'));let checkedProgress,component;
try{mkdirSync(join(tmp,'features/settings'),{recursive:true});cpSync(join(web,'src/features/settings/display.tsx'),join(tmp,'features/settings/display.tsx'));cpSync(join(web,'src/contracts.ts'),join(tmp,'contracts.ts'));writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const file of ['regional-format.tsx','size-metrics.ts','ProgressHistory.tsx','session.ts'])cpSync(join(web,'src',file),join(tmp,file));execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'compiled'),join(tmp,'ProgressHistory.tsx')]);component=createRequire(import.meta.url)(join(tmp,'compiled/ProgressHistory.js'));checkedProgress=component.checkedProgress;}finally{rmSync(tmp,{recursive:true,force:true});}
const sample=()=>({jobId:'job',profileId:'profile',state:'scanning',trigger:'ui',mode:'full',createdAt:'2026-10-10T00:00:00Z',startedAt:'2026-10-10T00:00:00Z',endedAt:null,entries:'3',files:'1',directories:'1',specials:'1',unknownLogical:'0',knownLogicalBytes:'9007199254740993',warnings:'1',observedAt:'2026-10-10T00:00:01Z',elapsedSeconds:'1',exactTotal:null,estimate:null,targetId:'share',measured:true,publishedComplete:false,errorCodes:[]});
// Unknown totals and large integer strings stay exact; estimates are explicitly labelled.
test('progress preserves unknown totals and exact metadata bytes',()=>{const p=sample();assert.equal(checkedProgress(p).knownLogicalBytes,'9007199254740993');p.estimate={entries:'2',snapshotId:'previous',basis:'previous-complete'};assert.equal(checkedProgress(p).estimate.entries,'2');});
// No response may claim publication before its terminal lifecycle or invent percentages.
test('progress rejects false completion, invalid estimates and private activity paths',()=>{for(const override of [{publishedComplete:true},{exactTotal:'100'},{estimate:{entries:'3',snapshotId:'old',basis:'exact'}},{targetId:'/private/name'},{warnings:-1},{errorCodes:['PRIVATE /path']}])assert.throws(()=>checkedProgress({...sample(),...override}));});
test('partial and failed terminal outcomes do not imply complete publication',()=>{for(const state of ['failed','cancelled','interrupted','completed'])assert.equal(checkedProgress({...sample(),state}).publishedComplete,false);assert.equal(checkedProgress({...sample(),state:'completed',publishedComplete:true}).publishedComplete,true);});

// Cross-mode mixtures and unsupported extra settings cannot be presented as effective presets.
const policies=[{version:1,mode:'gentle',workers:1,directoryBatch:32,paceEvery:32,paceDelayMs:8,sqliteCacheKiB:2048,tempCacheKiB:1024},{version:1,mode:'balanced',workers:1,directoryBatch:64,paceEvery:64,paceDelayMs:2,sqliteCacheKiB:4096,tempCacheKiB:2048},{version:1,mode:'fast',workers:1,directoryBatch:128,paceEvery:128,paceDelayMs:0,sqliteCacheKiB:8192,tempCacheKiB:2048}];
test('progress accepts only complete canonical effective resource policies',()=>{
 for(const policy of policies){assert.equal(checkedProgress({...sample(),resourcePolicy:policy}).resourcePolicy,policy);for(const key of Object.keys(policy)){const missing={...policy};delete missing[key];assert.throws(()=>checkedProgress({...sample(),resourcePolicy:missing}));}}
 for(const bad of [{...policies[0],version:2},{...policies[0],workers:2},{...policies[0],directoryBatch:64},{...policies[0],paceEvery:64},{...policies[0],paceDelayMs:8000000},{...policies[0],sqliteCacheKiB:8192},{...policies[0],tempCacheKiB:2048},{...policies[0],cpuPercent:10},{...policies[0],mode:'__proto__'},'gentle'])assert.throws(()=>checkedProgress({...sample(),resourcePolicy:bad}));
 for(const policy of [undefined,null]){assert.doesNotThrow(()=>checkedProgress({...sample(),resourcePolicy:policy}));assert.equal(renderToStaticMarkup(React.createElement(component.ResourcePolicyDetails,{policy,language:'de'})),'');}
});
// Bilingual output names actual units without claiming a process-memory ceiling.
test('effective resources render exact units and fast-mode load warning in both languages',()=>{
 for(const language of ['de','en'])for(const policy of policies){const html=renderToStaticMarkup(React.createElement(component.ResourcePolicyDetails,{policy,language}));for(const value of [String(policy.sqliteCacheKiB)+' KiB',String(policy.tempCacheKiB)+' KiB'])assert.ok(html.includes(value));assert.ok(html.includes(String(policy.paceDelayMs)+' ms'));assert.ok(html.includes(language==='de'?'keine Obergrenze für den gesamten Arbeitsspeicher':'do not limit total process memory'));assert.equal(html.includes(language==='de'?'zusätzliche Last':'increase load'),policy.mode==='fast');}
});

// The real progress surface includes captured settings for the selected run and historical entries.
test('progress surface integrates selected settings without reconstructing old runs',()=>{
 const original={state:React.useState,effect:React.useEffect};
 try{for(const policy of [policies[0],null,undefined]){
  let i=0;const p={...sample(),resourcePolicy:policy};const values=[p,[{...p,jobId:'old'}],false,false];React.useState=()=>[values[i++],()=>{}];React.useEffect=()=>{};
  const tree=component.ProgressHistory({language:'de',current:p,history:[{...p,jobId:'old'}],limited:false,failed:false});const html=renderToStaticMarkup(tree);
  assert.equal(html.includes('Für diesen Lauf festgelegte Ressourcenparameter'),Boolean(policy));
  assert.equal(html.includes('2048 KiB'),Boolean(policy));
 }}finally{React.useState=original.state;React.useEffect=original.effect;}
});
