import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import {categoryVisible} from '../src/design/category-visibility.ts';
// Execute the pure projection with its real shared category classifier.
const source=stripTypeScriptTypes(readFileSync(new URL('../src/design/visible-coverage.ts',import.meta.url),'utf8'),{mode:'transform'}).replace(/^import .*;$/gm,'').replace(/export /g,'');
const visibleCoverage=new Function('categoryVisible',source+';return visibleCoverage;')(categoryVisible);
const reason=relativePath=>({relativePath,reasonCode:'SOURCE_UNREADABLE'});
const row={shareId:'share',path:'.',state:'partial',bytes:'123',endedAt:'2026-10-15T12:00:00Z',snapshotId:'snapshot',recycleBytes:'0',reasons:[reason('#recycle'),reason('@eaDir/SYNO@.fileindexdb')],reasonCount:2};
test('hidden categories do not reduce visible coverage and toggles restore warnings',()=>{
 assert.equal(visibleCoverage(row,{}).state,'complete');
 assert.equal(visibleCoverage(row,{}).reasonCount,0);
 assert.deepEqual(visibleCoverage(row,{showRecycle:true}).reasons,[reason('#recycle')]);
 assert.equal(visibleCoverage(row,{showSystem:true}).state,'partial');
 assert.deepEqual(visibleCoverage(row,{showRecycle:true,showSystem:true}),row);
 assert.equal(row.state,'partial');assert.equal(row.reasons.length,2);
});
test('visible, unknown, truncated and reset gaps remain explicit',()=>{
 for(const r of [{...row,reasons:[...row.reasons,reason('Photos')],reasonCount:3},{...row,reasonCount:20},{...row,reasons:[],reasonCount:0}])assert.equal(visibleCoverage(r,{}).state,'partial');
 assert.equal(visibleCoverage({...row,state:'unscanned'},{}).state,'unscanned');
 assert.equal(visibleCoverage({...row,path:'#recycle/sub',reasons:[reason('.')],reasonCount:1},{}).state,'complete');
 assert.equal(visibleCoverage({...row,reasons:[reason('#recycle-old')],reasonCount:1},{}).state,'partial');
});
