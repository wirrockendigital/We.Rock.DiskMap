import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Compile the actual production component and adapter, with its existing bounded detail client.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/interactions-'));let api,ui,details,paths;
try{writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');cpSync(join(web,'src'),join(tmp,'src'),{recursive:true});execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--resolveJsonModule','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'src/features/analysisinteraction/AnalysisNodeList.tsx')],{stdio:'pipe'});const require=createRequire(import.meta.url);api=require(join(tmp,'out/features/analysisinteraction/interaction.js'));ui=require(join(tmp,'out/features/analysisinteraction/AnalysisNodeList.js'));details=require(join(tmp,'out/node-details-client.js'));paths=require(join(tmp,'out/NodeDetails.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
const identity={profileId:'p',viewId:'v',snapshotId:'s',rootId:'root',metric:'logical',epoch:1};
const node=(kind='directory',virtual=false)=>({id:'n',realNodeId:virtual?null:'n',parentId:'root',name:'Full <name> 😀',kind,virtual,hasMore:false,summary:{knownBytes:'9007199254740993',unknownFileCount:'1',fileCount:'2',directoryCount:'1',specialCount:'0'}});
const props=(extra={})=>({identity,nodes:[node()],node:node(),selectedNodeId:'n',language:'de',disabled:false,fileStationDisabled:false,onSelect:()=>{},onOpen:()=>{},onDetails:()=>{},onFileStation:()=>{},onSessionDenied:()=>{},...extra});
const pause=ms=>new Promise(r=>setTimeout(r,ms));
function flatten(tree){if(Array.isArray(tree))return tree.flatMap(flatten);if(!tree||typeof tree!=='object')return[];return[tree,...flatten(tree.props?.children)];}
// A small hook runner retains component state/effects across qualified identity and disabled changes.
function mount(initial,component=ui.AnalysisNodeRow){const old={useState:React.useState,useRef:React.useRef,useMemo:React.useMemo,useEffect:React.useEffect};const hooks=[];let cursor=0,p=initial,pending=[];
 React.useState=init=>{const i=cursor++;if(!hooks[i])hooks[i]={value:typeof init==='function'?init():init};return[hooks[i].value,value=>{hooks[i].value=typeof value==='function'?value(hooks[i].value):value;}];};
 React.useRef=init=>{const i=cursor++;return hooks[i]??= {current:init};};
 const changed=(a,b)=>!a||a.length!==b.length||a.some((x,i)=>x!==b[i]);
 React.useMemo=(fn,deps)=>{const i=cursor++;if(!hooks[i]||changed(hooks[i].deps,deps))hooks[i]={deps,value:fn()};return hooks[i].value;};
 React.useEffect=(fn,deps)=>{const i=cursor++;if(!hooks[i]||changed(hooks[i].deps,deps)){pending.push(()=>{hooks[i]?.cleanup?.();hooks[i]={deps,cleanup:fn()};});}};
 return {render(next=p){p=next;cursor=0;const tree=component(p);pending.splice(0).forEach(fn=>fn());return tree;},close(){hooks.forEach(x=>x?.cleanup?.());Object.assign(React,old);}};
}
test('capabilities preserve qualified IDs and reject group/residual file actions',()=>{for(const kind of ['group','residual','unknown'])assert.deepEqual(api.nodeCapabilities(node(kind,true)),{selectable:false,open:false,doubleClick:false,fileStation:false,copy:false});for(const kind of ['file','symlink','fifo','socket','blockDevice','characterDevice'])assert.equal(api.nodeCapabilities(node(kind)).open,false);assert.equal(api.nodeCapabilities(node('volume',true)).open,true);assert.equal(api.nodeCapabilities(node('volume',true)).fileStation,false);assert.equal(api.selectionID({...node(),id:'display',realNodeId:'real'}),'real');});
test('single pointer selection, folder doubleclick, keyboard and explicit touch open share one ID',async()=>{const calls=[],n=node();let valid=true;const adapter=api.nodeInteraction(n,{current:()=>valid,select:n=>calls.push(['select',api.selectionID(n)]),open:n=>calls.push(['open',api.selectionID(n)])});adapter.click();adapter.doubleClick();await pause(280);assert.deepEqual(calls,[['select','n'],['open','n']]);adapter.select();adapter.open();assert.deepEqual(calls.slice(2),[['select','n'],['open','n']]);valid=false;adapter.click();await pause(0);assert.equal(calls.length,4);const file=api.nodeInteraction(node('file'),{current:()=>true,select:()=>{},open:()=>assert.fail('file opened')});file.doubleClick();file.open();});
test('DE EN list exposes full names/exact bytes and reachable details without hover',()=>{for(const language of ['de','en']){const html=renderToStaticMarkup(React.createElement(ui.AnalysisNodeList,props({language})));assert.match(html,/Full &lt;name&gt; 😀/);assert.ok(html.includes(language==='de'?'9.007.199.254.740.993 B':'9,007,199,254,740,993 B'));assert.match(html,/Details/);assert.match(html,language==='de'?/Hier öffnen/:/Open here/);assert.doesNotMatch(html,/<a |download=/);}});
test('same-identity refreshed node objects remain actionable and disabled rows do not act',()=>{const calls=[],h=mount(props({onSelect:n=>calls.push(n.id)}));try{h.render();let tree=h.render(props({node:{...node()},onSelect:n=>calls.push(n.id)}));flatten(tree).find(x=>x.type==='button'&&x.props.children===node().name).props.onClick({detail:0});assert.deepEqual(calls,['n']);tree=h.render(props({disabled:true,onSelect:()=>assert.fail('disabled action')}));flatten(tree).find(x=>x.type==='button'&&x.props.children===node().name).props.onClick({detail:0});}finally{h.close();}});
test('context/keyboard menu shares details and respects File Station restriction',()=>{let selected;const h=mount(props({fileStationDisabled:true,onDetails:n=>selected=n.id}));try{let tree=h.render();const name=flatten(tree).find(x=>x.type==='button'&&x.props.children===node().name);name.props.onKeyDown({key:'F10',shiftKey:true,preventDefault(){}});tree=h.render();const menu=flatten(tree).find(x=>x.props.role==='menu');assert.ok(menu);const buttons=flatten(menu).filter(x=>x.type==='button');assert.equal(buttons.find(x=>x.props.children==='In File Station anzeigen').props.disabled,true);buttons.find(x=>x.props.children==='Details').props.onClick();assert.equal(selected,'n');assert.equal(flatten(h.render()).some(x=>x.props.role==='menu'),false);}finally{h.close();}});
test('hover is fixed, mutation-free and stale success/error is suppressed on identity switch',async()=>{const original=details.requestNodeDetail;let resolve,reject;details.requestNodeDetail=()=>new Promise((a,b)=>{resolve=a;reject=b;});for(const fail of [false,true]){const h=mount(props({onSelect:()=>assert.fail('hover selected'),onOpen:()=>assert.fail('hover navigated'),onSessionDenied:()=>assert.fail('stale failure')}));try{let tree=h.render();flatten(tree).find(x=>x.type==='button'&&x.props.children===node().name).props.onPointerEnter({pointerType:'mouse'});await pause(200);tree=h.render();assert.equal(flatten(tree).find(x=>x.type==='aside').props.style.position,'fixed');h.render(props({identity:{...identity,epoch:2}}));if(fail)reject(new Error('private stale error'));else resolve({detail:{name:'stale'}});await pause(0);assert.ok(!JSON.stringify(h.render()).includes('private stale error'));assert.ok(!JSON.stringify(h.render()).includes('"stale"'));}finally{h.close();}}details.requestNodeDetail=original;});

// Copy is a separate user action after the identity-checked historical response is displayed.
test('copy never synthesizes a path and keeps encoded historical meaning visible',async()=>{
 const old=details.requestNodeDetail,oldCopy=paths.copyPathText;let copied=null,requested;
 details.requestNodeDetail=async selection=>{requested=selection;return {detail:{name:'exact',node:{volumeId:'volume',shareId:'share'}},paths:{nasPath:{text:'/recorded/\\xFF',encoding:'escaped-bytes'}}};};
 paths.copyPathText=async value=>{copied=value;};const h=mount(props());
 try{let tree=h.render();tree.props.onContextMenu({preventDefault(){}});tree=h.render();flatten(tree).find(x=>x.props.role==='menuitem'&&x.props.children==='NAS-Pfad kopieren').props.onClick();await pause(0);tree=h.render();const dialog=flatten(tree).find(x=>x.props.role==='dialog');assert.ok(dialog);assert.equal(copied,null);assert.deepEqual(requested,{...identity,nodeId:'n'});assert.match(JSON.stringify(dialog),/Maskierte Originalbytes/);assert.match(JSON.stringify(dialog),/aktuelle Existenz nicht geprüft/);flatten(dialog).find(x=>x.type==='button'&&x.props.children==='NAS-Pfad kopieren').props.onClick();await pause(0);assert.equal(copied,'/recorded/\\xFF');}finally{h.close();details.requestNodeDetail=old;paths.copyPathText=oldCopy;}
});
test('detail timeout ends loading and does not erase the session or disclose raw errors',async()=>{
 const old=details.requestNodeDetail,timeout=AbortSignal.timeout;AbortSignal.timeout=()=>timeout(1);details.requestNodeDetail=(_s,_op,_cursor,signal)=>new Promise((_resolve,reject)=>signal.addEventListener('abort',()=>reject(new Error('private path'))));const h=mount(props({onSessionDenied:()=>assert.fail('timeout is not auth loss')}));
 try{let tree=h.render();tree.props.onContextMenu({preventDefault(){}});tree=h.render();flatten(tree).find(x=>x.props.role==='menuitem'&&x.props.children==='NAS-Pfad kopieren').props.onClick();await pause(10);tree=h.render();assert.match(JSON.stringify(tree),/Aktion nicht verfügbar/);assert.doesNotMatch(JSON.stringify(tree),/private path|Vorschau wird geladen/);}finally{h.close();details.requestNodeDetail=old;AbortSignal.timeout=timeout;}
});

// Selection publication must update guards without replacing the click target DOM identity.
test('row keys survive epoch and metric changes but distinguish profile lease snapshot root and node',()=>{
 const key=p=>{const h=mount(p,ui.AnalysisNodeList);try{return flatten(h.render()).find(x=>x.type===ui.AnalysisNodeRow).key;}finally{h.close();}};
 const base=props(),before=key(base);
 assert.equal(key(props({identity:{...identity,epoch:2}})),before);
 assert.equal(key(props({identity:{...identity,metric:'allocated'}})),before);
 for(const field of ['profileId','viewId','snapshotId','rootId'])assert.notEqual(key(props({identity:{...identity,[field]:'changed'}})),before);
 assert.notEqual(key(props({nodes:[{...node(),id:'other'}]})),before);
});

// Historical source groups can navigate, but even malformed real-ID flags cannot grant reveal or copy.
test('share and other containers select and open without filesystem actions',()=>{
 for(const kind of ['share','other']){
  for(const virtual of [true,false]){const n=node(kind,virtual),caps=api.nodeCapabilities(n);assert.equal(caps.selectable,true);assert.equal(caps.open,true);assert.equal(caps.copy,false);assert.equal(caps.fileStation,false);}
  const calls=[];const actions=api.nodeInteraction(node(kind,true),{select:()=>calls.push('select'),open:()=>calls.push('open'),current:()=>true});actions.click();actions.open();assert.deepEqual(calls,['select','open']);
 }
});

// Paging bounds mounted rows without dropping data or publishing a different selection.
test('301 nodes remain reachable in 100-row pages with accessible ranges and disabled guards',()=>{
 const nodes=Array.from({length:301},(_,i)=>({...node('file'),id:`n${i}`,realNodeId:`n${i}`}));
 const p=props({nodes,selectedNodeId:null,onSelect:()=>assert.fail('paging selected'),onOpen:()=>assert.fail('paging opened')}),h=mount(p,ui.AnalysisNodeList);
 const rows=t=>flatten(t).filter(x=>x.type===ui.AnalysisNodeRow),next=t=>flatten(t).find(x=>x.type==='button'&&x.props.children==='Nächste Seite'),prev=t=>flatten(t).find(x=>x.type==='button'&&x.props.children==='Vorherige Seite');
 try{const seen=[];for(let page=0;page<4;page++){const tree=h.render();const r=rows(tree);assert.equal(r.length,page===3?1:100);seen.push(...r.map(x=>x.props.node.id));assert.match(JSON.stringify(flatten(tree).find(x=>x.props.role==='status').props.children),new RegExp(String(page*100+1)));if(page<3)next(tree).props.onClick();else assert.equal(next(tree).props.disabled,true);}assert.deepEqual(seen,nodes.map(n=>n.id));let tree=h.render({...p,disabled:true});assert.equal(prev(tree).props.disabled,true);prev(tree).props.onClick();assert.equal(rows(h.render({...p,disabled:true}))[0].props.node.id,'n300');tree=h.render(p);prev(tree).props.onClick();assert.equal(rows(h.render())[0].props.node.id,'n200');}finally{h.close();}
});
// Selection from a map reveals its page; scope changes cannot retain an unrelated page offset.
test('external selection reveals its page while epochs preserve paging and new roots reset',()=>{
 const nodes=Array.from({length:301},(_,i)=>({...node(),id:`n${i}`,realNodeId:`real${i}`}));
 const p=props({nodes,selectedNodeId:null}),h=mount(p,ui.AnalysisNodeList),first=t=>flatten(t).find(x=>x.type===ui.AnalysisNodeRow)?.props.node.id;
 try{h.render();let q={...p,selectedNodeId:'real250'};assert.equal(first(h.render(q)),'n200');assert.equal(first(h.render({...q,identity:{...identity,epoch:2}})),'n200');let tree=h.render(q);flatten(tree).find(x=>x.type==='button'&&x.props.children==='Vorherige Seite').props.onClick();assert.equal(first(h.render(q)),'n100');assert.equal(first(h.render({...q,nodes:[...nodes]})),'n100');for(const field of ['rootId','snapshotId','viewId','profileId'])assert.equal(first(h.render({...p,identity:{...identity,[field]:'other'}})),'n0');assert.equal(first(h.render({...p,selectedNodeId:'missing'})),'n0');assert.equal(first(h.render({...p,nodes:[]})),undefined);}finally{h.close();}
});

// Presentation labels change language while the recorded name and selection identity remain exact.
test('node kinds are localized without translating recorded names',()=>{
 for(const [kind,de,en] of [['file','Datei','File'],['directory','Ordner','Directory'],['share','Freigabebereich','Shared-folder area'],['other','Weitere erfasste Volume-Bereiche','Other recorded volume areas'],['symlink','Symbolischer Link','Symbolic link']]){
  for(const [language,label] of [['de',de],['en',en]]){const n={...node(kind,['share','other'].includes(kind)),name:'Recorded directory Datei'};const html=renderToStaticMarkup(React.createElement(ui.AnalysisNodeList,props({language,nodes:[n]})));assert.ok(html.includes(label));assert.ok(html.includes('Recorded directory Datei'));assert.equal(n.id,'n');}
 }
});
