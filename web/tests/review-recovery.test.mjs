import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import vm from 'node:vm';
// Test the production retry policy and owner state transitions with a deterministic clock.
const compile=file=>stripTypeScriptTypes(readFileSync(new URL(file,import.meta.url),'utf8'),{mode:'transform'}).replace(/^import .*;$/gm,'').replace(/export /g,'');
test('observation retry is bounded and never repeats invalid data or denial',()=>{
 const c=vm.runInNewContext(compile('../src/design/scope-observations.ts')+'\n({observationRetryDelay,ScopeObservationUnavailable})',{TypeError});
 const transient=new c.ScopeObservationUnavailable(true);
 assert.deepEqual([0,1,2,3,4].map(n=>c.observationRetryDelay(transient,n)),[2000,4000,8000,null,null]);
 for(const error of [Error('INVALID_RESPONSE'),new c.ScopeObservationUnavailable(false),Error('SESSION_REQUIRED')])assert.equal(c.observationRetryDelay(error,0),null);
});
test('filter pending survives slow preparation and terminates with an actionable timeout',async()=>{
 let now=100,timer=null,calls=0;
 const noop=()=>{};class Dummy{};
 const globals={PackedMapClient:class{async open(){return {id:"root"};}},fetch:noop,Date:{now:()=>now},AbortController,AbortSignal,setTimeout:fn=>(timer=fn,1),clearTimeout:()=>{timer=null;},defaultWorkspace:()=>({}),defaultSidebars:()=>({}),defaultFreshness:()=>({}),emptyAnalysis:()=>({}),DiagnosticLog:class{add(){}clear(){}timing(){}},ScanPreviewClient:Dummy,PreferenceClient:Dummy,ProfileActionClient:Dummy,AnalysisController:class{constructor(){}},checkedReader:noop,readAnalysis:async()=>{calls++;return null;},SessionDenied:class extends Error{},safeErrorCode:e=>e.message};
 const NativeOwner=vm.runInNewContext(compile('../src/design/useNativeDiskMap.ts')+'\nNativeOwner',globals);
 const owner=new NativeOwner(noop,noop);owner.controller.current={state:{view:{map:'treemap'}},pin:{snapshotId:'snapshot'}};owner.budget={depth:1,elementBudget:100};
 await owner.hierarchy();assert(timer);assert.equal(owner.state.hierarchyPending,true);
 now=30100;await owner.hierarchy(true);assert(timer);assert.equal(owner.state.error,null);
 now=120100;await owner.hierarchy(true);assert.equal(timer,null);assert.equal(owner.state.hierarchyPending,false);assert.equal(owner.state.error,'FILTER_PREPARATION_TIMEOUT');assert.equal(calls,3);
});
