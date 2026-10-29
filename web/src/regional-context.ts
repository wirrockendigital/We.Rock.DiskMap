import {currentToken,SessionDenied} from './session';
export interface RegionalEnvironment {version:1;nasTimeZone:string|null;timeZoneSource:'system-zoneinfo'|'unavailable'}
// Read only the authenticated, bounded server observation; never infer NAS settings from the browser.
export async function readRegionalContext(signal:AbortSignal):Promise<RegionalEnvironment>{
 const token=currentToken();if(!token)throw new SessionDenied('SESSION_REQUIRED');
 const r=await fetch('./api.cgi?action=regional-context',{signal,credentials:'same-origin',cache:'no-store',redirect:'error',headers:{'X-SYNO-TOKEN':token,Accept:'application/json'}});signal.throwIfAborted();
 if(r.status===401||r.status===403)throw new SessionDenied('SESSION_REQUIRED');if(!r.ok||!r.body)throw Error('REGIONAL_UNAVAILABLE');
 const reader=r.body.getReader(),chunks:Uint8Array[]=[];let length=0;
 try{for(;;){const v=await reader.read();signal.throwIfAborted();if(v.done)break;length+=v.value.length;if(length>4096){await reader.cancel();throw Error('INVALID_REGIONAL_CONTEXT');}chunks.push(v.value);}}finally{reader.releaseLock();}
 const bytes=new Uint8Array(length);let offset=0;for(const v of chunks){bytes.set(v,offset);offset+=v.length;}
 const body=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes)),v=body.data as RegionalEnvironment;
 if(body.contractVersion!==1||v?.version!==1||!['system-zoneinfo','unavailable'].includes(v.timeZoneSource)||(v.timeZoneSource==='unavailable'?v.nasTimeZone!==null:typeof v.nasTimeZone!=='string'||v.nasTimeZone.length>128))throw Error('INVALID_REGIONAL_CONTEXT');
 if(v.nasTimeZone!==null){try{new Intl.DateTimeFormat('en',{timeZone:v.nasTimeZone});}catch{throw Error('INVALID_REGIONAL_CONTEXT');}}
 return v;
}
