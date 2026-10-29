import type {Scope} from './contracts';
import {checkedScopeInventory,type ScopeCheck,type ScopeInventory} from './scope-selection';
import type {FileStationTarget} from './dsmFileStation';
import {currentToken,SessionDenied} from './session';

// Retry transient read-only discovery failures without replaying scans or changing authorization.
async function fetchScope(url:string,init:RequestInit,token:string):Promise<Response>{
 const signal=init.signal;
 for(let attempt=0;;attempt++){
  signal?.throwIfAborted();if(currentToken()!==token)throw new SessionDenied('SESSION_REQUIRED');
  const response=await fetch(url,init);
  signal?.throwIfAborted();if(currentToken()!==token){await response.body?.cancel();throw new SessionDenied('SESSION_REQUIRED');}
  if(![502,503].includes(response.status)||attempt>=2)return response;
  await response.body?.cancel();
  await new Promise<void>((resolve,reject)=>{const abort=()=>{clearTimeout(timer);signal?.removeEventListener('abort',abort);reject(signal?.reason);};const timer=setTimeout(()=>{signal?.removeEventListener('abort',abort);resolve();},250*(attempt+1));signal?.addEventListener('abort',abort,{once:true});if(signal?.aborted)abort();});
 }
}

// Source preflight is explicit and bounded; it never calls the scan or permission APIs.
async function scopeRequest<T>(action:'scope-inventory'|'scope-preview',signal:AbortSignal,scope?:Scope):Promise<T>{
 const token=currentToken();if(!token)throw new SessionDenied('SESSION_REQUIRED');
 const response=await fetchScope(`./api.cgi?action=${action}`,{method:scope?'POST':'GET',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{Accept:'application/json','X-SYNO-TOKEN':token,...(scope?{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()}:{})},...(scope?{body:JSON.stringify({contractVersion:1,scope})}:{})},token);
 if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');
 const text=await response.text();if(text.length>1048576)throw new Error('SERVICE_RESPONSE_INVALID');
 let envelope:{contractVersion?:number;data?:T;error?:{code?:string}};try{envelope=JSON.parse(text);}catch{throw new Error('SERVICE_RESPONSE_INVALID');}
 if(!response.ok||envelope.contractVersion!==1||!envelope.data)throw new Error('SCOPE_UNAVAILABLE');
 return envelope.data;
}
// The component validates catalogue identities and discards superseded responses.
export const loadScopeInventory=(signal:AbortSignal):Promise<ScopeInventory>=>scopeRequest('scope-inventory',signal);
// This transient request obtains fresh root-level observations without saving its draft.
export const checkSelectedScope=(scope:Scope,signal:AbortSignal):Promise<ScopeCheck>=>scopeRequest('scope-preview',signal,scope);

export interface ScopeFolders {
 shareId:string;relativePath:string;entries:{name:string;relativePath:string}[];nextCursor:string|null;checkedAt:string;
}
// Picker paths are relative metadata identities and are never accepted as physical paths.
const folderPath=(value:unknown,root=false):value is string=>typeof value==='string'&&(value==='.'?root:value.length>0&&new TextDecoder().decode(new TextEncoder().encode(value))===value&&new TextEncoder().encode(value).length<=4096&&!/[\\\p{Cc}]/u.test(value)&&value.split('/').length<=256&&value.split('/').every(part=>part!==''&&part!=='.'&&part!=='..'));
// UTF-8 byte ordering matches the server, including supplementary Unicode names.
const folderOrder=(a:string,b:string):number=>{const x=new TextEncoder().encode(a),y=new TextEncoder().encode(b);for(let i=0;i<Math.min(x.length,y.length);i++){if(x[i]!==y[i])return x[i]-y[i];}return x.length-y.length;};
// One bounded, cancellable observation cannot publish a stale session error or a different directory.
export async function loadScopeFolders(shareId:string,relativePath:string,after:string|null,signal:AbortSignal):Promise<ScopeFolders> {
 if(!/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(shareId)||!folderPath(relativePath,true)||after!==null&&(!folderPath(after)||after.includes('/')))throw new Error('INVALID_SCOPE_FOLDER');
 const token=currentToken();if(!token)throw new SessionDenied('SESSION_REQUIRED');
 const request=new AbortController(),abort=()=>request.abort();signal.addEventListener('abort',abort,{once:true});if(signal.aborted)abort();
 const timer=setTimeout(abort,10000);
 try {
  const response=await fetchScope('./api.cgi?action=scope-folders',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal:request.signal,headers:{Accept:'application/json','Content-Type':'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({contractVersion:1,shareId,relativePath,after})},token);
  if(request.signal.aborted)throw new Error('SCOPE_UNAVAILABLE');
  if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');
  if(!response.body)throw new Error('SERVICE_RESPONSE_INVALID');
  const reader=response.body.getReader(),chunks:Uint8Array[]=[];let size=0;
  try{for(;;){const part=await reader.read();if(part.done)break;size+=part.value.byteLength;if(size>1048576)throw new Error('SERVICE_RESPONSE_INVALID');chunks.push(part.value);}}finally{await reader.cancel().catch(()=>undefined);}
  if(request.signal.aborted)throw new Error('SCOPE_UNAVAILABLE');
  const raw=new Uint8Array(size);let offset=0;for(const chunk of chunks){raw.set(chunk,offset);offset+=chunk.byteLength;}
  const envelope=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(raw)),v=envelope.data as ScopeFolders;
  if(!response.ok||envelope.contractVersion!==1||!v||v.shareId!==shareId||v.relativePath!==relativePath||!Array.isArray(v.entries)||v.entries.length>128||!Number.isFinite(Date.parse(v.checkedAt))||v.nextCursor!==null&&typeof v.nextCursor!=='string')throw new Error('SCOPE_UNAVAILABLE');
  let previous=after??'';
  for(const item of v.entries){if(!item||!folderPath(item.name)||item.name.includes('/')||folderOrder(item.name,previous)<=0||!folderPath(item.relativePath)||item.relativePath!==(relativePath==='.'?item.name:`${relativePath}/${item.name}`))throw new Error('SERVICE_RESPONSE_INVALID');previous=item.name;}
  if(v.nextCursor!==null&&(v.entries.length!==128||v.nextCursor!==previous))throw new Error('SERVICE_RESPONSE_INVALID');
  return v;
 }finally{clearTimeout(timer);signal.removeEventListener('abort',abort);request.abort();}
}

// Resolve tree identities against fresh authenticated inventory, independently of the selected snapshot.
// Folder enumeration verifies the exact relative directory; DSM enforces its own navigation rights.
export async function resolveScopeFileStation(scope:Scope,signal:AbortSignal):Promise<FileStationTarget> {
 if(!['shares','folder'].includes(scope.kind)||scope.targetIds.length!==1)throw Error('INVALID_SCOPE_FOLDER');
 const relative=scope.kind==='folder'?scope.relativePath:'.';
 if(!folderPath(relative,true))throw Error('INVALID_SCOPE_FOLDER');
 const inventory=checkedScopeInventory(await loadScopeInventory(signal));
 const share=inventory.shares.find(s=>s.id===scope.targetIds[0]&&s.readable&&s.state==='reachable');
 if(!share||!folderPath(share.name)||share.name.includes('/'))throw Error('SCOPE_UNAVAILABLE');
 await loadScopeFolders(share.id,relative,null,signal);signal.throwIfAborted();
 return {kind:'directory',fileStationPath:'/'+share.name+(relative==='.'?'':'/'+relative)};
}
