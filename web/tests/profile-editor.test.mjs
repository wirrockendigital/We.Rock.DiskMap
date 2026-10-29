import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Exercise production adapters and components with deterministic transport outcomes.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/profiles-'));let actions,ui;
try{mkdirSync(join(tmp,'features/settings'),{recursive:true});cpSync(join(web,'src/features/settings/display.tsx'),join(tmp,'features/settings/display.tsx'));cpSync(join(web,'src/contracts.ts'),join(tmp,'contracts.ts'));writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const file of ['regional-format.tsx','size-metrics.ts','ExclusionPreview.tsx','exclusion-preview.ts','ProfileEditor.tsx','profile-actions.ts','ScopeSelection.tsx','scope-selection.ts','contracts.ts','session.ts'])cpSync(join(web,'src',file),join(tmp,file));execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'ProfileEditor.tsx')],{stdio:'pipe'});const require=createRequire(import.meta.url);actions=require(join(tmp,'out/profile-actions.js'));ui=require(join(tmp,'out/ProfileEditor.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
const config=()=>({...actions.newProfileConfiguration(),name:'Profile <one>',scope:{kind:'shares',targetIds:['share-one']}});
const profile=()=>({id:'profile-one',revision:3,...config()});
const receipt=(operation,p)=>new Response(JSON.stringify({contractVersion:1,data:{operation,profileId:p.id,profile:p,cleanupScheduled:false,tasksRequireReview:false}}));
const fail=(code,status=409)=>new Response(JSON.stringify({contractVersion:1,error:{code}}),{status});
// The complete serialized UTF-8 request, rather than individual form fields, is the transport budget.
test('exact request budget 16384/16385 and UTF8 fields',()=>{
 const c=config();c.exclusions=Array.from({length:4},()=>({kind:'path',value:'x'}));
 const base=JSON.stringify({contractVersion:1,operation:'create',configuration:c});let left=16384-new TextEncoder().encode(base).length;
 for(const r of c.exclusions){const add=Math.min(left,4095);r.value+='x'.repeat(add);left-=add;}assert.equal(left,0);
 assert.equal(new TextEncoder().encode(actions.encodeProfileSave(null,c)).length,16384);
 c.name+='x';assert.throws(()=>actions.encodeProfileSave(null,c),/REQUEST_TOO_LARGE/);
 const unicode=config();unicode.name='🚀'.repeat(256);assert.doesNotThrow(()=>actions.encodeProfileSave(null,unicode));unicode.name+='🚀';assert.throws(()=>actions.encodeProfileSave(null,unicode),/NAME_INVALID/);
 for(const c of [{...config(),name:'\ud800'},{...config(),freshnessSeconds:Number.MAX_SAFE_INTEGER+1},{...config(),resourceMode:'custom'},{...config(),limits:{workers:2,ioBytesPerSecond:null,cpuPercent:null}}])assert.throws(()=>actions.encodeProfileSave(null,c));
});
test('create owns no ID and update keeps exact technical identity and CAS',async()=>{
 let seen;const c=new actions.ProfileActionClient(async(u,o)=>{seen=[u,o];const x=JSON.parse(o.body);return receipt(x.operation,{id:x.profileId??'server-id',revision:x.expectedRevision??1,...x.configuration});},()=> 'token',()=> 'key');
 const created=await c.save(null,config());assert.equal(created.id,'server-id');assert.equal(JSON.parse(seen[1].body).profileId,undefined);assert.equal(seen[0],'./api.cgi?action=profile-action');
 const rename={...config(),name:'Renamed'};const updated=await c.save(profile(),rename);assert.equal(updated.id,'profile-one');assert.equal(updated.revision,3);assert.equal(JSON.parse(seen[1].body).expectedRevision,3);assert.equal(seen[1].headers['Idempotency-Key'],'key');
});
test('uncertain create freezes bytes/key and rejects a replacement draft',async()=>{
 const calls=[];let first=true;const c=new actions.ProfileActionClient(async(u,o)=>{calls.push(o);if(first){first=false;throw Error('network');}const x=JSON.parse(o.body);return receipt('create',{id:'new-id',revision:1,...x.configuration});},()=> 'token',()=> 'stable');
 const draft=config();await assert.rejects(c.save(null,draft));draft.name='other';await assert.rejects(c.save(null,draft),/PENDING/);await c.retry();assert.equal(calls[0].body,calls[1].body);assert.equal(calls[0].headers['Idempotency-Key'],calls[1].headers['Idempotency-Key']);assert.equal(c.status,'idle');
});
test('CAS conflict never overwrites, invalid receipts remain uncertain, late session results ignored',async()=>{
 const c=new actions.ProfileActionClient(async()=>fail('PROFILE_REVISION_CONFLICT'),()=> 'token');await assert.rejects(c.save(profile(),config()),/REVISION_CONFLICT/);assert.equal(c.status,'conflict');await assert.rejects(c.save(profile(),config()),/RELOAD_REQUIRED/);
 const corrupt=new actions.ProfileActionClient(async()=>receipt('update',{...profile(),name:'wrong'}),()=> 'token');await assert.rejects(corrupt.save(profile(),config()),/RESPONSE_INVALID/);assert.equal(corrupt.status,'uncertain');
 let resolve;const late=new actions.ProfileActionClient(()=>new Promise(r=>resolve=r),()=> 'token');const pending=late.save(null,config());late.dispose();resolve(fail('SESSION_REQUIRED',401));await assert.rejects(pending,/DISPOSED/);
});
test('both languages expose real fields and native keyboard controls without implicit submit buttons',()=>{
 for(const language of ['de','en']){const html=renderToStaticMarkup(React.createElement(ui.ProfileEditor,{language,profile:profile(),editorIdentity:'p',sessionIdentity:'session',loadInventory:async()=>{},checkScope:async()=>{},loadProfile:async()=>profile(),onSaved(){},onCancel(){},onSessionDenied(){}}));assert.match(html,/Profile &lt;one&gt;/);assert.match(html,/type="submit"/);assert.match(html,/type="number"/);assert.match(html,/workers|Scan-Arbeiter|scan worker/);assert.ok(!html.includes('localStorage'));}
});
// Drive the actual editor's event handlers and rerenders; a dirty profile switch requires an explicit decision.
test('editor save/discard and dirty switch keep drafts isolated',()=>{
 const old={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect};const slots=[];let cursor=0;
 React.useState=initial=>{const i=cursor++;if(!(i in slots))slots[i]=typeof initial==='function'?initial():initial;return[slots[i],v=>slots[i]=typeof v==='function'?v(slots[i]):v];};React.useRef=initial=>{const i=cursor++;if(!(i in slots))slots[i]={current:initial};return slots[i];};React.useEffect=()=>{};
 const elements=(x,out=[])=>{if(Array.isArray(x))for(const y of x)elements(y,out);else if(x&&typeof x==='object'&&x.props){out.push(x);elements(x.props.children,out);}return out;};
 let p={language:'en',profile:profile(),editorIdentity:'one',sessionIdentity:'session',loadInventory:async()=>{},checkScope:async()=>{},loadProfile:async()=>profile(),onSaved(){},onCancel(){},onSessionDenied(){}};
 const render=()=>{cursor=0;return ui.EditorSession(p);};const named=(tree,text)=>elements(tree).find(x=>x.type==='button'&&x.props.children===text);const name=tree=>elements(tree).find(x=>x.type==='input'&&x.props.value=== 'Profile <one>');
 try{name(render()).props.onChange({target:{value:'Unsaved'}});let tree=render();assert.equal(named(tree,'Discard').props.disabled,false);p={...p,profile:{...profile(),id:'two',name:'Second'},editorIdentity:'two'};tree=render();assert.ok(named(tree,'Discard and switch'));named(tree,'Keep draft').props.onClick();tree=render();assert.ok(elements(tree).some(x=>x.type==='input'&&x.props.value==='Unsaved'));named(tree,'Discard').props.onClick();tree=render();assert.ok(name(tree));}finally{Object.assign(React,old);}
});

// Native browser fetch rejects arbitrary receivers; the default adapter must preserve its global call shape.
test('default transport never binds fetch to the profile client',async()=>{
 const previous=globalThis.fetch;let receiver;
 globalThis.fetch=async function(_url,options){receiver=this;assert.equal(this,undefined);const request=JSON.parse(options.body);return receipt('create',{id:'created',revision:1,...request.configuration});};
 try{await new actions.ProfileActionClient(undefined,()=> 'token',()=> 'key').save(null,config());assert.equal(receiver,undefined);}finally{globalThis.fetch=previous;}
});
test('public profile scope accepts 256 identities and rejects 257',()=>{
 const c=config();c.scope.targetIds=Array.from({length:256},(_,i)=>`share-${i}`);assert.doesNotThrow(()=>actions.encodeProfileSave(null,c));c.scope.targetIds.push('share-256');assert.throws(()=>actions.encodeProfileSave(null,c),/SCOPE_INVALID/);
});
// Exercise the existing close dialog after a real uncertain save, including direct handler invocation.
test('close dialog cannot discard an unresolved save',async()=>{
 const old={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect},originalSave=actions.ProfileActionClient.prototype.save;const slots=[];let cursor=0,closed=0;
 React.useState=initial=>{const i=cursor++;if(!(i in slots))slots[i]=typeof initial==='function'?initial():initial;return[slots[i],v=>slots[i]=typeof v==='function'?v(slots[i]):v];};React.useRef=initial=>{const i=cursor++;if(!(i in slots))slots[i]={current:initial};return slots[i];};React.useEffect=()=>{};
 actions.ProfileActionClient.prototype.save=function(){this.pending={body:'frozen',key:'same'};this.status='uncertain';return Promise.reject(Error('network'));};
 const elements=(x,out=[])=>{if(Array.isArray(x))for(const y of x)elements(y,out);else if(x&&typeof x==='object'&&x.props){out.push(x);elements(x.props.children,out);}return out;};
 const p={language:'en',profile:profile(),editorIdentity:'one',sessionIdentity:'session',loadInventory:async()=>{},checkScope:async()=>{},loadProfile:async()=>profile(),onSaved(){},onCancel(){closed++;},onSessionDenied(){}};
 const render=()=>{cursor=0;return ui.EditorSession(p);};
 try{let tree=render();elements(tree).find(x=>x.type==='input'&&x.props.value==='Profile <one>').props.onChange({target:{value:'Changed'}});tree=render();elements(tree).find(x=>x.type==='button'&&x.props.children==='Close').props.onClick();tree=render();elements(tree).find(x=>x.type==='form').props.onSubmit({preventDefault(){}});await new Promise(resolve=>setImmediate(resolve));tree=render();const discard=elements(tree).filter(x=>x.type==='button'&&x.props.children==='Discard').at(-1);assert.equal(discard.props.disabled,true);discard.props.onClick();assert.equal(closed,0);assert.ok(elements(tree).some(x=>x.type==='button'&&x.props.children==='Confirm save again'));}finally{Object.assign(React,old);actions.ProfileActionClient.prototype.save=originalSave;}
});

// An unresponsive transport releases the editor into retryable uncertainty without changing its intent.
test('request deadline preserves exact replay while session disposal stays disposed',async()=>{
 const timer=new AbortController(),calls=[];let first=true;
 const transport=async(_url,options)=>{calls.push(options);if(first){first=false;return new Promise((_,reject)=>options.signal.addEventListener('abort',()=>reject(options.signal.reason),{once:true}));}const request=JSON.parse(options.body);return receipt('create',{id:'created',revision:1,...request.configuration});};
 let deadlines=0;const client=new actions.ProfileActionClient(transport,()=> 'token',()=> 'same-key',()=>deadlines++===0?timer.signal:new AbortController().signal);
 const pending=client.save(null,config());timer.abort(new DOMException('Deadline','TimeoutError'));await assert.rejects(pending);assert.equal(client.status,'uncertain');assert.equal(client.unresolved,true);await client.retry();assert.equal(calls[1].signal.aborted,false);assert.equal(calls[0].body,calls[1].body);assert.equal(calls[0].headers['Idempotency-Key'],calls[1].headers['Idempotency-Key']);
 const disposed=new actions.ProfileActionClient(async(_url,options)=>new Promise((_,reject)=>options.signal.addEventListener('abort',()=>reject(options.signal.reason),{once:true})),()=> 'token',()=> 'key',()=>new AbortController().signal);
 const operation=disposed.save(null,config());disposed.dispose();await assert.rejects(operation,/PROFILE_DISPOSED/);
});
