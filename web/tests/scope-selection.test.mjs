import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Compile the actual selection implementation once; no network or filesystem sources are used.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/scope-'));let selection,ui;
try{mkdirSync(join(tmp,'features/settings'),{recursive:true});cpSync(join(web,'src/features/settings/display.tsx'),join(tmp,'features/settings/display.tsx'));cpSync(join(web,'src/contracts.ts'),join(tmp,'contracts.ts'));writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const file of ['regional-format.tsx','size-metrics.ts','ScopeSelection.tsx','scope-selection.ts','contracts.ts','session.ts'])cpSync(join(web,'src',file),join(tmp,file));execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'ScopeSelection.tsx')],{stdio:'pipe'});const require=createRequire(import.meta.url);selection=require(join(tmp,'out/scope-selection.js'));ui=require(join(tmp,'out/ScopeSelection.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
const inventory={observedAt:'2026-10-10T12:00:00Z',volumes:[{id:'v1',name:'Volume 1',state:'reachable'},{id:'v2',name:'Volume 2',state:'reachable'}],shares:[{id:'s1',name:'Private <share>',volumeId:'v1',state:'reachable',readable:true},{id:'s2',name:'Backup',volumeId:'v1',state:'unreadable',readable:false},{id:'s3',name:'Media',volumeId:'v2',state:'reachable',readable:true}]};
const toggle=(s,k,id,on)=>selection.toggleSelection(s,inventory,k,id,on);
// Direct shares do not promote themselves to volume or NAS scans.
test('single share, mixed scope and overlap normalization preserve intent',()=>{
 let s=toggle(selection.emptySelection(),'share','s1',true).selection;
 assert.deepEqual(selection.normalizedSelection(s,inventory),{kind:'shares',targetIds:['s1']});
 s=toggle(s,'share','s2',true).selection;
 assert.equal(selection.selectionCheckState(s,inventory,'volume','v1'),'mixed');
 assert.equal(selection.normalizedSelection(s,inventory).kind,'shares');
 s=toggle(s,'volume','v1',true).selection;s=toggle(s,'share','s3',true).selection;
 assert.deepEqual(selection.normalizedSelection(s,inventory),{kind:'mixed',targetIds:['s3','v1']});
 assert.equal(selection.selectionCheckState(s,inventory,'share','s1'),'checked');
 assert.equal(selection.selectionCheckState(s,inventory,'nas'),'mixed');
});
// Reducing a whole parent preserves only explicit remaining scopes and flags the semantic change.
test('NAS dynamic and descendant deselection have an explicit reduction',()=>{
 let s=toggle(selection.emptySelection(),'nas','',true).selection;assert.deepEqual(selection.normalizedSelection(s,inventory),{kind:'nas',targetIds:[]});
 let reduced=toggle(s,'share','s1',false);assert.equal(reduced.reduced,true);
 assert.deepEqual(selection.normalizedSelection(reduced.selection,inventory),{kind:'mixed',targetIds:['s2','v2']});
 assert.equal(selection.selectionCheckState(reduced.selection,inventory,'volume','v1'),'mixed');
 assert.deepEqual(toggle(s,'nas','',false).selection,selection.emptySelection());
 assert.equal(selection.normalizedSelection(selection.emptySelection(),inventory),null);
});
// Failed or missing identities remain explicit rather than being mapped by a display name.
test('strict inventory and vanished explicit IDs fail closed',()=>{
 assert.equal(selection.checkedScopeInventory(inventory),inventory);
 for(const bad of [{...inventory,shares:[{...inventory.shares[0],volumeId:'foreign'}]},{...inventory,shares:[{...inventory.shares[0],readable:false}]},{...inventory,volumes:[inventory.volumes[0],inventory.volumes[0]]}])assert.throws(()=>selection.checkedScopeInventory(bad));
 assert.throws(()=>selection.normalizedSelection({nas:false,volumeIds:[],shareIds:['deleted']},inventory));
});
// The response is tied to submitted scope and includes only qualified normalized root identities.
test('root precheck rejects foreign targets and retains unreadable observations',()=>{
 const scope={kind:'shares',targetIds:['s2']};const report={scope,checkedAt:inventory.observedAt,roots:[{targetId:'s2',kind:'share',volumeId:'v1',state:'unreadable'}],coveredTargetIds:['s2'],issues:[{targetId:'s2',code:'SCOPE_TARGET_UNREADABLE',state:'unreadable'}]};
 assert.equal(selection.checkedScopeCheck(report,scope,inventory),report);
 for(const bad of [{...report,scope:{kind:'shares',targetIds:['s1']}},{...report,roots:[]},{...report,coveredTargetIds:['s2','s3']},{...report,roots:[{...report.roots[0],volumeId:'v2'}]}])assert.throws(()=>selection.checkedScopeCheck(bad,scope,inventory));
});
// Native checkbox semantics expose partial state to assistive technology and ordinary keyboard input.
test('checkbox emits mixed state and a native change event without custom keyboard traps',()=>{
 const values=[];const oldRef=React.useRef,oldEffect=React.useEffect,oldId=React.useId;const element={indeterminate:false};
 React.useRef=()=>({current:element});React.useEffect=fn=>fn();React.useId=()=>':scope:';
 try{const tree=ui.ScopeCheckbox({state:'mixed',label:'<name>',onChange:x=>values.push(x)});const input=tree.props.children[0];assert.equal(element.indeterminate,true);assert.equal(input.props.type,'checkbox');assert.equal(input.props['aria-checked'],'mixed');assert.equal(input.props.onKeyDown,undefined);input.props.onChange({target:{checked:true}});assert.deepEqual(values,[true]);assert.ok(renderToStaticMarkup(tree).includes('&lt;name&gt;'));}finally{React.useRef=oldRef;React.useEffect=oldEffect;React.useId=oldId;}
});
// Translated guidance never treats root readability as proof of complete descendant coverage.
test('both languages explain no full scan, no permission changes and fixed versus dynamic intent',()=>{
 assert.deepEqual(Object.keys(ui.scopeSelectionText.de).sort(),Object.keys(ui.scopeSelectionText.en).sort());
 assert.match(ui.scopeSelectionText.de.limit,/Unterordner/);assert.match(ui.scopeSelectionText.en.limit,/Subfolders/);
 assert.match(ui.scopeSelectionText.de.help,/ändert keine Berechtigungen/);assert.match(ui.scopeSelectionText.en.help,/does not change permissions/);
});
// Drive the real component's asynchronous handlers without allowing a stale root result to reappear.
test('selection changes discard a late precheck response and failure',async()=>{
 const old={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect};const slots=[];let cursor=0;
 React.useState=initial=>{const n=cursor++;if(!(n in slots))slots[n]=typeof initial==='function'?initial():initial;return[slots[n],value=>{slots[n]=typeof value==='function'?value(slots[n]):value;}];};
 React.useRef=initial=>{const n=cursor++;if(!(n in slots))slots[n]={current:initial};return slots[n];};React.useEffect=()=>{};
 let resolve,reject;const changes=[];const props={language:'en',loadInventory:async()=>structuredClone(inventory),checkScope:()=>new Promise((yes,no)=>{resolve=yes;reject=no;}),onChange:s=>changes.push(s),onSessionDenied:()=>assert.fail('unexpected auth failure')};
 const render=()=>{cursor=0;return ui.ScopeSelectionBody(props);};
 const elements=(v,out=[])=>{if(Array.isArray(v)){for(const x of v)elements(x,out);}else if(v&&typeof v==='object'&&v.props){out.push(v);elements(v.props.children,out);}return out;};
 const button=(tree,text)=>elements(tree).find(x=>x.type==='button'&&x.props.children===text);
 const share=tree=>elements(tree).find(x=>x.type===ui.ScopeCheckbox&&x.props.label.startsWith('Private'));
 const tick=()=>new Promise(done=>setImmediate(done));
 try{
  button(render(),'Refresh available scopes').props.onClick();await tick();share(render()).props.onChange(true);
  button(render(),'Check selected root readability').props.onClick();share(render()).props.onChange(false);
  resolve({scope:{kind:'shares',targetIds:['s1']},checkedAt:inventory.observedAt,roots:[{targetId:'s1',kind:'share',volumeId:'v1',state:'reachable'}],coveredTargetIds:['s1'],issues:[]});await tick();
  assert.ok(!elements(render()).some(x=>x.type==='div'&&x.props.role==='status'));assert.equal(changes.at(-1),null);
  share(render()).props.onChange(true);button(render(),'Check selected root readability').props.onClick();share(render()).props.onChange(false);reject(new Error('late failure'));await tick();
  assert.ok(!elements(render()).some(x=>x.props.role==='alert'));
 }finally{Object.assign(React,old);}
});

// Controlled refresh keeps the caller's missing targets and never emits a clearing change.
test('controlled values retain missing IDs and expose disabled editor boundary',()=>{
 const scope={kind:'mixed',targetIds:['v1','missing']};
 assert.deepEqual(ui.selectionFromScope(scope,inventory),{nas:false,volumeIds:['v1'],shareIds:['missing']});
 const changes=[];const props={language:'en',value:scope,disabled:true,editorIdentity:'profile-b',loadInventory:async()=>inventory,checkScope:async()=>{throw Error('unused');},onChange:v=>changes.push(v),onSessionDenied(){}};
 const wrapper=ui.ScopeSelection(props);assert.equal(wrapper.key,'profile-b');
 const html=renderToStaticMarkup(wrapper);assert.match(html,/missing/);assert.match(html,/disabled/);assert.deepEqual(changes,[]);
});

// Native scans expand NAS/volume selection to permitted shares and reject broad volume roots.
test('NAS and volume checks require authorized share roots',()=>{
 for(const scope of [{kind:'nas',targetIds:[]},{kind:'volumes',targetIds:['v1']}]){
  const volumes=scope.kind==='nas'?inventory.volumes.map(x=>x.id):scope.targetIds;
  const shares=inventory.shares.filter(x=>volumes.includes(x.volumeId));
  const report={scope,checkedAt:inventory.observedAt,roots:shares.map(x=>({targetId:x.id,kind:'share',volumeId:x.volumeId,state:x.state})),coveredTargetIds:[...volumes,...shares.map(x=>x.id)],issues:[]};
  assert.equal(selection.checkedScopeCheck(report,scope,inventory),report);
  assert.throws(()=>selection.checkedScopeCheck({...report,roots:[{targetId:'v1',kind:'volume',volumeId:'v1',state:'reachable'}]},scope,inventory));
 }
});
