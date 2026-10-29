import React from 'react';
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
// Exercise actual compiled preferences and settings, without a separate appearance store.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/theme-'));let ui,client,resolveTheme;
try{writeFileSync(join(tmp,'package.json'),' {"type":"commonjs"}');cpSync(join(web,'src'),join(tmp,'src'),{recursive:true});execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),...['theme.tsx','preference-client.ts'].map(f=>join(tmp,'src',f))]);const require=createRequire(import.meta.url);ui=require(join(tmp,'out/theme.js'));client=require(join(tmp,'out/preference-client.js'));({resolveTheme}=require(join(tmp,'out/dsm-capabilities.js')));}finally{rmSync(tmp,{recursive:true,force:true});}
const pref={version:2,display:{units:'decimal',colorMode:'folder',detail:'auto',labels:true,percentages:true,hierarchyBorders:'normal',reducedMotion:'auto'},dsmUserId:'a',revision:4,lastView:null,language:'de',theme:'auto',preferredProfileId:null,map:'radial',locale:'de-DE',timeZone:'Europe/Amsterdam'};
const response=data=>new Response(JSON.stringify({contractVersion:1,data}));
test('manual light and dark override system; auto follows changes and unknown is explicit light',()=>{
 assert.deepEqual(resolveTheme('light',true),{theme:'light',source:'manual'});assert.deepEqual(resolveTheme('dark',false),{theme:'dark',source:'manual'});
 assert.deepEqual(resolveTheme('auto',true),{theme:'dark',source:'browser-system'});assert.deepEqual(resolveTheme('auto',false),{theme:'light',source:'browser-system'});assert.deepEqual(resolveTheme('auto',null),{theme:'light',source:'fallback'});
});
test('uncertain exact theme retry preserves language regional map and last view',async()=>{
 let first=true;const calls=[];const c=new client.PreferenceClient(async(u,o)=>{calls.push(o);if(first){first=false;throw Error('lost');}return response({...pref,theme:'dark',revision:5});},()=> 'token',()=> 'same-key');c.adopt(pref);
 await assert.rejects(c.writeTheme('dark'));assert.equal(c.status,'uncertain');assert.equal(c.preference.theme,'auto');await assert.rejects(c.writeTheme('light'),/PREFERENCE_PENDING/);
 const saved=await c.writeTheme('dark');assert.equal(calls[0].body,calls[1].body);assert.equal(calls[0].headers['Idempotency-Key'],calls[1].headers['Idempotency-Key']);assert.equal(saved.theme,'dark');assert.equal(saved.locale,pref.locale);assert.equal(saved.timeZone,pref.timeZone);assert.equal(saved.map,pref.map);
});
test('conflict requires reread; foreign receipt and invalid choice cannot apply',async()=>{
 const c=new client.PreferenceClient(async()=>new Response(JSON.stringify({contractVersion:1,error:{code:'PREFERENCE_CONFLICT'}}),{status:409}),()=> 'token');c.adopt(pref);
 await assert.rejects(c.writeTheme('neon'),/INVALID_REQUEST/);assert.equal(c.status,'idle');await assert.rejects(c.writeTheme('dark'),/PREFERENCE_CONFLICT/);await assert.rejects(c.writeTheme('light'),/RELOAD_REQUIRED/);
 const foreign=new client.PreferenceClient(async()=>response({...pref,dsmUserId:'b',theme:'dark',revision:5}),()=> 'token');foreign.adopt(pref);await assert.rejects(foreign.writeTheme('dark'),/INVALID_PREFERENCE_RESPONSE/);assert.equal(foreign.preference.theme,'auto');
});
for(const language of ['de','en'])test(`theme ${language} draft discard reset and confirmation are explicit`,async()=>{
 const old={useState:React.useState,useEffect:React.useEffect,useContext:React.useContext};const slots=[],effects=[];let at=0,dirty=false,writes=[];let value=pref;
 React.useState=init=>{const i=at++;if(!(i in slots))slots[i]=typeof init==='function'?init():init;return[slots[i],v=>slots[i]=typeof v==='function'?v(slots[i]):v];};React.useEffect=(f,deps)=>{const i=at++;if(!slots[i]||deps.some((v,j)=>!Object.is(v,slots[i][j]))){slots[i]=deps;effects.push(f);}};React.useContext=()=>({theme:'light',source:'browser-system'});
 const all=n=>!n||typeof n!=='object'?[]:[n,...React.Children.toArray(n.props?.children).flatMap(all)];const render=()=>{at=0;const t=ui.ThemeSettings({value,language,disabled:false,onSave:async t=>writes.push(t),onDirty:v=>dirty=v});effects.splice(0).forEach(f=>f());return t;};
 try{let t=render();all(t).find(n=>n.type==='select').props.onChange({target:{value:'dark'}});t=render();assert.equal(dirty,true);assert.equal(writes.length,0);value={...pref,revision:5};t=render();assert.equal(all(t).find(n=>n.type==='select').props.value,'dark');all(t).filter(n=>n.type==='button')[1].props.onClick();render();assert.equal(dirty,false);
 t=render();all(t).find(n=>n.type==='select').props.onChange({target:{value:'dark'}});t=render();all(t).filter(n=>n.type==='button')[0].props.onClick();await Promise.resolve();assert.deepEqual(writes,['dark']);value={...pref,theme:'dark',revision:6};render();t=render();assert.equal(dirty,false);all(t).filter(n=>n.type==='button')[2].props.onClick();render();assert.equal(dirty,true);assert.deepEqual(writes,['dark']);
 }finally{Object.assign(React,old);}
});
// Session teardown removes the personal DOM override and detaches its live system listener.
test('theme provider cleans up session and does not treat unavailable system signal as dark',()=>{
 const old={useState:React.useState,useEffect:React.useEffect,window:globalThis.window,document:globalThis.document};const effects=[],cleanups=[];let removed=0;
 React.useState=()=>[undefined,()=>{}];React.useEffect=f=>effects.push(f);globalThis.document={documentElement:{dataset:{}}};globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){removed++;}})};
 try{const got=ui.useResolvedTheme('dark');effects.forEach(f=>cleanups.push(f()));assert.deepEqual(got,{theme:'dark',source:'manual'});assert.equal(document.documentElement.dataset.theme,'dark');cleanups.forEach(f=>f?.());assert.equal(document.documentElement.dataset.theme,undefined);assert.equal(removed,1);}finally{React.useState=old.useState;React.useEffect=old.useEffect;globalThis.window=old.window;globalThis.document=old.document;}
});
