import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Compile the production component; mocked transport only tests lifecycle and never claims DSM acceptance.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/scheduler-'));let component;
try{mkdirSync(join(tmp,'features/settings'),{recursive:true});cpSync(join(web,'src/features/settings/display.tsx'),join(tmp,'features/settings/display.tsx'));cpSync(join(web,'src/contracts.ts'),join(tmp,'contracts.ts'));writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const f of ['regional-format.tsx','size-metrics.ts','SchedulerTemplate.tsx','SchedulerGuide.tsx','SchedulerStatus.tsx','session.ts'])cpSync(join(web,'src',f),join(tmp,f));execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'SchedulerTemplate.tsx')]);component=createRequire(import.meta.url)(join(tmp,'out/SchedulerTemplate.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
const command=id=>['/var/packages/we-rock-diskmap/target/bin/diskmapctl','--socket=/var/packages/we-rock-diskmap/var/run/control.sock','--protocol-version=1','--exit-contract-version=1','scan',`--profile-id=${id}`,'--mode=reconcile'].map(x=>`'${x}'`).join(' ');
const info=(id='stable')=>({templateVersion:1,profileId:id,command:command(id),mode:'reconcile',exitContractVersion:1,dsmBuild:'90080',qualification:'package-kernel-peer',executionAccount:'root'});
// Copy validation rejects injection and selection mismatches.
test('scheduler exact command and account boundary',()=>{assert.equal(component.checkedSchedulerTemplate(info(),'stable').command,command('stable'));for(const patch of [{command:command('stable')+'; evil'},{profileId:'other'},{executionAccount:'ordinary-user'},{executionAccount:'we-rock-diskmap'},{executionAccount:''},{dsmBuild:'99999'},{mode:'full'}])assert.throws(()=>component.checkedSchedulerTemplate({...info(),...patch},'stable'));assert.throws(()=>component.checkedSchedulerTemplate(info('a.b'),'a.b'));assert.equal(component.checkedSchedulerTemplate({...info(),dsmBuild:'99999',qualification:'unqualified',executionAccount:null},'stable').executionAccount,null);});
// Oversized responses stop before unbounded buffering.
test('scheduler bounded response',async()=>{let cancelled=false;const body=new ReadableStream({pull(c){c.enqueue(new Uint8Array(9000));},cancel(){cancelled=true;}});await assert.rejects(()=>component.readSchedulerTemplate(new Response(body)),/TEMPLATE_LIMIT/);assert.equal(cancelled,true);});
// Both localized entry points remain inert during rendering.
test('scheduler localized initial rendering',()=>{for(const language of ['de','en']){const html=renderToStaticMarkup(React.createElement(component.SchedulerTemplate,{profileId:'stable',language,onSessionDenied:()=>{}}));assert.ok(html.includes(language==='de'?'Aufrufvorlage laden':'Load command template'));assert.ok(!html.includes('root'));}});
// Actual component handlers preserve exact clipboard text and suppress late responses after profile changes.
test('scheduler copy and profile switch lifecycle',async()=>{
 const saved={state:React.useState,ref:React.useRef,effect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window,navigator:Object.getOwnPropertyDescriptor(globalThis,'navigator')};const states=[],refs=[];let profileId='stable',effects=[],copied='',denied=0;
 const render=(changed=false)=>{let i=0,j=0;React.useState=x=>{const n=i++;if(!(n in states))states[n]=x;return[states[n],v=>states[n]=v];};React.useRef=x=>refs[j++]??={current:x};React.useEffect=f=>{if(changed)effects.push(f);};const tree=component.SchedulerTemplate({profileId,language:'en',onSessionDenied:()=>denied++});for(const f of effects.splice(0))f();return tree;};
 const buttons=x=>Array.isArray(x)?x.flatMap(buttons):React.isValidElement(x)?x.type==='button'?[x]:buttons(x.props.children):[];
 try{globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'synthetic'}}}}};Object.defineProperty(globalThis,'navigator',{configurable:true,value:{clipboard:{writeText:async x=>{copied=x;}}}});globalThis.fetch=async()=>new Response(JSON.stringify({contractVersion:1,data:info()}));buttons(render(true))[0].props.onClick();await new Promise(r=>setImmediate(r));buttons(render())[1].props.onClick();await new Promise(r=>setImmediate(r));assert.equal(copied,command('stable'));assert.equal(states[2],'copied');
 let resolve;globalThis.fetch=()=>new Promise(r=>resolve=r);buttons(render())[0].props.onClick();profileId='other';render(true);resolve(new Response(JSON.stringify({contractVersion:1,data:info()})));await new Promise(r=>setImmediate(r));assert.equal(states[0],null);assert.equal(states[1],false);assert.equal(buttons(render()).length,1);
 globalThis.fetch=async()=>new Response('private',{status:403});buttons(render())[0].props.onClick();await new Promise(r=>setImmediate(r));assert.equal(denied,1);assert.equal(states[0],null);
 }finally{refs[0]?.current?.abort();React.useState=saved.state;React.useRef=saved.ref;React.useEffect=saved.effect;globalThis.fetch=saved.fetch;if(saved.window===undefined)delete globalThis.window;else globalThis.window=saved.window;if(saved.navigator)Object.defineProperty(globalThis,'navigator',saved.navigator);else delete globalThis.navigator;}
});
