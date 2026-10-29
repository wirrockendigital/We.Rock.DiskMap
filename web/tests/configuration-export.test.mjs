import {test} from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
// Compile the real transport and component with only their declared dependencies.
const web=fileURLToPath(new URL('../',import.meta.url)),tmp=mkdtempSync(join(web,'.export-test-'));let Client,checked,UI,limit,envelopeLimit,SessionDenied;
try{mkdirSync(join(tmp,'features/settings'),{recursive:true});writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const f of ['contracts.ts','profile-actions.ts','session.ts','features/settings/configuration-export-client.ts','features/settings/ConfigurationExport.tsx'])cpSync(join(web,'src',f),join(tmp,f));execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--strict','--skipLibCheck','--jsx','react-jsx','--target','ES2022','--module','Node16','--moduleResolution','Node16','--outDir',join(tmp,'out'),join(tmp,'features/settings/ConfigurationExport.tsx')],{stdio:'pipe'});const require=createRequire(import.meta.url),m=require(join(tmp,'out/features/settings/configuration-export-client.js'));SessionDenied=require(join(tmp,'out/session.js')).SessionDenied;Client=m.ConfigurationExportClient;checked=m.checkedConfigurationBundle;limit=m.configurationBundleLimit;envelopeLimit=m.configurationEnvelopeLimit;UI=require(join(tmp,'out/features/settings/ConfigurationExport.js')).ConfigurationExport;}finally{rmSync(tmp,{recursive:true,force:true});}
const fixture=()=>({formatVersion:1,exportedAt:'2026-10-11T00:00:00Z',profiles:[{sourceProfileId:'p',configuration:{name:'Privat <Familie> 東京',scope:{kind:'shares',targetIds:['share-1']},active:true,exclusions:[{kind:'path',value:'private/folder'}],resourceMode:'custom',limits:{workers:2,ioBytesPerSecond:'9007199254740993',cpuPercent:35},freshnessSeconds:86400}}],global:{exclusionTemplate:{rules:[{kind:'pattern',value:'*.tmp'}]},retentionCompleteCount:2,fileTypeMapping:{version:1,revision:1,assignments:[{extension:'txt',category:'document'}]}},personal:{appearance:{language:'en',theme:'auto',map:'radial',locale:null,timeZone:null},display:{units:'binary',colorMode:'mtime',detail:4,labels:false,percentages:true,hierarchyBorders:'subtle',reducedMotion:'reduce'},preferredProfileRef:'deleted-source-profile'}});
const response=b=>new Response(JSON.stringify({contractVersion:1,data:{bundle:b}}));
test('explicit GET exports only exact portable bundle including custom intent and deleted preference reference',async()=>{
 const large=fixture();large.profiles[0].configuration.limits.ioBytesPerSecond='9'.repeat(129);assert.equal(checked(large).profiles[0].configuration.limits.ioBytesPerSecond.length,129);large.global.fileTypeMapping.assignments=[{extension:'x\uFEFF',category:'document'}];assert.equal(checked(large).global.fileTypeMapping.assignments[0].extension,'x\uFEFF');large.global.fileTypeMapping.assignments[0].extension='x\u0085';assert.throws(()=>checked(large),/INVALID/);
 const calls=[],original=fixture(),c=new Client(async(url,options)=>{calls.push({url,options});return response(original);},()=> 'fixture-token');assert.equal(calls.length,0);const result=await c.read();assert.equal(calls.length,1);assert.equal(calls[0].options.method,'GET');assert.equal(calls[0].options.cache,'no-store');assert.equal(calls[0].options.redirect,'error');assert.equal(calls[0].options.headers['X-SYNO-TOKEN'],'fixture-token');assert.equal(calls[0].options.body,undefined);assert.deepEqual(JSON.parse(result.json),original);assert.equal(result.bundle.profiles[0].configuration.limits.ioBytesPerSecond,'9007199254740993');assert.doesNotMatch(result.json,/fixture-token|dsmUserId|lastView/);
});
test('private extension fields are rejected throughout the nested export rather than silently downloaded',()=>{
 const paths=[[],['personal'],['personal','appearance'],['personal','display'],['global'],['global','exclusionTemplate'],['global','exclusionTemplate','rules',0],['global','fileTypeMapping'],['global','fileTypeMapping','assignments',0],['profiles',0],['profiles',0,'configuration'],['profiles',0,'configuration','scope'],['profiles',0,'configuration','limits'],['profiles',0,'configuration','exclusions',0]];
 for(const path of paths){const b=fixture();let value=b;for(const part of path)value=value[part];value.privateSecret='must-not-export';assert.throws(()=>checked(b),/INVALID/);}
});
test('version, counts, uniqueness and complete personal fields fail closed',()=>{
 for(const mutate of [b=>b.formatVersion=2,b=>b.profiles=Array(201).fill(b.profiles[0]),b=>b.profiles.push(structuredClone(b.profiles[0])),b=>b.profiles[0].configuration.scope.targetIds.push('share-1'),b=>b.personal.display.detail='4',b=>delete b.personal.display.labels,b=>b.global.fileTypeMapping.assignments.push({extension:'txt',category:'image'}),b=>delete b.personal.appearance.timeZone]){const b=fixture();mutate(b);assert.throws(()=>checked(b));}
});
test('UTF8 stream envelope bound is enforced independently of bundle bound',async()=>{
 assert.equal(limit,4194304);assert.equal(envelopeLimit,4198400);
 let cancelled=false;const stream=new ReadableStream({start(c){c.enqueue(new Uint8Array(envelopeLimit));c.enqueue(new Uint8Array(1));},cancel(){cancelled=true;}});
 await assert.rejects(new Client(async()=>new Response(stream),()=> 't').read(),/TOO_LARGE/);assert.equal(cancelled,true);
 // Valid small bundles can arrive with transport whitespace up to the exact envelope limit.
 const raw=JSON.stringify({contractVersion:1,data:{bundle:fixture()}}),padding=' '.repeat(envelopeLimit-new TextEncoder().encode(raw).length);
 assert.equal((await new Client(async()=>new Response(raw+padding),()=> 't').read()).bundle.formatVersion,1);
 const b=fixture();b.profiles=[];
 for(let i=0;i<200;i++){const p=structuredClone(fixture().profiles[0]);p.sourceProfileId='p'+i;p.configuration.exclusions=Array.from({length:4},(_,j)=>({kind:'pattern',value:'界'.repeat(1300)+j}));b.profiles.push(p);}
 // Escaped JSON contributes to the real encoded budget, rather than character count.
 for(const p of b.profiles)p.configuration.name='界'.repeat(300);
 assert.ok(new TextEncoder().encode(JSON.stringify(b)).length<limit);assert.equal(checked(b).profiles.length,200);
 for(const p of b.profiles)p.configuration.scope.targetIds=Array.from({length:256},(_,i)=>'target-'+i+'x'.repeat(100));
 assert.ok(new TextEncoder().encode(JSON.stringify(b)).length>limit);assert.throws(()=>checked(b),/TOO_LARGE/);
});
test('timeout, disposal and token changes suppress late success and late authentication',async()=>{
 for(const kind of ['dispose','token','timeout'])for(const status of [200,403]){let resolve,token='first';const timer=new AbortController(),c=new Client(()=>new Promise(r=>resolve=r),()=>token,()=>timer.signal);const pending=c.read();if(kind==='dispose')c.dispose();if(kind==='token')token='second';if(kind==='timeout')timer.abort();resolve(status===200?response(fixture()):new Response('',{status}));await assert.rejects(pending,e=>!(e instanceof SessionDenied));}
});
test('live authentication and safe errors contain no server diagnostics',async()=>{
 for(const status of [401,403])await assert.rejects(new Client(async()=>new Response('private',{status}),()=> 't').read(),e=>e instanceof SessionDenied);
 for(const [status,code] of [[413,'CONFIGURATION_TOO_LARGE'],[503,'CONFIGURATION_UNAVAILABLE']])await assert.rejects(new Client(async()=>new Response('private/path/token',{status}),()=> 't').read(),new RegExp(code));
 await assert.rejects(new Client(async()=>new Response(new Uint8Array([0xff])),()=> 't').read(),/CONFIGURATION_INVALID/);
});
test('DE and EN warn about private names and paths without exporting on render',()=>{
 for(const language of ['de','en']){const html=renderToStaticMarkup(React.createElement(UI,{language,disabled:false,sessionIdentity:'s',onSessionDenied(){}}));assert.match(html,language==='de'?/nicht anonym/:/not anonymous/);assert.match(html,language==='de'?/Profilnamen und Pfade/:/Profile names and paths/);assert.doesNotMatch(html,/download=/);}
});
// Drive real component handlers to verify explicit preparation, URL cleanup and stale-session suppression.
test('download lifetime is explicit and bound to session, disabled state and unmount',async()=>{
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect},env={fetch:globalThis.fetch,window:globalThis.window},create=URL.createObjectURL,revoke=URL.revokeObjectURL;
 const slots=[],effects=[],cleanups=[];let at=0,calls=0,resolve,blob,revoked=[];
 React.useState=x=>{const i=at++;if(!(i in slots))slots[i]=typeof x==='function'?x():x;return[slots[i],v=>slots[i]=typeof v==='function'?v(slots[i]):v];};React.useRef=x=>{const i=at++;return slots[i]??={current:x};};React.useEffect=(fn,deps)=>{const i=at++,old=slots[i];if(!old||deps.some((v,j)=>!Object.is(v,old[j]))){slots[i]=deps;effects.push(()=>{cleanups[i]?.();cleanups[i]=fn();});}};
 globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'test'}}}}};globalThis.fetch=async()=>{calls++;return new Promise(r=>resolve=r);};URL.createObjectURL=value=>{blob=value;return 'blob:qualified';};URL.revokeObjectURL=value=>revoked.push(value);
 let denied=0;const props={language:'en',disabled:false,sessionIdentity:'one',onSessionDenied(){denied++;}},render=()=>{at=0;const out=UI(props);for(const f of effects.splice(0))f();return out;},all=n=>!n||typeof n!=='object'?[]:[n,...React.Children.toArray(n.props?.children).flatMap(all)],button=t=>all(t).find(n=>n.type==='button'&&n.props.children==='Prepare export file'),settle=()=>new Promise(r=>setImmediate(r));
 try{let t=render();assert.equal(calls,0);button(t).props.onClick();button(t).props.onClick();assert.equal(calls,1);resolve(response(fixture()));await settle();t=render();const link=all(t).find(n=>n.type==='a');assert.equal(link.props.download,'we-rock-diskmap-configuration.json');assert.deepEqual(JSON.parse(await blob.text()),fixture());
 const oldPrepare=button(t);props.sessionIdentity='two';t=render();oldPrepare.props.onClick();assert.equal(calls,1,'stale session handler cannot export for the new session');assert.equal(all(t).filter(n=>n.type==='a').length,0);assert.ok(revoked.includes('blob:qualified'));let prevented=false;link.props.onClick({preventDefault(){prevented=true;}});assert.equal(prevented,true);
 button(render()).props.onClick();props.disabled=true;render();resolve(new Response('',{status:403}));await settle();assert.equal(denied,0);assert.equal(all(render()).filter(n=>n.type==='a').length,0);
 props.disabled=false;render();button(render()).props.onClick();resolve(response(fixture()));await settle();render();for(const f of cleanups)f?.();assert.ok(revoked.length>=2);
 }finally{Object.assign(React,saved);Object.assign(globalThis,env);URL.createObjectURL=create;URL.revokeObjectURL=revoke;}
});
