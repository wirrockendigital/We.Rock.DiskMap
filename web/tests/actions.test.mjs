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
const output = mkdtempSync(join(web, '.test-build/actions-'));
let requestScan;
let scanIntent;
try {
  writeFileSync(join(output, 'package.json'), '{"type":"commonjs"}');
  for (const name of ['actions.ts', 'session.ts']) cpSync(join(web, 'src', name), join(output, name));
  execFileSync(join(web, 'node_modules/.bin/tsc'), ['--ignoreConfig', '--target', 'ES2022',
    '--module', 'Node16', '--moduleResolution', 'Node16', '--strict', '--skipLibCheck',
    '--outDir', join(output, 'compiled'), join(output, 'actions.ts')], { stdio: 'pipe' });
  ({ requestScan, scanIntent } = createRequire(import.meta.url)(join(output, 'compiled/actions.js')));
} finally { rmSync(output, { recursive: true, force: true }); }

// Conflict codes remain distinct while untrusted backend diagnostics are discarded.
test('scan preserves only the documented conflict codes', async () => {
 const signal = new AbortController().signal;
 for (const code of ['JOB_CONFLICT', 'IDEMPOTENCY_CONFLICT']) {
  const request = async () => new Response(JSON.stringify({contractVersion:1,error:{code}}), {status:409});
  await assert.rejects(requestScan({contractVersion:1,profileId:'p',mode:'full'},'key',signal,request,'synthetic'), new RegExp(code));
 }
 for (const body of [{contractVersion:2,error:{code:'JOB_CONFLICT'}},{contractVersion:1,error:{code:'PRIVATE_DIAGNOSTIC'}}]) {
  const request = async () => new Response(JSON.stringify(body),{status:409});
  await assert.rejects(requestScan({contractVersion:1,profileId:'p',mode:'full'},'key',signal,request,'synthetic'), /SERVICE_RESPONSE_INVALID/);
 }
});

// Each visible scan mode reaches the same guarded mutation route with its exact versioned payload.
test('full rebuild and reconcile submit exact scan mutations with stable retry keys', async () => {
 const signal=new AbortController().signal;
 for(const mode of ['full','rebuild','reconcile']){
  const bodies=[];
  const request=async(url,options)=>{assert.equal(url,'./api.cgi?action=scan');assert.equal(options.method,'POST');assert.equal(options.credentials,'same-origin');assert.equal(options.headers['Idempotency-Key'],'same-intent');bodies.push(JSON.parse(options.body));return new Response(JSON.stringify({contractVersion:1,data:{job:{scanMode:mode},deduplicated:false}}));};
  const intent={contractVersion:1,profileId:'p',mode};
  await requestScan(intent,'same-intent',signal,request,'synthetic');await requestScan(intent,'same-intent',signal,request,'synthetic');
  assert.deepEqual(bodies,[intent,intent]);
 }
 let calls=0;
 await assert.rejects(requestScan({contractVersion:1,profileId:'p',mode:'incremental'},'key',signal,async()=>{calls++;},'synthetic'),/INVALID_REQUEST/);assert.equal(calls,0);
});

// Delivery uncertainty cannot rebind an existing replay key to a different user operation.
test('pending scan binds both replay key and mode until resolved', () => {
 let generated=0;
 const first=scanIntent(null,'reconcile',()=>{generated++;return 'stable';});
 assert.deepEqual(first,{key:'stable',mode:'reconcile'});
 assert.equal(scanIntent(first,'reconcile',()=>{throw new Error('must not regenerate');}),first);
 assert.throws(()=>scanIntent(first,'rebuild'),/SCAN_INTENT_PENDING/);assert.equal(generated,1);
 assert.deepEqual(scanIntent(null,'rebuild',()=> 'new'),{key:'new',mode:'rebuild'});
});
