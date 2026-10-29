import { currentToken, SessionDenied } from './session';

// Each retry retains the complete original job and desired action, never a replacement intent.
export interface ControlIntent { jobId: string; action: 'pause'|'resume'|'cancel'|'cancel-queued'; key: string }
export interface ControlStatus { requested: 'run'|'pause'|'cancel'; version: number }
export function checkedControl(value: unknown): ControlStatus | null {
  if (value === null || value === undefined) return null;
  const v=value as Partial<ControlStatus>;
  if (!['run','pause','cancel'].includes(v.requested??'') || !Number.isSafeInteger(v.version) || (v.version??0)<1) throw new Error('INVALID_CONTROL_STATUS');
  return v as ControlStatus;
}

// The server receipt confirms intent admission; only subsequent job polling confirms a reached pause.
export async function requestJobControl(intent: ControlIntent, signal: AbortSignal, request: typeof fetch=fetch, token=currentToken()): Promise<ControlStatus> {
  const id=/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
  if (!id.test(intent.jobId)||!id.test(intent.key)||!['pause','resume','cancel','cancel-queued'].includes(intent.action)) throw new Error('INVALID_REQUEST');
  if (!token) throw new SessionDenied('SESSION_REQUIRED');
  const response=await request('./api.cgi?action=job-control',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{'Content-Type':'application/json',Accept:'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':intent.key},body:JSON.stringify({contractVersion:1,jobId:intent.jobId,action:intent.action})});
  if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');
  const reader=response.body?.getReader(); if(!reader)throw new Error('SERVICE_RESPONSE_INVALID');
  const chunks:Uint8Array[]=[];let size=0;
  try {while(true){const part=await reader.read();if(part.done)break;size+=part.value.byteLength;if(size>4096)throw new Error('SERVICE_RESPONSE_INVALID');chunks.push(part.value);}}
  finally{await reader.cancel().catch(()=>{});reader.releaseLock();}
  const bytes=new Uint8Array(size);let at=0;for(const part of chunks){bytes.set(part,at);at+=part.byteLength;}
  const body=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes));
  if(body?.contractVersion!==1)throw new Error('SERVICE_RESPONSE_INVALID');
  if(response.status===409 && ['JOB_CONFLICT','IDEMPOTENCY_CONFLICT'].includes(body.error?.code))throw new Error(body.error.code);
  if(!response.ok)throw new Error('ACTION_UNAVAILABLE');
  if(body.data?.jobId!==intent.jobId || body.data.action!==intent.action)throw new Error('SERVICE_RESPONSE_INVALID');
  return checkedControl({requested:intent.action==='resume'?'run':intent.action==='cancel-queued'?'cancel':intent.action,version:body.data.version})!;
}
