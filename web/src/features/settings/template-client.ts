import {currentToken,SessionDenied} from '../../session';
export type TemplateRule={kind:'path'|'pattern';value:string};
export type GlobalTemplate={version:1;revision:number;rules:TemplateRule[]};
export type TemplateStatus='idle'|'saving'|'uncertain'|'conflict';
const bytes=(s:string)=>new TextEncoder().encode(s).length;
// Transport validation preserves literal values; rule semantics remain server-owned.
function rules(value:unknown):value is TemplateRule[]{return Array.isArray(value)&&value.length<=256&&value.every(r=>r&&Object.keys(r).length===2&&['path','pattern'].includes(r.kind)&&typeof r.value==='string'&&r.value.length>0&&bytes(r.value)<=4096&&!/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(r.value));}
// Set comparison attests canonical server deduplication without reimplementing glob matching.
function ruleKey(value:TemplateRule[]){return JSON.stringify([...new Set(value.map(r=>JSON.stringify([r.kind,r.value])))].sort());}
function checked(value:any):GlobalTemplate{if(!value||value.version!==1||!Number.isInteger(value.revision)||value.revision<0||value.revision>2147483647||!rules(value.rules)||(value.revision===0&&value.rules.length)||bytes(JSON.stringify(value))>16384)throw Error('TEMPLATE_INVALID');return structuredClone(value);}
// The client serializes requests and retains exactly one unresolved mutation in memory.
export class TemplateClient {
 status:TemplateStatus='idle';private pending:{body:string;key:string}|null=null;private controller:AbortController|null=null;private disposed=false;
 constructor(private transport:typeof fetch=(input,init)=>fetch(input,init),private token:()=>string|null=currentToken,private key:()=>string=()=>crypto.randomUUID(),private deadline:()=>AbortSignal=()=>AbortSignal.timeout(10000)){}
 get unresolved(){return this.pending!==null;}
 dispose(){this.disposed=true;this.controller?.abort();this.pending=null;}
 private live(){if(this.disposed)throw Error('TEMPLATE_DISPOSED');}
 async read():Promise<GlobalTemplate>{this.live();if(this.pending||this.controller)throw Error('TEMPLATE_PENDING');const result=await this.request();this.status='idle';return result;}
 async save(revision:number,value:TemplateRule[]):Promise<GlobalTemplate>{this.live();if(this.pending||this.controller)throw Error('TEMPLATE_PENDING');if(this.status==='conflict')throw Error('TEMPLATE_RELOAD_REQUIRED');if(!Number.isInteger(revision)||revision<0||revision>=2147483647||!rules(value))throw Error('TEMPLATE_INVALID');const body=JSON.stringify({contractVersion:1,expectedRevision:revision,rules:value});if(bytes(body)>16384)throw Error('TEMPLATE_LIMIT');this.pending={body,key:this.key()};return this.send();}
 async retry():Promise<GlobalTemplate>{this.live();if(this.status!=='uncertain'||!this.pending||this.controller)throw Error('TEMPLATE_RETRY_UNAVAILABLE');return this.send();}
 private async send(){this.status='saving';try{const result=await this.request(this.pending!);this.pending=null;this.status='idle';return result;}catch(e){this.live();if(this.pending)this.status='uncertain';throw e;}}
 private async request(intent?:{body:string;key:string}):Promise<GlobalTemplate>{
  const controller=new AbortController();this.controller=controller;const signal=AbortSignal.any([controller.signal,this.deadline()]);let rejectAbort:()=>void=()=>{};
  // Race every awaited transport/body operation so an abort-ignoring transport cannot hold the editor forever.
  const aborted=new Promise<never>((_,reject)=>{rejectAbort=()=>reject(Error('TEMPLATE_ABORTED'));signal.addEventListener('abort',rejectAbort,{once:true});if(signal.aborted)rejectAbort();});
  const wait=async<T,>(p:Promise<T>)=>{try{const v=await Promise.race([p,aborted]);this.live();signal.throwIfAborted();return v;}catch(e){this.live();signal.throwIfAborted();throw e;}};
  try{this.live();signal.throwIfAborted();const token=this.token();if(!token)throw new SessionDenied();const response=await wait(this.transport('./api.cgi?action=exclusion-template',{method:intent?'POST':'GET',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{'X-SYNO-TOKEN':token,Accept:'application/json',...(intent?{'Content-Type':'application/json','Idempotency-Key':intent.key}:{})},body:intent?.body}));
   if(response.status===401||response.status===403)throw new SessionDenied();if(!response.body)throw Error('TEMPLATE_INVALID');const reader=response.body.getReader(),chunks:Uint8Array[]=[];let length=0;
   try{for(;;){const part=await wait(reader.read());if(part.done)break;length+=part.value.length;if(length>65536)throw Error('TEMPLATE_LIMIT');chunks.push(part.value);}}finally{void reader.cancel().catch(()=>{});reader.releaseLock();}
   const all=new Uint8Array(length);let offset=0;for(const chunk of chunks){all.set(chunk,offset);offset+=chunk.length;}const envelope=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(all));
   if(!response.ok){const code=envelope?.contractVersion===1?envelope.error?.code:null;if(intent&&((response.status===400&&code==='INVALID_REQUEST')||(response.status===409&&['TEMPLATE_CONFLICT','IDEMPOTENCY_CONFLICT'].includes(code)))){this.pending=null;this.status=response.status===409?'conflict':'idle';throw Error(code);}throw Error('TEMPLATE_UNAVAILABLE');}
   if(envelope.contractVersion!==1)throw Error('TEMPLATE_INVALID');const result=checked(envelope.data);if(intent){const submitted=JSON.parse(intent.body);if(result.revision!==submitted.expectedRevision+1||ruleKey(result.rules)!==ruleKey(submitted.rules))throw Error('TEMPLATE_INVALID');}return result;
  }finally{signal.removeEventListener('abort',rejectAbort);if(this.controller===controller)this.controller=null;}
 }
}
