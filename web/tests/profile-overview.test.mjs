import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Compile the production management boundary and real component, without a browser or NAS fixture.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/overview-'));let api,ui;
try{writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');cpSync(join(web,'src'),join(tmp,'src'),{recursive:true});execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'src/ProfileOverview.tsx')],{stdio:'pipe'});const require=createRequire(import.meta.url);api=require(join(tmp,'out/profile-management.js'));ui=require(join(tmp,'out/ProfileOverview.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
const profile=()=>({id:'source-profile',revision:3,name:'Private <name>',active:true,scope:{kind:'shares',targetIds:['share-id']},exclusions:[],resourceMode:'gentle',limits:{workers:1,ioBytesPerSecond:null,cpuPercent:null},freshnessSeconds:86400});
const envelope=data=>({contractVersion:1,data});
const receipt=(operation,p)=>envelope({operation,profileId:p.id,profile:p,cleanupScheduled:false,tasksRequireReview:operation==='deactivate'});
const duplicate=()=>({operation:'duplicate',profile:profile(),name:'Copy'});
const reply=request=>new Response(JSON.stringify(receipt('duplicate',{...request.profile,id:'new-id',name:request.name,revision:1})));
test('existing wire shapes preserve CAS and explicit delete confirmations',()=>{
 for(const op of ['duplicate','deactivate','delete']){const input={...duplicate(),operation:op,confirmProfileName:profile().name,confirmOwnDataDeletion:true,acknowledgeTasks:true};const wire=api.encodeManagement(input),body=JSON.parse(wire.body);assert.equal(wire.route,'profile-action');assert.equal(body.expectedRevision,3);assert.equal(body.profileId,'source-profile');if(op==='delete')assert.equal(body.acknowledgeTasks,true);}
 for(const overrides of [{confirmOwnDataDeletion:false},{acknowledgeTasks:false},{confirmProfileName:'wrong'}])assert.throws(()=>api.encodeManagement({operation:'delete',profile:profile(),confirmProfileName:profile().name,confirmOwnDataDeletion:true,acknowledgeTasks:true,...overrides}),/CONFIRMATION_REQUIRED/);
 for(const mode of ['reconcile','rebuild'])assert.deepEqual(JSON.parse(api.encodeManagement({operation:mode,profile:profile()}).body),{contractVersion:1,profileId:'source-profile',mode});
 assert.throws(()=>api.encodeManagement({operation:'rebuild',profile:{...profile(),active:false}}),/INACTIVE/);
});
test('duplicate has new server identity; deactivate preserves configuration and revision',()=>{
 const d=duplicate();assert.equal(api.checkedManagementReceipt(receipt('duplicate',{...profile(),id:'new',name:'Copy',revision:1}),d).profileId,'new');
 assert.throws(()=>api.checkedManagementReceipt(receipt('duplicate',{...profile(),name:'Copy',revision:1}),d));
 const p={...profile(),active:false};assert.equal(api.checkedManagementReceipt(receipt('deactivate',p),{operation:'deactivate',profile:profile()}).profile.active,false);
 assert.throws(()=>api.checkedManagementReceipt(receipt('deactivate',{...p,revision:4}),{operation:'deactivate',profile:profile()}));
});
test('actual scan receipt mode and revision remain truthful after catalogue changes',()=>{
 const input={operation:'reconcile',profile:profile()},data=envelope({job:{id:'job',profileId:'source-profile',scanMode:'reconcile',profileRevision:4},deduplicated:true});
 const r=api.checkedManagementReceipt(data,input);assert.equal(r.jobRevision,4);assert.equal(r.deduplicated,true);data.data.job.scanMode='rebuild';assert.throws(()=>api.checkedManagementReceipt(data,input));
});
test('timeout freezes intent across cards and retries with identical bytes/key',async()=>{
 const timer=new AbortController(),calls=[];let deadlines=0;
 const client=new api.ProfileManagementClient(async(_url,options)=>{calls.push(options);if(calls.length===1)return new Promise((_,reject)=>options.signal.addEventListener('abort',()=>reject(options.signal.reason)));return reply(duplicate());},()=> 'token',()=> 'same',()=>deadlines++===0?timer.signal:new AbortController().signal);
 const input=duplicate(),first=client.run(input);input.name='changed';timer.abort();await assert.rejects(first);assert.equal(client.status,'uncertain');await assert.rejects(client.run({operation:'deactivate',profile:profile()}),/PENDING/);await client.retry();assert.equal(calls[0].body,calls[1].body);assert.equal(calls[0].headers['Idempotency-Key'],calls[1].headers['Idempotency-Key']);
});
test('definite conflict clears intent; oversized/wrong receipts stay uncertain; disposed late denial is ignored',async()=>{
 for(const code of ['PROFILE_REVISION_CONFLICT','PROFILE_JOB_ACTIVE','JOB_CONFLICT']){const client=new api.ProfileManagementClient(async()=>new Response(JSON.stringify({contractVersion:1,error:{code}}),{status:409}),()=> 'token');await assert.rejects(client.run(duplicate()),new RegExp(code));assert.equal(client.unresolved,false);}
 const big=new api.ProfileManagementClient(async()=>new Response(' '.repeat(65537)),()=> 'token');await assert.rejects(big.run(duplicate()));assert.equal(big.unresolved,true);
 let resolve;const late=new api.ProfileManagementClient(()=>new Promise(r=>resolve=r),()=> 'token');const pending=late.run(duplicate());late.dispose();resolve(new Response('',{status:401}));await assert.rejects(pending,/DISPOSED/);
});
test('native fetch receiver and delete receipt do not fabricate cleanup completion',async()=>{
 const previous=globalThis.fetch;globalThis.fetch=async function(){assert.equal(this,undefined);return reply(duplicate());};try{await new api.ProfileManagementClient(undefined,()=> 'token').run(duplicate());}finally{globalThis.fetch=previous;}
 const r=api.checkedManagementReceipt(envelope({operation:'delete',profileId:'source-profile',cleanupScheduled:true,tasksRequireReview:true}),{operation:'delete',profile:profile()});assert.equal(r.cleanupScheduled,true);assert.equal(r.profile,null);assert.equal(r.complete,undefined);
});
test('DE EN overview has real actions, no overlapping total and bounded visible cards',()=>{
 const profiles=Array.from({length:45},(_,i)=>({profile:{...profile(),id:`p${i}`,name:`Profile <${i}>`},activeSnapshotId:null,latestJobId:null}));
 for(const language of ['de','en']){const html=renderToStaticMarkup(React.createElement(ui.ProfileOverview,{language,profiles,sessionIdentity:'session',loadSummary:async()=>[],onReload:async()=>{},onOpen(){},onEdit(){},onCreate(){},onChanged(){},onSessionDenied(){}}));assert.equal((html.match(/<article /g)||[]).length,20);assert.match(html,/Profile &lt;0&gt;/);assert.match(html,language==='de'?/Duplizieren/:/Duplicate/);assert.match(html,/reconcile/);assert.ok(!html.includes('Profile &lt;20&gt;'));assert.match(html,language==='de'?/aria-label="Nächste Profilseite"/:/aria-label="Next profile page"/);}
});

const summary=()=>({profileId:'source-profile',currentRevision:3,snapshotId:'snapshot',capturedRevision:2,endedAt:'2026-10-10T10:00:00Z',quality:'complete',freshness:'stale',ageSeconds:'9007199254740994',integrityQuality:'healthy',integrityCheckedAt:'2026-10-10T10:01:00Z',lastAttempt:{jobId:'latest',state:'completed',scanMode:'reconcile',startedAt:null,endedAt:'2026-10-10T10:02:00Z',snapshotState:'partial'}});
test('summary identity ordering, unknown quality and exact large age are validated',async()=>{
 const raw=envelope({observedAt:'2026-10-10T10:03:00Z',profiles:[summary()]});assert.equal(api.checkedProfileSummaries(raw,['source-profile'])[0].ageSeconds,'9007199254740994');assert.throws(()=>api.checkedProfileSummaries(raw,['other']));
 for(const patch of [{capturedRevision:null},{integrityQuality:'invented'},{lastAttempt:{...summary().lastAttempt,state:'unknown'}},{ageSeconds:9007199254740994}])assert.throws(()=>api.checkedProfileSummaries(envelope({observedAt:'2026-10-10T10:03:00Z',profiles:[{...summary(),...patch}]}),['source-profile']));
 let sent;await api.loadProfileSummaries(['source-profile'],new AbortController().signal,async(_url,options)=>{sent=JSON.parse(options.body);return new Response(JSON.stringify(raw));},'token');assert.deepEqual(sent,{contractVersion:1,profileIds:['source-profile']});await assert.rejects(api.loadProfileSummaries(Array.from({length:33},(_,i)=>`p${i}`),new AbortController().signal,undefined,'token'));
});
test('cleanup pending and complete are fresh separate observations, not receipt inference',async()=>{
 for(const pending of [2,0]){const out=await api.readProfileCleanup('source-profile',new AbortController().signal,async()=>new Response(JSON.stringify(envelope({profileId:'source-profile',deleted:true,pendingGenerations:pending,complete:pending===0,scope:'sealed-generations'}))),'token');assert.equal(out.complete,pending===0);}
 await assert.rejects(api.readProfileCleanup('source-profile',new AbortController().signal,async()=>new Response(JSON.stringify(envelope({profileId:'other',deleted:true,pendingGenerations:0,complete:true,scope:'sealed-generations'}))),'token'));
});
test('actual card renders old revision, recorded quality and latest partial separately',()=>{
 const old={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect};let injected=false;React.useRef=initial=>({current:initial});React.useEffect=()=>{};React.useState=initial=>{if(initial===null&&!injected){injected=true;return [[summary()],()=>{}];}return[typeof initial==='function'?initial():initial,()=>{}];};let tree;
 try{tree=ui.OverviewSession({language:'en',profiles:[{profile:profile(),activeSnapshotId:'snapshot',latestJobId:'latest'}],sessionIdentity:'session',loadSummary:async()=>[],onReload:async()=>{},onOpen(){},onEdit(){},onCreate(){},onChanged(){},onSessionDenied(){}});}finally{Object.assign(React,old);}
 const html=renderToStaticMarkup(tree);assert.match(html,/Recorded publication/);assert.match(html,/earlier configuration/);assert.match(html,/Partial coverage/);assert.match(html,/9,007,199,254,740,994/);assert.match(html,/Healthy at the recorded check/);
});
// Real component handlers enforce all confirmations and retain the dialog when delivery is uncertain.
test('delete dialog requires name own-data and task confirmation and blocks uncertain cancellation',async()=>{
 const old={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect},oldRun=api.ProfileManagementClient.prototype.run;const slots=[];let cursor=0,runs=0;
 React.useState=initial=>{const i=cursor++;if(!(i in slots))slots[i]=typeof initial==='function'?initial():initial;return[slots[i],v=>slots[i]=typeof v==='function'?v(slots[i]):v];};React.useRef=initial=>{const i=cursor++;if(!(i in slots))slots[i]={current:initial};return slots[i];};React.useEffect=()=>{};
 api.ProfileManagementClient.prototype.run=function(input){runs++;api.encodeManagement(input);this.pending={input,body:'frozen',key:'same'};this.status='uncertain';return Promise.reject(Error('timeout'));};
 const elements=(x,out=[])=>{if(Array.isArray(x))for(const y of x)elements(y,out);else if(x&&typeof x==='object'&&x.props){out.push(x);elements(x.props.children,out);}return out;};
 const p={language:'en',profiles:[{profile:profile(),activeSnapshotId:null,latestJobId:null}],sessionIdentity:'session',loadSummary:async()=>[],onReload:async()=>{},onOpen(){},onEdit(){},onCreate(){},onChanged(){},onSessionDenied(){}};
 const render=()=>{cursor=0;return ui.OverviewSession(p);},button=(tree,label)=>elements(tree).find(x=>x.type==='button'&&x.props.children===label);
 try{button(render(),'Delete').props.onClick();let tree=render();assert.equal(button(tree,'Confirm').props.disabled,true);elements(tree).find(x=>x.type==='input'&&!x.props.type).props.onChange({target:{value:profile().name}});tree=render();assert.equal(button(tree,'Confirm').props.disabled,true);for(const checkbox of elements(tree).filter(x=>x.type==='input'&&x.props.type==='checkbox'))checkbox.props.onChange({target:{checked:true}});tree=render();assert.equal(button(tree,'Confirm').props.disabled,false);button(tree,'Confirm').props.onClick();await new Promise(resolve=>setImmediate(resolve));tree=render();assert.equal(runs,1);assert.equal(button(tree,'Back').props.disabled,true);button(tree,'Back').props.onClick();tree=render();assert.ok(elements(tree).some(x=>x.props.role==='dialog'));const modal=elements(tree).find(x=>x.type==='dialog');assert.ok(modal);let prevented=false;modal.props.onCancel({preventDefault:()=>prevented=true});assert.equal(prevented,true);assert.ok(elements(render()).some(x=>x.type==='dialog'),'uncertain Escape cannot dismiss');assert.ok(elements(modal).includes(button(tree,'Confirm the same action again')),'retry is inside trapped dialog');}finally{Object.assign(React,old);api.ProfileManagementClient.prototype.run=oldRun;}
});

// A historically qualified complete result can lack time provenance; its age stays unknown.
test('historical complete without end timestamp retains unknown freshness',()=>{
 const row={...summary(),endedAt:null,freshness:'unknown',ageSeconds:null};const parsed=api.checkedProfileSummaries(envelope({observedAt:'2026-10-10T10:03:00Z',profiles:[row]}),['source-profile']);assert.equal(parsed[0].quality,'complete');assert.equal(parsed[0].endedAt,null);assert.equal(parsed[0].freshness,'unknown');assert.equal(parsed[0].ageSeconds,null);
});
