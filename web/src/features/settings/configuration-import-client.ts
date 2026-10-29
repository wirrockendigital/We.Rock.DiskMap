import {currentToken,SessionDenied} from '../../session';
import {checkedScopeInventory,type ScopeInventory} from '../../scope-selection';
import {checkedConfigurationBundle,configurationBundleLimit,type ConfigurationBundle} from './configuration-export-client';
export const importSettingKeys=['appearance','display','preferredProfile','exclusionTemplate','retention','fileTypeMapping'] as const;
export type ImportSettings=Record<typeof importSettingKeys[number],boolean>;
export type ImportSelection={profileIds:string[];settings:ImportSettings};
export type ImportMapping={sourceTargetId:string;targetId:string|null};
export type ImportExpected={preferenceRevision:number;templateRevision:number;mappingRevision:number;retentionCompleteCount:number;profileCount:number;catalogDigest:string};
export type ImportCurrent=ConfigurationBundle['global']&ConfigurationBundle['personal'];
export interface ImportPreview {bundleDigest:string;selectionDigest:string;expected:ImportExpected;profiles:{sourceProfileId:string;name:string;selected:boolean;ready:boolean;issues:string[]}[];unassignedTargetIds:string[];settings:{current:ImportCurrent;selected:ImportSettings};issues:string[];canApply:boolean;tasksRequireReview:true}
export interface ImportReceipt {bundleDigest:string;selectionDigest:string;created:{sourceProfileId:string;profileId:string}[];settingsApplied:ImportSettings;tasksRequireReview:true}
export type ImportStatus='idle'|'saving'|'uncertain'|'conflict';
export const emptyImportSettings=():ImportSettings=>({appearance:false,display:false,preferredProfile:false,exclusionTemplate:false,retention:false,fileTypeMapping:false});
const id=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(v);
const digest=(v:unknown)=>typeof v==='string'&&/^[a-f0-9]{64}$/.test(v);
const fields=(v:any,keys:string[])=>v!==null&&typeof v==='object'&&!Array.isArray(v)&&Object.keys(v).length===keys.length&&keys.every(k=>Object.hasOwn(v,k));
const size=(s:string)=>new TextEncoder().encode(s).length;
const bad=()=>{throw Error('IMPORT_INVALID');};
const settings=(v:any):v is ImportSettings=>fields(v,[...importSettingKeys])&&importSettingKeys.every(k=>typeof v[k]==='boolean');
const sameSettings=(a:ImportSettings,b:ImportSettings)=>importSettingKeys.every(k=>a[k]===b[k]);
const issues=['MISSING_MAPPING','TARGET_UNAVAILABLE','SCOPE_INVALID','RESOURCE_UNSUPPORTED','PROFILE_LIMIT','PREFERRED_PROFILE_UNMAPPED'];
const integer=(v:unknown,max:number)=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0&&v<=max;
// Browser file access remains bounded and does not upload anything until the user requests a preview.
export async function parseConfigurationFile(file:Blob,signal:AbortSignal):Promise<ConfigurationBundle>{
 if(file.size>configurationBundleLimit)throw Error('CONFIGURATION_TOO_LARGE');signal.throwIfAborted();let rejectAbort=()=>{};const abort=new Promise<never>((_,reject)=>{rejectAbort=()=>reject(Error('IMPORT_ABORTED'));signal.addEventListener('abort',rejectAbort,{once:true});});let bytes:ArrayBuffer;try{bytes=await Promise.race([file.arrayBuffer(),abort]);}finally{signal.removeEventListener('abort',rejectAbort);}signal.throwIfAborted();if(bytes.byteLength>configurationBundleLimit)throw Error('CONFIGURATION_TOO_LARGE');
 try{return checkedConfigurationBundle(JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes)));}catch(e){if(e instanceof Error&&e.message==='CONFIGURATION_TOO_LARGE')throw e;throw Error('IMPORT_INVALID');}
}
// Canonical request identity is local only; server digests are never recomputed with JavaScript JSON rules.
function input(bundle:ConfigurationBundle,selection:ImportSelection,mappings:ImportMapping[]){
 const b=checkedConfigurationBundle(bundle);
 if(!fields(selection,['profileIds','settings'])||!Array.isArray(selection.profileIds)||selection.profileIds.length>200||new Set(selection.profileIds).size!==selection.profileIds.length||selection.profileIds.some(p=>!b.profiles.some(x=>x.sourceProfileId===p))||!settings(selection.settings)||!Array.isArray(mappings)||mappings.length>1536)bad();
 const sources=new Set(b.profiles.filter(p=>selection.profileIds.includes(p.sourceProfileId)).flatMap(p=>p.configuration.scope.targetIds)),mapped=new Set<string>();
 if(sources.size>1536)bad();for(const m of mappings){if(!fields(m,['sourceTargetId','targetId'])||!sources.has(m.sourceTargetId)||mapped.has(m.sourceTargetId)||!(m.targetId===null||id(m.targetId)))bad();mapped.add(m.sourceTargetId);}
 return {contractVersion:1,bundle:b,selection:structuredClone(selection),mappings:structuredClone(mappings),expected:null as ImportExpected|null};
}
// Every preview row and selected setting must describe exactly the submitted bundle and selection.
function checkedPreview(value:any,request:ReturnType<typeof input>):ImportPreview{
 const p=value as ImportPreview,e=p?.expected;
 if(!fields(p,['bundleDigest','selectionDigest','expected','profiles','unassignedTargetIds','settings','issues','canApply','tasksRequireReview'])||!digest(p.bundleDigest)||!digest(p.selectionDigest)||p.tasksRequireReview!==true||typeof p.canApply!=='boolean'||!fields(e,['preferenceRevision','templateRevision','mappingRevision','retentionCompleteCount','profileCount','catalogDigest'])||!integer(e.preferenceRevision,2147483647)||!integer(e.templateRevision,2147483647)||!integer(e.mappingRevision,Number.MAX_SAFE_INTEGER)||e.mappingRevision<1||![1,2,3,7,30].includes(e.retentionCompleteCount)||!integer(e.profileCount,200)||!digest(e.catalogDigest))bad();
 if(!Array.isArray(p.profiles)||p.profiles.length!==request.bundle.profiles.length||new Set(p.profiles.map(x=>x?.sourceProfileId)).size!==p.profiles.length)bad();
 for(const row of p.profiles){const source=request.bundle.profiles.find(x=>x.sourceProfileId===row.sourceProfileId);if(!fields(row,['sourceProfileId','name','selected','ready','issues'])||!source||row.name!==source.configuration.name||row.selected!==request.selection.profileIds.includes(row.sourceProfileId)||typeof row.ready!=='boolean'||!Array.isArray(row.issues)||row.issues.length>issues.length||new Set(row.issues).size!==row.issues.length||row.issues.some(x=>!issues.includes(x)))bad();}
 const targets=new Set(request.bundle.profiles.filter(x=>request.selection.profileIds.includes(x.sourceProfileId)).flatMap(x=>x.configuration.scope.targetIds));
 if(!Array.isArray(p.unassignedTargetIds)||p.unassignedTargetIds.length>1536||new Set(p.unassignedTargetIds).size!==p.unassignedTargetIds.length||p.unassignedTargetIds.some(x=>!targets.has(x))||!fields(p.settings,['current','selected'])||!settings(p.settings.selected)||!sameSettings(p.settings.selected,request.selection.settings))bad();
 const c=p.settings.current;
 if(!fields(c,['appearance','display','preferredProfileRef','exclusionTemplate','retentionCompleteCount','fileTypeMapping'])||c.retentionCompleteCount!==e.retentionCompleteCount||c.fileTypeMapping?.revision!==e.mappingRevision)bad();
 // Reuse the exact existing configuration validator rather than weakening current-settings shape.
 checkedConfigurationBundle({formatVersion:1,exportedAt:request.bundle.exportedAt,profiles:[],global:{exclusionTemplate:c.exclusionTemplate,retentionCompleteCount:c.retentionCompleteCount,fileTypeMapping:c.fileTypeMapping},personal:{appearance:c.appearance,display:c.display,preferredProfileRef:c.preferredProfileRef}});
 if(!Array.isArray(p.issues)||p.issues.length>issues.length||new Set(p.issues).size!==p.issues.length||p.issues.some(x=>!issues.includes(x)))bad();
 if(p.canApply&&(p.issues.length||p.unassignedTargetIds.length||p.profiles.some(x=>x.selected&&!x.ready)||(!request.selection.profileIds.length&&!Object.values(request.selection.settings).some(Boolean))))bad();
 return structuredClone(p);
}
function checkedReceipt(value:any,preview:ImportPreview,selection:ImportSelection):ImportReceipt{
 const r=value as ImportReceipt;
 if(!fields(r,['bundleDigest','selectionDigest','created','settingsApplied','tasksRequireReview'])||r.bundleDigest!==preview.bundleDigest||r.selectionDigest!==preview.selectionDigest||r.tasksRequireReview!==true||!settings(r.settingsApplied)||!sameSettings(r.settingsApplied,selection.settings)||!Array.isArray(r.created)||r.created.length!==selection.profileIds.length)bad();
 const seen=new Set<string>(),created=new Set<string>();for(const p of r.created){if(!fields(p,['sourceProfileId','profileId'])||!selection.profileIds.includes(p.sourceProfileId)||seen.has(p.sourceProfileId)||!id(p.profileId)||preview.profiles.some(x=>x.sourceProfileId===p.profileId)||created.has(p.profileId))bad();seen.add(p.sourceProfileId);created.add(p.profileId);}return structuredClone(r);
}
// A single client owns preview admission and one immutable unresolved Apply intent.
export class ConfigurationImportClient {
 status:ImportStatus='idle';private disposed=false;private controller:AbortController|null=null;
 private previewValue:{request:ReturnType<typeof input>;preview:ImportPreview}|null=null;
 private pending:{body:string;key:string;preview:ImportPreview;selection:ImportSelection}|null=null;
 constructor(private transport:typeof fetch=(i,o)=>fetch(i,o),private token:()=>string|null=currentToken,private key:()=>string=()=>crypto.randomUUID(),private deadline:()=>AbortSignal=()=>AbortSignal.timeout(15000)){}
 get unresolved(){return this.pending!==null;}
 dispose(){this.disposed=true;this.controller?.abort();this.previewValue=null;this.pending=null;}
 private available(){if(this.disposed)throw Error('IMPORT_DISPOSED');if(this.controller||this.pending)throw Error('IMPORT_PENDING');}
 async inventory():Promise<ScopeInventory>{this.available();return checkedScopeInventory(await this.request('scope-inventory',undefined,1048576));}
 async preview(bundle:ConfigurationBundle,selection:ImportSelection,mappings:ImportMapping[]):Promise<ImportPreview>{this.available();this.previewValue=null;const request=input(bundle,selection,mappings);const result=checkedPreview(await this.request('configuration-import-preview',JSON.stringify(request),1048576),request);this.previewValue={request,preview:result};this.status='idle';return structuredClone(result);}
 async apply():Promise<ImportReceipt>{this.available();if(!this.previewValue?.preview.canApply)throw Error('IMPORT_PREVIEW_REQUIRED');const {request,preview}=this.previewValue;const body=JSON.stringify({...request,expected:preview.expected});if(size(body)>configurationBundleLimit+524288)throw Error('CONFIGURATION_TOO_LARGE');this.pending={body,key:this.key(),preview:structuredClone(preview),selection:structuredClone(request.selection)};return this.send();}
 async retry():Promise<ImportReceipt>{if(this.disposed)throw Error('IMPORT_DISPOSED');if(this.controller||!this.pending||this.status!=='uncertain')throw Error('IMPORT_RETRY_UNAVAILABLE');return this.send();}
 private async send(){this.status='saving';const intent=this.pending!;try{const result=checkedReceipt(await this.request('configuration-import-apply',intent.body,65536,intent.key),intent.preview,intent.selection);this.pending=null;this.previewValue=null;this.status='idle';return result;}catch(e){if(!this.disposed&&this.pending)this.status='uncertain';throw e;}}
 private async request(action:string,body:string|undefined,limit:number,key?:string):Promise<any>{
  if(body&&size(body)>configurationBundleLimit+524288)throw Error('CONFIGURATION_TOO_LARGE');
  const controller=new AbortController();this.controller=controller;const signal=AbortSignal.any([controller.signal,this.deadline()]),token=this.token();
  const live=()=>{if(this.disposed||this.token()!==token)throw Error('IMPORT_DISPOSED');signal.throwIfAborted();};let rejectAbort=()=>{};
  const aborted=new Promise<never>((_,reject)=>{rejectAbort=()=>reject(Error('IMPORT_ABORTED'));signal.addEventListener('abort',rejectAbort,{once:true});if(signal.aborted)rejectAbort();});
  const wait=async<T,>(promise:Promise<T>)=>{try{const value=await Promise.race([promise,aborted]);live();return value;}catch(e){live();throw e;}};
  try{
   live();if(!token)throw new SessionDenied();const response=await wait(this.transport(`./api.cgi?action=${action}`,{method:body?'POST':'GET',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{Accept:'application/json','X-SYNO-TOKEN':token,...(body?{'Content-Type':'application/json','Idempotency-Key':key??this.key()}:{})},body}));
   if(response.status===401||response.status===403)throw new SessionDenied();if(!response.body)bad();const reader=response.body!.getReader(),chunks:Uint8Array[]=[];let n=0;
   try{for(;;){const part=await wait(reader.read());if(part.done)break;n+=part.value.length;if(n>limit)throw Error('IMPORT_RESPONSE_LIMIT');chunks.push(part.value);}}finally{void reader.cancel().catch(()=>{});reader.releaseLock();}
   const bytes=new Uint8Array(n);let at=0;for(const chunk of chunks){bytes.set(chunk,at);at+=chunk.length;}let e:any;try{e=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes));}catch{bad();}
   if(e.contractVersion!==1)bad();if(!response.ok){const code=e.error?.code;const definitive=(response.status===409&&['IMPORT_CONFLICT','IMPORT_UNMAPPED','SCOPE_CHANGED','IDEMPOTENCY_CONFLICT'].includes(code))||(response.status===400&&code==='INVALID_REQUEST')||(response.status===413&&code==='CONFIGURATION_TOO_LARGE');if(definitive){this.pending=null;this.previewValue=null;this.status=response.status===409?'conflict':'idle';throw Error(code);}throw Error('IMPORT_UNAVAILABLE');}return e.data;
  }finally{signal.removeEventListener('abort',rejectAbort);if(this.controller===controller)this.controller=null;}
 }
}
