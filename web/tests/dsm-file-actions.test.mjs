import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, writeFileSync, rmSync, cpSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { createRequire } from 'node:module';

// Compile the production adapter and its real session boundary with the pinned compiler.
const web = fileURLToPath(new URL('../', import.meta.url));
mkdirSync(join(web, '.test-build'), { recursive: true });
const output = mkdtempSync(join(web, '.test-build/filestation-'));
let actions;
try {
  writeFileSync(join(output, 'package.json'), '{"type":"commonjs"}');
  for (const name of ['dsmFileActions.ts', 'dsmFileStation.ts', 'session.ts']) cpSync(join(web, 'src', name), join(output, name));
  execFileSync(join(web, 'node_modules/.bin/tsc'), ['--ignoreConfig', '--target', 'ES2022',
    '--module', 'Node16', '--moduleResolution', 'Node16', '--strict', '--skipLibCheck',
    '--outDir', join(output, 'compiled'), join(output, 'dsmFileActions.ts')], { stdio: 'pipe' });
  actions = createRequire(import.meta.url)(join(output, 'compiled/dsmFileActions.js'));
} finally { rmSync(output, { recursive: true, force: true }); }

// Fake transports exercise only serialization/ownership; no DSM request is performed.
function fixture() {
 const calls=[]; const sds={Session:{SynoToken:'synthetic'}}; const controller=new AbortController();
 const options={dsmBuild:'90080',confirmed:true,signal:controller.signal,host:{parent:{SYNO:{SDS:sds}}},request:async(url,init)=>{
  calls.push({url,init});
  if(url.startsWith('./api.cgi'))return Response.json({contractVersion:1,data:{authenticated:true}});
  if(url.startsWith('/webapi/query.cgi'))return Response.json({success:true,data:{'SYNO.FileStation.Delete':{path:'entry.cgi',minVersion:1,maxVersion:2}}});
  return Response.json({success:true,data:{taskid:'FileStation_test'}});
 }}; return {calls,options,sds,controller};
}
const target={kind:'file',fileStationPath:'/share/Grüße % # " .txt'};
test('single confirmed file uses literal JSON path and nonrecursive start once',async()=>{
 const f=fixture();assert.deepEqual(await actions.deleteSingleFile(target,f.options),{taskId:'FileStation_test',state:'started'});
 const writes=f.calls.filter(c=>c.init.method==='POST');assert.equal(writes.length,1);
 assert.deepEqual(JSON.parse(writes[0].init.body.get('path')),[target.fileStationPath]);assert.equal(writes[0].init.body.get('recursive'),'false');
 assert.equal(writes[0].init.headers['X-SYNO-TOKEN'],'synthetic');assert.ok(!writes[0].url.includes('synthetic'));
});
test('directory/share/unconfirmed rejected before request',async()=>{
 for(const t of [{kind:'directory',fileStationPath:'/share/d'},{kind:'file',fileStationPath:'/share'},{kind:'file',fileStationPath:'/share/../a'}]){
  const f=fixture();await assert.rejects(actions.deleteSingleFile(t,f.options));assert.equal(f.calls.length,0);
 }
 const f=fixture();await assert.rejects(actions.deleteSingleFile(target,{...f.options,confirmed:false}));assert.equal(f.calls.length,0);
});
test('lost start response stays unknown and never retries',async()=>{
 const f=fixture(),base=f.options.request;f.options.request=async(u,i)=>{if(i.method==='POST'){f.calls.push({url:u,init:i});throw Error('raw private message');}return base(u,i);};
 await assert.rejects(actions.deleteSingleFile(target,f.options),e=>e.code==='OUTCOME_UNKNOWN'&&!e.message.includes('private'));assert.equal(f.calls.filter(c=>c.init.method==='POST').length,1);
});
test('session changes during discovery prevent delete',async()=>{
 const f=fixture(),base=f.options.request;f.options.request=async(u,i)=>{const r=await base(u,i);if(u.startsWith('/webapi/query.cgi'))f.sds.Session.SynoToken='different';return r;};
 await assert.rejects(actions.deleteSingleFile(target,f.options));assert.equal(f.calls.filter(c=>c.init.method==='POST').length,0);
});
test('unknown viewers fail explicitly without network or launch',async()=>{
 const f=fixture();await assert.rejects(actions.openInDSMViewer(target,f.options),e=>e.code==='VIEWER_UNAVAILABLE');assert.equal(f.calls.length,0);
});
