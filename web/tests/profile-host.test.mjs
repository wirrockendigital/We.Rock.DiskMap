import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Run actual Workbench callbacks against an authenticated transport fixture, without native deployment.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const temp=mkdtempSync(join(web,'.test-build/profile-host-'));let Workbench,ProfileOverview,ProfileEditor,session;
try{writeFileSync(join(temp,'package.json'),'{"type":"commonjs"}');cpSync(join(web,'src'),join(temp,'src'),{recursive:true});execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--resolveJsonModule','--skipLibCheck','--outDir',join(temp,'out'),join(temp,'src/Workbench.tsx')],{stdio:'pipe'});const require=createRequire(import.meta.url);Workbench=require(join(temp,'out/Workbench.js')).Workbench;ProfileOverview=require(join(temp,'out/ProfileOverview.js')).ProfileOverview;ProfileEditor=require(join(temp,'out/ProfileEditor.js')).ProfileEditor;session=require(join(temp,'out/session.js'));}finally{rmSync(temp,{recursive:true,force:true});}
const profile=()=>({id:'own-profile',revision:2,name:'Own test',scope:{kind:'shares',targetIds:['own-share']},active:true,exclusions:[],resourceMode:'gentle',limits:{workers:1,ioBytesPerSecond:null,cpuPercent:null},freshnessSeconds:86400});
const catalogue=()=>({profiles:[{profile:profile(),activeSnapshotId:null,latestJobId:null}],dsmBuild:'90080'});
const elements=(x,out=[])=>{if(Array.isArray(x))for(const y of x)elements(y,out);else if(x&&typeof x==='object'&&x.props){out.push(x);elements(x.props.children,out);}return out;};
// Each render preserves real hook state and callbacks; bootstrap effects are intentionally not part of these two integration regressions.
function harness(){
 const old={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect};const states=[catalogue()],refs=[];let si=0,ri=0;
 const install=()=>{React.useState=initial=>{const i=si++;if(!(i in states))states[i]=typeof initial==='function'?initial():initial;return[states[i],value=>states[i]=typeof value==='function'?value(states[i]):value];};React.useRef=initial=>refs[ri++]??={current:initial};React.useEffect=()=>{};};
 return{render(){si=0;ri=0;install();try{return Workbench({language:'en',selected:'own-profile',onSelect(){},denied(){assert.fail('unexpected session denial');}});}finally{Object.assign(React,old);}},find(tree,type){return elements(tree).find(x=>x.type===type);}};
}
const settled=()=>new Promise(resolve=>setImmediate(resolve));
test('a clean open editor disables overview mutations until explicitly closed',()=>{
 const h=harness();let tree=h.render(),overview=h.find(tree,ProfileOverview);assert.equal(overview.props.disabled,false);overview.props.onEdit(profile());tree=h.render();const editor=h.find(tree,ProfileEditor);assert.ok(editor);editor.props.onDirtyChange(false);tree=h.render();overview=h.find(tree,ProfileOverview);assert.equal(overview.props.disabled,true);
 const html=renderToStaticMarkup(React.createElement(ProfileOverview,overview.props));for(const label of ['Duplicate','Deactivate','Delete','Refresh now','Rebuild completely'])assert.match(html,new RegExp(`<button[^>]*disabled=""[^>]*>${label}</button>`));
 h.find(tree,ProfileEditor).props.onCancel();assert.equal(h.find(h.render(),ProfileOverview).props.disabled,false);
});
test('qualified catalogue reload and editor-save reload clear a previous host error',async()=>{
 const oldFetch=globalThis.fetch,oldToken=session.currentToken;session.currentToken=()=> 'test-token';let failing=true,reads=0;
 globalThis.fetch=async url=>{assert.match(String(url),/action=profiles/);reads++;if(failing)throw Error('simulated transport failure');return new Response(JSON.stringify({contractVersion:1,data:catalogue()}));};
 try{const h=harness();let tree=h.render();h.find(tree,ProfileOverview).props.onChanged({operation:'deactivate',profileId:'own-profile'});await settled();tree=h.render();assert.ok(elements(tree).some(x=>x.props.role==='alert'));assert.equal(elements(tree).some(x=>x.type?.name==='ProfileView'),false);
 failing=false;await h.find(tree,ProfileOverview).props.onReload(new AbortController().signal);tree=h.render();assert.equal(elements(tree).some(x=>x.props.role==='alert'),false);assert.ok(elements(tree).some(x=>x.type?.name==='ProfileView'));
 h.find(tree,ProfileOverview).props.onEdit(profile());tree=h.render();failing=true;const editor=h.find(tree,ProfileEditor);editor.props.onSaved(profile(),editor.props.editorIdentity);await settled();tree=h.render();assert.ok(elements(tree).some(x=>x.props.role==='alert'));
 failing=false;h.find(tree,ProfileEditor).props.onSaved(profile(),editor.props.editorIdentity);await settled();tree=h.render();assert.equal(elements(tree).some(x=>x.props.role==='alert'),false);assert.ok(elements(tree).some(x=>x.type?.name==='ProfileView'));assert.equal(reads,4);
 }finally{globalThis.fetch=oldFetch;session.currentToken=oldToken;}
});
