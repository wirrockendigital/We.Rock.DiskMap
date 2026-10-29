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
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/integrity-'));let component;
try{mkdirSync(join(tmp,'features/settings'),{recursive:true});cpSync(join(web,'src/features/settings/display.tsx'),join(tmp,'features/settings/display.tsx'));cpSync(join(web,'src/contracts.ts'),join(tmp,'contracts.ts'));writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');for(const f of ['regional-format.tsx','size-metrics.ts','IntegritySettings.tsx','session.ts'])cpSync(join(web,'src',f),join(tmp,f));execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'IntegritySettings.tsx')]);component=createRequire(import.meta.url)(join(tmp,'out/IntegritySettings.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
const info=profileId=>({profileId,supportedSchema:11,truncated:false,generations:[{snapshotId:'snap',quality:'healthy',operationState:'completed',checkedAt:'2026-10-10T00:00:00Z',deletable:true}]});
// Known states are bounded and display no raw diagnostic payload.
test('integrity validates profile identity, status allowlists and bounded lists',()=>{assert.equal(component.checkedIntegrity(info('p'),'p').generations.length,1);for(const value of [{...info('q')},{...info('p'),generations:[{...info('p').generations[0],quality:'raw SQL error'}]},{...info('p'),generations:Array(33).fill(info('p').generations[0])}])assert.throws(()=>component.checkedIntegrity(value,'p'));});
// Both languages explain the exact destructive scope before the user can confirm anything.
test('integrity deletion stays disabled without a chosen qualified index and exact profile confirmation',()=>{for(const language of ['de','en']){const html=renderToStaticMarkup(React.createElement(component.IntegritySettings,{profileId:'p',profileName:'<private>',language,pendingIntent:{current:null},onSnapshotUnavailable:()=>{},onBeforeDelete:async()=>{},onRebuild:()=>{},onSessionDenied:()=>{}}));assert.ok(html.includes('&lt;private&gt;'));assert.ok(html.includes(language==='de'?'Deine NAS-Dateien und das Scan-Profil werden nicht gelöscht':'Your NAS files and scan profile will not be deleted'));assert.match(html,/<button disabled="">(?:Ausgewählten Index löschen|Delete selected index)<\/button>/);}});
// Run real component callbacks across an uncertain result and a keyed remount to another profile.
test('confirmed deletion releases its original view before POST and preserves exact intent across profile remount',async()=>{
 const original={state:React.useState,ref:React.useRef,effect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window};
 const make=()=>({states:[],refs:[],cleanup:null,render(props,mount=false){let s=0,r=0;let effect;React.useState=initial=>{const i=s++;if(!(i in this.states))this.states[i]=initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=v=>this.refs[r++]??={current:v};React.useEffect=fn=>{if(mount)effect=fn;};const out=component.IntegritySettings(props);if(effect)this.cleanup=effect();return out;}});
 const find=(tree,predicate)=>{if(Array.isArray(tree)){for(const v of tree){const hit=find(v,predicate);if(hit)return hit;}}else if(React.isValidElement(tree)){if(predicate(tree))return tree;return find(tree.props.children,predicate);}return null;};
 const tick=()=>new Promise(resolve=>setImmediate(resolve));const pending={current:null};const order=[];const posts=[];const first=make(),second=make();let mode='network';
 const props={profileId:'p',profileName:'Original',language:'en',pendingIntent:pending,onSessionDenied:()=>assert.fail('unexpected denial'),onRebuild:()=>{},onSnapshotUnavailable:()=>{},onBeforeDelete:async(snapshot,profile)=>{order.push(`release:${profile}:${snapshot}`);}};
 try{
  globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'synthetic'}}}}};globalThis.fetch=async(url,options)=>{if(options.method==='GET'){const profile=new URL(url,'https://test').searchParams.get('profileId');return new Response(JSON.stringify({contractVersion:1,data:info(profile)}),{status:200});}order.push('post');posts.push({key:options.headers['Idempotency-Key'],body:options.body});if(mode==='network')throw new Error('lost response');return new Response('{"contractVersion":1,"error":{"code":"JOB_CONFLICT"}}',{status:409});};
  first.render(props,true);await tick();let tree=first.render(props);find(tree,x=>x.type==='select').props.onChange({target:{value:'snap'}});tree=first.render(props);find(tree,x=>x.type==='input').props.onChange({target:{value:'Original'}});tree=first.render(props);const remove=find(tree,x=>x.type==='button'&&x.props.children==='Delete selected index');assert.equal(remove.props.disabled,false);remove.props.onClick();await tick();assert.deepEqual(order,['release:p:snap','post']);assert.ok(pending.current);
  first.cleanup();const next={...props,profileId:'q',profileName:'Other'};second.render(next,true);await tick();tree=second.render(next);mode='reject';find(tree,x=>x.type==='button'&&x.props.children==='Retry uncertain operation').props.onClick();await tick();assert.deepEqual(order,['release:p:snap','post','release:p:snap','post']);assert.deepEqual(posts[0],posts[1]);assert.equal(pending.current,null,'definite rejection must permit a new explicit action');
 }finally{first.cleanup?.();second.cleanup?.();React.useState=original.state;React.useRef=original.ref;React.useEffect=original.effect;globalThis.fetch=original.fetch;if(original.window===undefined)delete globalThis.window;else globalThis.window=original.window;}
});

// The response limit applies while streaming, before JSON parsing or rendering occurs.
test('integrity response stops and cancels oversized streams',async()=>{
 let cancelled=false;const stream=new ReadableStream({pull(controller){controller.enqueue(new Uint8Array(40000));},cancel(){cancelled=true;}});
 await assert.rejects(()=>component.readIntegrityBody(new Response(stream)),/INTEGRITY_RESPONSE_LIMIT/);assert.equal(cancelled,true);
});

