import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import vm from 'node:vm';
// Exercise production owner methods with deterministic barriers at the actual asynchronous boundaries.
const source=stripTypeScriptTypes(readFileSync(new URL('../src/design/useNativeDiskMap.ts',import.meta.url),'utf8'),{mode:'transform'}).replace(/^import .*;$/gm,'').replace(/export /g,'');
const barrier=()=>{let release;const promise=new Promise(r=>release=r);return {promise,release};};
for(const phase of ['source-already-opening','catalogue','reader','mode-during-source'])test(`rescan completion preserves user source ownership during ${phase}`,async()=>{
 const noop=()=>{};class Dummy{};const entered=barrier(),resume=barrier(),childrenEntered=barrier(),childrenResume=barrier();let readerOpens=0;
 const globals={structuredClone,fetch:noop,Date,AbortController,AbortSignal,DOMException,crypto,setTimeout:(fn,ms)=>{if(ms===75)queueMicrotask(fn);return 0;},clearTimeout:noop,document:{visibilityState:'visible'},defaultWorkspace:()=>({source:{kind:'shares',targetIds:['old']},jobs:[]}),defaultSidebars:()=>({}),defaultFreshness:()=>({}),emptyAnalysis:()=>({}),DiagnosticLog:class{add(){}clear(){}timing(){}observe(){}},ScanPreviewClient:class{clear(){}},PreferenceClient:Dummy,ProfileActionClient:Dummy,AnalysisController:class{cancel(){}},defaultView:()=>({rootId:'root',map:'treemap'}),checkedReader:noop,SessionDenied:class extends Error{},safeErrorCode:e=>e.message,id:x=>typeof x==='string'&&x.length>0,hasRecordedResult:()=>true,scopeCovers:()=>true,scopeEqual:()=>false,
 readStatus:async()=>({active:null,queued:[]}),readProgressObservation:async()=>({history:[{jobId:'job',state:'completed'}]}),
 requestReader:async(body,signal)=>{if(body.operation==='open'){readerOpens++;entered.release();if(readerOpens===1)await resume.promise;signal.throwIfAborted();}return {view:{state:'complete',snapshotId:'snapshot',endedAt:'2026-10-18T00:00:00Z'},lease:{id:body.viewId}};}};
 const Owner=vm.runInNewContext(source+'\nNativeOwner',globals),owner=new Owner(noop,noop);
 owner.controller.current={pin:{profileId:'profile',snapshotId:'snapshot',viewId:'lease'},state:{view:{profileId:'profile',snapshotId:'snapshot',rootId:'root',map:'treemap',search:{text:''}}},navigation:{breadcrumb:[{id:'root'}]}};
 owner.state.analysis=owner.controller.current;owner.state.profileId='profile';owner.state.sources={volumes:[{id:'volume'}],shares:[{id:'old',volumeId:'volume'},{id:'new',volumeId:'volume'}]};owner.state.profiles=[{profile:{id:'profile',active:true,name:'Scan',scope:{kind:'nas',targetIds:[]}}}];owner.state.summariesReady=true;owner.lastRenewal=Date.now();owner.observedJobs.set('job','profile');
 owner.catalogue=async()=>{if(phase==='catalogue'){entered.release();await resume.promise;}};
 owner.controller.accept=async()=>{owner.state.analysis=owner.controller.current;return true;};owner.readLatestResult=async()=>{};let firstRoot=true;
 const navigations=[];owner.navigate=async id=>{navigations.push(id);owner.navigationVersion++;owner.controller.current.state.view={...owner.controller.current.state.view,rootId:id};};owner.controller.move=async view=>{owner.controller.current.state.view=view;};
 owner.children=async parent=>{if(parent==='root'){if(firstRoot){firstRoot=false;childrenEntered.release();await childrenResume.promise;}return {items:[{id:'volume-node',sourceKind:'volume',volumeId:'volume'}],nextCursor:null};}return {items:[{id:'new-share',sourceKind:'share',shareId:'new'},{id:'old-share',sourceKind:'share',shareId:'old'}],nextCursor:null};};
 let user,observing,userRequest;
 if(phase==='source-already-opening'||phase==='mode-during-source'){user=owner.openScope({kind:'shares',targetIds:['new']});await childrenEntered.promise;userRequest=owner.sourceRequest;if(phase==='mode-during-source')await owner.setMap('radial');observing=owner.observe();}
 else{observing=owner.observe();await entered.promise;user=owner.openScope({kind:'shares',targetIds:['new']});await childrenEntered.promise;userRequest=owner.sourceRequest;resume.release();}
 await observing;assert.equal(userRequest.signal.aborted,false,'late scan restoration cancelled the explicit source request');assert.equal(owner.state.statusError,null);
 childrenResume.release();assert.equal(await user,true);assert.deepEqual(navigations,['new-share']);assert.equal(owner.sourceOpening,null);assert.equal(owner.controller.current.state.view.map,phase==='mode-during-source'?'radial':'treemap');
 assert.equal(readerOpens,phase==='reader'?2:0,'completion started an unowned profile adoption');
});
// A cancelled source opening must not reuse a stale controller lease behind an empty presenter.
test('returning to the previous source reacquires an interrupted presenter',async()=>{
 const noop=()=>{};class Dummy{};let cancelled=0,reopened=0;
 const globals={structuredClone,Date,AbortController,AbortSignal,DOMException,setTimeout,clearTimeout,defaultWorkspace:()=>({}),defaultSidebars:()=>({}),defaultFreshness:()=>({}),emptyAnalysis:()=>({pin:null}),DiagnosticLog:class{add(){}},ScanPreviewClient:Dummy,PreferenceClient:Dummy,ProfileActionClient:Dummy,AnalysisController:class{cancel(){cancelled++;}},checkedReader:noop,id:()=>true,hasRecordedResult:()=>true,scopeCovers:()=>true,scopeEqual:()=>true};
 const Owner=vm.runInNewContext(source+'\nNativeOwner',globals),owner=new Owner(noop,noop);
 owner.controller.current={pin:{profileId:'profile',snapshotId:'snapshot',viewId:'old-lease'},state:{view:{profileId:'profile',snapshotId:'snapshot',rootId:'root',map:'treemap'}},navigation:{breadcrumb:[{id:'root'}]}};
 owner.state.sources={volumes:[],shares:[]};owner.state.profiles=[{profile:{id:'profile',active:true,scope:{kind:'nas'}}}];owner.state.summaries=[];owner.state.analysis={pin:null};
 owner.openProfile=async(profile,_partial,preserve)=>{assert.equal(profile,'profile');assert.equal(preserve,true);reopened++;owner.state.analysis=owner.controller.current;};owner.navigate=async()=>{};
 assert.equal(await owner.openScope({kind:'nas'}),true);assert.equal(reopened,1);assert.equal(cancelled,1);assert.equal(owner.sourceOpening,null);
});
// Same-root selection restarts only an already cancelled stream; active work remains untouched.
test('same-root selection resumes cancelled map preparation without restarting active work',async()=>{
 const noop=()=>{};class Dummy{};const globals={structuredClone,Date,AbortController,AbortSignal,setTimeout,clearTimeout,defaultWorkspace:()=>({}),defaultSidebars:()=>({}),defaultFreshness:()=>({}),emptyAnalysis:()=>({}),DiagnosticLog:Dummy,ScanPreviewClient:Dummy,PreferenceClient:Dummy,ProfileActionClient:Dummy,AnalysisController:Dummy,checkedReader:noop};
 const Owner=vm.runInNewContext(source+'\nNativeOwner',globals),owner=new Owner(noop,noop),view={rootId:'root',map:'treemap'};owner.controller.current={state:{view}};let starts=0;owner.hierarchy=async()=>{starts++;};owner.query=new AbortController();await owner.move(view);assert.equal(starts,0);owner.query.abort();await owner.move(view);assert.equal(starts,1);
});
// Returning to a share revalidates navigation but skips directory enumeration only for the same catalogue generation.
test('source selector cache is bounded and invalidated by revision, snapshot, job and inventory',async()=>{
 const noop=()=>{};class Dummy{};let enumerations=0,qualified=0,renders=0;
 const globals={structuredClone,Date,AbortController,AbortSignal,DOMException,setTimeout:(fn,ms)=>{if(ms===75)queueMicrotask(fn);return 0;},clearTimeout:noop,defaultWorkspace:()=>({}),defaultSidebars:()=>({}),defaultFreshness:()=>({}),emptyAnalysis:()=>({}),DiagnosticLog:class{add(){}},ScanPreviewClient:class{clear(){}},PreferenceClient:Dummy,ProfileActionClient:Dummy,AnalysisController:class{cancel(){}},checkedReader:noop,hasRecordedResult:()=>true,scopeCovers:()=>true,scopeEqual:()=>true};
 const Owner=vm.runInNewContext(source+'\nNativeOwner',globals),owner=new Owner(noop,noop);
 const profile={profile:{id:'profile',revision:1,active:true,scope:{kind:'nas'}},activeSnapshotId:'snapshot',latestJobId:'job'};
 const inventory={volumes:[{id:'volume'}],shares:[{id:'share',volumeId:'volume'}]};
 owner.state.sources=inventory;owner.state.profiles=[profile];
 owner.controller.current={pin:{profileId:'profile',snapshotId:'snapshot',viewId:'lease'},state:{view:{profileId:'profile',snapshotId:'snapshot',rootId:'root',map:'treemap'}},navigation:{breadcrumb:[{id:'root'}]}};
 owner.state.analysis=owner.controller.current;
 owner.hierarchy=async()=>{renders++;};owner.navigate=async root=>{owner.controller.current.state.view={...owner.controller.current.state.view,rootId:root};};
 owner.controller.move=async view=>{qualified++;owner.controller.current.state.view=view;};
 owner.children=async root=>{enumerations++;return {items:root==='root'?[{id:'volume-node',sourceKind:'volume',volumeId:'volume'}]:[{id:'share-node',sourceKind:'share',shareId:'share'}],nextCursor:null};};
 const scope={kind:'shares',targetIds:['share']};
 assert.equal(await owner.openScope(scope),true);assert.equal(enumerations,2);
 owner.controller.current.state.view={...owner.controller.current.state.view,rootId:'elsewhere'};
 assert.equal(await owner.openScope(scope),true);assert.equal(enumerations,2);assert.equal(qualified,1);assert.equal(renders,2);
 for(const invalidate of [()=>profile.profile.revision++,()=>profile.activeSnapshotId='new-snapshot',()=>profile.latestJobId='new-job',()=>owner.state.sources={...inventory}]){
  invalidate();await owner.openScope(scope);
 }
 assert.equal(enumerations,10,'changed catalogue identities must resolve their roots again');
 // A stale cached selector cannot publish after the authoritative move was rejected.
 owner.controller.current.state.view={...owner.controller.current.state.view,rootId:'elsewhere'};
 owner.controller.move=async()=>{};
 assert.equal(await owner.openScope(scope),false);assert.equal(renders,6);
});
