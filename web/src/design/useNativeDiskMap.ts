import {requestIndexReset,IndexResetRejected,type IndexResetIntent} from './index-reset';
import {PackedMapClient,type GeometryLoader} from './packed-map-client';
import {useEffect, useMemo, useRef, useState} from 'react';
import type {ListOptions,FileListPage} from './list-options';
import {ScanPreviewClient,type ScanPreviewResponse} from './scan-preview';
import {DiagnosticLog,safeErrorCode,type DebugFilter} from './diagnostics';
import {defaultSidebars,type SidebarPreference} from './layout-preference';
import {loadProfileSummaries,type ProfileCardSummary} from '../profile-management';
import {defaultFreshness,readFreshness,writeFreshness,scopeCovers,scopeEqual,type FreshnessPreference} from './scan-freshness';
import {PreferenceClient} from '../preference-client';
import {matchingCachedProfile,hasRecordedResult} from './scan-cache';
import {defaultWorkspace} from './workspace-state';
import type {PreferenceWorkspace,ScanBoundaryReport,Profile, Scope} from '../contracts';
import {AnalysisController, defaultView, emptyAnalysis, type ConfirmedAnalysis} from '../AnalysisController';
import {checkedScanBoundaryReport,checkedReader, readView} from '../Workbench';
import {requestReader} from '../snapshot-reader';
import type {RestoredReader} from '../view-state-api';
import {readAnalysis, readSearchList, type AnalysisRenderBudget} from '../analysis-query';
import {adaptHierarchy} from '../visualization/shared/adapter';
import {readGroupPage, type GroupPage} from '../visualization/shared/group-client';
import type {MapModel, MapNode} from '../visualization/shared/model';
import type {SearchListPage} from '../features/searchlist/types';
import {loadScopeInventory, checkSelectedScope} from '../scope-api';
import {checkedScopeInventory, checkedScopeCheck, type ScopeInventory} from '../scope-selection';
import {ProfileActionClient, newProfileConfiguration, configurationKey} from '../profile-actions';
import {requestScan, scanIntent, type PendingScan} from '../actions';
import {readStatus, readProgressObservation, type StatusSnapshot, type ProgressObservation} from '../features/scan-status/status-client';
import {SessionDenied} from '../session';
import type {StorageNode} from './model';
import {requestJobControl, type ControlIntent} from '../job-control';
import type {FileStationTarget} from '../dsmFileStation';
import {readLeasedPaths} from '../Workbench';

export interface NativeStorageNode extends StorageNode {
  bytesExact?:string; unavailable?:boolean; sourceKind:string; scope?:Scope;volumeId?:string;shareId?:string|null;
}
export interface NativeProfileEntry {profile:Profile;activeSnapshotId:string|null;latestJobId:string|null}
interface PartialResult {jobId:string;snapshotId:string;endedAt:string;coverageErrors:number;reasons?:Record<string,number>}
interface NativeState {
 language:'de'|'en';theme:'dark'|'light';mapTree:StorageNode|null;mapPreparing:boolean;workspace:PreferenceWorkspace;
  completion:{jobId:string;name:string;state:string}|null;
  preview:ScanPreviewResponse|null;
  partial:PartialResult|null;terminalCode:string|null;debugEnabled:boolean;debugEntries:string[];debugLimit:number;debugFilter:DebugFilter;debugBytes:number;debugCount:number;
  freshness:FreshnessPreference;summaries:ProfileCardSummary[];summariesReady:boolean;
  inventoryRefreshing:boolean;inventoryError:boolean;sources:ScopeInventory|null;profiles:NativeProfileEntry[];analysis:ConfirmedAnalysis;model:MapModel|null;
  searchPage:SearchListPage|null;groupPage:GroupPage|null;status:StatusSnapshot|null;progress:ProgressObservation|null;
  sidebars:SidebarPreference;layoutReady:boolean;dsmBuild:string;profileId:string|null;loading:boolean;busy:boolean;error:string|null;statusError:string|null;pending:boolean;hierarchyPending:boolean;
}
export interface NativeDiskMapOptions {
  sessionIdentity:string;onSessionDenied:()=>void;budget:AnalysisRenderBudget|null;
}
const initial=():NativeState=>({language:'de',theme:'dark',mapTree:null,mapPreparing:false,workspace:defaultWorkspace(),completion:null,preview:null,partial:null,terminalCode:null,debugEnabled:false,debugEntries:[],debugLimit:10,debugFilter:'all',debugBytes:0,debugCount:0,freshness:defaultFreshness(),summaries:[],summariesReady:false,sidebars:defaultSidebars(),layoutReady:false,inventoryRefreshing:false,inventoryError:false,sources:null,profiles:[],analysis:emptyAnalysis(),model:null,searchPage:null,groupPage:null,status:null,progress:null,dsmBuild:'',profileId:null,loading:false,busy:false,error:null,statusError:null,pending:false,hierarchyPending:false});
const id=(value:unknown):value is string=>typeof value==='string'&&/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(value);

// The session owner serializes observations and retains uncertain mutations without resubmission.
class NativeOwner {
  state=initial();diagnostics=new DiagnosticLog(()=>this.update({debugEnabled:this.diagnostics.enabled,debugEntries:this.diagnostics.entries,debugLimit:this.diagnostics.limitMB,debugFilter:this.diagnostics.filter,debugBytes:this.diagnostics.bytes,debugCount:this.diagnostics.count}));alive=new AbortController();controller:AnalysisController;
  private previewClient=new ScanPreviewClient();
  private sourceRequest:AbortController|null=null;private sourceOpening:AbortController|null=null;private navigationVersion=0;private summaryRequest:AbortController|null=null;
  private listedNodes=new Map<string,MapNode>();private listedSnapshot="";
  private mapWorker:PackedMapClient|null=null;
  // Prepared reads remain private until server navigation confirms the exact target.
  private preparedMap:{key:string;result:ReturnType<NativeOwner['readMapSeed']>}|null=null;
  private mapSeedKey(view:NonNullable<ConfirmedAnalysis['state']['view']>,pin:NonNullable<ConfirmedAnalysis['pin']>){return JSON.stringify([view,pin.viewId,this.state.workspace.showRecycle,this.state.workspace.showSystem]);}
  private async readMapSeed(view:NonNullable<ConfirmedAnalysis['state']['view']>,pin:NonNullable<ConfirmedAnalysis['pin']>,signal:AbortSignal){
    const started=Date.now();let wire:Awaited<ReturnType<typeof readAnalysis>>=null,failed=1;
    const worker=this.mapWorker??(this.mapWorker=new PackedMapClient(error=>this.error(error),(...args)=>this.diagnostics.timing(...args)));
    const metadata=(async()=>{try{wire=await readAnalysis(view,pin,signal,fetch,{depth:1,elementBudget:1});failed=0;return wire;}finally{this.diagnostics.timing('HIERARCHY',Date.now()-started,{failed,pending:wire===null?1:0});}})();
    return Promise.all([metadata,worker.open({view,pin},signal,this.state.workspace.showRecycle,this.state.workspace.showSystem,()=>{if(!signal.aborted)this.update({mapPreparing:true});})]);
  }
  // Cache selectors, never leases: every return still obtains server-qualified navigation.
  private sourceViews=new Map<string,{inventory:ScopeInventory;view:NonNullable<ConfirmedAnalysis['state']['view']>;partial:PartialResult|null;terminalCode:string|null}>();
  readonly loadGeometry:GeometryLoader=async(root,mode,box,zoom,onFrame,signal)=>{const started=Date.now();let failed=1,first=false;try{if(!this.mapWorker)throw Error('MAP_NOT_READY');const result=await this.mapWorker.layout(root,{mode,box,zoom,recycle:this.state.workspace.showRecycle,system:this.state.workspace.showSystem,dpr:devicePixelRatio||1},frame=>{if(!first){first=true;this.diagnostics.timing('MAP_FIRST_FRAME',Date.now()-started);}onFrame?.(frame);},signal);failed=0;return result;}catch(error){if(!(error instanceof DOMException&&error.name==='AbortError'))this.error(error);throw error;}finally{this.diagnostics.timing('MAP_LAYOUT',Date.now()-started,{failed});}};
  private workspaceDirty=false;private workspaceTimer:ReturnType<typeof setTimeout>|undefined;
  private preferences=new PreferenceClient();private remembering=false;
  private profileClient=new ProfileActionClient();private operation:AbortController|null=null;
  private query:AbortController|null=null;private searchRequest:AbortController|null=null;
  private pendingTimer:ReturnType<typeof setTimeout>|undefined;private pendingStarted=0;private groupId:string|null=null;
  private timer:ReturnType<typeof setTimeout>|undefined;private epoch=0;private lastRenewal=0;
  private scanPending:{profileId:string;intent:PendingScan}|null=null;
  private controlPending:ControlIntent|null=null;
  private resetPending:IndexResetIntent|null=null;

