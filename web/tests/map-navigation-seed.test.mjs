import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import vm from 'node:vm';
// A constrained metadata endpoint must not prevent the independent detailed map from loading.
const source=stripTypeScriptTypes(readFileSync(new URL('../src/design/useNativeDiskMap.ts',import.meta.url),'utf8'),{mode:'transform'}).replace(/^import .*;$/gm,'').replace(/export /g,'');
for(const mode of ['treemap','radial'])test(`${mode} opens a detailed packed map without expanding a duplicate hierarchy`,async()=>{
 const noop=()=>{};class Dummy{};const streamed=[{id:'root'},{id:'folder',parent:'root'},{id:'deep',parent:'folder'},{id:'file',parent:'deep',bytes:'42'}];
 let metadataCalls=0,appended=[],manifestStarted;const manifest=new Promise(resolve=>{manifestStarted=resolve;});
 class Worker {async open(){manifestStarted();appended=streamed;return {id:'root',name:'Root',bytesExact:'42',children:[]};}async layout(){return {hits:appended.map(entry=>({node:entry})),groups:[]};}dispose(){}}
 const globals={devicePixelRatio:1,fetch:noop,Date,AbortController,AbortSignal,setTimeout,clearTimeout,defaultWorkspace:()=>({showRecycle:false,showSystem:false}),defaultSidebars:()=>({}),defaultFreshness:()=>({}),emptyAnalysis:()=>({}),DiagnosticLog:class{add(){}clear(){}timing(){}},ScanPreviewClient:Dummy,PreferenceClient:Dummy,ProfileActionClient:Dummy,AnalysisController:class{},checkedReader:noop,SessionDenied:class extends Error{},safeErrorCode:e=>e.message,PackedMapClient:Worker,
  readAnalysis:async(_view,_pin,_signal,_fetch,budget)=>{metadataCalls++;await manifest;if(budget.depth>1||budget.elementBudget>1)throw Error('QUERY_BUDGET_EXCEEDED');return {snapshotId:'snapshot',rootId:'root',nodes:[{id:'root'}]};},
  adaptHierarchy:wire=>wire,streamMapEntries:async(_view,_pin,_signal,append)=>append(streamed,4,'root')};
 const Owner=vm.runInNewContext(source+'\nNativeOwner',globals),owner=new Owner(noop,noop);
 owner.controller.current={state:{view:{map:mode,rootId:'root',snapshotId:'snapshot',metric:'logical',filterMode:'highlight'}},pin:{snapshotId:'snapshot'}};owner.budget={depth:8,elementBudget:mode==='treemap'?10000:2000};
 await owner.hierarchy();assert.equal(owner.state.error,null);assert.equal(owner.state.loading,false);assert.equal(metadataCalls,1);assert.equal(owner.state.mapTree.children.length,0);assert.equal(owner.state.mapTree.bytesExact,'42');
 const geometry=await owner.loadGeometry('root',mode,{width:1000,height:700},{scale:1,x:0,y:0});assert.equal(geometry.hits.length,4);assert.equal(geometry.hits.at(-1).node.id,'file');assert.equal(owner.budget.depth,8);
});
