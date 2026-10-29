// A complete request identity prevents reuse across profiles, readers and query selectors.
export interface QueryIdentity {
 profileId:string; viewId:string; snapshotId:string; rootId:string; metric:string;
 effectiveFilters:Record<string,unknown>; sort:string; direction:string; page:string; limit:number; depth:number;
}
// Stable object-key ordering preserves array order and rejects unbounded or non-JSON inputs.
export function queryIdentity(value:QueryIdentity):string {
 let values=0;
 const stable=(input:unknown,depth=0):unknown=>{
  if(++values>1024||depth>8)throw new Error('QUERY_IDENTITY_INVALID');
  if(typeof input==='string'){if(input.length>16384)throw new Error('QUERY_IDENTITY_INVALID');return input;}
  if(input===null||typeof input==='boolean')return input;
  if(typeof input==='number'&&Number.isSafeInteger(input))return input;
  if(Array.isArray(input)){if(input.length>256)throw new Error('QUERY_IDENTITY_INVALID');return input.map(item=>stable(item,depth+1));}
  if(typeof input==='object'&&input&&Object.getPrototypeOf(input)===Object.prototype){const keys=Object.keys(input);if(keys.length>256||keys.some(key=>key.length>256))throw new Error('QUERY_IDENTITY_INVALID');return Object.fromEntries(keys.sort().map(key=>[key,stable((input as Record<string,unknown>)[key],depth+1)]));}
  throw new Error('QUERY_IDENTITY_INVALID');
 };
 const key=JSON.stringify(stable(value));if(key.length>16384)throw new Error('QUERY_IDENTITY_INVALID');return key;
}
// One current request is retained; completed or cancelled work cannot grow a response cache.
export class QuerySession {
 private sequence=0;
 private currentKey='';
 begin(identity:QueryIdentity):{key:string;sequence:number}{this.currentKey=queryIdentity(identity);return {key:this.currentKey,sequence:++this.sequence};}
 accepts(ticket:{key:string;sequence:number}):boolean{return ticket.sequence===this.sequence&&ticket.key===this.currentKey;}
 invalidate():void{this.currentKey='';++this.sequence;}
}
