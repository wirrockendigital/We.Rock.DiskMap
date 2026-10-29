import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
// Compile the actual presentation and preference code against the installed runtime.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/regional-'));let format,language,client,settingsUI,regionalAPI;
try{writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');cpSync(join(web,'src'),join(tmp,'src'),{recursive:true});execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),...['regional-format.tsx','dsm-capabilities.ts','preference-client.ts','RegionalSettings.tsx','regional-context.ts'].map(f=>join(tmp,'src',f))],{stdio:'pipe'});const require=createRequire(import.meta.url);format=require(join(tmp,'out/regional-format.js'));language=require(join(tmp,'out/dsm-capabilities.js'));client=require(join(tmp,'out/preference-client.js'));settingsUI=require(join(tmp,'out/RegionalSettings.js'));regionalAPI=require(join(tmp,'out/regional-context.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
test('manual DSM browser English order distinguishes absent and unsupported qualified DSM',()=>{
 assert.equal(language.resolveLanguage('de','en-US',{qualified:true,language:'enu'}).language,'de');
 assert.equal(language.resolveLanguage('auto','en-US',{qualified:true,language:'ger'}).language,'de');
 assert.equal(language.resolveLanguage('auto','de-DE',{qualified:true,language:'unsupported'}).language,'en');
 assert.equal(language.resolveLanguage('auto','de-DE',{qualified:false,language:'unsupported'}).language,'de');
 assert.equal(language.resolveLanguage('auto',undefined).language,'en');
});
test('exact large integers and GB GiB remain independent from locale',()=>{
 assert.equal(format.regionalInteger('9007199254740993','de-DE'),'9.007.199.254.740.993');
 assert.equal(format.regionalInteger('9007199254740993','en-US'),'9,007,199,254,740,993');
 assert.equal(format.regionalSize('1073741824','GiB','de-DE'),'1,00 GiB');
 assert.equal(format.regionalSize('1073741824','GB','en-US'),'1.07 GB');
 assert.equal(format.regionalInteger(null,'en-US'),'—');
});
test('NAS default personal override unknown UTC and DST gap overlap are explicit',()=>{
 const settings=format.regionalSettings('en',null,null,'Europe/Amsterdam');assert.equal(settings.zoneSource,'system-zoneinfo');
 assert.equal(format.regionalSettings('en',null,'UTC','Europe/Amsterdam').zoneSource,'personal');
 assert.equal(format.regionalSettings('de',null,null,null).zoneSource,'unavailable');
 const before=format.regionalTimestamp('2026-03-29T00:30:00Z',settings),after=format.regionalTimestamp('2026-03-29T01:30:00Z',settings);
 assert.match(before,/01:30:00.*GMT\+01:00/);assert.match(after,/03:30:00.*GMT\+02:00/);
 const first=format.regionalTimestamp('2026-10-25T00:30:00Z',settings),second=format.regionalTimestamp('2026-10-25T01:30:00Z',settings);
 assert.match(first,/02:30:00.*GMT\+02:00/);assert.match(second,/02:30:00.*GMT\+01:00/);assert.notEqual(first,second);
 assert.equal(format.regionalTimestamp('2026-10-25T02:30:00',settings),'—');
});
const pref=()=>({version:2,display:{units:'decimal',colorMode:'folder',detail:'auto',labels:true,percentages:true,hierarchyBorders:'normal',reducedMotion:'auto'},dsmUserId:'a',revision:4,lastView:{profileId:'p',snapshotId:'s',rootId:'nas',selectedNodeId:null,map:'radial',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'name',direction:'asc'},language:'de',theme:'dark',preferredProfileId:'p',map:'radial',locale:'de-DE',timeZone:'Europe/Amsterdam'});
const response=data=>new Response(JSON.stringify({contractVersion:1,data}));
test('uncertain regional change replays exact intent and preserves unrelated personal settings',async()=>{
 const original=pref(),calls=[],choice={language:'en',locale:'en-GB',timeZone:'UTC'};let first=true;
 const c=new client.PreferenceClient(async(u,o)=>{calls.push(o);if(first){first=false;throw Error('lost');}return response({...original,...choice,revision:5});},()=> 'token',()=> 'regional-key');c.adopt(original);
 await assert.rejects(c.writeRegional(choice));await assert.rejects(c.writeRegional({...choice,timeZone:null}),/PREFERENCE_PENDING/);
 const saved=await c.writeRegional(choice);assert.equal(calls[0].body,calls[1].body);assert.equal(calls[0].headers['Idempotency-Key'],calls[1].headers['Idempotency-Key']);assert.equal(saved.theme,'dark');assert.equal(saved.map,'radial');assert.equal(saved.preferredProfileId,'p');assert.deepEqual(saved.lastView,original.lastView);
});
test('reset affects only regional group and CAS conflict cannot silently overwrite',async()=>{
 const original=pref(),choice={language:'auto',locale:null,timeZone:null};
 const c=new client.PreferenceClient(async(u,o)=>{const request=JSON.parse(o.body);assert.equal(request.appearance.theme,'dark');assert.equal(request.appearance.map,'radial');return response({...original,...choice,revision:5});},()=> 'token');c.adopt(original);assert.equal((await c.writeRegional(choice)).preferredProfileId,'p');
 const conflict=new client.PreferenceClient(async()=>new Response(JSON.stringify({contractVersion:1,error:{code:'PREFERENCE_CONFLICT'}}),{status:409}),()=> 'token');conflict.adopt(original);await assert.rejects(conflict.writeRegional(choice),/PREFERENCE_CONFLICT/);await assert.rejects(conflict.writeRegional(choice),/RELOAD_REQUIRED/);
});

// A context change reformats existing values only; the presenter never dispatches a request.
test('live confirmed format provider changes presentation without altering canonical timestamp or bytes',()=>{
 const value='9007199254740993',instant='2026-10-25T00:30:00Z';const render=settings=>renderToStaticMarkup(React.createElement(format.RegionalContext.Provider,{value:settings},React.createElement(React.Fragment,null,React.createElement(format.RegionalNumber,{value}),React.createElement(format.RegionalTime,{value:instant}))));
 const de=render(format.regionalSettings('de','de-DE',null,'Europe/Amsterdam')),en=render(format.regionalSettings('en','en-US','UTC','Europe/Amsterdam'));
 assert.match(de,/9\.007\.199\.254\.740\.993/);assert.match(en,/9,007,199,254,740,993/);assert.ok(de.includes(instant)&&en.includes(instant));assert.match(de,/Europe\/Amsterdam/);assert.match(en,/\[UTC\]/);
 assert.equal(renderToStaticMarkup(React.createElement(format.RegionalPercentage,{value:'33.33 %',language:'de'})),'33,33 %');
 assert.equal(renderToStaticMarkup(React.createElement(format.RegionalPercentage,{value:'33.33 %',language:'en'})),'33.33 %');
});
// The form owns a draft, but not another preferences store or save scheduler.
test('regional form preserves local draft across unrelated revision and exposes explicit discard reset save',async()=>{
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect},slots=[],effects=[];let at=0,dirty=false,writes=[];
 React.useState=initial=>{const i=at++;if(!(i in slots))slots[i]=typeof initial==='function'?initial():initial;return[slots[i],v=>slots[i]=typeof v==='function'?v(slots[i]):v];};
 React.useRef=initial=>{const i=at++;return slots[i]??=( {current:initial});};
 React.useEffect=(fn,deps)=>{const i=at++,old=slots[i];if(!old||deps.some((v,j)=>!Object.is(v,old[j]))){slots[i]=deps;effects.push(fn);}};
 let value=pref();const render=()=>{at=0;const tree=settingsUI.RegionalSettings({value,language:'en',disabled:false,nasTimeZone:'Europe/Amsterdam',onSave:async v=>writes.push(v),onDirty:v=>dirty=v});for(const effect of effects.splice(0))effect();return tree;};
 const all=node=>!node||typeof node!=='object'?[]:[node,...React.Children.toArray(node.props?.children).flatMap(all)];
 try{let tree=render();all(tree).find(n=>n.type==='select').props.onChange({target:{value:'en'}});render();assert.equal(dirty,true);value={...value,revision:5,lastView:null};render();tree=render();assert.equal(all(tree).find(n=>n.type==='select').props.value,'en');
 all(tree).find(n=>n.type==='button'&&n.props.children==='Save').props.onClick();await Promise.resolve();assert.deepEqual(writes,[{language:'en',locale:'de-DE',timeZone:'Europe/Amsterdam'}]);
 all(tree).find(n=>n.type==='button'&&n.props.children==='Discard').props.onClick();render();assert.equal(dirty,false);
 tree=render();all(tree).find(n=>n.type==='button'&&n.props.children==='Reset language and formats to defaults').props.onClick();render();assert.equal(dirty,true);assert.equal(writes.length,1,'reset is an explicit draft until Save');
 }finally{Object.assign(React,saved);}
});
test('regional context rejects malformed oversized and late denied responses',async()=>{
 const old={fetch:globalThis.fetch,window:globalThis.window};globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};
 try{for(const data of [{version:1,nasTimeZone:'Europe/Amsterdam',timeZoneSource:'system-zoneinfo'},{version:1,nasTimeZone:null,timeZoneSource:'unavailable'}]){globalThis.fetch=async()=>response(data);assert.deepEqual(await regionalAPI.readRegionalContext(new AbortController().signal),data);}
 for(const data of [{version:1,nasTimeZone:'Europe/Amsterdam',timeZoneSource:'unavailable'},{version:1,nasTimeZone:'invalid',timeZoneSource:'system-zoneinfo'}]){globalThis.fetch=async()=>response(data);await assert.rejects(regionalAPI.readRegionalContext(new AbortController().signal));}
 globalThis.fetch=async()=>new Response('x'.repeat(4097));await assert.rejects(regionalAPI.readRegionalContext(new AbortController().signal),/INVALID_REGIONAL/);
 const owner=new AbortController();globalThis.fetch=async()=>{owner.abort();return new Response('',{status:403});};await assert.rejects(regionalAPI.readRegionalContext(owner.signal),e=>e.name==='AbortError');
 }finally{Object.assign(globalThis,old);}
});

// Separate client lifetimes keep principal preferences separate; backend authorization is covered by Go tests.
test('two personal preference clients reopen independently and reject a foreign receipt',async()=>{
 const a=pref(),b={...pref(),dsmUserId:'b',language:'en',locale:'en-GB',timeZone:'UTC'};
 const ca=new client.PreferenceClient(async()=>response(a),()=> 'a'),cb=new client.PreferenceClient(async()=>response(b),()=> 'b');
 assert.equal((await ca.read()).language,'de');assert.equal((await cb.read()).language,'en');ca.dispose();
 const reopened=new client.PreferenceClient(async()=>response(a),()=> 'a');assert.equal((await reopened.read()).timeZone,'Europe/Amsterdam');assert.equal(cb.preference.timeZone,'UTC');
 const foreign=new client.PreferenceClient(async()=>response({...b,revision:5}),()=> 'a');foreign.adopt(a);await assert.rejects(foreign.writeRegional({language:'en',locale:'en-GB',timeZone:'UTC'}),/INVALID_PREFERENCE_RESPONSE/);assert.equal(foreign.status,'uncertain');assert.equal(foreign.preference.dsmUserId,'a');
});
