import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Compile production adapters and actual editor integration in an isolated, disposable test directory.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/exclusions-'));let api,ui,editor;
try{mkdirSync(join(tmp,'features/settings'),{recursive:true});cpSync(join(web,'src/features/settings/display.tsx'),join(tmp,'features/settings/display.tsx'));cpSync(join(web,'src/contracts.ts'),join(tmp,'contracts.ts'));writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const f of ['regional-format.tsx','size-metrics.ts','ExclusionPreview.tsx','exclusion-preview.ts','ProfileEditor.tsx','profile-actions.ts','ScopeSelection.tsx','scope-selection.ts','contracts.ts','session.ts'])cpSync(join(web,'src',f),join(tmp,f));execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'ProfileEditor.tsx')]);const require=createRequire(import.meta.url);api=require(join(tmp,'out/exclusion-preview.js'));ui=require(join(tmp,'out/ExclusionPreview.js'));editor=require(join(tmp,'out/ProfileEditor.js'));}finally{rmSync(tmp,{recursive:true,force:true})}
const valid=()=>({syntaxVersion:1,valid:true,effectiveExclusions:[],warnings:['SYNTAX_ONLY'],profileRevision:0,snapshotId:null,snapshotProfileRevision:null,snapshotState:'none',examined:0,matched:0,matches:[],truncated:false,matchesTruncated:false,availablePartial:null});
test('preview distinguishes no index, partial and bounded observations',()=>{
 assert.equal(api.checkedPreview(valid()).snapshotId,null);
 const p={...valid(),snapshotId:'snapshot',snapshotProfileRevision:1,profileRevision:2,snapshotState:'partial',examined:1000,matched:70,matches:Array.from({length:50},(_,i)=>({nodeId:`n${i}`,targetId:'target'})),truncated:true,matchesTruncated:true};assert.equal(api.checkedPreview(p).snapshotState,'partial');
 for(const bad of [{...p,matched:1001},{...p,examined:1001},{...valid(),snapshotId:'fake'},{...valid(),availablePartial:{jobId:'../wrong',profileRevision:1,endedAt:'today'}}])assert.throws(()=>api.checkedPreview(bad));
});
test('both languages make copying and no-scan semantics explicit',()=>{
 for(const language of ['de','en']){const html=renderToStaticMarkup(React.createElement(ui.ExclusionPreview,{language,profileId:null,identity:'new',local:[],adopted:{version:1,revision:4,rules:[{kind:'path',value:'<private>'}]},disabled:false,onAdopt(){},onValidated(){},onSessionDenied(){}}));assert.match(html,/&lt;private&gt;/);assert.match(html,/4/);assert.match(html,/type="button"/);assert.match(html,language==='de'?/bewusst|kopiert/:/copied|Explicit/)}
});
// Drive actual EditorSession state and callbacks, rather than duplicating the save-gate implementation.
test('changed rules require the exact preview; template adoption dirties and discard clears it',()=>{
 const old={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect},slots=[];let cursor=0;
 React.useState=initial=>{const i=cursor++;if(!(i in slots))slots[i]=typeof initial==='function'?initial():initial;return[slots[i],v=>slots[i]=typeof v==='function'?v(slots[i]):v]};React.useRef=initial=>{const i=cursor++;if(!(i in slots))slots[i]={current:initial};return slots[i]};React.useEffect=()=>{};
 const p={language:'en',profile:{id:'p',revision:1,name:'P',active:true,scope:{kind:'shares',targetIds:['s']},exclusions:[],resourceMode:'gentle',limits:{workers:1,ioBytesPerSecond:null,cpuPercent:null},freshnessSeconds:1},editorIdentity:'edit',sessionIdentity:'session',loadInventory:async()=>{},checkScope:async()=>{},loadProfile:async()=>null,onSaved(){},onCancel(){},onSessionDenied(){}};
 const els=(x,out=[])=>{if(Array.isArray(x))x.forEach(v=>els(v,out));else if(x?.props){out.push(x);els(x.props.children,out)}return out};const render=()=>{cursor=0;return editor.EditorSession(p)};const component=t=>els(t).find(x=>x.type===ui.ExclusionPreview);const save=t=>els(t).find(x=>x.type==='button'&&x.props.type==='submit');
 try{let t=render();component(t).props.onAdopt({version:1,revision:2,rules:[{kind:'path',value:'cache'}]});t=render();assert.equal(save(t).props.disabled,true);const e=component(t).props,key=ui.exclusionIntentKey(e.profileId,e.identity,e.local,e.adopted);e.onValidated(key,{...valid(),effectiveExclusions:[{kind:'path',value:'cache'}]});t=render();assert.equal(save(t).props.disabled,false);els(t).find(x=>x.type==='button'&&x.props.children==='Discard').props.onClick();t=render();assert.equal(component(t).props.adopted,null);assert.equal(save(t).props.disabled,true)}finally{Object.assign(React,old)}
});

// Real component handlers ignore delayed success and authorization errors from a previous editor intent.
test('preview late success and late session denial cannot affect a switched draft',async()=>{
 const old={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect},original=api.previewExclusions;
 for(const failure of [false,true]){const slots=[];let cursor=0,resolve,reject,validated=0,denied=0;
 React.useState=initial=>{const i=cursor++;if(!(i in slots))slots[i]=typeof initial==='function'?initial():initial;return[slots[i],v=>slots[i]=typeof v==='function'?v(slots[i]):v]};React.useRef=initial=>{const i=cursor++;if(!(i in slots))slots[i]={current:initial};return slots[i]};React.useEffect=()=>{};
 api.previewExclusions=()=>new Promise((a,b)=>{resolve=a;reject=b});let p={language:'en',profileId:'one',identity:'one',local:[],adopted:null,disabled:false,onAdopt(){},onValidated(){validated++},onSessionDenied(){denied++}};
 const els=(x,out=[])=>{if(Array.isArray(x))x.forEach(v=>els(v,out));else if(x?.props){out.push(x);els(x.props.children,out)}return out};const render=()=>{cursor=0;return ui.ExclusionPreview(p)};
 try{const t=render();els(t).find(x=>x.type==='button'&&x.props.children==='Check syntax and index preview').props.onClick();p={...p,profileId:'two',identity:'two'};render();if(failure)reject(Error('late unauthorized'));else resolve(valid());await new Promise(r=>setImmediate(r));assert.equal(validated,0);assert.equal(denied,0);assert.ok(!JSON.stringify(render()).includes('Preview or template unavailable'))}finally{Object.assign(React,old);api.previewExclusions=original}
 }
});