// Accepted mutations survive refresh errors, and pre-mutation reads cannot resurrect deleted state.
test('accepted deletion distinguishes refresh failure and ignores an older healthy response',async()=>{
 const original={state:React.useState,ref:React.useRef,effect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window};
 const states=[],refs=[];let cleanup;const render=(mount=false)=>{let s=0,r=0,effect;React.useState=initial=>{const i=s++;if(!(i in states))states[i]=initial;return[states[i],v=>states[i]=typeof v==='function'?v(states[i]):v];};React.useRef=v=>refs[r++]??={current:v};React.useEffect=fn=>{if(mount)effect=fn;};const tree=component.IntegritySettings(props);if(effect)cleanup=effect();return tree;};
 const find=(tree,predicate)=>{if(Array.isArray(tree)){for(const child of tree){const hit=find(child,predicate);if(hit)return hit;}}else if(React.isValidElement(tree)){if(predicate(tree))return tree;return find(tree.props.children,predicate);}return null;};
 const tick=()=>new Promise(resolve=>setImmediate(resolve));const response=data=>new Response(JSON.stringify({contractVersion:1,data}),{status:200});const pending={current:null};const invalidations=[];
 const props={profileId:'p',profileName:'Original',language:'en',pendingIntent:pending,onSessionDenied:()=>assert.fail('unexpected denial'),onRebuild:()=>{},onBeforeDelete:async()=>{},onSnapshotUnavailable:(_p,_s,q)=>invalidations.push(q)};
 let getCount=0,oldRead,posts=0;
 try{
  globalThis.window={parent:{SYNO:{SDS:{Session:{SynoToken:'synthetic'}}}}};
  globalThis.fetch=async(_url,options)=>{if(options.method==='POST'){posts++;return response({operationId:'op',operation:'delete',profileId:'p',snapshotId:'snap',state:'queued'});}getCount++;if(getCount===1)return response(info('p'));if(getCount===2)return new Promise(resolve=>oldRead=resolve);if(getCount===3)throw new Error('temporary status outage');return response({...info('p'),generations:[{...info('p').generations[0],quality:'deleted',deletable:false}]});};
  render(true);await tick();let tree=render();find(tree,x=>x.type==='select').props.onChange({target:{value:'snap'}});tree=render();find(tree,x=>x.type==='input').props.onChange({target:{value:'Original'}});tree=render();
  find(tree,x=>x.type==='button'&&x.props.children==='Refresh check status').props.onClick();
  find(tree,x=>x.type==='button'&&x.props.children==='Delete selected index').props.onClick();await tick();tree=render();
  assert.equal(posts,1);assert.equal(pending.current,null);assert.equal(find(tree,x=>x.props.role==='status').props.children,'Operation accepted; the current status is temporarily unavailable.');
  find(tree,x=>x.type==='button'&&x.props.children==='Refresh check status').props.onClick();await tick();tree=render();assert.equal(find(tree,x=>x.props.role==='status').props.children,'Operation accepted; the check result is reported separately.');assert.equal(find(tree,x=>x.type==='button'&&x.props.children==='Delete selected index').props.disabled,true);assert.deepEqual(invalidations,['deleted']);
  oldRead(response(info('p')));await tick();tree=render();assert.equal(find(tree,x=>x.type==='button'&&x.props.children==='Delete selected index').props.disabled,true);assert.equal(states[0].generations[0].quality,'deleted');assert.deepEqual(invalidations,['deleted']);
 }finally{cleanup?.();React.useState=original.state;React.useRef=original.ref;React.useEffect=original.effect;globalThis.fetch=original.fetch;if(original.window===undefined)delete globalThis.window;else globalThis.window=original.window;}
});

// Supported schema is a server observation, not a browser generation decoder or legacy alias.
test('integrity supports current schema information and rejects malformed versions',()=>{assert.equal(component.checkedIntegrity(info('p'),'p').supportedSchema,11);for(const supportedSchema of [0,-1,1.5,'11',2147483648])assert.throws(()=>component.checkedIntegrity({...info('p'),supportedSchema},'p'));});
// Cancelling confirmation is local; protected snapshots cannot be removed even through a direct callback.
test('deletion cancellation and last-usable protection perform no mutation',async()=>{
 const old={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect},states=[info('p'),'snap','Original',false,''],refs=[{current:new AbortController()}];let released=0;
 const elements=t=>Array.isArray(t)?t.flatMap(elements):React.isValidElement(t)?[t,...elements(t.props.children)]:[];
 const props={profileId:'p',profileName:'Original',language:'en',pendingIntent:{current:null},onSessionDenied:()=>assert.fail('denied'),onRebuild:()=>{},onBeforeDelete:async()=>{released++;},onSnapshotUnavailable:()=>{}};
 const render=()=>{let i=0,j=0;React.useState=initial=>{const n=i++;return[states[n]??initial,v=>states[n]=typeof v==='function'?v(states[n]):v];};React.useRef=initial=>refs[j++]??={current:initial};React.useEffect=()=>{};return elements(component.IntegritySettings(props));};
 try{let tree=render();tree.find(x=>x.type==='button'&&x.props.children==='Cancel deletion confirmation').props.onClick();tree=render();assert.equal(states[2],'');const remove=()=>render().find(x=>x.type==='button'&&x.props.children==='Delete selected index');assert.equal(remove().props.disabled,true);remove().props.onClick();assert.equal(released,0);states[2]='Original';states[0]={...info('p'),generations:[{...info('p').generations[0],deletable:false}]};assert.equal(remove().props.disabled,true);remove().props.onClick();assert.equal(released,0);assert.equal(props.pendingIntent.current,null);}finally{Object.assign(React,old);}
});
