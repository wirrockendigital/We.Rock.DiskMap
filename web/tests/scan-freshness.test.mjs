import assert from 'node:assert/strict';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';import {join} from 'node:path';import {pathToFileURL} from 'node:url';import {rolldown} from 'rolldown';
// Verify exact scope boundaries, conservative coverage and account-local thresholds.
const dir=await mkdtemp(join(tmpdir(),'diskmap-age-'));
try{
 const b=await rolldown({cwd:new URL('../',import.meta.url).pathname,input:'src/design/scan-freshness.ts',platform:'node'});await b.write({file:join(dir,'age.mjs'),format:'esm'});await b.close();
 const m=await import(pathToFileURL(join(dir,'age.mjs'))),storage=new Map(),s={getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v)};
 m.writeFreshness('a',{value:2,unit:'weeks'},s);assert.deepEqual(m.readFreshness('a',s),{value:2,unit:'weeks'});assert.deepEqual(m.readFreshness('b',s),{value:7,unit:'days'});assert.throws(()=>m.writeFreshness('a',{value:0,unit:'days'},s));
 const inv={volumes:[{id:'v1'},{id:'v2'}],shares:[{id:'s1',volumeId:'v1'},{id:'s2',volumeId:'v2'}]},volume={kind:'volumes',targetIds:['v2']},folder={kind:'folder',targetIds:['s2'],relativePath:'Music'};
 assert(m.scopeCovers(volume,folder,inv));assert(!m.scopeCovers(volume,{kind:'shares',targetIds:['s1']},inv));assert(!m.scopeCovers(folder,{...folder,relativePath:'Musical'},inv));assert(m.scopeCovers(folder,{...folder,relativePath:'Music/Live'},inv));
 const p=[{profile:{id:'p',revision:1,active:true,exclusions:[],scope:volume}}],summaries=[{profileId:'p',quality:'complete',capturedRevision:1,endedAt:'2026-10-14T00:00:00Z',lastAttempt:null}];
 assert.equal(m.scopeStamp(volume,p,summaries,inv,true).state,'complete');assert.equal(m.scopeStamp({kind:'nas',targetIds:[]},p,summaries,inv,true).state,'partial');assert.equal(m.scopeStamp({kind:'volumes',targetIds:['v1']},p,summaries,inv,true).state,'unscanned');
 assert.equal(m.scopeStamp(folder,p,summaries,inv,true).state,'complete');assert.equal(m.scopeStamp(volume,p,summaries,inv,false).state,'unknown');
 const stamp={state:'complete',endedAt:'2026-10-14T00:00:00Z'},start=Date.parse(stamp.endedAt),pref={value:1,unit:'hours'};
 assert.equal(m.freshnessDisplay({...stamp,state:'partial'},pref,'de',start).color,m.freshnessDisplay(stamp,pref,'de',start).color);
 assert.notEqual(m.freshnessDisplay(stamp,pref,'de',start).color,m.freshnessDisplay(stamp,pref,'de',start+3600000).color);assert.match(m.scanTimestamp(stamp,'de'),/14.10.2026/);
 console.log('PASS: scope and path boundaries, no false complete parent, unknown summaries, account settings and age gradient');
}finally{await rm(dir,{recursive:true,force:true});}
