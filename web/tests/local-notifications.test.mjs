import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Compile only the real notification slice and shared DTOs in an isolated directory.
const web=fileURLToPath(new URL('../',import.meta.url)),tmp=mkdtempSync(join(web,'.notices-test-'));
let createNoticeSession,LocalNotices,NOTICE_LIMIT,NOTICE_DEDUP_LIMIT;
try {
 mkdirSync(join(tmp,'features/notifications'),{recursive:true});writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');
 for(const f of ['contracts.ts','features/notifications/notices.ts','features/notifications/LocalNotices.tsx'])cpSync(join(web,'src',f),join(tmp,f));
 execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--strict','--skipLibCheck','--jsx','react-jsx','--target','ES2022','--module','Node16','--moduleResolution','Node16','--outDir',join(tmp,'out'),join(tmp,'features/notifications/LocalNotices.tsx')],{stdio:'pipe'});
 const require=createRequire(import.meta.url);({createNoticeSession,NOTICE_LIMIT,NOTICE_DEDUP_LIMIT}=require(join(tmp,'out/features/notifications/notices.js')));({LocalNotices}=require(join(tmp,'out/features/notifications/LocalNotices.js')));
} finally {rmSync(tmp,{recursive:true,force:true});}
const outcome=(state='completed',snapshotState='complete',id='job1',warnings=[])=>({job:{id,state,warnings},snapshotState,coverageErrors:0});
test('success, active jobs and explicit cancellation are silent; terminal problems are local',()=>{
 const s=createNoticeSession();for(const state of ['queued','scanning','completed','cancelled'])assert.equal(s.observe(outcome(state)),false);
 assert.equal(s.observe(outcome('completed','partial')),true);assert.equal(s.observe(outcome('failed')),true);assert.equal(s.observe(outcome('interrupted')),true);
 assert.deepEqual(s.snapshot().map(n=>n.code),['SCAN_PARTIAL','SCAN_FAILED','SCAN_INTERRUPTED']);
});
test('same session deduplicates across profile consumers and dismissal; new session is independent',()=>{
 const s=createNoticeSession(),o=outcome('failed');assert.equal(s.observe(o),true);const id=s.snapshot()[0].id;s.dismiss(id);assert.equal(s.observe(o),false);assert.equal(s.snapshot().length,0);
 assert.equal(createNoticeSession().observe(o),true);s.dispose();assert.equal(s.observe(outcome('failed','complete','late')),false);assert.deepEqual(s.snapshot(),[]);
});
test('only bound reports and allowlisted codes can create target notices; private text never crosses',()=>{
 const s=createNoticeSession(),o=outcome('failed','complete','job1',[{code:'SOURCE_MISSING',message:'/private/secret'},{code:'/private/token',message:'password'}]);
 assert.equal(s.observe(o,{jobId:'foreign',boundaries:[{reasonCode:'SOURCE_LOCKED'}]}),true);
 assert.deepEqual(s.snapshot().map(n=>n.code),['SCAN_FAILED','TARGET_MISSING']);
 s.observe(o,{jobId:'job1',boundaries:[{reasonCode:'SOURCE_LOCKED',relativePath:'/private/name'},{reasonCode:'SOURCE_UNREADABLE'}]});
 assert.deepEqual(s.snapshot().map(n=>n.code),['SCAN_FAILED','TARGET_MISSING','TARGET_LOCKED','TARGET_UNREADABLE']);assert.doesNotMatch(JSON.stringify(s.snapshot()),/private|password|job1/);
});
test('visible and dedup memory remain bounded without eviction-triggered duplicate notices',()=>{
 const s=createNoticeSession();for(let i=0;i<NOTICE_DEDUP_LIMIT+20;i++)s.observe(outcome('failed','complete',`job${i}`));
 assert.equal(s.snapshot().length,NOTICE_LIMIT);assert.equal(s.observe(outcome('failed')),false);assert.equal(s.observe(outcome('failed','complete','beyond')),false);assert.ok(Object.isFrozen(s.snapshot()));assert.ok(s.snapshot().every(Object.isFrozen));
});
test('DE/EN expose description, action, expandable fixed code and dismiss without DSM sent claims',()=>{
 const s=createNoticeSession();s.observe(outcome('interrupted'));
 for(const language of ['de','en']) {const html=renderToStaticMarkup(React.createElement(LocalNotices,{language,notices:s.snapshot(),onDismiss(){}}));assert.match(html,/aria-live="polite"/);assert.match(html,/<details>/);assert.match(html,/SCAN_INTERRUPTED/);assert.match(html,language==='de'?/Scanstatus/:/scan status/);assert.doesNotMatch(html,/fetch|https:|sent|gesendet|job1/);}
});
