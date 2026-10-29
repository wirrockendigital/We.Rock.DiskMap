import {useCallback,useEffect,useRef,useState} from 'react';
import type {Profile,Scope} from '../../contracts';
import {ProfileEditor} from '../../ProfileEditor';
import {ProfileActionClient,encodeProfileSave,profileConfiguration,type ProfileConfiguration} from '../../profile-actions';
import {ScopeSelection,scopeSelectionText} from '../../ScopeSelection';
import {loadScopeInventory,checkSelectedScope} from '../../scope-api';
import {checkedScopeCheck,checkedScopeInventory,type ScopeCheck,type ScopeInventory} from '../../scope-selection';
import {SchedulerTemplate} from '../../SchedulerTemplate';
import {requestScan,scanIntent,type PendingScan} from '../../actions';
import {SessionDenied} from '../../session';
import {OnboardingClient} from './onboarding-client';
import type {OnboardingResponse,OnboardingState,OnboardingCreateRequest} from './onboarding-types';
import {draftConfiguration,newWizardDraft,readableScope,storedConfiguration,type WizardDraft} from './onboarding-model';
import {onboardingMessages} from './onboarding-messages';

export interface OnboardingWizardProps {
 language:'de'|'en';sessionIdentity:string;dsmBuild:string;
 onLanguageChange:(choice:'auto'|'de'|'en')=>Promise<void>;
 loadProfile:(id:string,signal:AbortSignal)=>Promise<Profile|null>;
 onSessionDenied:()=>void;onClose:()=>void;onProfileSaved:(profile:Profile)=>void;onStarted:(profileId:string,jobId:string)=>Promise<void>;
}
// A fresh session remount discards all transient UI data and reads only the newly authenticated principal.
export function OnboardingWizard(p:OnboardingWizardProps){return <WizardSession key={p.sessionIdentity} {...p}/>;}
export function WizardSession(p:OnboardingWizardProps){
 const client=useRef(new OnboardingClient()),creator=useRef(new ProfileActionClient()),alive=useRef(true),lifetime=useRef(new AbortController()),serial=useRef(0),lock=useRef(false);
 const [server,setServer]=useState<OnboardingState|null>(null),[recovery,setRecovery]=useState<OnboardingResponse|null>(null),[draft,setDraft]=useState<WizardDraft>(newWizardDraft),[saved,setSaved]=useState(''),[editorEpoch,setEditorEpoch]=useState(0);
 const draftRef=useRef(draft);draftRef.current=draft;
 const [busy,setBusy]=useState(true),[notice,setNotice]=useState(''),[profile,setProfile]=useState<Profile|null>(null),[inventory,setInventory]=useState<ScopeInventory|null>(null),[check,setCheck]=useState<ScopeCheck|null>(null),[closeQuestion,setCloseQuestion]=useState(false),[ruleReady,setRuleReady]=useState(true),[languageFailed,setLanguageFailed]=useState(false);
 const pendingScan=useRef<PendingScan|null>(null),autoFailed=useRef(''),startedJob=useRef<{profileId:string;jobId:string}|null>(null);
 const language=draft.language==='auto'?p.language:draft.language,m=onboardingMessages[language];
 const configuration=draftConfiguration(draft),scope=configuration.scope;
 const unresolved=client.current.unresolved||server?.pendingProfileCreate!==null&&server?.pendingProfileCreate!==undefined||creator.current.unresolved||pendingScan.current!==null;
 const dirty=JSON.stringify(draft)!==saved;
 const blocked=busy||unresolved||languageFailed||startedJob.current!==null;
 const freshSignal=()=>AbortSignal.any([lifetime.current.signal,AbortSignal.timeout(10000)]);
 const fail=(e:unknown)=>{if(!alive.current)return;if(e instanceof SessionDenied){setServer(null);setRecovery(null);setProfile(null);setDraft(newWizardDraft());client.current.dispose();creator.current.dispose();p.onSessionDenied();return;}setNotice(client.current.status==='conflict'?'conflict':client.current.unresolved?'uncertain':'error');};
 // Adopting a server draft never submits its pending create or starts a scan.
 const adopt=async(value:OnboardingResponse)=>{
  setServer(value);setRecovery(value);const d=value.draft??newWizardDraft();setDraft(d);setSaved(JSON.stringify(d));setCheck(null);setProfile(null);setRuleReady(true);setEditorEpoch(x=>x+1);
  if(d.profileId){const found=await p.loadProfile(d.profileId,freshSignal());if(!alive.current)return;setProfile(found);}
 };
 const reload=async()=>{if(lock.current||client.current.unresolved)return;lock.current=true;setBusy(true);setNotice('');const seq=++serial.current;try{const value=await client.current.read();if(!alive.current||seq!==serial.current)return;await adopt(value);}catch(e){if(seq===serial.current)fail(e);}finally{lock.current=false;if(alive.current&&seq===serial.current)setBusy(false);}};
 useEffect(()=>{alive.current=true;void reload();return()=>{alive.current=false;++serial.current;lifetime.current.abort();client.current.dispose();creator.current.dispose();};},[]);
 const install=(value:OnboardingState)=>{setServer(value);setRecovery(null);const d=value.draft??newWizardDraft();setDraft(d);setSaved(JSON.stringify(d));};
 const save=async(next:WizardDraft,preserveLocal=false)=>{
  if(!server)throw Error('UNAVAILABLE');
  if(next.configuration?.exclusions&&next.configuration.exclusions.length>128||new TextEncoder().encode(JSON.stringify(next)).length>24576)throw Error('DRAFT_LIMIT');
  const value=await client.current.change(server.revision,{operation:'replace',draft:next});if(!alive.current)throw Error('DISPOSED');
  if(preserveLocal){setServer(value);setRecovery(null);setSaved(JSON.stringify(value.draft??newWizardDraft()));if(JSON.stringify(draftRef.current)===JSON.stringify(next))setDraft(value.draft??newWizardDraft());}else install(value);return value;
 };
 const run=async(task:()=>Promise<void>)=>{if(lock.current)return;lock.current=true;setBusy(true);setNotice('');try{await task();}catch(e){if(e instanceof Error&&e.message==='DRAFT_LIMIT')setNotice('bounds');else fail(e);}finally{lock.current=false;if(alive.current)setBusy(false);}};
 // Debounced writes preserve newer local edits and stop at the first uncertain CAS outcome.
 useEffect(()=>{if(!server||!dirty||blocked||!ruleReady||autoFailed.current===JSON.stringify(draft))return;const snapshot=structuredClone(draft);const timer=setTimeout(()=>{void run(async()=>{try{await save(snapshot,true);}catch(e){autoFailed.current=JSON.stringify(snapshot);throw e;}});},800);return()=>clearTimeout(timer);},[draft,saved,server?.revision,blocked,ruleReady]);
 const move=(step:number)=>{if(blocked||!ruleReady)return;void run(async()=>{await save({...draft,step});setCheck(null);});};
 const changeScope=(value:Scope|null)=>{if(blocked)return;setDraft(d=>({...d,configuration:{...d.configuration,scope:value??{kind:'shares',targetIds:[]}}}));setCheck(null);};
 const loadInventory=async(signal:AbortSignal)=>{const value=checkedScopeInventory(await loadScopeInventory(signal));if(!signal.aborted&&alive.current)setInventory(value);return value;};
 const validateAccess=async(target:Scope)=>{const signal=freshSignal();const cat=checkedScopeInventory(await loadScopeInventory(signal));const report=checkedScopeCheck(await checkSelectedScope(target,signal),target,cat);if(!alive.current||signal.aborted)throw Error('ABORTED');setInventory(cat);setCheck(report);return report;};
 const probe=()=>{if(blocked)return;void run(async()=>{await validateAccess(scope);});};
 // Effective copied rules are materialized only after the existing editor's syntax/preview check succeeds.
 const editorDraft=useCallback((value:ProfileConfiguration,validated:boolean)=>{setRuleReady(validated);setDraft(old=>({...old,configuration:storedConfiguration(value)}));},[]);
 const created=async(value:Profile)=>{if(!alive.current)return;setProfile(value);p.onProfileSaved(value);};
 const saveProfile=async(value:ProfileConfiguration):Promise<Profile>=>{
  if(lock.current||!server||server.pendingProfileCreate)throw Error('PENDING');lock.current=true;setBusy(true);setNotice('');
  try{
   const report=await validateAccess(value.scope);if(!readableScope(report,value.scope))throw Error('ACCESS_CHANGED');
   const savedState=await save({...draft,step:4,configuration:storedConfiguration(value)});
   const request=JSON.parse(encodeProfileSave(null,value)) as OnboardingCreateRequest;
   const prepared=await client.current.change(savedState.revision,{operation:'prepareProfileCreate',create:{key:crypto.randomUUID(),request}});if(!alive.current)throw Error('DISPOSED');install(prepared);
   const pending=prepared.pendingProfileCreate;if(!pending)throw Error('INVALID_RECEIPT');
   const result=await creator.current.submitPreparedCreate(pending.canonicalPayload,pending.key);if(!alive.current)throw Error('DISPOSED');
   const ack=await client.current.change(prepared.revision,{operation:'acknowledgeProfileCreate'});if(!alive.current)throw Error('DISPOSED');install(ack);await created(result);return result;
  }catch(e){fail(e);throw e;}finally{lock.current=false;if(alive.current)setBusy(false);}
 };
 const retryCreate=()=>void run(async()=>{
  const pending=server?.pendingProfileCreate;if(!pending)return;
  // Only this explicit button repeats the stored immutable create intent after an uncertain outcome.
  const result=await(creator.current.unresolved?creator.current.retry():creator.current.submitPreparedCreate(pending.canonicalPayload,pending.key));if(!alive.current)return;
  const ack=await client.current.change(server!.revision,{operation:'acknowledgeProfileCreate'});if(!alive.current)return;install(ack);await created(result);
 });
 const acknowledge=()=>void run(async()=>{if(!server)return;const out=await client.current.change(server.revision,{operation:'acknowledgeProfileCreate'});if(!alive.current)return;creator.current.dispose();creator.current=new ProfileActionClient();install(out);await adopt(await client.current.read());});
 // Only a persisted server rejection permits explicit recovery; unknown outcomes remain immutable.
 const correctRejected=()=>void run(async()=>{if(!server||recovery?.pendingProfileCreate?.status!=='rejected')return;const out=await client.current.change(server.revision,{operation:'acknowledgeProfileRejection'});if(!alive.current)return;creator.current.dispose();creator.current=new ProfileActionClient();install(out);setCheck(null);setEditorEpoch(x=>x+1);setDraft(d=>({...d,step:2}));});
 const retryDraft=()=>void run(async()=>{const out=await client.current.retry();if(!alive.current)return;install(out);await adopt(await client.current.read());});
 const start=()=>{if(blocked&&!pendingScan.current&&!startedJob.current)return;void run(async()=>{try{
  // Once accepted, retry only the UI handoff; the original scan must never be submitted again.
  if(startedJob.current){await p.onStarted(startedJob.current.profileId,startedJob.current.jobId);return;}
  if(!draft.profileId)throw Error('MISSING_PROFILE');const current=await p.loadProfile(draft.profileId,freshSignal());if(!alive.current)return;
  if(!current?.active){setProfile(current);throw Error('MISSING_PROFILE');}
  if(JSON.stringify(profileConfiguration(current))!==JSON.stringify(configuration)){setProfile(current);setNotice('unavailable');return;}
  const report=await validateAccess(current.scope);if(!readableScope(report,current.scope))throw Error('ACCESS_CHANGED');
  pendingScan.current=scanIntent(pendingScan.current,'full');
  const response=await requestScan({contractVersion:1,profileId:current.id,mode:'full'},pendingScan.current.key,freshSignal()) as {contractVersion:number;data:{job:{id:string;profileId:string;scanMode:string}}};if(!alive.current)return;
  if(response.contractVersion!==1||response.data?.job?.profileId!==current.id||response.data.job.scanMode!=='full'||!/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(response.data.job.id))throw Error('INVALID_SCAN');
  pendingScan.current=null;startedJob.current={profileId:current.id,jobId:response.data.job.id};await p.onStarted(current.id,response.data.job.id);
 }catch(e){if(e instanceof Error&&['JOB_CONFLICT','QUEUE_FULL','IDEMPOTENCY_CONFLICT','INVALID_REQUEST'].includes(e.message))pendingScan.current=null;throw e;}
 });};
 const applyLanguage=(choice:WizardDraft['language'])=>void run(async()=>{try{await p.onLanguageChange(choice);await save({...draft,language:choice});setLanguageFailed(false);}catch(e){setLanguageFailed(true);throw e;}});
 const close=()=>{if(blocked)return;if(dirty||!ruleReady)setCloseQuestion(true);else p.onClose();};
 if(!server)return <section aria-label={m.title}><h2>{m.title}</h2><p role="status">{busy?m.loading:m.error}</p>{!busy&&<button onClick={()=>void reload()}>{m.reload}</button>}</section>;
 const pending=server.pendingProfileCreate,observed=recovery?.pendingProfileCreate;
 const profileAvailable=!!profile?.active&&draft.profileId===profile.id;
 const canContinue=draft.step===1||draft.step===2&&(scope.kind==='nas'||scope.targetIds.length>0)||draft.step===3&&!!check&&readableScope(check,scope)||draft.step===4&&profileAvailable||draft.step===5&&draft.updateChoice!=='undecided';
 return <section aria-label={m.title} aria-busy={busy} style={{minWidth:0,overflowWrap:'anywhere'}}><h2>{m.title}</h2><p>{m.intro}</p><ol aria-label={m.title}>{m.steps.map((title,i)=><li key={title} aria-current={draft.step===i+1?'step':undefined}>{title}</li>)}</ol>
  <p role="status">{dirty?m.unsaved:m.saved}</p>
  {notice&&<p role="alert">{m[notice as keyof typeof m] as string??m.error}</p>}
  {languageFailed&&<button disabled={busy||client.current.unresolved} onClick={()=>applyLanguage(draft.language)}>{m.languageFailed} — {m.retry}</button>}
  {client.current.unresolved&&<button disabled={busy} onClick={retryDraft}>{m.retry}</button>}
  {client.current.status==='conflict'&&<button disabled={busy} onClick={()=>void reload()}>{m.reload}</button>}
  {pending?<section><p role="alert">{m.pending}</p><p>{observed?.status==='committed'?m.saved:observed?.status==='profile-unavailable'?m.unavailable:m.pending}</p><button disabled={busy||client.current.unresolved} onClick={()=>void reload()}>{m.recover}</button>{observed?.status==='rejected'?<><p role="alert">{m.rejected}</p><button disabled={busy||client.current.unresolved} onClick={correctRejected}>{m.correct}</button></>:observed?.receipt?<><p>{m.profile}: {observed.receipt.profileId}</p>{observed.status==='profile-unavailable'&&<p>{m.unavailable}</p>}<button disabled={busy} onClick={acknowledge}>{m.ack}</button></>:<button disabled={busy||client.current.unresolved} onClick={retryCreate}>{m.retry}</button>}</section>:<>
   {draft.step===1&&<label>{m.steps[0]}<select disabled={blocked} value={draft.language} onChange={e=>{const choice=e.target.value as WizardDraft['language'];if(blocked)return;setDraft(d=>({...d,language:choice}));applyLanguage(choice);}}><option value="auto">{m.auto}</option><option value="de">Deutsch</option><option value="en">English</option></select></label>}
   {draft.step===2&&<ScopeSelection language={language} value={scope} disabled={blocked||!!draft.profileId} editorIdentity="onboarding" loadInventory={loadInventory} checkScope={checkSelectedScope} onSessionDenied={p.onSessionDenied} onChange={changeScope}/>}
   {draft.step===3&&<section><button disabled={blocked} onClick={probe}>{m.check}</button><p>{check&&readableScope(check,scope)?m.readable:m.unchecked}</p>{check&&<ul>{check.roots.map(r=><li key={r.targetId}>{inventory?.shares.find(s=>s.id===r.targetId)?.name??inventory?.volumes.find(v=>v.id===r.targetId)?.name??r.targetId}: {scopeSelectionText[language].states[r.state]}</li>)}</ul>}<p>{p.dsmBuild==='90080'?m.rights:m.unqualified}</p><p>{m.limit}</p></section>}
   {draft.step===4&&(draft.profileId?<section><p>{m.profile}: {draft.profileId}</p><p>{profileAvailable?profile?.name:m.unavailable}</p></section>:<ProfileEditor key={`wizard-${editorEpoch}`} disabled={blocked} language={language} profile={null} initialConfiguration={configuration} editorIdentity="onboarding-profile" sessionIdentity={p.sessionIdentity} loadInventory={loadInventory} checkScope={checkSelectedScope} loadProfile={p.loadProfile} onDraftChange={editorDraft} onSaveConfiguration={saveProfile} onSaved={value=>void created(value)} onCancel={close} onSessionDenied={p.onSessionDenied}/>)}
   {draft.step===5&&<section><label><input disabled={blocked} type="radio" checked={draft.updateChoice==='manual'} onChange={()=>setDraft(d=>({...d,updateChoice:'manual'}))}/>{m.manual}</label><label><input disabled={blocked||!profileAvailable} type="radio" checked={draft.updateChoice==='dsm-help'} onChange={()=>setDraft(d=>({...d,updateChoice:'dsm-help'}))}/>{m.help}</label>{draft.updateChoice==='dsm-help'&&profileAvailable&&<SchedulerTemplate profileId={profile!.id} language={language} onSessionDenied={p.onSessionDenied}/>}<button disabled={blocked} onClick={()=>void run(async()=>{await save({...draft,step:6,updateChoice:'manual'});})}>{m.skip}</button></section>}
   {draft.step===6&&<section><p>{m.profile}: {draft.profileId??m.missing}</p><p>{m.startHint}</p>{!profileAvailable&&<p role="alert">{m.unavailable}</p>}<button disabled={busy||!profileAvailable&&!startedJob.current||client.current.unresolved} onClick={start}>{startedJob.current?m.openStatus:pendingScan.current?m.retry:m.start}</button></section>}
   <nav><button disabled={blocked||draft.step===1||!ruleReady} onClick={()=>move(draft.step-1)}>{m.back}</button>{draft.step<6&&<button disabled={blocked||!canContinue||!ruleReady} onClick={()=>move(draft.step+1)}>{m.next}</button>}<button disabled={blocked||!dirty||!ruleReady} onClick={()=>void run(async()=>{await save(draft);setNotice('saved');})}>{m.save}</button><button disabled={blocked} onClick={close}>{m.close}</button></nav>
   {draft.profileId&&!profileAvailable&&<button disabled={blocked} onClick={()=>void run(async()=>{const value=await client.current.change(server.revision,{operation:'reset'});if(alive.current){install(value);setProfile(null);}})}>{m.reset}</button>}
  </>}
  {closeQuestion&&<div role="alertdialog" aria-label={m.dirty}><p>{m.dirty}</p><button disabled={blocked} onClick={()=>{if(!blocked)p.onClose();}}>{m.discard}</button><button onClick={()=>setCloseQuestion(false)}>{m.keep}</button></div>}
 </section>;
}
