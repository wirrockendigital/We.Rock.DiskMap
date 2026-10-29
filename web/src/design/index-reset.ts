import type {Scope} from '../contracts';
import {currentToken,SessionDenied} from '../session';
import {queryEnvelope} from '../analysis-query';
// Only qualified pre-mutation rejections can release the replay intent.
export class IndexResetRejected extends Error {}
export interface IndexResetIntent {key:string;scope:Scope|null}
// Retrying this exact intent reconciles the same durable operation instead of issuing a second reset.
export async function requestIndexReset(intent:IndexResetIntent,signal:AbortSignal):Promise<void>{
 const token=currentToken();if(!token)throw new SessionDenied();
 // Poll the same principal-bound intent; no repeated destructive operation is created.
 while(true){
 const response=await fetch('./api.cgi?action=index-reset',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{'Content-Type':'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':intent.key},body:JSON.stringify({contractVersion:1,confirmed:true,all:intent.scope===null,scope:intent.scope})});
 signal.throwIfAborted();if(currentToken()!==token||response.status===401||response.status===403)throw new SessionDenied();
 const envelope=await queryEnvelope(response);signal.throwIfAborted();if(!response.ok){const code=envelope.error?.code??'INVALID_RESPONSE';if(envelope.contractVersion===1&&['PROFILE_JOB_ACTIVE','INVALID_REQUEST','IDEMPOTENCY_CONFLICT'].includes(code))throw new IndexResetRejected(code);throw Error(code);}
 const receipt=envelope.data;if(envelope.contractVersion!==1||typeof receipt?.completed!=='boolean'||typeof receipt.resetId!=='string'||!Number.isInteger(receipt.generations)||receipt.generations<0)throw Error('INVALID_RESPONSE');
 if(receipt.completed)return;
 await new Promise<void>((resolve,reject)=>{const abort=()=>{clearTimeout(timer);reject(signal.reason);};const timer=setTimeout(()=>{signal.removeEventListener('abort',abort);resolve();},1000);signal.addEventListener('abort',abort,{once:true});if(signal.aborted)abort();});
 }
}