  // Preserve the chosen map while a newly scanned profile has no readable generation.
  private preferredMap:'radial'|'treemap'='treemap';
  // Jobs belong to the service, not to the currently displayed profile.
  private observedJobs=new Map<string,string>();
  private budget:AnalysisRenderBudget|null=null;
  constructor(private publish:(s:NativeState)=>void,private denied:()=>void){
    this.controller=new AnalysisController(analysis=>{
      if(this.state.model&&this.state.mapTree&&analysis.pin?.snapshotId===this.state.analysis.pin?.snapshotId&&JSON.stringify({...analysis.state.view,selectedNodeId:null})===JSON.stringify({...this.state.analysis.state.view,selectedNodeId:null})){this.update({analysis,loading:false});return;}
      if(analysis.state.view)this.preferredMap=analysis.state.view.map;this.update({analysis,profileId:analysis.state.view?.profileId??this.state.profileId,model:null,mapTree:null,searchPage:null,groupPage:null});this.groupId=null;void this.hierarchy();},checkedReader,3);
  }
  private update(p:Partial<NativeState>){if(!this.alive.signal.aborted){this.state={...this.state,...p};this.publish(this.state);}}
  private error(error:unknown){
    if(this.alive.signal.aborted)return;
    if(error instanceof SessionDenied){this.dispose();this.publish(initial());this.denied();return;}
    // Only fixed public failure classes reach the presenter, never server exception text.
    const code=safeErrorCode(error);this.diagnostics.add('REQUEST_FAILED',code);
    this.update({error:code,...(code==='VIEW_EXPIRED'?{model:null}: {})});
  }
  // Observation recovery never clears unrelated query errors or unresolved mutation state.
  private observationError(error:unknown){
    if(error instanceof SessionDenied){this.error(error);return;}
    if(error instanceof Error&&['VIEW_EXPIRED','SNAPSHOT_MISMATCH'].includes(error.message)){this.error(error);return;}
    if(!this.state.statusError)this.diagnostics.add('STATUS_UNAVAILABLE');this.update({statusError:'STATUS_UNAVAILABLE'});
  }
  private runningProfile(status:StatusSnapshot){
    const shares=new Set(this.state.sources?.shares.map(s=>s.id)),volumes=new Set(this.state.sources?.volumes.map(v=>v.id));
    return [ ...(status.active?[status.active]:[]), ...status.queued ].find(job=>{
      const profile=this.state.profiles.find(p=>p.profile.id===job.profileId)?.profile;if(!profile?.active)return false;
      const scope=profile.scope;
      return scope.kind==='nas'?volumes.size>0:scope.targetIds.length>0&&scope.targetIds.every(id=>
        scope.kind==='shares'||scope.kind==='folder'?shares.has(id):scope.kind==='volumes'?volumes.has(id):shares.has(id)||volumes.has(id));
    });
  }
  setBudget(budget:AnalysisRenderBudget|null){if(JSON.stringify(budget)===JSON.stringify(this.budget))return;this.budget=budget;void this.hierarchy();}
  private async catalogue(signal:AbortSignal){
    const data=await readView<{profiles:NativeProfileEntry[];dsmBuild:string}>({action:'profiles'},signal);signal.throwIfAborted();
    if(!Array.isArray(data.profiles)||data.profiles.length>200||new Set(data.profiles.map(p=>p.profile?.id)).size!==data.profiles.length||data.profiles.some(p=>!id(p.profile?.id)||typeof p.profile.name!=='string'||!Number.isInteger(p.profile.revision)||!p.profile.scope||!(p.activeSnapshotId===null||id(p.activeSnapshotId))))throw Error('INVALID_RESPONSE');
    if(typeof data.dsmBuild!=='string')throw Error('INVALID_RESPONSE');
    this.update({profiles:data.profiles,dsmBuild:data.dsmBuild});void this.refreshSummaries(data.profiles);return data.profiles;
  }
  // Metadata batches are scoped to this authenticated catalogue and cancelled on replacement.
  private async refreshSummaries(profiles:NativeProfileEntry[]){
    this.summaryRequest?.abort();const request=this.summaryRequest=new AbortController();
    const signal=AbortSignal.any([request.signal,this.alive.signal]),rows:ProfileCardSummary[]=[];
    try{const ids=profiles.filter(p=>p.profile.active).map(p=>p.profile.id);for(let i=0;i<ids.length;i+=32)rows.push(...await loadProfileSummaries(ids.slice(i,i+32),signal));signal.throwIfAborted();this.update({summaries:rows,summariesReady:true});}
    catch(error){if(!signal.aborted){this.update({summaries:[],summariesReady:false});if(error instanceof SessionDenied)this.error(error);}}
  }
  setFreshness(value:FreshnessPreference){const user=this.preferences.preference?.dsmUserId;if(!user)return;try{writeFreshness(user,value,localStorage);this.update({freshness:value});}catch{this.error(Error('PREFERENCE_UNAVAILABLE'));}}
  // Resolve a live source against one qualified reader; never splice together cached generations.
  async openScope(scope:Scope):Promise<boolean>{
    // A source selection owns subsequent profile work; background adoption cannot overtake it.
    this.sourceRequest?.abort();this.operation?.abort();this.controller.cancel();const request=this.sourceRequest=new AbortController();
    const navigationVersion=++this.navigationVersion;this.sourceOpening=request;let resolved=false;
    try{
    const signal=AbortSignal.any([request.signal,this.alive.signal,AbortSignal.timeout(15000)]),inventory=this.state.sources;
    const qualify=()=>{signal.throwIfAborted();if(navigationVersion!==this.navigationVersion)throw new DOMException('Superseded','AbortError');};
    // Coalesce rapid source selections before starting reader and map requests on the NAS.
    // Every abandoned selection settles promptly and cannot publish after its owning abort.
    await new Promise<void>(resolve=>setTimeout(resolve,75));qualify();
    if(!inventory)return false;
    const candidates=this.state.profiles.filter(p=>p.profile.active&&hasRecordedResult(p,this.state.summaries.find(s=>s.profileId===p.profile.id),this.state.summariesReady)&&scopeCovers(p.profile.scope,scope,inventory));
    const captured=(p:NativeProfileEntry)=>{const s=this.state.summaries.find(s=>s.profileId===p.profile.id);return Math.max(Date.parse(s?.endedAt??'1970-01-01'),Date.parse(s?.lastAttempt?.endedAt??'1970-01-01'));};
    candidates.sort((a,b)=>captured(b)-captured(a)||Number(scopeEqual(b.profile.scope,scope))-Number(scopeEqual(a.profile.scope,scope))||Date.parse(this.state.summaries.find(s=>s.profileId===b.profile.id)?.endedAt??'1970-01-01')-Date.parse(this.state.summaries.find(s=>s.profileId===a.profile.id)?.endedAt??'1970-01-01'));
    const candidate=candidates[0];if(!candidate)return false;
    // A new profile revision, published snapshot or job result invalidates the selector.
    const summary=this.state.summaries.find(s=>s.profileId===candidate.profile.id);
    const key=JSON.stringify([scope,candidate.profile.id,candidate.profile.revision,candidate.activeSnapshotId,candidate.latestJobId,summary?.snapshotId,summary?.lastAttempt]);
    const cached=this.sourceViews.get(key);
    if(cached?.inventory===inventory){
      this.previewClient.clear();
      await this.move({...cached.view,map:this.preferredMap},'replace');signal.throwIfAborted();
      const current=this.controller.current;
      if(current.pin?.profileId===cached.view.profileId&&current.pin.snapshotId===cached.view.snapshotId&&current.state.view?.rootId===cached.view.rootId){
        this.update({preview:null,partial:cached.partial,terminalCode:cached.terminalCode});resolved=true;return true;
      }
      return false;
    }
    // An interrupted profile opening may leave the old controller lease behind a cleared presenter.
    // Reacquire that reader instead of treating its old root as an already displayed selection.
    if(this.controller.current.pin?.profileId!==candidate.profile.id||!this.controller.current.state.view||this.state.analysis.pin?.viewId!==this.controller.current.pin?.viewId)await this.openProfile(candidate.profile.id,null,true);qualify();
    if(this.controller.current.pin?.profileId!==candidate.profile.id||!this.controller.current.state.view)return false;
    // Use the reader-qualified virtual root, never its request alias.
    let root=this.controller.current.navigation?.breadcrumb[0]?.id??this.state.model?.rootId;if(!root)return false;
    const find=async(parent:string,predicate:(n:NativeStorageNode)=>boolean)=>{
      let cursor='',pageRoot=parent;const seen=new Set<string>();
      for(let i=0;i<32;i++){const page=await this.children(pageRoot,cursor,signal);qualify();const found=page.items.find(n=>predicate(n as NativeStorageNode));if(found)return found as NativeStorageNode;
        if(!page.nextCursor)return null;if(seen.has(page.nextCursor))throw Error('CURSOR_CYCLE');seen.add(page.nextCursor);cursor=page.nextCursor;pageRoot=page.rootId;}
      throw Error('QUERY_BUDGET_EXCEEDED');
    };
    if(scope.kind!=='nas'){
      const target=scope.targetIds[0],volume=scope.kind==='volumes'?target:inventory.shares.find(s=>s.id===target)?.volumeId;
      const v=await find(root,n=>n.sourceKind==='volume'&&n.volumeId===volume);if(!v)return false;root=v.id;
      if(scope.kind!=='volumes'){
        const share=await find(root,n=>n.sourceKind==='share'&&n.shareId===target);if(!share)return false;root=share.id;
        if(scope.kind==='folder'){
          const prefix=candidate.profile.scope.kind==='folder'?(candidate.profile.scope.relativePath??'.'):'.',path=scope.relativePath??'.';
          const tail=prefix==='.'?path:path===prefix?'.':path.slice(prefix.length+1);
          for(const name of tail==='.'?[]:tail.split('/')){const child=await find(root,n=>n.kind==='folder'&&n.name===name);if(!child)return false;root=child.id;}
        }
      }
    }
    qualify();await this.navigate(root);signal.throwIfAborted();
    // Apply a mode chosen during source loading only after that source owns the reader.
    const view=this.controller.current.state.view;
    if(view?.rootId===root&&view.map!==this.preferredMap){await this.move({...view,map:this.preferredMap},'replace');signal.throwIfAborted();}
    const current=this.controller.current.state.view;
    if(current&&current.rootId===root){
      this.sourceViews.set(key,{inventory,view:structuredClone(current),partial:this.state.partial,terminalCode:this.state.terminalCode});
      while(this.sourceViews.size>64)this.sourceViews.delete(this.sourceViews.keys().next().value!);
    }
    resolved=true;return true;
    }finally{if(this.sourceOpening===request){this.sourceOpening=null;if(resolved)void this.hierarchy();}}
  }
  async start(){
    const started=Date.now();
    const signal=AbortSignal.any([this.alive.signal,AbortSignal.timeout(10000)]);this.update({loading:true});await this.diagnostics.configure(signal);
    try{
      const [inventory,profiles,preference]=await Promise.all([loadScopeInventory(signal),this.catalogue(signal),this.preferences.read(signal).catch(error=>{if(error instanceof SessionDenied)throw error;return null;})]);signal.throwIfAborted();
      this.update({sources:checkedScopeInventory(inventory)});
      // Read browser-local presentation only after the DSM account identity has been verified.
      if(preference){const workspace=preference.workspace??defaultWorkspace();let freshness=defaultFreshness();try{freshness=readFreshness(preference.dsmUserId,localStorage);}catch{/* Browser storage may be disabled. */}this.update({language:preference.language==='en'?'en':'de',theme:preference.theme==='light'?'light':'dark',workspace,sidebars:{left:workspace.left,right:workspace.right},freshness,layoutReady:true});}
      // Prefer a completed native share scan over synthetic acceptance profiles on first launch.
      const shareIds=new Set(this.state.sources?.shares.map(share=>share.id));
      let running:ReturnType<NativeOwner['runningProfile']>;
      try{const status=await readStatus(signal);signal.throwIfAborted();this.update({status,statusError:null});running=this.runningProfile(status);}
      catch(error){if(error instanceof SessionDenied||signal.aborted)throw error;this.observationError(error);}
      for(const saved of preference?.workspace?.jobs??[]){const [job,profile]=saved.split('|');if(id(job)&&id(profile))this.observedJobs.set(job,profile);}
      if(running){this.observedJobs.set(running.jobId,running.profileId);}
      const first=profiles.find(p=>p.profile.id===preference?.lastView?.profileId&&p.profile.active&&(p.activeSnapshotId||p.latestJobId))??[...profiles].reverse().find(p=>p.activeSnapshotId&&p.profile.active&&['shares','folder'].includes(p.profile.scope.kind)&&p.profile.scope.targetIds.every(id=>shareIds.has(id)))??profiles.find(p=>p.activeSnapshotId)??profiles[0];
      if(first){await this.openProfile(first.profile.id);
        // Restore only an exact captured snapshot; stale identities never cross generations.
        const saved=preference?.lastView,pin=this.controller.current.pin;
        if(saved&&pin&&saved.profileId===pin.profileId&&saved.snapshotId===pin.snapshotId)await this.move(saved,'replace');
      }
    }catch(error){if(!this.alive.signal.aborted)this.error(error);}
    finally{this.diagnostics.timing('STARTUP',Date.now()-started);this.update({loading:false});if(!this.alive.signal.aborted)void this.observe();}
  }
  async openProfile(profileId:string,partial:PartialResult|null=null,preserveNavigation=false){
    if(this.alive.signal.aborted||!this.state.profiles.some(p=>p.profile.id===profileId))return;
    // Nested partial adoption retains its caller's intent instead of creating a competing selection.
    const navigationVersion=preserveNavigation?this.navigationVersion:++this.navigationVersion;
    this.previewClient.clear();this.operation?.abort();this.query?.abort();this.searchRequest?.abort();this.controller.cancel();
    const operation=this.operation=new AbortController(),epoch=++this.epoch;
    const signal=AbortSignal.any([this.alive.signal,operation.signal,AbortSignal.timeout(10000)]),viewId=crypto.randomUUID();
    let retained=false;const started=Date.now();
    this.diagnostics.add('SOURCE_OPEN');
    this.update({preview:null,partial,terminalCode:partial?'INCOMPLETE_COVERAGE':null,profileId,analysis:emptyAnalysis(),model:null,searchPage:null,loading:true,error:null});
    try{
      // Resolve a completed partial result before acquiring a reader, avoiding an unused NAS lease.
      if(!partial)partial=await this.readLatestResult(profileId,epoch,signal,navigationVersion,false)??null;
      signal.throwIfAborted();if(epoch!==this.epoch||navigationVersion!==this.navigationVersion)return;
      const reader=await requestReader<RestoredReader>({contractVersion:1,operation:'open',profileId,viewId,...(partial?{jobId:partial.jobId}:{})},signal);
      signal.throwIfAborted();if(epoch!==this.epoch||navigationVersion!==this.navigationVersion)return;
      if(reader.lease&&reader.lease.id!==viewId)throw Error('SNAPSHOT_MISMATCH');
      if(partial){if(reader.view.state!=='partial'||reader.view.snapshotId!==partial.snapshotId||reader.view.endedAt!==partial.endedAt)throw Error('SNAPSHOT_MISMATCH');this.controller.rememberPartial(partial.snapshotId,partial.jobId);}
      const desired=reader.view.snapshotId?{...defaultView(profileId,reader.view.snapshotId),map:this.preferredMap}:undefined;
      retained=await this.controller.accept(profileId,reader,signal,desired);
      if(retained)void this.remember();
    }catch(error){if(!operation.signal.aborted&&epoch===this.epoch){this.update({loading:false});this.error(error);}}
    finally{
      this.diagnostics.timing('READER_OPEN',Date.now()-started,{failed:retained?0:1});
      if(!retained)void requestReader({contractVersion:1,operation:'release',profileId,viewId},AbortSignal.timeout(8000)).catch(()=>{});
      if(epoch===this.epoch&&!this.controller.current.state.view)this.update({loading:false});
    }
  }
  // Display completed partial results automatically without changing the complete snapshot pointer.
  private async readLatestResult(profileId:string,epoch:number,signal:AbortSignal,navigationVersion:number,adopt=true):Promise<PartialResult|undefined>{
    const jobId=this.state.profiles.find(p=>p.profile.id===profileId)?.latestJobId;if(!jobId||!id(jobId))return;
    const result=await readView<{job:{id:string;profileId:string;state:string;profileRevision:number;resultSnapshotId:string|null};snapshotState:string;snapshotEndedAt:string|null;coverageErrors:number;reasons?:Record<string,number>}>({action:'job',id:jobId},signal);
    signal.throwIfAborted();if(epoch!==this.epoch||navigationVersion!==this.navigationVersion)return;
    if(result?.job?.id!==jobId||result.job.profileId!==profileId)throw Error('SNAPSHOT_MISMATCH');
    if(result.job.state==='completed'&&result.snapshotState==='partial'){
      if(!id(result.job.resultSnapshotId)||!result.snapshotEndedAt||!Number.isFinite(Date.parse(result.snapshotEndedAt))||!Number.isSafeInteger(result.coverageErrors)||result.coverageErrors<0)throw Error('INVALID_RESPONSE');
      const partial:PartialResult={jobId,snapshotId:result.job.resultSnapshotId,endedAt:result.snapshotEndedAt,coverageErrors:result.coverageErrors};
      this.update({preview:null,partial,terminalCode:'INCOMPLETE_COVERAGE'});
      try{
        const report=checkedScanBoundaryReport(await readView<ScanBoundaryReport>({action:'scan-boundaries',profileId,jobId},signal),{jobId,snapshotId:partial.snapshotId,state:'partial',profileRevision:result.job.profileRevision});
        signal.throwIfAborted();if(epoch!==this.epoch)return;
        // A truncated sample must never be presented as a complete cause breakdown.
        if(!report.truncated){const reasons:Record<string,number>={};for(const boundary of report.boundaries)if(!['PACKAGE_STATE_EXCLUDED','PROFILE_EXCLUDED','SNAPSHOT_TREE_EXCLUDED'].includes(boundary.reasonCode))reasons[boundary.reasonCode]=(reasons[boundary.reasonCode]??0)+1;this.update({partial:{...partial,reasons}});}
      }catch(error){if(error instanceof SessionDenied)throw error;this.diagnostics.add('REQUEST_FAILED');}
      this.diagnostics.add('SCAN_PARTIAL','INCOMPLETE_COVERAGE');
      // Reuse the qualified job reader; preserve coverage details and avoid stale navigation.
      if(adopt&&!signal.aborted&&epoch===this.epoch&&navigationVersion===this.navigationVersion&&this.state.profileId===profileId&&this.controller.current.pin?.snapshotId!==partial.snapshotId){
        await this.openProfile(profileId,this.state.partial??partial,true);
      }
      return this.state.partial??partial;
    }else if(['failed','cancelled','interrupted'].includes(result.job.state)){
      const code=result.job.state==='failed'?'SCAN_FAILED':result.job.state==='cancelled'?'SCAN_CANCELLED':'SCAN_INTERRUPTED';this.update({terminalCode:code});this.diagnostics.add('SCAN_FAILED',code);
    }
  }
  async openPartial(){const partial=this.state.partial,profileId=this.state.profileId;if(partial&&profileId&&!this.state.busy)await this.openProfile(profileId,partial);}
  // Persist only the qualified selector in existing per-user preferences, never file data in browser storage.
  private async remember(){
    const view=this.controller.current.state.view;
    if(!view||this.alive.signal.aborted||!this.preferences.preference)return;
    if(this.remembering||this.preferences.status==='saving'){setTimeout(()=>void this.remember(),300);return;}
    this.remembering=true;
    try{if(this.preferences.status==='uncertain')await this.preferences.retry();if(this.preferences.status==='conflict')await this.preferences.read(this.alive.signal);await this.preferences.writeLastView(view);}catch(error){if(error instanceof SessionDenied)this.error(error);}
    finally{this.remembering=false;}
  }
  private async hierarchy(retry=false){
    // Scope resolution may visit intermediate NAS roots; render only its final qualified target.
    if(this.sourceOpening)return;
    clearTimeout(this.pendingTimer);if(!retry)this.pendingStarted=Date.now();
    this.query?.abort();const request=this.query=new AbortController();
    const {view}=this.controller.current.state,pin=this.controller.current.pin,measuredBudget=this.budget;
    // Hierarchy qualifies root metadata only; the independent packed map stream supplies all rendered detail.
    // Expanding this duplicate tree can exhaust the query deadline before the actual map starts.
    const budget=view&&measuredBudget?{depth:1,elementBudget:1}:null;
    if(!view||!pin||!budget){this.update({model:null,hierarchyPending:false});return;}
    const signal=AbortSignal.any([request.signal,this.alive.signal]);this.update({loading:true,error:null,mapPreparing:false});
    try{
      const key=this.mapSeedKey(view,pin),prepared=this.preparedMap;this.preparedMap=null;
      const [wire,root]=await (prepared?.key===key?prepared.result:this.readMapSeed(view,pin,signal));
      signal.throwIfAborted();
      const model=wire?adaptHierarchy(wire,{snapshotId:view.snapshotId,rootId:view.rootId,metric:view.metric,mode:view.filterMode,maxNodes:budget.elementBudget}):null;
      this.update({model,hierarchyPending:wire===null,...(wire?{mapTree:{...root,name:root.name||model?.nodes[model.rootId]?.name||''}}:{})});
      // Background projections may outlive a few quick retries, but never leave an endless spinner.
      if(wire===null){if(Date.now()-this.pendingStarted>=120000){this.update({hierarchyPending:false});throw Error('FILTER_PREPARATION_TIMEOUT');}this.pendingTimer=setTimeout(()=>{if(!signal.aborted)void this.hierarchy(true);},1000);}
    }catch(error){if(!signal.aborted)this.error(error);}
    finally{if(this.query===request)this.update({loading:false,mapPreparing:false});}
  }
  // Tree and list pages share the leased snapshot, but not the map's rendering budget.
  async children(nodeId:string,cursor:string,caller:AbortSignal):Promise<import('./DirectoryTree').DirectoryPage>{
    const view=this.controller.current.state.view,pin=this.controller.current.pin;
    if(!view||!pin||!id(nodeId))throw Error('SNAPSHOT_MISMATCH');
    const signal=AbortSignal.any([caller,this.alive.signal]);
    const page=await readGroupPage(view,pin,{rootId:nodeId,cursor:'',reason:'directory-list',directChildren:1n},cursor,signal);
    signal.throwIfAborted();
    if(this.controller.current.pin?.snapshotId!==pin.snapshotId||this.controller.current.pin?.viewId!==pin.viewId)throw new DOMException('Superseded','AbortError');
    if(!page)throw Error('QUERY_BUDGET_EXCEEDED');
    if(this.listedSnapshot!==pin.snapshotId){this.listedSnapshot=pin.snapshotId;this.listedNodes.clear();}
    for(const node of page.items)this.listedNodes.set(node.id,node);
    // Cap the navigation admission cache independently of the persisted index size.
    while(this.listedNodes.size>20000)this.listedNodes.delete(this.listedNodes.keys().next().value!);
    const parent=this.state.model?.nodes[nodeId]??this.listedNodes.get(nodeId)??this.controller.current.navigation?.breadcrumb.find(n=>n.id===nodeId);
    if(!cursor&&!page.nextCursor&&page.items.length===1&&page.items[0].kind==='directory'&&(page.items[0].name==='.'||parent?.kind==='share'&&page.items[0].name===parent.name))return this.children(page.items[0].id,'',caller);
    return {items:page.items.map(present),nextCursor:page.nextCursor,rootId:page.rootId};
  }
  private fileListRoot:{viewId:string;snapshotId:string;rootId:string;resolved:string}|null=null;
  // Query one globally ordered page under the current lease; late replies cannot cross views.
  async fileList(options:ListOptions,cursor:string,caller:AbortSignal):Promise<FileListPage>{
    const view=this.controller.current.state.view,pin=this.controller.current.pin;
    if(!view||!pin)throw Error('SNAPSHOT_MISMATCH');
    const signal=AbortSignal.any([caller,this.alive.signal]);
    // NAS/volume lists retain their direct virtual children instead of flattening into captured roots.
    const kind=this.state.model?.nodes[view.rootId]?.kind??this.controller.current.navigation?.breadcrumb.find(n=>n.id===view.rootId)?.kind;
    if(!options.recursive&&['nas','volume','other'].includes(kind??'')){
      const items:NativeStorageNode[]=[];let after='';const seen=new Set<string>();
      do{
        const part=await this.children(view.rootId,after,signal);items.push(...part.items as NativeStorageNode[]);
        if(!part.nextCursor)break;
        if(seen.has(part.nextCursor))throw Error('INVALID_RESPONSE');seen.add(part.nextCursor);after=part.nextCursor;
      }while(true);
      const text=options.text.toLocaleLowerCase(),escaped=text.split('*').map(s=>s.replace(/[.*+?^${}()|[\]\\]/g,'\\$&'));
      const pattern=text.includes('*')?new RegExp('^'+escaped.join('.*')+'$','i'):null;
      const rows=items.filter(n=>(options.kind==='all'||(options.kind==='file')===(n.kind==='file'))&&!options.category&&(!text||(pattern?pattern.test(n.name):n.name.toLocaleLowerCase().includes(text))));
      rows.sort((a,b)=>{
        const av=BigInt(a.bytesExact??'0'),bv=BigInt(b.bytesExact??'0');
        const value=options.sort==='size'?(av<bv?-1:av>bv?1:0):a.name.localeCompare(b.name);
        return (options.direction==='desc'?-value:value)||a.id.localeCompare(b.id);
      });
      const start=Number(cursor)||0;return {items:rows.slice(start,start+50),nextCursor:start+50<rows.length?String(start+50):null};
    }
    // Hide the physical shared-folder wrapper just as direct child navigation does.
    const cached=this.fileListRoot;
    const resolved=cached?.viewId===pin.viewId&&cached.snapshotId===pin.snapshotId&&cached.rootId===view.rootId?cached.resolved:(await this.children(view.rootId,'',signal)).rootId;
    this.fileListRoot={viewId:pin.viewId,snapshotId:pin.snapshotId,rootId:view.rootId,resolved};
    const queryView={...view,rootId:resolved,filter:{categories:options.category?[options.category]:[],objectKinds:options.kind==='all'?[]:[options.kind]},search:{text:options.text,field:(options.text?'both':'name') as 'both'|'name',scope:options.recursive?'recursive' as const:'direct' as const},sort:options.sort,direction:options.direction};
    const page=await readSearchList(queryView,pin,cursor,signal,fetch,{matchMode:options.text.includes('*')?'glob':'literal',foldersFirst:options.foldersFirst,categorySort:options.sort==='type'});
    signal.throwIfAborted();if(this.controller.current.pin?.viewId!==pin.viewId||this.controller.current.pin?.snapshotId!==pin.snapshotId)throw new DOMException('Superseded','AbortError');
    // Register real result identities for folder navigation, including results outside the map budget.
    for(const n of page.items)this.listedNodes.set(n.nodeId,{id:n.nodeId,kind:n.kind} as MapNode);
    while(this.listedNodes.size>20000)this.listedNodes.delete(this.listedNodes.keys().next().value!);
    return {items:page.items.map(n=>({id:n.nodeId,name:n.name,kind:n.kind==='file'?'file':'folder',parentId:n.parentId,path:n.path,category:n.classification?.category??undefined,bytesExact:n.size??undefined,unavailable:n.size===null||!['file','directory'].includes(n.kind)})),nextCursor:page.hasMore?page.nextCursor:null};
  }
  async navigate(nodeId:string,listedKind?:StorageNode['kind']){
    const view=this.controller.current.state.view;if(!view||!id(nodeId))return;
    const known=this.state.model?.nodes[nodeId]??this.state.groupPage?.items.find(n=>n.id===nodeId)??(this.listedSnapshot===view.snapshotId?this.listedNodes.get(nodeId):undefined),crumb=this.controller.current.navigation?.breadcrumb.some(n=>n.id===nodeId),result=this.state.searchPage?.items.find(n=>n.nodeId===nodeId);
    if(known?.kind==='group'){
      // Group identities are presentation-only; enter their qualified parent to expose real children.
      const root=known.continuation?.rootId;if(root&&root!==view.rootId)await this.move({...view,rootId:root,selectedNodeId:null,search:{...view.search,text:''}});
      return;
    }
    // Displayed directory rows may outlive the bounded cache; the server still validates the leased node.
    if(!crumb&&!(known&&['nas','volume','share','other','directory'].includes(known.kind))&&result?.kind!=='directory'&&listedKind!=='folder'&&listedKind!=='share'&&listedKind!=='volume')return;
    await this.move({...view,rootId:nodeId,selectedNodeId:null,search:{...view.search,text:''}});
  }
  private async move(view:NonNullable<ConfirmedAnalysis['state']['view']>,kind:'navigate'|'back'|'replace'='navigate'){
    // Re-selecting the current folder must not cancel its in-flight map preparation.
    if(kind!=='back'&&JSON.stringify(view)===JSON.stringify(this.controller.current.state.view)){
      // A superseded mode/source transition may have cancelled this root's stream.
      if(this.query?.signal.aborted)void this.hierarchy();
      return;
    }
    this.navigationVersion++;
    this.operation?.abort();this.query?.abort();this.searchRequest?.abort();const epoch=++this.epoch;
    this.update({loading:true,error:null,searchPage:null});
    try{await this.controller.move(view,kind,(target,pin,caller)=>{
      const request=new AbortController(),signal=AbortSignal.any([caller,request.signal,this.alive.signal]);
      const key=this.mapSeedKey(target,pin),result=this.readMapSeed(target,pin,signal);void result.catch(()=>{});
      return {discard:()=>request.abort(),confirm:()=>{if(!signal.aborted)this.preparedMap={key,result};}};
    });if(epoch===this.epoch)void this.remember();}catch(error){if(epoch===this.epoch){this.update({loading:false});this.error(error);}}finally{if(epoch===this.epoch&&!this.controller.current.state.view)this.update({loading:false});}
  }
  // Server groups use their qualified child cursor; virtual IDs never enter navigation requests.
  async openGroup(nodeId:string,cursor?:string){
    const group=this.state.model?.nodes[nodeId],view=this.controller.current.state.view,pin=this.controller.current.pin;
    if(!group?.continuation||!view||!pin)return;
    this.searchRequest?.abort();const request=this.searchRequest=new AbortController();const signal=AbortSignal.any([request.signal,this.alive.signal]);
    try{const page=await readGroupPage(view,pin,group.continuation,cursor??group.continuation.cursor,signal);signal.throwIfAborted();this.groupId=nodeId;this.update({groupPage:page,searchPage:null,hierarchyPending:page===null});}
    catch(error){if(!signal.aborted)this.error(error);}
  }
  async nextGroup(){if(this.groupId&&this.state.groupPage?.nextCursor)await this.openGroup(this.groupId,this.state.groupPage.nextCursor);}
  // Persist layout independently of remembered navigation and its server CAS writer.
  setSidebars(sidebars:SidebarPreference){this.setWorkspace(sidebars);}
  // Coalesce drag/scroll events and serialize with the existing preference CAS writer.
  setWorkspace(patch:Partial<PreferenceWorkspace>){
    const workspace={...this.state.workspace,...patch};
    const visibilityChanged=workspace.showRecycle!==this.state.workspace.showRecycle||workspace.showSystem!==this.state.workspace.showSystem;
    this.update({workspace,sidebars:{left:workspace.left,right:workspace.right}});this.workspaceDirty=true;if(visibilityChanged){this.update({mapTree:null});void this.hierarchy();}
    clearTimeout(this.workspaceTimer);this.workspaceTimer=setTimeout(()=>void this.saveWorkspace(),300);
  }
  private async saveWorkspace(){
    if(!this.workspaceDirty||this.alive.signal.aborted||!this.preferences.preference)return;
    // Index maintenance owns the control-store mutation lock; save presentation after it finishes.
    if(this.resetPending||this.remembering||this.preferences.status==='saving'){this.workspaceTimer=setTimeout(()=>void this.saveWorkspace(),300);return;}
    this.remembering=true;const workspace=this.state.workspace;
    try{
      if(this.preferences.status==='uncertain')await this.preferences.retry();
      if(this.preferences.status==='conflict')await this.preferences.read(this.alive.signal);
      await this.preferences.writeWorkspace(workspace);
      this.workspaceDirty=this.state.workspace!==workspace;
    }catch(error){if(error instanceof SessionDenied)this.error(error);else this.diagnostics.add('REQUEST_FAILED','PREFERENCE_UNAVAILABLE');}
    finally{this.remembering=false;if(this.workspaceDirty&&!this.alive.signal.aborted)this.workspaceTimer=setTimeout(()=>void this.saveWorkspace(),2000);}
  }

