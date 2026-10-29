// The immutable record contract is shared with internal/mapindex/record.go.
export const RECORD_BYTES=128,BLOCK_BYTES=256*1024,RECORDS_PER_BLOCK=2048;
export interface IndexRecord {source:bigint;parent:number;left:number;right:number;kind:number;flags:number;presence:number;depth:number;maxDepth:number;files:bigint;unknown:bigint;bytes:bigint[]}
export interface MapManifest {version:1;status:'preparing'|'ready';snapshotId:string;indexId:string;rootId:string;root:number;count:number;blockBytes:number;record:string;identities:{slot:number;id:string;name:string;kind:string}[]}
// No decimal quantity crosses a floating-point boundary before layout ratios are calculated.
export function decodeRecord(raw:ArrayBufferLike,offset=0,count=Number.MAX_SAFE_INTEGER):IndexRecord{
 const v=new DataView(raw,offset,RECORD_BYTES),slot=(at:number)=>{const n=Number(v.getBigUint64(at,true));if(!Number.isSafeInteger(n)||n>count)throw Error('INVALID_MAP_INDEX');return n;};
 const kind=v.getUint8(32),flags=v.getUint8(33);if(kind>4||flags>3||v.getUint8(38)>3)throw Error('INVALID_MAP_INDEX');
 for(const i of [39,120,121,122,123,124,125,126,127])if(v.getUint8(i))throw Error('INVALID_MAP_INDEX');
 return {source:v.getBigUint64(0,true),parent:slot(8),left:slot(16),right:slot(24),kind,flags,presence:v.getUint8(38),depth:v.getUint16(34,true),maxDepth:v.getUint16(36,true),files:v.getBigUint64(40,true),unknown:v.getBigUint64(48,true),bytes:[0,1,2,3].map(i=>v.getBigUint64(56+i*16,true)+(v.getBigUint64(64+i*16,true)<<64n))};
}
// Category masks select disjoint buckets, including the recycle/system intersection exactly once.
export function visibleBytes(r:IndexRecord,recycle:boolean,system:boolean):bigint{return r.bytes[0]+(system?r.bytes[1]:0n)+(recycle?r.bytes[2]:0n)+(recycle&&system?r.bytes[3]:0n);}
export function decodeBase64(value:string):Uint8Array{const raw=atob(value);return Uint8Array.from(raw,c=>c.charCodeAt(0));}
