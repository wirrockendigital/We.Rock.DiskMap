import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Compile and exercise production parsing and component handlers without claiming native DSM evidence.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/guide-'));let guide;
try{mkdirSync(join(tmp,'features/settings'),{recursive:true});cpSync(join(web,'src/features/settings/display.tsx'),join(tmp,'features/settings/display.tsx'));cpSync(join(web,'src/contracts.ts'),join(tmp,'contracts.ts'));writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const f of ['regional-format.tsx','size-metrics.ts','SchedulerGuide.tsx','session.ts'])cpSync(join(web,'src',f),join(tmp,f));execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'SchedulerGuide.tsx')]);guide=createRequire(import.meta.url)(join(tmp,'out/SchedulerGuide.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
const job={id:'job1',profileId:'profile1',trigger:'cli',state:'completed',createdAt:'2020-01-01T00:00:00Z',resultSnapshotId:'snapshot1'};
const envelope=data=>new Response(JSON.stringify({contractVersion:1,data}));
const history={jobs:[{jobId:'job1',profileId:'profile1',trigger:'cli',createdAt:job.createdAt}],truncated:false};
// Receipt quality requires bound terminal provenance, not just a successful HTTP response.
test('guide receipt binding and partial semantics',()=>{assert.equal(guide.checkedSchedulerReceipt({job,snapshotState:'complete'},'profile1','job1').quality,'complete');assert.equal(guide.checkedSchedulerReceipt({job,snapshotState:'partial'},'profile1','job1').quality,'partial');assert.equal(guide.checkedSchedulerReceipt({job:{...job,state:'scanning'},snapshotState:'complete'},'profile1','job1').snapshotId,null);for(const patch of [{profileId:'other'},{id:'other'},{trigger:'ui'},{resultSnapshotId:123},{createdAt:'bad'}])assert.throws(()=>guide.checkedSchedulerReceipt({job:{...job,...patch},snapshotState:'complete'},'profile1','job1'));});
// The bounded history never presents an unrelated UI request as a scheduled invocation.
test('guide latest local history and strict scope',()=>{assert.equal(guide.latestLocalInvocation({...history,jobs:[...history.jobs,{...history.jobs[0],jobId:'ui1',trigger:'ui',createdAt:'2026-01-01T00:00:00Z'}]},'profile1'),'job1');assert.throws(()=>guide.latestLocalInvocation({...history,jobs:[{...history.jobs[0],profileId:'other'}]},'profile1'));assert.throws(()=>guide.latestLocalInvocation({...history,jobs:Array(51).fill(history.jobs[0])},'profile1'));});
// Localized guidance explicitly separates setup, observation and DSM-owned schedule state.
test('guide DE EN setup and inert rendering',()=>{for(const language of ['de','en']){const t=guide.schedulerGuideText[language];assert.equal(t.steps.length,7);const html=renderToStaticMarkup(React.createElement(guide.SchedulerGuide,{profileId:'profile1',language,onSessionDenied(){}}));assert.ok(html.includes('90080'));assert.ok(html.includes('root'));assert.ok(html.includes(language==='de'?'Paketkonto':'package account'));assert.ok(html.includes(language==='de'?'Sommerzeit':'daylight saving'));assert.ok(html.includes(language==='de'?'geschlossener':'closed'));assert.ok(html.includes(t.limits.replaceAll('’','’')));}});
// Streaming limits apply before parsing, and denied session bodies are never consumed.
test('guide bounded authenticated transport',async()=>{const oldFetch=globalThis.fetch,oldWindow=globalThis.window;try{globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'test'}}}}};let cancelled=false;globalThis.fetch=async()=>new Response(new ReadableStream({pull(c){c.enqueue(new Uint8Array(65537));},cancel(){cancelled=true;}}));await assert.rejects(()=>guide.schedulerObservationRead('history',new AbortController().signal),/OBSERVATION_LIMIT/);assert.ok(cancelled);globalThis.fetch=async()=>new Response('private',{status:403});await assert.rejects(()=>guide.schedulerObservationRead('history',new AbortController().signal),e=>e.constructor.name==='SessionDenied');}finally{globalThis.fetch=oldFetch;globalThis.window=oldWindow;}});
// Actual handlers abort profile-stale reads and distinguish old jobs from a newly marked test.
test('guide manual observation lifecycle and old test',async()=>{const saved={state:React.useState,ref:React.useRef,effect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window};const states=[],refs=[];let profileId='profile1',denied=0;
const render=(changed=false)=>{let i=0,j=0;const effects=[];React.useState=x=>{const n=i++;if(!(n in states))states[n]=x;return[states[n],v=>states[n]=v];};React.useRef=x=>refs[j++]??={current:x};React.useEffect=f=>{if(changed)effects.push(f);};const tree=guide.SchedulerGuide({profileId,language:'en',onSessionDenied:()=>denied++});for(const f of effects)f();return tree;};const buttons=x=>Array.isArray(x)?x.flatMap(buttons):React.isValidElement(x)?x.type==='button'?[x]:buttons(x.props.children):[];const tick=()=>new Promise(r=>setImmediate(r));
try{globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'test'}}}}};let calls=0;globalThis.fetch=async url=>{calls++;return envelope(url.includes('history')?history:{job,snapshotState:'complete'});};render(true);assert.equal(calls,0);buttons(render())[0].props.onClick();buttons(render())[1].props.onClick();await tick();await tick();assert.equal(calls,2);assert.equal(states[1].quality,'complete');states[1]={...states[1],quality:'none',snapshotId:null};assert.ok(renderToStaticMarkup(render()).includes(guide.schedulerGuideText.en.failed));assert.ok(!renderToStaticMarkup(render()).includes(guide.schedulerGuideText.en.pending));assert.ok(renderToStaticMarkup(render()).includes(guide.schedulerGuideText.en.old));let resolve;globalThis.fetch=()=>new Promise(r=>resolve=r);buttons(render())[1].props.onClick();profileId='other';render(true);resolve(envelope(history));await tick();assert.equal(states[1],null);assert.equal(states[4],null);globalThis.fetch=async()=>new Response('',{status:401});buttons(render())[1].props.onClick();await tick();assert.equal(denied,1);assert.equal(states[1],null);}finally{refs[0]?.current?.abort();React.useState=saved.state;React.useRef=saved.ref;React.useEffect=saved.effect;globalThis.fetch=saved.fetch;globalThis.window=saved.window;}});
// Dependency-aware effects prove that a local language change preserves bound observation state.
test('guide local language switches instructions and receipt without resetting the test',async()=>{
 const saved={state:React.useState,ref:React.useRef,effect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window};
 const values=[],refs=[],effects=[];let language='de',profileId='profile1',calls=0;
 const render=()=>{let si=0,ri=0,ei=0;const pending=[];
  React.useState=initial=>{const n=si++;if(!(n in values))values[n]=initial;return[values[n],next=>{values[n]=next;}];};
  React.useRef=initial=>refs[ri++]??={current:initial};
  React.useEffect=(run,deps)=>{const n=ei++,previous=effects[n];if(!previous||deps.some((d,i)=>!Object.is(d,previous.deps[i])))pending.push(()=>{previous?.cleanup?.();effects[n]={deps,cleanup:run()};});};
  const tree=guide.SchedulerGuide({profileId,language,onSessionDenied(){}});for(const run of pending)run();return tree;
 };
 const elements=(tree,type)=>Array.isArray(tree)?tree.flatMap(v=>elements(v,type)):React.isValidElement(tree)?tree.type===type?[tree]:elements(tree.props.children,type):[];
 const html=()=>renderToStaticMarkup(render());const tick=()=>new Promise(resolve=>setImmediate(resolve));
 try{
  globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'test'}}}}};globalThis.fetch=async url=>{calls++;return envelope(url.includes('history')?history:{job,snapshotState:'complete'});};
  render();elements(render(),'button')[0].props.onClick();elements(render(),'button')[1].props.onClick();await tick();await tick();
  const marker=values[4],receipt=values[1];assert.ok(marker);assert.equal(receipt.profileId,'profile1');assert.ok(html().includes(guide.schedulerGuideText.de.complete));
  elements(render(),'select')[0].props.onChange({target:{value:'en'}});
  assert.ok(html().includes('daylight saving'));assert.ok(html().includes(guide.schedulerGuideText.en.complete));assert.ok(html().includes(guide.schedulerGuideText.en.old));assert.equal(values[4],marker);assert.equal(values[1],receipt);assert.equal(calls,2);
  elements(render(),'select')[0].props.onChange({target:{value:'fr'}});assert.equal(values[0],'en');
  // An actual parent prop transition synchronizes the local choice without clearing the receipt.
  language='en';render();language='de';render();assert.ok(html().includes(guide.schedulerGuideText.de.complete));assert.equal(values[4],marker);assert.equal(values[1],receipt);
  profileId='other';render();assert.equal(values[4],null);assert.equal(values[1],null);assert.equal(calls,2);
 }finally{for(const effect of effects)effect?.cleanup?.();React.useState=saved.state;React.useRef=saved.ref;React.useEffect=saved.effect;globalThis.fetch=saved.fetch;globalThis.window=saved.window;}
});
