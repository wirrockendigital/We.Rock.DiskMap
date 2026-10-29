import {strict as assert} from 'node:assert';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
import {rolldown} from 'rolldown';
// Bundle the production policy to exercise exact scope identity without browser or network access.
const directory=await mkdtemp(join(tmpdir(),'diskmap-cache-test-'));
try {
 const bundle=await rolldown({cwd:new URL('../',import.meta.url).pathname,input:'src/design/scan-cache.ts',platform:'node'});
 await bundle.write({file:join(directory,'policy.mjs'),format:'esm'});await bundle.close();
 const {matchingCachedProfile,cachedResultLabel,hasRecordedResult}=await import(pathToFileURL(join(directory,'policy.mjs')));
 const configuration={name:'New display name',scope:{kind:'folder',targetIds:['shareA'],relativePath:'photos'},active:true,exclusions:[],resourceMode:'gentle',limits:{workers:1,ioBytesPerSecond:null,cpuPercent:null},freshnessSeconds:86400};
 const row={profile:{...configuration,name:'Previous name',id:'profileA',revision:1},activeSnapshotId:'snapshotA',latestJobId:null};
 assert.equal(matchingCachedProfile([row],configuration),row);
 // A cancelled fresh attempt cannot replace a valid older result in source selection.
 assert.equal(hasRecordedResult({...row,activeSnapshotId:null,latestJobId:'cancelled'},{snapshotId:null,lastAttempt:{snapshotState:''}},true),false);
 assert.equal(hasRecordedResult(row,{snapshotId:null,lastAttempt:{snapshotState:''}},true),true);
 assert.equal(hasRecordedResult({...row,activeSnapshotId:null},{snapshotId:null,lastAttempt:{snapshotState:'partial'}},true),true);
 for(const altered of [
  {...row,activeSnapshotId:null},
  {...row,profile:{...row.profile,active:false}},
  {...row,profile:{...row.profile,scope:{...row.profile.scope,relativePath:'photos2'}}},
  {...row,profile:{...row.profile,scope:{...row.profile.scope,targetIds:['shareB']}}},
  {...row,profile:{...row.profile,exclusions:[{kind:'pattern',value:'*.jpg'}]}}
 ])assert.equal(matchingCachedProfile([altered],configuration),undefined);
 assert.match(cachedResultLabel('2026-10-13T13:34:00','de'),/13\.10\.2026 um 13:34 Uhr/);
 assert.match(cachedResultLabel(null,'de'),/unbekannt/);
 assert.match(cachedResultLabel('invalid','en'),/unknown/);
 console.log('PASS: scope/config isolation, snapshot reuse, source timestamp and unknown date');
}finally{await rm(directory,{recursive:true,force:true});}
