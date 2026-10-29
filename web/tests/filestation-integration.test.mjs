import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Compile the production client and panel, including the historical path renderer.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/details-'));let api,ui,session;
try{writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');cpSync(join(web,'src'),join(tmp,'src'),{recursive:true});execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--resolveJsonModule','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'src/FileStationIntegrationTest.tsx')],{stdio:'pipe'});const require=createRequire(import.meta.url);api=require(join(tmp,'out/node-details-client.js'));ui=require(join(tmp,'out/FileStationIntegrationTest.js'));session=require(join(tmp,'out/session.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
const selection={profileId:'p',viewId:'v',snapshotId:'s',rootId:'root',nodeId:'n',metric:'logical',epoch:1};
import {fixture} from './node-details-fixture.mjs';
// Exercise the rendered component with a qualified receipt, including stale retained button handlers.
test('settings probe launches only explicit complete real selection and rejects old authority',()=>{
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect};const refs=[];let ri=0,si=0,value=fixture(),calls=[];
 const find=tree=>{if(Array.isArray(tree))return tree.map(find).find(Boolean);if(React.isValidElement(tree)){if(tree.type==='button'&&String(tree.props.children).startsWith('Test:'))return tree;return find(tree.props.children);}return null;};
 const render=(patch={})=>{ri=0;si=0;const props={language:'en',dsmBuild:'90080',selection,disabled:false,onTest:s=>calls.push(s),onSessionDenied:()=>assert.fail('auth'),...patch};React.useRef=x=>refs[ri++]??={current:x};React.useEffect=()=>{};React.useState=x=>[si++===0?{key:props.selection?api.detailSelectionKey(props.selection):'',value}:x,()=>{}];return ui.FileStationIntegrationTest(props);};
 try{
  value.detail.snapshotState='complete';
  const button=find(render());assert.equal(button.props.disabled,false);assert.equal(calls.length,0);button.props.onClick();assert.deepEqual(calls,[selection]);
  render({selection:{...selection,viewId:'new'}});button.props.onClick();assert.equal(calls.length,1);
  for(const patch of [{disabled:true},{selection:null},{dsmBuild:'unknown'}])assert.equal(find(render(patch)).props.disabled,true);
  value.detail.snapshotState='partial';assert.equal(find(render()).props.disabled,true);
  value.detail.snapshotState='complete';value.detail.virtual=true;assert.equal(find(render()).props.disabled,true);
  value.detail.virtual=false;value.detail.kind='file';value.detail.node.kind='file';assert.equal(find(render()).props.children,'Test: open parent folder');
  assert.match(renderToStaticMarkup(render()),/Long &lt;name&gt;/);
 }finally{Object.assign(React,saved);}
});
// Actual asynchronous effects discard old success/auth replies after cleanup and never launch automatically.
test('settings probe suppresses abandoned detail success and authentication errors',async()=>{
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,request:api.requestNodeDetail};
 try{for(const auth of [false,true]){
  let settle,cleanup,calls=0;const pending=new Promise((resolve,reject)=>settle=auth?reject:resolve);
  api.requestNodeDetail=()=>pending;React.useRef=x=>({current:x});React.useState=x=>[x,()=>{}];React.useEffect=fn=>{cleanup=fn();};
  ui.FileStationIntegrationTest({language:'en',dsmBuild:'90080',selection,disabled:false,onTest:()=>calls++,onSessionDenied:()=>calls++});cleanup();settle(auth?new session.SessionDenied('SESSION_REQUIRED'):fixture());await new Promise(r=>setImmediate(r));assert.equal(calls,0);
 }}finally{Object.assign(React,{useState:saved.useState,useRef:saved.useRef,useEffect:saved.useEffect});api.requestNodeDetail=saved.request;}
});