  // Appearance updates reuse the existing authenticated CAS writer and preserve unrelated fields.
  async setAppearance(kind:'language'|'theme',value:'de'|'en'|'dark'|'light'){
    if(this.alive.signal.aborted)return;
    this.update(kind==='language'?{language:value as 'de'|'en'}:{theme:value as 'dark'|'light'});
    if(this.remembering||this.preferences.status==='saving'){setTimeout(()=>{if((kind==='language'?this.state.language:this.state.theme)===value)void this.setAppearance(kind,value);},300);return;}
    this.remembering=true;
    try{if(this.preferences.status==='uncertain')await this.preferences.retry();if(this.preferences.status==='conflict')await this.preferences.read(this.alive.signal);
      if(kind==='language')await this.preferences.writeLanguage(value as 'de'|'en');else await this.preferences.writeTheme(value as 'dark'|'light');
    }catch(error){this.error(error);}finally{this.remembering=false;}
  }
  async setMap(map:'radial'|'treemap'){this.preferredMap=map;if(this.sourceOpening)return;const view=this.controller.current.state.view;if(view&&view.map!==map)await this.move({...view,map},'replace');}
  // Selection reuses the same geometry and persists only the qualified node identity.
  async selectFile(nodeId:string){const view=this.controller.current.state.view;if(view&&id(nodeId))await this.move({...view,selectedNodeId:nodeId},'replace');}
  async back(){const target=this.controller.current.state.back.at(-1);if(target)await this.move(target,'back');}
  async search(text:string,cursor=''){
    // Closing or clearing search must restore the map list, not run an unfiltered recursive search.
    if(!text.trim()){this.searchRequest?.abort();this.groupId=null;this.update({searchPage:null,groupPage:null});return;}
    const view=this.controller.current.state.view,pin=this.controller.current.pin;if(!view||!pin||text.length>512)return;
    this.searchRequest?.abort();const request=this.searchRequest=new AbortController();
    const signal=AbortSignal.any([this.alive.signal,request.signal]);this.update({error:null,groupPage:null});
    try{const page=await readSearchList({...view,search:{...view.search,text,field:'name',scope:'recursive'}},pin,cursor,signal);signal.throwIfAborted();this.update({searchPage:page});}
    catch(error){if(!signal.aborted)this.error(error);}
  }
  // Refresh only root inventory; never open a reader, start a scan, or reset navigation.
  async refreshInventory(){
    if(this.state.inventoryRefreshing||this.alive.signal.aborted)return;
    this.update({inventoryRefreshing:true,inventoryError:false});
    try{const inventory=checkedScopeInventory(await loadScopeInventory(AbortSignal.any([this.alive.signal,AbortSignal.timeout(10000)])));this.update({sources:inventory});}
    catch(error){if(error instanceof SessionDenied)this.error(error);else this.update({inventoryError:true});}
    finally{this.update({inventoryRefreshing:false});}
  }
  async refresh(){if(this.state.profileId)await this.openProfile(this.state.profileId);else{try{const profiles=await this.catalogue(AbortSignal.any([this.alive.signal,AbortSignal.timeout(8000)]));if(profiles[0])await this.openProfile(profiles[0].profile.id);}catch(error){this.error(error);}}}
  // A scan is always bound to a server-qualified profile, never to a historical node identifier.
  async scanScope(scope:Scope,name:string,force=false){
    if(this.state.busy||this.scanPending||this.profileClient.unresolved||this.alive.signal.aborted)return;
    this.update({busy:true,error:null});
    const signal=AbortSignal.any([this.alive.signal,AbortSignal.timeout(10000)]);
    try{
      const inventory=checkedScopeInventory(await loadScopeInventory(signal));signal.throwIfAborted();
      const check=checkedScopeCheck(await checkSelectedScope(scope,signal),scope,inventory);signal.throwIfAborted();
      if(check.roots.some(root=>root.state!=='reachable'))throw Error('SCOPE_UNAVAILABLE');
      // Capture scan category choices as immutable native exclusion rules for this job.
      const excluded=[...(this.state.workspace.scanRecycle===false?['#recycle/']:[]),...(this.state.workspace.scanSystem===false?['@eaDir/','.DS_Store','.SMB*','.smb*','.AppleDouble/','._*','.Spotlight-V100/','.Trashes/','.fseventsd/']:[])];
      const configuration={...newProfileConfiguration(),name,scope,exclusions:excluded.map(value=>({kind:'pattern' as const,value}))};
      const profiles=await this.catalogue(signal);
      // Reuse only an equivalent active scope/configuration; names are presentation, not cache identity.
      const cached=matchingCachedProfile(profiles,configuration);
      let profile=cached?.profile??profiles.find(p=>p.profile.active&&configurationKey({...p.profile,name:''})===configurationKey({...configuration,name:''}))?.profile;
      if(cached&&!force){this.diagnostics.add('CACHE_REUSED');await this.openProfile(cached.profile.id);return;}
      if(!force&&profile&&profiles.find(p=>p.profile.id===profile!.id)?.latestJobId){await this.openProfile(profile.id);return;}
      if(!profile)profile=await this.profileClient.save(null,configuration);
      this.alive.signal.throwIfAborted();if(!this.state.profiles.some(p=>p.profile.id===profile!.id))this.update({profiles:[...this.state.profiles,{profile,activeSnapshotId:null,latestJobId:null}]});
      await this.beginScan(profile.id);
    }catch(error){this.error(error);}finally{this.update({busy:false,pending:!!this.resetPending||!!this.scanPending||!!this.controlPending||this.profileClient.unresolved});}
  }
  private async beginScan(profileId:string){this.scanPending={profileId,intent:scanIntent(null,'full')};await this.sendScan();}
  private async sendScan(){
    const pending=this.scanPending;if(!pending)return;
    const signal=AbortSignal.any([this.alive.signal,AbortSignal.timeout(8000)]);
    const response=await requestScan({contractVersion:1,profileId:pending.profileId,mode:pending.intent.mode},pending.intent.key,signal).catch(error=>{if(error instanceof Error&&['JOB_CONFLICT','QUEUE_FULL','IDEMPOTENCY_CONFLICT'].includes(error.message))this.scanPending=null;throw error;}) as {contractVersion:number;data:{job:{id:string;profileId:string}}};
    signal.throwIfAborted();const job=response?.data?.job;
    if(response.contractVersion!==1||!job||!id(job.id)||job.profileId!==pending.profileId)throw Error('INVALID_RESPONSE');
    // Queue admission must not replace the view or progress of an existing job.
    const background=!!this.state.status?.active||!!this.state.status?.queued.length;
    this.diagnostics.add('SCAN_SUBMITTED');this.scanPending=null;this.observedJobs.set(job.id,pending.profileId);this.persistObservedJobs();this.update({pending:false});
    const status=await readStatus(signal);signal.throwIfAborted();this.update({status});
    if(background||status.queued.some(item=>item.jobId===job.id)||(status.active&&status.active.jobId!==job.id))return;
    this.update({terminalCode:null,partial:null,profileId:pending.profileId});
    if(this.controller.current.state.view?.profileId!==pending.profileId)await this.openProfile(pending.profileId);
  }
  async rescan(){if(!this.state.profileId||this.state.busy||this.scanPending||this.profileClient.unresolved)return;this.update({busy:true,error:null});try{await this.beginScan(this.state.profileId);}catch(error){this.error(error);}finally{this.update({busy:false,pending:!!this.scanPending});}}
  // Reset invalidates every local reader; an uncertain response retains the original replay key.
  async resetIndex(scope:Scope|null){
    if(this.state.busy||this.state.pending)return;
    this.resetPending={key:crypto.randomUUID(),scope};
    await this.retryMutation();
  }
  private async sendReset(){
    if(!this.resetPending)return;
    try{await requestIndexReset(this.resetPending,AbortSignal.any([this.alive.signal,AbortSignal.timeout(100000)]));}
    catch(error){if(error instanceof IndexResetRejected)this.resetPending=null;throw error;}
    this.alive.signal.throwIfAborted();this.resetPending=null;
    this.epoch++;this.query?.abort();this.searchRequest?.abort();this.mapWorker?.dispose();
    this.mapWorker=null;this.listedNodes.clear();this.controller.clear();
    this.update({mapTree:null,model:null,partial:null,terminalCode:null,summaries:[],summariesReady:false});
    await this.catalogue(this.alive.signal);await this.refresh();
  }
  async retryMutation(){
    if(this.state.busy||this.alive.signal.aborted)return;this.update({busy:true,error:null});
    try{
      if(this.resetPending){await this.sendReset();}
      else if(this.controlPending){await requestJobControl(this.controlPending,AbortSignal.any([this.alive.signal,AbortSignal.timeout(8000)]));this.alive.signal.throwIfAborted();this.controlPending=null;}
      else if(this.profileClient.status==='uncertain'){const profile=await this.profileClient.retry();this.alive.signal.throwIfAborted();this.update({profiles:[...this.state.profiles.filter(p=>p.profile.id!==profile.id),{profile,activeSnapshotId:null,latestJobId:null}]});await this.beginScan(profile.id);}
      else if(this.scanPending)await this.sendScan();
    }catch(error){if(this.controlPending?.action==='cancel-queued'&&error instanceof Error&&['JOB_CONFLICT','IDEMPOTENCY_CONFLICT'].includes(error.message))this.controlPending=null;this.error(error);}finally{this.update({busy:false,pending:!!this.resetPending||!!this.scanPending||!!this.controlPending||this.profileClient.unresolved});}
  }
  // Resolve current filesystem authority only on an explicit action, never from chart labels.
  async actionTarget(nodeId:string,caller:AbortSignal):Promise<FileStationTarget>{
    const pin=this.controller.current.pin,epoch=this.epoch;
    // Partial coverage does not revoke a known node; the server revalidates its lease, scope and current identity.
    const state=this.controller.current.reader?.view.state;
    if(!pin||(state!=='complete'&&state!=='partial')||!id(nodeId))throw Error('SNAPSHOT_MISMATCH');
    const signal=AbortSignal.any([caller,this.alive.signal,AbortSignal.timeout(8000)]);
    const target=await readView<FileStationTarget>({action:'filestation',profileId:pin.profileId,viewId:pin.viewId,snapshotId:pin.snapshotId,nodeId},signal);
    signal.throwIfAborted();if(epoch!==this.epoch||this.controller.current.pin!==pin)throw new DOMException('Superseded','AbortError');
    if(!target||!['file','directory'].includes(target.kind)||typeof target.fileStationPath!=='string'||!target.fileStationPath.startsWith('/')||target.fileStationPath.split('/').slice(1).some(p=>!p||p==='.'||p==='..')||/[\u0000-\u001f\u007f]/.test(target.fileStationPath))throw Error('INVALID_RESPONSE');
    return target;
  }
  async recordedPaths(nodeId:string,caller:AbortSignal){
    const pin=this.controller.current.pin,epoch=this.epoch;if(!pin)throw Error('SNAPSHOT_MISMATCH');
    const signal=AbortSignal.any([caller,this.alive.signal,AbortSignal.timeout(8000)]);
    const result=await readLeasedPaths({contractVersion:1,profileId:pin.profileId,viewId:pin.viewId,snapshotId:pin.snapshotId,nodeId},signal);
    signal.throwIfAborted();if(epoch!==this.epoch||this.controller.current.pin!==pin)throw new DOMException('Superseded','AbortError');return result;
  }
  // Queue removal carries a server-side state guard, including uncertain-request retries.
  async removeQueuedScan(jobId:string){
    if(this.state.busy||this.state.pending||!this.state.status?.queued.some(j=>j.jobId===jobId))return;
    this.controlPending={jobId,action:'cancel-queued',key:crypto.randomUUID()};
    this.update({busy:true,error:null});
    try{await requestJobControl(this.controlPending,AbortSignal.any([this.alive.signal,AbortSignal.timeout(8000)]));this.controlPending=null;}
    catch(error){if(error instanceof Error&&['JOB_CONFLICT','IDEMPOTENCY_CONFLICT'].includes(error.message))this.controlPending=null;this.error(error);}
    finally{try{const status=await readStatus(AbortSignal.any([this.alive.signal,AbortSignal.timeout(8000)]));this.update({status});}catch(error){this.observationError(error);}this.update({busy:false,pending:!!this.controlPending});}
  }
  async cancelScan(){
    if(this.state.busy||this.alive.signal.aborted)return;
    const job=this.state.status?.active??this.state.status?.queued[0];
    if(!job)return;if(this.controlPending&&this.controlPending.jobId!==job.jobId)return;
    this.controlPending??={jobId:job.jobId,action:'cancel',key:crypto.randomUUID()};
    this.update({busy:true,error:null});try{await requestJobControl(this.controlPending,AbortSignal.any([this.alive.signal,AbortSignal.timeout(8000)]));this.alive.signal.throwIfAborted();this.controlPending=null;}
    catch(error){if(error instanceof Error&&['JOB_CONFLICT','IDEMPOTENCY_CONFLICT'].includes(error.message))this.controlPending=null;this.error(error);}
    finally{this.update({busy:false,pending:!!this.controlPending||!!this.scanPending||this.profileClient.unresolved});}
  }
  // Persist only job/profile selectors, never scanned paths or credentials.
  private persistObservedJobs(){const jobs=[...this.observedJobs].map(([job,profile])=>job+'|'+profile).slice(-32);if(JSON.stringify(jobs)!==JSON.stringify(this.state.workspace.jobs??[]))this.setWorkspace({jobs});}
  // Poll global job ownership independently of navigation; only reader renewal belongs to the view.
  private async observe(){
    const navigationVersion=this.navigationVersion,sourceOpening=!!this.sourceOpening;
    try{
      if(document.visibilityState==='visible'&&!this.resetPending){
        const signal=AbortSignal.any([this.alive.signal,AbortSignal.timeout(10000)]);
        const status=await readStatus(signal);signal.throwIfAborted();this.update({status});
        const liveJobs=[...(status.active?[status.active]:[]),...status.queued];
        for(const job of liveJobs)this.observedJobs.set(job.jobId,job.profileId);
        const active=status.active??status.queued[0];
        if(active){
          const progress=await readProgressObservation(active.profileId,active.jobId,signal);
          signal.throwIfAborted();this.update({progress,preview:null});
          this.diagnostics.observe(active.jobId,active.state,{...(progress.current??{})});
        }else this.update({progress:null,preview:null});
        // Completed jobs are reconciled exactly once even while another share is displayed.
        for(const [jobId,profileId] of this.observedJobs){
          if(liveJobs.some(j=>j.jobId===jobId))continue;
          const progress=await readProgressObservation(profileId,null,signal);signal.throwIfAborted();
          const terminal=progress.history.find(j=>j.jobId===jobId&&['completed','failed','cancelled','interrupted'].includes(j.state));
          if(!terminal)continue;
          // Record final worker diagnostics even after the job leaves the active queue.
          this.diagnostics.observe(jobId,terminal.state,{...terminal});
          await this.catalogue(signal);signal.throwIfAborted();
          this.observedJobs.delete(jobId);
          this.update({completion:{jobId,name:this.state.profiles.find(p=>p.profile.id===profileId)?.profile.name??'Scan',state:terminal.state}});
          // Publish the new result only into the affected open profile; other views never jump.
          if(profileId===this.state.profileId&&!sourceOpening&&!this.sourceOpening&&navigationVersion===this.navigationVersion){
            const scope=this.state.workspace.source??this.state.profiles.find(p=>p.profile.id===profileId)?.profile.scope;
            const opening=this.openProfile(profileId),adoptionVersion=this.navigationVersion;
            await opening;
            // A click during the await owns the view; never abort its source request with a late restore.
            if(scope&&adoptionVersion===this.navigationVersion&&!this.sourceOpening&&profileId===this.state.profileId&&!this.operation?.signal.aborted)await this.openScope(scope);
          }
        }
        this.persistObservedJobs();
        const pin=this.controller.current.pin;
        if(pin&&Date.now()-this.lastRenewal>30000){
          try{const reader=await requestReader<RestoredReader>({contractVersion:1,operation:'read',profileId:pin.profileId,viewId:pin.viewId},signal);signal.throwIfAborted();if(this.controller.current.pin===pin)checkedReader(reader,pin);}
          catch(error){if(!(error instanceof Error)||error.message!=='VIEW_EXPIRED')throw error;
            const reader=await requestReader<RestoredReader>({contractVersion:1,operation:'open',profileId:pin.profileId,snapshotId:pin.snapshotId,viewId:crypto.randomUUID()},signal);signal.throwIfAborted();
            if(this.controller.current.pin===pin)this.controller.rebind(reader);
          }
          this.lastRenewal=Date.now();
        }
        if(this.state.statusError)this.diagnostics.add('STATUS_RECOVERED');this.update({statusError:null});
      }
    }catch(error){
      // Superseded background restoration is normal navigation cancellation, not a failed status read.
      const superseded=!!error&&typeof error==='object'&&'name' in error&&error.name==='AbortError'&&navigationVersion!==this.navigationVersion;
      if(!this.alive.signal.aborted&&!superseded)this.observationError(error);
    }
    finally{if(!this.alive.signal.aborted)this.timer=setTimeout(()=>void this.observe(),2000);}
  }

