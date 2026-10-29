import type { PreferenceView } from './contracts';
import { analysisReducer, initialAnalysisState, type AnalysisState } from './analysis-state';
import type { NavigationState } from './NavigationBar';
import type { ViewPin } from './snapshot-reader';
import { requestReader } from './snapshot-reader';
import { requestNavigation, type RestoredReader } from './view-state-api';

// One publication binds navigation, selection and lease; no independent selection state is retained.
export interface ConfirmedAnalysis { state: AnalysisState; pin: ViewPin | null; reader: RestoredReader | null; navigation: NavigationState | null }
// A preparation is disposable until navigation confirms the exact requested root.
export type SameLeasePreparation=(view:PreferenceView,pin:ViewPin,signal:AbortSignal)=>{discard:()=>void;confirm:()=>void};
export const emptyAnalysis = (): ConfirmedAnalysis => ({ state: initialAnalysisState(), pin: null, reader: null, navigation: null });
export function defaultView(profileId:string,snapshotId:string):PreferenceView {
 return {profileId,snapshotId,rootId:'nas',selectedNodeId:null,map:'treemap',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'name',direction:'asc'};
}
// Qualifying a transition never persists preferences or changes a scan's active generation.
export class AnalysisController {
 current=emptyAnalysis();
 private sequence=0;
 private partialJobs=new Map<string,string>();
 rememberPartial(snapshotId:string,jobId:string){this.partialJobs.set(snapshotId,jobId);if(this.partialJobs.size>64)this.partialJobs.delete(this.partialJobs.keys().next().value!);}
 // Retain a small number of already qualified readers for native return navigation.
 private qualifiedReaders=new WeakSet<RestoredReader>();
 private retained=new Map<string,{profileId:string;reader:RestoredReader}>();
 private request:AbortController|null=null;
 constructor(private changed:(next:ConfirmedAnalysis)=>void,private validate:(reader:RestoredReader,pin:ViewPin|null)=>unknown,private retainedLimit=0){}
 private release(profileId:string,reader:RestoredReader){if(reader.lease)void requestReader({contractVersion:1,operation:'release',profileId,viewId:reader.lease.id},AbortSignal.timeout(8000)).catch(()=>{});}
 // Expiry and eviction release capabilities; retained metadata never authorizes a navigation by itself.
 private retire(profileId:string,reader:RestoredReader){
  if(!reader.lease||!this.retainedLimit||Date.parse(reader.lease.expiresAt)<=Date.now()+5000){this.release(profileId,reader);return;}
  const key=profileId+':'+reader.lease.snapshotId,old=this.retained.get(key);if(old)this.release(old.profileId,old.reader);
  this.retained.delete(key);this.retained.set(key,{profileId,reader});
  for(const [id,value]of this.retained){if(this.retained.size>this.retainedLimit||Date.parse(value.reader.lease!.expiresAt)<=Date.now()+5000){this.retained.delete(id);this.release(value.profileId,value.reader);}}
 }
 releaseRetained(){for(const value of this.retained.values())this.release(value.profileId,value.reader);this.retained.clear();}
 private publish(next:ConfirmedAnalysis){this.current=next;this.changed(next);}
 cancel(){++this.sequence;this.request?.abort();}
 // Reacquire an expired lease for the same immutable snapshot without changing presentation state.
 rebind(reader:RestoredReader){
  const pin=this.current.pin;if(!pin)throw Error('VIEW_EXPIRED');this.validate(reader,null);
  if(reader.lease?.snapshotId!==pin.snapshotId||reader.view.snapshotId!==pin.snapshotId)throw Error('SNAPSHOT_MISMATCH');
  this.cancel();this.publish({...this.current,reader,pin:{...pin,viewId:reader.lease.id}});
 }
 clear(){this.releaseRetained();this.cancel();this.publish({...emptyAnalysis(),state:analysisReducer(this.current.state,{type:'reset'})});}
 // Bootstrap is already server-qualified and is deliberately not a user navigation or preference write.
 restored(view:PreferenceView,reader:RestoredReader,navigation:NavigationState){
  this.cancel();this.validate(reader,null);
  if(!reader.lease||reader.lease.snapshotId!==view.snapshotId||navigation.snapshotId!==view.snapshotId||navigation.rootId!==view.rootId)throw new Error('SNAPSHOT_MISMATCH');
  this.qualifiedReaders.add(reader);
  this.publish({state:analysisReducer(this.current.state,{type:'replace',view}),reader,navigation,pin:{profileId:view.profileId,viewId:reader.lease.id,snapshotId:view.snapshotId,nodeId:view.selectedNodeId}});
 }
 async accept(profileId:string,reader:RestoredReader,signal:AbortSignal,desired?:PreferenceView,kind:'navigate'|'replace'='replace',prepare?:SameLeasePreparation){
  if(signal.aborted)return false;
  const previousPin=this.current.pin,previousReader=this.current.reader;
  this.validate(reader,null);
  if(!reader.lease||!reader.view.snapshotId){this.cancel();this.publish({...emptyAnalysis(),reader,state:analysisReducer(this.current.state,{type:'navigate',view:null})});if(previousPin)void requestReader({contractVersion:1,operation:'release',profileId:previousPin.profileId,viewId:previousPin.viewId},AbortSignal.timeout(8000)).catch(()=>{});return true;}
  const same=this.current.state.view?.profileId===profileId&&this.current.state.view.snapshotId===reader.view.snapshotId;
  const previous=this.current.state.view;
  const base=same?previous!:previous?.profileId===profileId?{...previous,snapshotId:reader.view.snapshotId,rootId:'nas',selectedNodeId:reader.selection.nodeId}:defaultView(profileId,reader.view.snapshotId);
  const view=structuredClone(desired??base);
  view.profileId=profileId;view.snapshotId=reader.view.snapshotId;
  if(!same&&!desired)view.selectedNodeId=reader.selection.nodeId;
  const sequence=++this.sequence;
  // Only a previously qualified lease may authorize disposable overlapping stored-index reads.
  const preparation=(same&&previousPin?.viewId===reader.lease.id||this.qualifiedReaders.has(reader))?prepare?.(structuredClone(view),{profileId,viewId:reader.lease.id,snapshotId:view.snapshotId,nodeId:view.selectedNodeId},signal):undefined;
  let navigation:NavigationState;
  try{navigation=await requestNavigation(profileId,reader.lease.id,view.snapshotId,view.rootId,view.selectedNodeId,AbortSignal.any([signal,AbortSignal.timeout(8000)]));}catch(error){preparation?.discard();if(signal.aborted||sequence!==this.sequence)return false;throw error;}
  if(signal.aborted||sequence!==this.sequence){preparation?.discard();return false;}
  if(navigation.rootId!==view.rootId)preparation?.discard();else preparation?.confirm();
  this.qualifiedReaders.add(reader);
  view.rootId=navigation.rootId;view.selectedNodeId=navigation.selectedNodeId;
  this.publish({state:analysisReducer(this.current.state,{type:kind,view}),reader,navigation,pin:{profileId,viewId:reader.lease.id,snapshotId:view.snapshotId,nodeId:view.selectedNodeId}});
  if(previousPin&&previousReader&&previousPin.viewId!==reader.lease.id){if(previousPin.profileId===profileId&&previousPin.snapshotId===reader.lease.snapshotId)this.release(previousPin.profileId,previousReader);else this.retire(previousPin.profileId,previousReader);}
  return true;
 }
 async move(view:PreferenceView,history:'navigate'|'replace'|'back'|'forward'='navigate',prepare?:SameLeasePreparation){
  this.cancel();const controller=this.request=new AbortController();const signal=controller.signal;
  const previous=this.current;let ownedViewId:string|null=null;let transferred=false;
  try{
   let reader=previous.reader,reused=false;
   const acquire=async()=>{
    const viewId=crypto.randomUUID();ownedViewId=viewId;
    const result=await requestReader<RestoredReader>({contractVersion:1,operation:'open',profileId:view.profileId,viewId,...(this.partialJobs.has(view.snapshotId)?{jobId:this.partialJobs.get(view.snapshotId)}:{snapshotId:view.snapshotId})},signal);
    if(result.lease?.id!==viewId||result.lease.snapshotId!==view.snapshotId)throw new Error('SNAPSHOT_MISMATCH');return result;
   };
   if(!previous.pin||previous.pin.profileId!==view.profileId||previous.pin.snapshotId!==view.snapshotId){
    const key=view.profileId+':'+view.snapshotId,cached=this.retained.get(key);this.retained.delete(key);
    if(cached&&Date.parse(cached.reader.lease!.expiresAt)>Date.now()+5000){reader=cached.reader;ownedViewId=reader.lease!.id;reused=true;}
    else{if(cached)this.release(cached.profileId,cached.reader);reader=await acquire();}
   }
   if(!reader)throw new Error('SNAPSHOT_MISMATCH');
   let accepted=false;
   try{accepted=await this.accept(view.profileId,reader,signal,view,history==='navigate'?'navigate':'replace',prepare);}
   catch(error){
    // A service restart may expire an otherwise fresh retained lease; reacquire once, never retry denial.
    if(!reused||signal.aborted||!(error instanceof Error)||error.message!=='VIEW_EXPIRED')throw error;
    this.release(view.profileId,reader);reader=await acquire();accepted=await this.accept(view.profileId,reader,signal,view,history==='navigate'?'navigate':'replace',prepare);
   }
   if(!accepted)return;
   if(signal.aborted)return;
   transferred=true;
   if(history==='back'||history==='forward'){
    const state=analysisReducer(previous.state,{type:history});
    this.publish({...this.current,state:{...state,view:this.current.state.view,epoch:this.current.state.epoch}});
   }
  }catch(error){
   if(!signal.aborted)throw error;
  }finally{
   // Release the acquisition intent even when cancellation hides the server response.
   // The server tombstone also prevents a delayed open from reviving this abandoned handle.
   if(ownedViewId&&!transferred)void requestReader({contractVersion:1,operation:'release',profileId:view.profileId,viewId:ownedViewId},AbortSignal.timeout(8000)).catch(()=>{});
  }
 }
}
