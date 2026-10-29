import {test} from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
// Compile the production client and component without modifying shared test fixtures.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/template-'));let Client,UI;
try{writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');cpSync(join(web,'src'),join(tmp,'src'),{recursive:true});execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'src/features/settings/GlobalExclusionSettings.tsx')],{stdio:'pipe'});const require=createRequire(import.meta.url);Client=require(join(tmp,'out/features/settings/template-client.js')).TemplateClient;UI=require(join(tmp,'out/features/settings/GlobalExclusionSettings.js')).GlobalExclusionSettings;}finally{rmSync(tmp,{recursive:true,force:true});}
const response=(revision,rules=[])=>new Response(JSON.stringify({contractVersion:1,data:{version:1,revision,rules}}));
const error=(status,code)=>new Response(JSON.stringify({contractVersion:1,error:{code}}),{status});
const rule={kind:'pattern',value:'*.tmp'};
test('two clients share global CAS; conflict requires explicit reload; reset sends only empty rules',async()=>{
 let revision=1,rules=[rule];const transport=async(_,o)=>{if(o.method==='GET')return response(revision,rules);const x=JSON.parse(o.body);if(x.expectedRevision!==revision)return error(409,'TEMPLATE_CONFLICT');rules=x.rules;return response(++revision,rules);};
 const a=new Client(transport,()=> 'a'),b=new Client(transport,()=> 'b');const av=await a.read(),bv=await b.read();await a.save(av.revision,[]);await assert.rejects(b.save(bv.revision,[rule]),/TEMPLATE_CONFLICT/);assert.equal(b.status,'conflict');await assert.rejects(b.save(2,[rule]),/RELOAD_REQUIRED/);const fresh=await b.read();assert.equal(fresh.revision,2);assert.deepEqual(fresh.rules,[]);assert.equal((await b.save(2,[rule])).revision,3);
});
test('unknown save retains exact body/key and disallows new intent or read',async()=>{
 const calls=[];let first=true;const c=new Client(async(_,o)=>{calls.push(o);if(first){first=false;throw Error('network');}return response(8,[rule]);},()=> 'token',()=> 'fixed');
 await assert.rejects(c.save(7,[rule]));assert.equal(c.status,'uncertain');await assert.rejects(c.read(),/PENDING/);await assert.rejects(c.save(7,[]),/PENDING/);await c.retry();assert.equal(calls[0].body,calls[1].body);assert.equal(calls[0].headers['Idempotency-Key'],calls[1].headers['Idempotency-Key']);assert.equal(c.unresolved,false);
});
test('mismatched success remains uncertain and permits only original retry',async()=>{
 const c=new Client(async()=>response(4,[]),()=> 't');await assert.rejects(c.save(3,[rule]),/INVALID/);assert.equal(c.status,'uncertain');assert.equal(c.unresolved,true);
});
test('definitive invalid request permits correction while local UTF8 budget never sends',async()=>{
 let calls=0;const c=new Client(async()=>{calls++;return error(400,'INVALID_REQUEST');},()=> 't');await assert.rejects(c.save(1,[{kind:'path',value:'../bad'}]),/INVALID_REQUEST/);assert.equal(c.status,'idle');assert.equal(c.unresolved,false);await assert.rejects(c.save(1,[{kind:'path',value:'😀'.repeat(1100)}]),/INVALID/);assert.equal(calls,1);
});
test('dispose rejects late auth and success without resurfacing session denial',async()=>{
 for(const status of [200,403]){let release;const c=new Client(()=>new Promise(r=>release=r),()=> 't');const read=c.read();c.dispose();release(status===200?response(0):error(403,'SESSION_REQUIRED'));await assert.rejects(read,/DISPOSED/);}
});
test('deadline bounds abort-ignoring transport and uncertain mutation can be retried',async()=>{
 const abort=new AbortController();const c=new Client(()=>new Promise(()=>{}),()=> 't',()=> 'k',()=>abort.signal);const pending=c.save(0,[rule]);abort.abort();await assert.rejects(pending);assert.equal(c.status,'uncertain');
});
test('response streaming and malformed template are rejected',async()=>{
 for(const r of [new Response('x'.repeat(65537)),response(0,[rule]),new Response(JSON.stringify({contractVersion:1,data:{version:1,revision:1,rules:null}}))]){const c=new Client(async()=>r,()=> 't');await assert.rejects(c.read());}
});
test('global editor labels distinguish copy-only reset from profile changes in DE and EN',()=>{
 for(const language of ['de','en']){const html=renderToStaticMarkup(React.createElement(UI,{language,disabled:false,onDirty(){},onSessionDenied(){}}));assert.match(html,language==='de'?/keine bestehenden Profile/:/no existing profiles/);assert.match(html,language==='de'?/Standardwerte setzen \(leer\)/:/defaults \(empty\)/);assert.doesNotMatch(html,/localStorage/);}
});
// Exercise the actual form handlers and effects with deterministic hook storage.
test('form save discard reset and uncertainty all bind the host dirty guard',async()=>{
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect},env={fetch:globalThis.fetch,window:globalThis.window};const slots=[],effects=[],cleanups=[],setups=[];let at=0,dirty=false,revision=1,posted=[],lose=false;
 React.useState=initial=>{const i=at++;if(!(i in slots))slots[i]=typeof initial==='function'?initial():initial;return[slots[i],v=>slots[i]=typeof v==='function'?v(slots[i]):v];};React.useRef=initial=>{const i=at++;return slots[i]??={current:initial};};React.useEffect=(fn,deps)=>{const i=at++,old=slots[i];if(!old||deps.some((v,j)=>!Object.is(v,old[j]))){slots[i]=deps;setups[i]=fn;effects.push(()=>{cleanups[i]?.();cleanups[i]=fn();});}};
 globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};globalThis.fetch=async(_,o)=>{if(o.method==='GET')return response(revision,[rule]);posted.push(o);if(lose){lose=false;throw Error('lost');}return response(++revision,JSON.parse(o.body).rules);};
 const props={language:'en',disabled:false,onDirty:v=>dirty=v,onSessionDenied:()=>assert.fail('unexpected auth')};const render=()=>{at=0;const tree=UI(props);for(const fn of effects.splice(0))fn();return tree;};const all=n=>!n||typeof n!=='object'?[]:[n,...React.Children.toArray(n.props?.children).flatMap(all)];const button=(tree,label)=>all(tree).find(n=>n.type==='button'&&n.props.children===label);const settle=()=>new Promise(r=>setImmediate(r));
 try{render();
 // Replay setup/cleanup while the first read is in flight, as the real StrictMode host does.
 for(let i=0;i<setups.length;i++)if(setups[i]){cleanups[i]?.();cleanups[i]=setups[i]();}
 await settle();let tree=render();all(tree).find(n=>n.type==='input').props.onChange({target:{value:'*.cache'}});tree=render();assert.equal(dirty,true);button(tree,'Discard').props.onClick();tree=render();assert.equal(dirty,false);assert.equal(all(tree).find(n=>n.type==='input').props.value,'*.tmp');
 button(tree,'Reset template to defaults (empty)').props.onClick();tree=render();assert.equal(dirty,true);assert.equal(posted.length,0);button(tree,'Save template').props.onClick();await settle();tree=render();assert.equal(dirty,false);assert.deepEqual(JSON.parse(posted[0].body).rules,[]);
 button(tree,'Add rule').props.onClick();tree=render();all(tree).find(n=>n.type==='input').props.onChange({target:{value:'*.cache'}});tree=render();lose=true;button(tree,'Save template').props.onClick();await settle();tree=render();assert.equal(dirty,true);assert.equal(button(tree,'Discard').props.disabled,true);button(tree,'Retry unconfirmed save exactly').props.onClick();await settle();tree=render();assert.equal(dirty,false);assert.equal(posted[1].body,posted[2].body);assert.equal(posted[1].headers['Idempotency-Key'],posted[2].headers['Idempotency-Key']);
 }finally{for(const fn of cleanups)fn?.();Object.assign(React,saved);Object.assign(globalThis,env);}
});