  dispose(){
    // Retire sensitive diagnostic and result memory together with the owner session.
    this.mapWorker?.dispose();clearTimeout(this.workspaceTimer);this.diagnostics.clear();this.listedNodes.clear();this.sourceViews.clear();this.preparedMap=null;
    this.previewClient.dispose();this.alive.abort();this.operation?.abort();this.query?.abort();this.searchRequest?.abort();clearTimeout(this.timer);clearTimeout(this.pendingTimer);this.profileClient.dispose();this.preferences.dispose();this.controller.cancel();this.controller.releaseRetained();
    const pin=this.controller.current.pin;if(pin)void requestReader({contractVersion:1,operation:'release',profileId:pin.profileId,viewId:pin.viewId},AbortSignal.timeout(8000)).catch(()=>{});
  }
}

// Exact subtree totals remain strings; displayed children are a bounded window, never a new total.
function present(node:MapNode):NativeStorageNode {
  const kind=node.kind==='volume'?'volume':node.kind==='share'?'share':node.kind==='file'?'file':'folder';
  return {id:node.id,aggregated:node.kind==='group'||node.opaque,fileCount:node.summary.fileCount.toString(),colorParentId:node.parentId??undefined,name:node.kind==='group'?'Weitere Inhalte':node.kind==='volume'?node.name.replace(/^volume(\d+)$/i,'Volume $1'):node.kind==='other'?'Weitere Bereiche':node.name,kind,sourceKind:node.kind,volumeId:node.volumeId,shareId:node.provenance?.sourceId??node.shareId,bytesExact:node.weight.toString(),unavailable:node.unknown||!['nas','volume','share','other','directory','file','group'].includes(node.kind)};
}
export function useNativeDiskMap(options:NativeDiskMapOptions){
  const [state,setState]=useState(initial),owner=useRef<NativeOwner|null>(null),ownerIdentity=useRef(''),latest=useRef(options);latest.current=options;
  useEffect(()=>{const current=new NativeOwner(setState,()=>latest.current.onSessionDenied());owner.current=current;ownerIdentity.current=options.sessionIdentity;setState(initial());current.setBudget(latest.current.budget);void current.start();return()=>{current.dispose();if(owner.current===current)owner.current=null;};},[options.sessionIdentity]);
  useEffect(()=>owner.current?.setBudget(options.budget),[options.budget?.depth,options.budget?.elementBudget]);
  // A pending navigation must never present the previous root under the new reader selector.
  const view=state.analysis.state.view;
  const model=state.model&&view&&state.model.snapshotId===view.snapshotId&&state.model.rootId===view.rootId?state.model:null;
  // Immutable generation geometry must survive progress, hover and diagnostic updates.
  const {current,path}=useMemo(()=>{
  const nodes:Record<string,NativeStorageNode>={};
  if(model){for(const node of Object.values(model.nodes))nodes[node.id]=present(node);for(const node of Object.values(model.nodes))nodes[node.id].children=node.children.map(id=>nodes[id]).filter(Boolean);}
  const rawCurrent=model?nodes[model.rootId]??null:null;
  // A captured share root is structural; hide its single dot wrapper in the visual tree.
  const structural=rawCurrent?.children?.length===1&&(rawCurrent.children[0].name==='.'||rawCurrent.sourceKind==='share'&&rawCurrent.children[0].name===rawCurrent.name)?rawCurrent.children[0]:null;
  const current=rawCurrent?{...rawCurrent,...(structural?.children?.length?{children:structural.children}:{}),name:rawCurrent.name==='.'?(state.analysis.navigation?.breadcrumb.filter(n=>n.name!=='.').at(-1)?.name??rawCurrent.name):rawCurrent.name}:null;
  if(current?.sourceKind==='nas')current.name='NAS';
  const path=(state.analysis.navigation?.breadcrumb??[]).filter((n,i,all)=>n.name!=='.'&&!(n.kind==='directory'&&all[i-1]?.kind==='share'&&n.name===all[i-1].name)).map(n=>nodes[n.id]??{id:n.id,name:n.name,kind:n.kind==='volume'?'volume':n.kind==='share'?'share':'folder',sourceKind:n.kind} as NativeStorageNode);
  for(const node of path)if(node.sourceKind==='nas')node.name='NAS';
   return {current,path};
  },[model,state.analysis.navigation]);
  const volumes:NativeStorageNode[]=(state.sources?.volumes??[]).map(v=>({id:v.id,name:v.name,kind:'volume',sourceKind:'volume',unavailable:v.state!=='reachable',scope:{kind:'volumes',targetIds:[v.id]},children:(state.sources?.shares??[]).filter(s=>s.volumeId===v.id).map(s=>({id:s.id,name:s.name,kind:'share',sourceKind:'share',unavailable:!s.readable,scope:{kind:'shares',targetIds:[s.id]}}))}));
  // Old event handlers cannot borrow a subsequent session's controller.
  const currentOwner=()=>ownerIdentity.current===options.sessionIdentity?owner.current:null;
  return {...state,debugTiming:(...args:Parameters<DiagnosticLog['timing']>)=>currentOwner()?.diagnostics.timing(...args),refreshInventory:()=>currentOwner()?.refreshInventory(),loadGeometry:owner.current?.loadGeometry,setWorkspace:(patch:Partial<PreferenceWorkspace>)=>currentOwner()?.setWorkspace(patch),readFileList:(options:ListOptions,cursor:string,signal:AbortSignal)=>currentOwner()?.fileList(options,cursor,signal)??Promise.reject(Error('SESSION_REQUIRED')),openScope:(scope:Scope)=>currentOwner()?.openScope(scope)??Promise.resolve(false),setFreshness:(p:FreshnessPreference)=>currentOwner()?.setFreshness(p),readChildren:(nodeId:string,cursor:string,signal:AbortSignal)=>currentOwner()?.children(nodeId,cursor,signal)??Promise.reject(Error('SESSION_REQUIRED')),preview:ownerIdentity.current===options.sessionIdentity?state.preview:null,current,path,openPartial:()=>currentOwner()?.openPartial(),setDebug:(enabled:boolean)=>currentOwner()?.diagnostics.set(enabled),clearDebug:()=>currentOwner()?.diagnostics.clear(),setDebugLimit:(n:number)=>currentOwner()?.diagnostics.setLimit(n),setDebugFilter:(f:DebugFilter)=>currentOwner()?.diagnostics.setFilter(f),exportDebug:()=>currentOwner()?.diagnostics.exportText()??'',canBack:state.analysis.state.back.length>0,entries:current?.children??[],groupEntries:state.groupPage?.items.map(present)??[],volumes:[{id:'scope-nas',name:'NAS',kind:'volume',sourceKind:'nas',scope:{kind:'nas',targetIds:[]},children:volumes,unavailable:!volumes.length} as NativeStorageNode],
    setSidebars:(value:SidebarPreference)=>currentOwner()?.setSidebars(value),openGroup:(id:string)=>currentOwner()?.openGroup(id),nextGroup:()=>currentOwner()?.nextGroup(),setMap:(map:'radial'|'treemap')=>currentOwner()?.setMap(map),openProfile:(id:string)=>currentOwner()?.openProfile(id),navigate:(id:string,kind?:StorageNode['kind'])=>currentOwner()?.navigate(id,kind),back:()=>currentOwner()?.back(),
    search:(text:string,cursor='')=>currentOwner()?.search(text,cursor),scanScope:(scope:Scope,name:string,force=false)=>currentOwner()?.scanScope(scope,name,force),
    actionTarget:(id:string,signal:AbortSignal)=>currentOwner()?.actionTarget(id,signal)??Promise.reject(Error('SESSION_REQUIRED')),
    recordedPaths:(id:string,signal:AbortSignal)=>currentOwner()?.recordedPaths(id,signal)??Promise.reject(Error('SESSION_REQUIRED')),
    setAppearance:(kind:'language'|'theme',value:'de'|'en'|'dark'|'light')=>currentOwner()?.setAppearance(kind,value),selectFile:(id:string)=>currentOwner()?.selectFile(id),resetIndex:(scope:Scope|null)=>currentOwner()?.resetIndex(scope),removeQueuedScan:(id:string)=>currentOwner()?.removeQueuedScan(id),cancelScan:()=>currentOwner()?.cancelScan(),rescan:()=>currentOwner()?.rescan(),refresh:()=>currentOwner()?.refresh(),retryMutation:()=>currentOwner()?.retryMutation()};
}
