import { currentToken, SessionDenied } from './session';

// Only identifiers survive an authorization recheck; no source metadata or credentials are retained.
export interface ViewPin { profileId: string; viewId: string; snapshotId: string; nodeId: string | null }
export interface ReaderIntent { contractVersion: 1; operation: 'open' | 'read' | 'switch' | 'release'; profileId: string; viewId: string; jobId?: string; newViewId?: string; snapshotId?: string; nodeId?: string }

// Ephemeral reader operations use the same origin/session boundary as other controlled requests.
export async function requestReader<T>(intent: ReaderIntent, signal: AbortSignal, request: typeof fetch = fetch): Promise<T> {
 const token=currentToken(); if(!token)throw new SessionDenied('SESSION_REQUIRED');
 const response=await request('./api.cgi?action=view',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{'Content-Type':'application/json',Accept:'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':intent.newViewId??intent.viewId},body:JSON.stringify(intent)});
 if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');
 const text=await response.text();if(text.length>65536)throw new Error('SERVICE_RESPONSE_INVALID');
 let value:{contractVersion?:number;data?:T;error?:{code?:string}};try{value=JSON.parse(text);}catch{throw new Error('SERVICE_RESPONSE_INVALID');}
 if(!response.ok){const code=value.error?.code;if(value.contractVersion===1&&['VIEW_EXPIRED','VIEW_LIMIT','SNAPSHOT_MISMATCH','IDEMPOTENCY_CONFLICT'].includes(code??''))throw new Error(code);throw new Error('SERVICE_RESPONSE_INVALID');}
 if(value.contractVersion!==1||!value.data)throw new Error('SERVICE_RESPONSE_INVALID');return value.data;
}

// Deliver neither success nor failure after the caller has adopted another lease.
export async function settlePinnedRead<T>(pending:Promise<T>, current:()=>boolean, accept:(value:T)=>void, reject:(error:unknown)=>void):Promise<void> {
 try { const value=await pending;if(current())accept(value); } catch(error) { if(current())reject(error); }
}

// Unknown outcomes retain the complete original payload until an authoritative response resolves it.
export function retainReaderIntent(pending:ReaderIntent|null,candidate:ReaderIntent):ReaderIntent {return pending??Object.freeze({...candidate});}
