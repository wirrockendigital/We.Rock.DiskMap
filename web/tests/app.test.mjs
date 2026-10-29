import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, mkdirSync, mkdtempSync, writeFileSync, rmSync, cpSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { createRequire } from 'node:module';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
// Complete synthetic query envelopes preserve the same metadata and five counters as the backend.
function completeHierarchy(query,nodes) {
 const root=nodes.find(n=>n.id===query.rootId);assert.ok(root,'fixture must contain its requested root');
 const summary=root.summary;
 return {...query,state:'complete',truncated:false,selectionOrder:'recorded-rowid',coverage:[],sourceAvailability:{total:'0',truncated:false,records:[]},originalSummary:summary,matchSummary:summary,denominator:summary,matchState:BigInt(summary.fileCount)+BigInt(summary.directoryCount)+BigInt(summary.specialCount)===0n?'empty':'matches',nodes:nodes.map(n=>({...n,classification:null,originalSummary:n.summary,matchSummary:n.summary,matchKind:n.virtual?'ancestor':'direct'}))};
}
// Hook fixtures explicitly mount a measured synthetic viewport; production has no size fallback.
class MeasuredViewportObserver {
 constructor(callback){this.callback=callback;}
 observe(target){this.callback([{target,contentRect:{width:900,height:500}}]);}
 disconnect(){}
}
function attachFixtureViewport(tree,viewport){
 if(Array.isArray(tree)){tree.forEach(child=>attachFixtureViewport(child,viewport));return;}
 if(!React.isValidElement(tree))return;
 if(tree.props.className==='map-viewport')tree.props.ref(viewport);
 attachFixtureViewport(tree.props.children,viewport);
}
// Compile the real component with the pinned TypeScript CLI, including its JSON catalogues.
const web = fileURLToPath(new URL('../', import.meta.url));
const base = join(web, '.test-build');
mkdirSync(base, { recursive: true });
const output = mkdtempSync(join(base, 'component-'));
let App;
let gateState;
let localHelp;
let session;
let monitorSession;
let workbench;
let fileStation;
let nodeDetails;
let snapshotReader;
let jobControl;
let querySession;
try {
  writeFileSync(join(output, 'package.json'), '{"type":"commonjs"}');
  // Copy the actual source closure so new app modules cannot silently escape this harness.
  cpSync(join(web, 'src'), output, { recursive: true });
  // Match the native browser API libraries used by the production TypeScript configuration.
  execFileSync(join(web, 'node_modules/.bin/tsc'), ['--ignoreConfig', '--target', 'ES2022', '--lib', 'ES2024,DOM,DOM.Iterable',
    '--module', 'Node16', '--moduleResolution', 'Node16', '--jsx', 'react-jsx',
    '--esModuleInterop', '--resolveJsonModule', '--skipLibCheck', '--outDir', join(output, 'compiled'),
    // Compile the independently tested query utility even when the UI no longer imports it.
    join(output, 'App.tsx'), join(output, 'Workbench.tsx'), join(output, 'LocalHelp.tsx'), join(output, 'query-session.ts')], { stdio: 'pipe' });
  writeFileSync(join(output, 'package.json'), '{"type":"commonjs"}');
  localHelp = createRequire(import.meta.url)(join(output, 'compiled/LocalHelp.js'));
  gateState = createRequire(import.meta.url)(join(output, 'compiled/App.js')).gateState;
  App = createRequire(import.meta.url)(join(output, 'compiled/App.js')).App;
  snapshotReader = createRequire(import.meta.url)(join(output,'compiled/snapshot-reader.js'));
  workbench = createRequire(import.meta.url)(join(output, 'compiled/Workbench.js'));
  querySession = createRequire(import.meta.url)(join(output,'compiled/query-session.js'));
  jobControl = createRequire(import.meta.url)(join(output, 'compiled/job-control.js'));
  fileStation = createRequire(import.meta.url)(join(output, 'compiled/dsmFileStation.js'));
  nodeDetails = createRequire(import.meta.url)(join(output, 'compiled/NodeDetails.js'));
  session = createRequire(import.meta.url)(join(output, 'compiled/session.js'));
  monitorSession = createRequire(import.meta.url)(join(output, 'compiled/session-monitor.js')).monitorSession;
} finally { rmSync(output, { recursive: true, force: true }); }
// Independent package measurement and coverage metadata accompany both present and absent snapshots.
const storageFixture = () => ({state:'complete',startedAt:'2026-10-10T00:00:00Z',endedAt:'2026-10-10T00:01:00Z',scanBoundaries:[],scanBoundaryCount:'0',scanBoundariesTruncated:false,packageUsage:{logicalBytes:'8192',allocatedBytes:'12288',measuredAt:'2026-10-10T00:00:00Z',state:'complete',method:'package-state-stat'}});
for (const language of ['de', 'en']) test(`application renders the actual ${language} catalogue`, () => {
  const messages = JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`, import.meta.url), 'utf8'));
  const html = renderToStaticMarkup(React.createElement(App, { language }));
  assert.ok(html.includes(`<main class="disk-session-gate" lang="${language}">`));
  assert.ok(html.includes('<h1>We.Rock.DiskMap</h1>'));
  assert.ok(html.includes(messages['session.checking']));
  assert.ok(!html.includes(messages['setup.status']));
  assert.ok(html.includes('role="status"'));
});

// The same frontend request boundary used by the app keeps credentials out of URLs and storage.
test('session adapter fails closed and sends transient same-origin credentials', async () => {
  const signal = new AbortController().signal;
  let calls = 0;
  const request = async (url, options) => {
    calls++;
    assert.equal(url, './api.cgi?action=session');
    assert.equal(options.credentials, 'same-origin'); assert.equal(options.cache, 'no-store');
    assert.equal(options.redirect, 'error'); assert.equal(options.headers['X-SYNO-TOKEN'], 'synthetic');
    return new Response(JSON.stringify({contractVersion:1,data:{authenticated:true}}));
  };
  await assert.rejects(session.sessionRequest('session', signal, null, request), session.SessionDenied);
  assert.equal(calls,0);
  await session.sessionRequest('session', signal, 'synthetic', request); assert.equal(calls,1);
  for(const status of [401,403]) await assert.rejects(session.sessionRequest('session',signal,'synthetic',async()=>new Response('',{status})),session.SessionDenied);
  for(const response of [new Response('{}'),new Response('not-json'),new Response('',{status:503})]) {
    await assert.rejects(session.sessionRequest('session',signal,'synthetic',async()=>response));
  }
});
// A changed DSM token is read on every call, with no process-global token cache.
test('DSM token lookup is fresh and isolated from inaccessible parent contexts', () => {
  const host={parent:{SYNO:{SDS:{Session:{SynoToken:'first'}}}}};
  assert.equal(session.currentToken(host),'first');host.parent.SYNO.SDS.Session.SynoToken='second';
  assert.equal(session.currentToken(host),'second');
  assert.equal(session.currentToken({parent:{}}),null);
  assert.equal(session.currentToken({get parent(){throw new Error('cross-origin');}}),null);
});

// Deterministic timers exercise actual monitor transitions without browser or network sleeps.
test('periodic authorization preserves view; hidden state stops polling and invalidates pending work', async () => {
  const timers=new Map();let sequence=0;let visible=true;const states=[];const requests=[];
  const monitor=monitorSession({visible:()=>visible,changed:state=>states.push(state),
    schedule:(callback,delay)=>{const id=++sequence;timers.set(id,{callback,delay});return id;},cancel:id=>timers.delete(id),
    request:signal=>new Promise((resolve,reject)=>requests.push({signal,resolve,reject}))});
  const flush=async()=>{await Promise.resolve();await Promise.resolve();};
  const fire=delay=>{const entry=[...timers].find(([,t])=>t.delay===delay);assert.ok(entry);timers.delete(entry[0]);entry[1].callback();};
  assert.deepEqual(states,['checking']); requests[0].resolve();await flush();assert.equal(states.at(-1),'authenticated');
  const count=states.length;fire(15000);assert.equal(requests.length,2);assert.equal(states.length,count);
  requests[1].resolve();await flush();assert.deepEqual(states.slice(count),['authenticated']);
  fire(15000);visible=false;monitor.visibility();assert.equal(states.at(-1),'authenticated');assert.equal(timers.size,0);assert.ok(requests[2].signal.aborted);
  requests[2].resolve();await flush();assert.equal(states.at(-1),'authenticated');assert.equal(timers.size,0);
  monitor.focus();assert.equal(requests.length,3);assert.equal(timers.size,0);
  visible=true;monitor.visibility();assert.equal(requests.length,4);assert.equal(states.at(-1),'authenticated');
  requests[3].resolve();await flush();assert.equal(states.at(-1),'authenticated');
  fire(15000);requests[4].reject(new session.SessionDenied());await flush();assert.equal(states.at(-1),'denied');
  monitor.stop();assert.equal(timers.size,0);
});
// A hung periodic request loses access at five seconds even if an adapter ignores abort.
test('periodic timeout hides content and ignores a late successful response', async () => {
  const timers=new Map();let sequence=0;const states=[];const requests=[];
  const monitor=monitorSession({visible:()=>true,changed:state=>states.push(state),
    schedule:(callback,delay)=>{const id=++sequence;timers.set(id,{callback,delay});return id;},cancel:id=>timers.delete(id),
    request:signal=>new Promise(resolve=>requests.push({resolve,signal}))});
  const flush=async()=>{await Promise.resolve();await Promise.resolve();};
  const fire=delay=>{const [id,timer]=[...timers].find(([,t])=>t.delay===delay);timers.delete(id);timer.callback();};
  requests[0].resolve();await flush();fire(15000);fire(5000);
  assert.equal(states.at(-1),'unavailable');assert.ok(requests[1].signal.aborted);
  requests[1].resolve();await flush();assert.equal(states.at(-1),'unavailable');
  monitor.focus();assert.equal(states.at(-1),'checking');assert.equal(requests.length,3);
  monitor.stop();requests[2].resolve();await flush();assert.equal(timers.size,0);
});

// View requests preserve the same session boundary and carry only encoded IDs.
test('index reads require current authentication and reject incompatible envelopes', async () => {
  const signal = new AbortController().signal;
  let calls = 0;
  const request = async (url, options) => {
    calls++;
    assert.equal(url, './api.cgi?action=snapshot&profileId=profile%26other');
    assert.equal(options.headers['X-SYNO-TOKEN'], 'synthetic');
    assert.equal(options.credentials, 'same-origin');
    assert.equal(options.cache, 'no-store');
    assert.equal(options.redirect, 'error');
    return new Response(JSON.stringify({contractVersion:1,data:{snapshotId:null,nodes:[],truncated:false}}));
  };
  const parameters = {action:'snapshot', profileId:'profile&other'};
  await assert.rejects(workbench.readView(parameters, signal, request, null), session.SessionDenied);
  assert.equal(calls, 0);
  assert.equal((await workbench.readView(parameters, signal, request, 'synthetic')).snapshotId, null);
  for (const status of [401,403]) await assert.rejects(workbench.readView(parameters,signal,async()=>new Response('',{status}),'synthetic'),session.SessionDenied);
  for (const body of [{contractVersion:2,data:{}},{contractVersion:1,data:null},{}]) await assert.rejects(workbench.readView(parameters,signal,async()=>new Response(JSON.stringify(body)),'synthetic'));
});
// Real table rendering retains large integers, distinguishes unknown from zero, and escapes filenames.
test('index table validates generations and renders bounded measured rows safely', () => {
  const messages = JSON.parse(readFileSync(new URL('../src/locales/de.json', import.meta.url), 'utf8'));
  const node = {linkCount:1,id:'n1',snapshotId:'s1',originalName:'<script>unsafe</script>',relativePath:'a&b',kind:'file',logicalBytes:'9007199254740993',allocatedBytes:'0'};
  const value = {...storageFixture(),profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'Original profile',snapshotId:'s1',nodes:[node,{...node,id:'n2',kind:'symlink',logicalBytes:null,allocatedBytes:null}],truncated:true};
  assert.equal(workbench.checkedSnapshot(value),value);
  assert.equal(workbench.checkedSnapshot({...storageFixture(),snapshotId:null,state:null,startedAt:null,endedAt:null,nodes:[],truncated:false,profileRevision:null,currentProfileRevision:1,profileChanged:false,capturedProfileName:null}),null);
  for (const bad of [{...value,snapshotId:null},{...value,nodes:[node,node]},{...value,nodes:[{...node,snapshotId:'old'}]},{...value,nodes:[{...node,logicalBytes:9007199254740992}]},{...value,nodes:[{...node,allocatedBytes:'-1'}]}]) assert.throws(()=>workbench.checkedSnapshot(bad));
  const html=renderToStaticMarkup(React.createElement(workbench.IndexTable,{snapshot:value,messages,open:()=>{},disabled:false}));
  assert.ok(html.includes('9,007,199,254,740,993 B'));assert.ok(html.includes('0 B'));assert.ok(html.includes(messages['probe.unknown']));
  assert.ok(html.includes(messages['probe.truncated']));assert.ok(html.includes('&lt;script&gt;'));assert.ok(!html.includes('<script>'));
  assert.equal((html.match(/<button/g)||[]).length,1);
});

// The actual App reducer preserves only selection through remounting and clears it on lost access.
test('focus authorization remount restores selection but denial clears it', () => {
  let state = {session:'authenticated',selected:'second-profile'};
  state = gateState(state,{session:'checking'});
  assert.deepEqual(state,{session:'checking',selected:'second-profile'});
  assert.equal(gateState(state,{selected:'late-response'}),state);
  state = gateState(state,{session:'authenticated'});
  assert.equal(state.selected,'second-profile');
  for(const session of ['denied','unavailable']) assert.deepEqual(gateState(state,{session}),{session,selected:''});
});

// Focus arrives before click: a confirmed view must stay mounted while fresh authorization is pending.
test('visible focus preserves the click target and repeated focus cannot extend its deadline', async () => {
  const timers=new Map();let sequence=0;const states=[];const requests=[];
  const monitor=monitorSession({visible:()=>true,changed:state=>states.push(state),
    schedule:(callback,delay)=>{const id=++sequence;timers.set(id,{callback,delay});return id;},cancel:id=>timers.delete(id),
    request:signal=>new Promise((resolve,reject)=>requests.push({resolve,reject,signal}))});
  const flush=async()=>{await Promise.resolve();await Promise.resolve();};
  requests[0].resolve();await flush();const count=states.length;
  monitor.focus();assert.equal(requests.length,2);assert.equal(states.length,count);
  const deadline=[...timers].find(([,timer])=>timer.delay===5000);
  for(let n=0;n<10;n++) monitor.focus();
  assert.equal(requests.length,2);assert.ok(timers.has(deadline[0]));assert.equal(states.at(-1),'authenticated');
  timers.delete(deadline[0]);deadline[1].callback();
  assert.equal(states.at(-1),'unavailable');assert.ok(requests[1].signal.aborted);
  requests[1].resolve();await flush();assert.equal(states.at(-1),'unavailable');
  monitor.stop();
});
// Both an explicit denial and a transport error remove the preserved view immediately on settlement.
test('visible focus fails closed on denial or network failure', async () => {
  for(const [error,expected] of [[new session.SessionDenied(),'denied'],[new Error('offline'),'unavailable']]) {
    const timers=new Map();let sequence=0;const states=[];const requests=[];
    const monitor=monitorSession({visible:()=>true,changed:state=>states.push(state),
      schedule:(callback,delay)=>{const id=++sequence;timers.set(id,{callback,delay});return id;},cancel:id=>timers.delete(id),
      request:signal=>new Promise((resolve,reject)=>requests.push({resolve,reject,signal}))});
    const flush=async()=>{await Promise.resolve();await Promise.resolve();};
    requests[0].resolve();await flush();monitor.focus();assert.equal(states.at(-1),'authenticated');
    requests[1].reject(error);await flush();assert.equal(states.at(-1),expected);
    monitor.stop();assert.equal(timers.size,0);
  }
});

// Bundled help is independently renderable without private application data.
test('local help is bilingual and contains no remote resources', () => {
  for (const language of ['de','en']) {
    const messages=JSON.parse(readFileSync(new URL('../src/features/settings/help-topics.json',import.meta.url),'utf8'))[language];
    const html=renderToStaticMarkup(React.createElement(localHelp.LocalHelp,{language}));
    assert.ok(html.includes(`<details class="local-help" lang="${language}">`));
    assert.ok(html.includes(messages.find(t=>t.id==='window').paragraphs[0]));
    assert.ok(html.includes(messages.find(t=>t.id==='session').paragraphs[0]));
    assert.ok(!html.includes('<iframe'));assert.ok(!html.includes('https://'));
    assert.ok(!html.includes('<table'));
  }
});

// The selector's production transition function keeps the choice local, strict and reversible.
test('help language selection reaches both catalogues and ignores unsupported values', () => {
  let current='de';
  current=localHelp.selectHelpLanguage(current,'en');assert.equal(current,'en');
  current=localHelp.selectHelpLanguage(current,'unsupported');assert.equal(current,'en');
  current=localHelp.selectHelpLanguage(current,'de');assert.equal(current,'de');
  for(const language of ['de','en']) {
    const html=renderToStaticMarkup(React.createElement(localHelp.LocalHelp,{language}));
    assert.ok(html.includes(`<option value="${language}" lang="${language}" selected="">`));
    assert.ok(html.includes('>Deutsch</option>'));assert.ok(html.includes('>English</option>'));
    assert.ok(html.includes(`lang="${language}"`));
  }
});

// Server diagnostic text never reaches the user; only bounded, known conflict codes select local messages.
test('view failures distinguish source rebind, refresh and path encoding without leaking diagnostics', async () => {
  const signal=new AbortController().signal;
  const parameters={action:'filestation',profileId:'p',snapshotId:'s',nodeId:'n'};
  const pairs=[['SNAPSHOT_MISMATCH','probe.stale'],['PROFILE_REBIND_REQUIRED','probe.rebind'],['INDEX_REFRESH_REQUIRED','probe.reindex'],['PATH_ENCODING_UNSUPPORTED','probe.pathEncoding'],['FILE_STATION_UNAVAILABLE','probe.fileStationUnavailable']];
  for(const language of ['de','en']) {
    const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
    for(const [code,key] of pairs) {
      const response=JSON.stringify({contractVersion:1,error:{code,message:'/private/sensitive <script>'}});
      await assert.rejects(workbench.readView(parameters,signal,async()=>new Response(response,{status:409}),'synthetic'),error=>{
        assert.equal(workbench.readFailureMessage(error,messages),messages[key]);
        assert.ok(!JSON.stringify(error).includes('sensitive'));return true;
      });
    }
    for(const raw of ['not-json','null',JSON.stringify({contractVersion:2,error:{code:'INDEX_REFRESH_REQUIRED'}}),JSON.stringify({contractVersion:1,error:{code:'UNKNOWN',message:'secret'}}),' '.repeat(4097)+JSON.stringify({contractVersion:1,error:{code:'INDEX_REFRESH_REQUIRED'}})]) {
      await assert.rejects(workbench.readView(parameters,signal,async()=>new Response(raw,{status:409}),'synthetic'),error=>{
        assert.equal(workbench.readFailureMessage(error,messages),messages['probe.error']);return true;
      });
    }
    await assert.rejects(workbench.readView(parameters,signal,async()=>new Response(JSON.stringify({contractVersion:1,error:{code:'INDEX_REFRESH_REQUIRED'}}),{status:500}),'synthetic'),error=>{
      assert.equal(workbench.readFailureMessage(error,messages),messages['probe.error']);return true;
    });
  }
});

// The production reveal flow requires a separate click and a fresh target plus DSM session check.
test('removed-file recovery waits for explicit confirmation and revalidates before launch', async () => {
  const selection={profileId:'p',snapshotId:'s',nodeId:'n',viewId:'qualified-view'};
  const signal=new AbortController().signal;
  let reads=0, sessions=0, launches=0;
  let path='/share/parent';
  const read=async parameters=>{
    reads++;assert.deepEqual(parameters,{action:'filestation',...selection});
    return {fileStationPath:path,kind:'directory',noticeCode:'FILE_REMOVED_PARENT_AVAILABLE'};
  };
  const host={parent:{SYNO:{SDS:{Session:{SynoToken:'synthetic'},AppLaunch:(_name,args)=>{launches++;assert.equal(args.opendir,path);}}}}};
  const launch=(target,options)=>fileStation.openFileStation(target,{...options,host,request:async()=>{
    sessions++;return new Response(JSON.stringify({contractVersion:1,data:{authenticated:true}}));
  }});
  const pending=await workbench.revealSelection(selection,null,'90080',signal,read,launch);
  assert.deepEqual(pending,{selection,parentPath:path});
  assert.equal(reads,1);assert.equal(sessions,0);assert.equal(launches,0);
  assert.equal(await workbench.revealSelection(pending.selection,pending.parentPath,'90080',signal,read,launch),null);
  assert.equal(reads,2);assert.equal(sessions,1);assert.equal(launches,1);
  path='/share';
  const changed=await workbench.revealSelection(selection,pending.parentPath,'90080',signal,read,launch);
  assert.equal(changed.parentPath,path);assert.equal(launches,1);
  await assert.rejects(workbench.revealSelection(selection,path,'90080',signal,read,(target,options)=>fileStation.openFileStation(target,{...options,host,request:async()=>new Response('',{status:401})})),session.SessionDenied);
  assert.equal(launches,1);
  const aborted=new AbortController();aborted.abort();
  await assert.rejects(workbench.revealSelection(selection,path,'90080',aborted.signal,read,launch));
  assert.equal(launches,1);
});

// Invalidation during a delayed path response must prevent both navigation and late detail delivery.
test('generation invalidation suppresses delayed detail and File Station effects', async()=>{
 const controller=new AbortController();let resolveRead;let launches=0;const delivered=[];
 const pending=new Promise(resolve=>{resolveRead=resolve;});
 const selection={profileId:'p',snapshotId:'s',nodeId:'n'};
 const navigation=workbench.revealSelection(selection,null,'90080',controller.signal,()=>pending,async()=>{launches++;});
 const details=snapshotReader.settlePinnedRead(pending,()=>!controller.signal.aborted,value=>delivered.push(value),error=>delivered.push(error));
 controller.abort();resolveRead({fileStationPath:'/share/parent',kind:'directory'});
 await assert.rejects(navigation);await details;
 assert.equal(launches,0);assert.deepEqual(delivered,[]);
});

// Recovery consent cannot silently turn into a different file action after the target changes.
test('recovery rejects restored or malformed targets and exposes localized inline action', async()=>{
  const selection={profileId:'p',snapshotId:'s',nodeId:'n'};
  const signal=new AbortController().signal;
  let launches=0;
  for(const target of [{kind:'file',fileStationPath:'/share/file'}, {kind:'file',fileStationPath:'/share/file',noticeCode:'FILE_REMOVED_PARENT_AVAILABLE'}]) {
    await assert.rejects(workbench.revealSelection(selection,'/share','90080',signal,async()=>target,async()=>{launches++;}));
  }
  assert.equal(launches,0);
  for(const language of ['de','en']) {
    const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
    const html=renderToStaticMarkup(React.createElement(workbench.ParentNotice,{messages,disabled:false,confirm:()=>{launches++;}}));
    assert.ok(html.includes(messages['probe.removedParent']));
    assert.ok(html.includes(messages['probe.openParent']));
    assert.equal(launches,0);
  }
});

// Product status distinguishes unsupported DSM versions and unavailable launchers using bundled text only.
test('launcher capability failures have specific bilingual messages with no raw error rendering', () => {
  const pairs=[['BUILD_UNSUPPORTED','probe.launcherBuild'],['UNAVAILABLE','probe.launcherUnavailable'],['TARGET_INVALID','probe.fileStationUnavailable'],['LAUNCH_FAILED','probe.launcherFailed']];
  for(const language of ['de','en']) {
    const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
    for(const [code,key] of pairs) assert.equal(workbench.readFailureMessage(new fileStation.FileStationFailure(code),messages),messages[key]);
    assert.equal(workbench.readFailureMessage(new Error('FILE_STATION_UNAVAILABLE /private/path'),messages),messages['probe.error']);
    assert.ok(messages['probe.parentRequested'].includes(language==='de'?'nicht automatisch markiert':'not highlighted automatically'));
  }
});

// Clipboard writes preserve literal names exactly; permission failures never claim success.
test('path copies retain Unicode, percent and HTML-looking names without execution',async()=>{
 const names=['Grüße 😀 % # " \u0027 <script>alert(1)</script>','literal%2F%23', 'a/'.repeat(1000)+'end'];
 for(const text of names){
  let copied;
  await nodeDetails.copyPathText(text,{writeText:async value=>{copied=value;}});
  assert.equal(copied,text);
 }
 await assert.rejects(nodeDetails.copyPathText('x',null),/UNAVAILABLE/);
 await assert.rejects(nodeDetails.copyPathText('x',{writeText:async()=>{throw new Error('private permission diagnostic');}}),error=>error.message==='CLIPBOARD_DENIED');
 const messages=JSON.parse(readFileSync(new URL('../src/locales/de.json',import.meta.url),'utf8'));
 const paths={snapshotId:'s',nodeId:'n',name:{text:names[0],encoding:'utf8'},nasPath:{text:'/volume1/share/'+names[0],encoding:'utf8'},fileStationPath:'/share/'+names[0]};
 assert.equal(nodeDetails.checkedPaths(paths,'s','n'),paths);
 assert.throws(()=>nodeDetails.checkedPaths(paths,'other','n'));
 const html=renderToStaticMarkup(React.createElement(nodeDetails.NodeDetails,{paths,messages}));
 assert.ok(!html.includes('<script>'));assert.ok(html.includes('&lt;script&gt;'));
 assert.ok(html.includes(messages['paths.copyName']));assert.ok(html.includes(messages['paths.copyNAS']));assert.ok(html.includes(messages['paths.copyFileStation']));
 const escaped={...paths,name:{text:'"x\\xff"',encoding:'escaped-bytes'},nasPath:{text:'"/volume1/x\\xff"',encoding:'escaped-bytes'},fileStationPath:null};
 const marked=renderToStaticMarkup(React.createElement(nodeDetails.NodeDetails,{paths:escaped,messages}));
 assert.ok(marked.includes(messages['paths.escaped']));assert.ok(marked.includes(messages['paths.copyRepresentation']));assert.ok(!marked.includes(messages['paths.copyFileStation']));
});

// Historical revisions and captured names remain visible without repainting records as current configuration.
test('snapshot profile revisions are validated and changed configuration remains visibly marked',()=>{
 const value={...storageFixture(),snapshotId:'old',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:2,profileChanged:true,capturedProfileName:'Old <profile> name'};
 assert.equal(workbench.checkedSnapshot(value),value);
 for(const patch of [{profileChanged:false},{profileRevision:null},{currentProfileRevision:0},{profileRevision:1.5},{capturedProfileName:null},{snapshotId:null}])assert.throws(()=>workbench.checkedSnapshot({...value,...patch}));
 for(const language of ['de','en']){
  const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
  const html=renderToStaticMarkup(React.createElement(workbench.IndexTable,{snapshot:value,messages,open:()=>{},disabled:false}));
  assert.ok(html.includes(messages['snapshot.profileChanged']));assert.ok(html.includes('Old &lt;profile&gt; name'));
  assert.ok(html.includes(messages['snapshot.profileRevision']));assert.ok(html.includes('<dd>1</dd>'));assert.ok(html.includes('<dd>2</dd>'));
  const renamed={...value,currentProfileRevision:1,profileChanged:false};
  assert.equal(workbench.checkedSnapshot(renamed),renamed);
  const unchanged=renderToStaticMarkup(React.createElement(workbench.IndexTable,{snapshot:renamed,messages,open:()=>{},disabled:false}));
  assert.ok(!unchanged.includes(messages['snapshot.profileChanged']));assert.ok(unchanged.includes('Old &lt;profile&gt; name'));
 }
});

// Special objects retain unknown payload sizes; only normal files/directories receive launch actions.
test('all seven node kinds have honest sizes, link hints and action eligibility', () => {
 const kinds=['file','directory','symlink','fifo','socket','blockDevice','characterDevice'];
 const nodes=kinds.map((kind,n)=>({id:`n${n}`,snapshotId:'special',kind,originalName:`<${kind}>`,relativePath:`entry-${n}`,logicalBytes:kind==='file'?'10':null,allocatedBytes:kind==='file'?'4096':null,linkCount:kind==='directory'?9:2}));
 const value={...storageFixture(),snapshotId:'special',nodes,truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'Specials'};
 assert.equal(workbench.checkedSnapshot(value),value);
 for(const language of ['de','en']){
  const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
  const html=renderToStaticMarkup(React.createElement(workbench.IndexTable,{snapshot:value,messages,open:()=>{},disabled:false}));
  for(const node of nodes)assert.ok(html.includes(workbench.nodeType(node,messages)));
  assert.equal((html.match(/<button/g)||[]).length,2);
  assert.equal(workbench.nodeHints(nodes[0],messages).filter(h=>h===messages['node.hardlink']).length,1);
  assert.ok(!workbench.nodeHints(nodes[1],messages).includes(messages['node.hardlink']));
  assert.ok(html.includes(messages['node.pathSums']));assert.ok(html.includes(messages['node.reflink']));
  assert.ok(!html.includes('>0 B<'));assert.ok(!html.includes('<fifo>'));
  for(const name of ['#recycle','@eaDir'])assert.equal(workbench.nodeHints({...nodes[1],originalName:name},messages).length,1);
  assert.equal(workbench.nodeHints({...nodes[1],originalName:'@ordinary'},messages).length,0);
 }
 for(const kind of ['symlink','fifo','socket','blockDevice','characterDevice'])assert.throws(()=>workbench.checkedSnapshot({...value,nodes:[{...nodes[0],kind,logicalBytes:'0'}]}));
 assert.throws(()=>workbench.checkedSnapshot({...value,nodes:[{...nodes[0],linkCount:2.5}]}));
});

// Current package usage remains separate from scan data and preserves explicit unknown measurements.
test('package usage and bounded scan boundaries render independently in both languages', () => {
 const data={...storageFixture(),scanBoundaries:[{targetId:'v1',nodeId:null,relativePath:'<hidden>&area',reasonCode:'PACKAGE_STATE_EXCLUDED'}],scanBoundaryCount:'9007199254740993',scanBoundariesTruncated:true};
 assert.equal(workbench.checkedStorage(data),data);
 for(const language of ['de','en']){
  const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
  const html=renderToStaticMarkup(React.createElement(workbench.StorageNotes,{data,messages}));
  for(const key of ['storage.title','storage.separate','boundary.truncated','boundary.package'])assert.ok(html.includes(messages[key]));
  assert.ok(html.includes('9007199254740993'));assert.ok(html.includes('&lt;hidden&gt;&amp;area'));assert.ok(!html.includes('<hidden>'));
  const unavailable={...storageFixture(),packageUsage:{...data.packageUsage,state:'unavailable',logicalBytes:null,allocatedBytes:null}};
  assert.equal(workbench.checkedStorage(unavailable),unavailable);
  const unknown=renderToStaticMarkup(React.createElement(workbench.StorageNotes,{data:unavailable,messages}));
  assert.ok(unknown.includes(messages['probe.unknown']));assert.ok(unknown.includes(messages['storage.unavailable']));assert.ok(!unknown.includes('0 B'));
 }
 for(const patch of [{scanBoundaryCount:'00'},{scanBoundaryCount:'0'},{scanBoundariesTruncated:false},{scanBoundaries:Array(33).fill(data.scanBoundaries[0])},{scanBoundaries:[{...data.scanBoundaries[0],reasonCode:'ARBITRARY'}]},{scanBoundaries:[{...data.scanBoundaries[0],relativePath:'x'.repeat(8193)}]},{packageUsage:{...data.packageUsage,logicalBytes:42}},{packageUsage:{...data.packageUsage,state:'unavailable'}},{packageUsage:{...data.packageUsage,method:'invented'}},{packageUsage:{...data.packageUsage,measuredAt:'invalid'}}])assert.throws(()=>workbench.checkedStorage({...data,...patch}));
 const absent={...storageFixture(),snapshotId:null,state:null,startedAt:null,endedAt:null,nodes:[],truncated:false,profileRevision:null,currentProfileRevision:1,profileChanged:false,capturedProfileName:null};
 assert.equal(workbench.checkedSnapshot(absent),null);assert.ok(workbench.checkedStorage(absent).packageUsage);
});

// The actual detail panel explains shared references without claiming exclusive or reclaimable bytes.
test('node detail hints retain hardlink and special-object distinctions', () => {
 const paths={snapshotId:'s',nodeId:'n',name:{text:'file',encoding:'utf8'},nasPath:{text:'/volume1/share/file',encoding:'utf8'},fileStationPath:null};
 for(const language of ['de','en']){
  const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
  const render=kind=>renderToStaticMarkup(React.createElement(nodeDetails.NodeDetails,{paths,node:{snapshotId:'s',id:'n',kind,linkCount:2},messages}));
  assert.ok(render('file').includes(messages['node.hardlink']));assert.ok(render('file').includes(messages['node.reflink']));
  assert.ok(!render('directory').includes(messages['node.hardlink']));
  assert.ok(render('socket').includes(messages['node.special']));assert.ok(render('symlink').includes(messages['node.symlink']));
 }
});

// A terminal partial result has its own generation, revision and time, without rewriting the active snapshot.
test('partial scan boundaries stay bound to their own job and separate from the active complete list', () => {
 const report={jobId:'partial-job',snapshotId:'partial-result',state:'partial',profileRevision:2,endedAt:'2026-10-10T01:02:03.123456789Z',boundaries:[{targetId:'volume',nodeId:null,relativePath:'snapshot-tree',reasonCode:'SUBVOLUME_CLASSIFICATION_UNAVAILABLE'}],count:'1',truncated:false};
 const expected={jobId:'partial-job',snapshotId:'partial-result',state:'partial',profileRevision:2};
 assert.equal(workbench.checkedScanBoundaryReport(report,expected),report);
 for(const patch of [{jobId:'foreign'},{snapshotId:'old-complete'},{state:'complete'},{profileRevision:1},{endedAt:'invalid'},{count:'0'},{truncated:true},{boundaries:[] }])assert.throws(()=>workbench.checkedScanBoundaryReport({...report,...patch},expected));
 const old={...storageFixture(),snapshotId:'old-complete',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:2,profileChanged:true,capturedProfileName:'Old configuration'};
 for(const language of ['de','en']){
  const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
  const html=renderToStaticMarkup(React.createElement(workbench.ScanBoundaryNotes,{report,messages}));
  for(const key of ['boundary.latestTitle','boundary.partialResult','boundary.separate','boundary.unclassified'])assert.ok(html.includes(messages[key]));
  assert.ok(html.includes('partial-job'));assert.ok(html.includes(report.endedAt));
  assert.equal(workbench.checkedSnapshot(old),old);assert.equal(old.snapshotId,'old-complete');
 }
});

// Every versioned coverage reason has a safe bilingual explanation; intentional exclusions remain distinct.
test('coverage reasons are allowlisted, actionable and distinguish gaps from intentional exclusions', () => {
 const reasons=['PACKAGE_STATE_EXCLUDED','SNAPSHOT_TREE_EXCLUDED','PROFILE_EXCLUDED','OUTSIDE_SCOPE_MOUNT','SUBVOLUME_CLASSIFICATION_UNAVAILABLE','SOURCE_UNREADABLE','SOURCE_MISSING','SOURCE_TRANSIENT','SOURCE_CHANGED','SOURCE_IO_ERROR','SOURCE_LOCKED','SOURCE_NOT_MOUNTED','SOURCE_UNVERIFIED','DEPTH_LIMIT','SCOPE_CHANGED','SCOPE_VALIDATION_UNAVAILABLE'];
 const intentional=new Set(reasons.slice(0,3));
 for(const reasonCode of reasons){
  const report={jobId:'job',snapshotId:'result',state:intentional.has(reasonCode)?'complete':'partial',profileRevision:1,endedAt:'2026-10-10T01:02:03Z',boundaries:[{targetId:'<target>&identifier',nodeId:null,relativePath:'<private>&relative',reasonCode}],count:'1',truncated:false};
  const expected={jobId:'job',snapshotId:'result',state:report.state,profileRevision:1};
  assert.equal(workbench.checkedScanBoundaryReport(report,expected),report);
  assert.equal(workbench.intentionalBoundary(reasonCode),intentional.has(reasonCode));
  if(!intentional.has(reasonCode))assert.throws(()=>workbench.checkedScanBoundaryReport({...report,state:'complete'},{...expected,state:'complete'}));
  for(const language of ['de','en']){
   const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
   const html=renderToStaticMarkup(React.createElement(workbench.ScanBoundaryNotes,{report,messages}));
   assert.ok(html.includes(messages[intentional.has(reasonCode)?'boundary.intentional':'boundary.gap']));
   assert.ok(html.includes(messages['boundary.records']));assert.ok(html.includes(messages['boundary.liveSources']));
   assert.ok(html.includes('&lt;target&gt;&amp;identifier'));assert.ok(html.includes('&lt;private&gt;&amp;relative'));
   assert.ok(!html.includes('<target>'));assert.ok(!html.includes('undefined'));assert.ok(!html.includes('>0 B<'));
  }
 }
 const base={...storageFixture(),scanBoundaries:[{targetId:'v',nodeId:null,relativePath:'.',reasonCode:'SOURCE_MISSING'}],scanBoundaryCount:'1'};
 assert.equal(workbench.checkedStorage(base),base);
 for(const reasonCode of ['toString','__proto__','constructor','SOURCE_unknown'])assert.throws(()=>workbench.checkedStorage({...base,scanBoundaries:[{...base.scanBoundaries[0],reasonCode}]}));
 assert.throws(()=>workbench.checkedStorage({...base,scanBoundaries:Array(33).fill(base.scanBoundaries[0]),scanBoundaryCount:'33'}));
 assert.throws(()=>workbench.checkedStorage({...base,scanBoundaries:[{...base.scanBoundaries[0],relativePath:'x'.repeat(8193)}]}));
});

// Rendering keeps real scan actions distinct from the read-only reload and preserves uncertain full retries.
test('scan controls expose three honest modes and separate stored snapshot reload', () => {
 for(const language of ['de','en']){
  const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
  const render=props=>renderToStaticMarkup(React.createElement(workbench.ScanControls,{messages,busy:false,hasSnapshot:false,pending:null,start:()=>{},reload:()=>{},...props}));
  const first=render({});for(const key of ['scan.full','scan.rebuild','scan.reconcile','probe.refresh','scan.cost','scan.reloadOnly'])assert.ok(first.includes(messages[key]));
  assert.equal((first.match(/<button/g)||[]).length,4);
  const existing=render({hasSnapshot:true});assert.equal((existing.match(/<button/g)||[]).length,3);assert.ok(!existing.includes(`>${messages['scan.full']}</button>`));
  const pending=render({hasSnapshot:true,pending:{key:'same',mode:'full'}});assert.ok(pending.includes(`>${messages['scan.full']}</button>`));assert.equal((pending.match(/disabled=""/g)||[]).length,2);assert.ok(pending.includes(messages['scan.retry']));
  for(const mode of ['full','rebuild','reconcile'])assert.equal(workbench.scanModeLabel(mode,messages),messages[`scan.${mode}`]);
  assert.throws(()=>workbench.scanModeLabel('incremental',messages),/INVALID_SCAN_MODE/);
 }
});

// Deduplication shows the persisted job mode instead of inferring it from a client action.
test('job status renders actual returned scan mode for normal and deduplicated jobs', () => {
 const messages=JSON.parse(readFileSync(new URL('../src/locales/de.json',import.meta.url),'utf8'));
 for(const scanMode of ['full','rebuild','reconcile'])for(const deduplicated of [false,true]){
  const job={scanMode,state:'scanning',entriesVisited:'7',bytesObserved:'123'};
  const html=renderToStaticMarkup(React.createElement(workbench.ScanJobStatus,{job,deduplicated,messages,language:'de'}));
  assert.ok(html.includes(messages[`scan.${scanMode}`]));assert.equal(html.includes(messages['scan.deduplicated']),deduplicated);
 }
});

// Invoke the compiled controls' real callbacks to catch a labelled scan button wired to a read-only reload.
test('scan buttons dispatch their exact modes and reload dispatches only a read', () => {
 const messages=JSON.parse(readFileSync(new URL('../src/locales/en.json',import.meta.url),'utf8'));
 const actions=[];
 const tree=workbench.ScanControls({messages,busy:false,hasSnapshot:false,pending:null,start:mode=>actions.push(mode),reload:()=>actions.push('read-only')});
 const buttons=tree.props.children[0].props.children.filter(React.isValidElement);
 for(const button of buttons)button.props.onClick();
 assert.deepEqual(actions,['full','rebuild','reconcile','read-only']);
 assert.equal(workbench.readFailureMessage(new Error('JOB_CONFLICT'),messages),messages['scan.jobConflict']);
 assert.equal(workbench.readFailureMessage(new Error('IDEMPOTENCY_CONFLICT'),messages),messages['scan.replayConflict']);
});

// Partial quality and header times remain explicit and cannot silently become a default label.
test('partial snapshot keeps its own interval and persistent warning in both languages',()=>{
 const value={...storageFixture(),state:'partial',snapshotId:'partial',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'Partial source'};
 const checked=workbench.checkedSnapshot(value);
 assert.equal(checked.state,'partial');
 for(const language of ['de','en']){
  const locale=JSON.parse(readFileSync(join(web,`src/locales/${language}.json`),'utf8'));
  const html=renderToStaticMarkup(React.createElement(workbench.IndexTable,{snapshot:checked,messages:locale,open:()=>{},disabled:true}));
  assert.ok(html.includes(locale['snapshot.partialWarning']));
  assert.ok(html.includes(value.startedAt));assert.ok(html.includes(value.endedAt));
 }
 for(const patch of [{state:null},{state:'complete-ish'},{endedAt:null},{startedAt:'invalid'},{endedAt:'2026-10-09T00:00:00Z'}])assert.throws(()=>workbench.checkedSnapshot({...value,...patch}));
});

// An older result can be displayed as a job without inventing new snapshot provenance.
test('partial selection requires explicit capture provenance and dispatches only on user intent',()=>{
 const messages=JSON.parse(readFileSync(join(web,'src/locales/de.json'),'utf8'));
 let selected=0;
 const job={job:{id:'partial-job',resultSnapshotId:'partial'},snapshotState:'partial',snapshotEndedAt:'2026-10-10T00:01:00Z'};
 const valid=workbench.PartialControls({job,snapshot:null,messages,busy:false,select:()=>selected++,back:()=>{}});
 assert.equal(selected,0);
 const button=valid.props.children.find(child=>child?.type==='button');assert.ok(button);button.props.onClick();assert.equal(selected,1);
 for(const snapshotEndedAt of [null,undefined]){
  const html=renderToStaticMarkup(React.createElement(workbench.PartialControls,{job:{...job,snapshotEndedAt},snapshot:null,messages,busy:false,select:()=>selected++,back:()=>{}}));
  assert.ok(html.includes(messages['snapshot.partialUnqualified']));assert.ok(!html.includes('<button'));assert.equal(selected,1);
 }
});

// Lease and generation identity must survive transient authorization checks as one set of IDs.
test('view pin survives reauthentication but cannot cross profile or denied session',()=>{
 const pin={profileId:'p',viewId:'lease',snapshotId:'old',nodeId:'node'};
 const base={session:'authenticated',selected:'p',pin};
 assert.deepEqual(gateState(base,{session:'checking'}).pin,pin);
 assert.equal(gateState(base,{selected:'other'}).pin,undefined);
 assert.equal(gateState(base,{session:'denied'}).pin,undefined);
 assert.deepEqual(gateState(base,{pin:{...pin,profileId:'other'}}),base);
});

// Even a correctly shaped late response cannot replace a pinned generation.
test('reader rejects late mismatched snapshots and exposes newer pointer independently',()=>{
 const view={...storageFixture(),snapshotId:'old',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'Name'};
 const response={view,lease:{id:'lease',snapshotId:'old',expiresAt:'2026-10-10T00:02:00Z'},newCompleteSnapshotId:'new',selection:{nodeId:null,fallback:'none'}};
 const pin={profileId:'p',viewId:'lease',snapshotId:'old',nodeId:null};
 assert.equal(workbench.checkedReader(response,pin).snapshotId,'old');
 assert.throws(()=>workbench.checkedReader({...response,view:{...view,snapshotId:'new'},lease:{...response.lease,snapshotId:'new'}},pin));
 assert.throws(()=>workbench.checkedReader({...response,lease:{...response.lease,id:'foreign'}},pin));
});

// A delayed renewal from the released view must not expire or replace its successor.
test('stale renewal success and rejection are ignored after adoption',async()=>{
 for(const rejectOld of [false,true]){
 let finish;let pin='old';const events=[];
 const pending=new Promise((resolve,reject)=>{finish=()=>rejectOld?reject(new Error('VIEW_EXPIRED')):resolve('old-data');});
 const done=snapshotReader.settlePinnedRead(pending,()=>pin==='old',value=>events.push(value),error=>events.push(error.message));
 pin='new';finish();await done;assert.deepEqual(events,[]);
 }
 const events=[];await snapshotReader.settlePinnedRead(Promise.reject(new Error('VIEW_EXPIRED')),()=>true,()=>{},error=>events.push(error.message));assert.deepEqual(events,['VIEW_EXPIRED']);
});

// Every nonterminal phase keeps background polling and pinned selection alive.
test('background job phases remain distinct from terminal view transitions',()=>{
 for(const state of ['queued','preflight','scanning','paused','aggregating','validating','publishing'])assert.equal(workbench.jobPolling(state),true);
 for(const state of ['completed','failed','cancelled',undefined])assert.equal(workbench.jobPolling(state),false);
});

// Selection changes and a newer background pointer cannot alter an uncertain switch retry.
test('uncertain reader intent retains original lease snapshot and selection',()=>{
 const original={contractVersion:1,operation:'switch',profileId:'p',viewId:'old',newViewId:'next',snapshotId:'generation2',nodeId:'first'};
 const pending=snapshotReader.retainReaderIntent(null,original);
 original.nodeId='mutated';
 const retry=snapshotReader.retainReaderIntent(pending,{...original,newViewId:'different',snapshotId:'generation3',nodeId:'second'});
 assert.deepEqual(retry,{contractVersion:1,operation:'switch',profileId:'p',viewId:'old',newViewId:'next',snapshotId:'generation2',nodeId:'first'});
 assert.equal(retry,pending);
});

// A UI join preserves the persisted first trigger, and CLI alone never proves a DSM schedule.
test('job status exposes original trigger in both languages without rewriting deduplicated origins', () => {
 for (const language of ['de','en']) {
  const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
  for (const trigger of ['ui','cli','scheduler']) for (const deduplicated of [false,true]) {
   const job={trigger,scanMode:'full',state:'queued',entriesVisited:'0',bytesObserved:'0'};
   const html=renderToStaticMarkup(React.createElement(workbench.ScanJobStatus,{job,deduplicated,messages,language}));
   assert.ok(html.includes(messages['job.trigger']));
   assert.ok(html.includes(messages[`job.trigger.${trigger}`]));
   for (const other of ['ui','cli','scheduler'].filter(value=>value!==trigger)) assert.ok(!html.includes(messages[`job.trigger.${other}`]));
   assert.equal(html.includes(messages['scan.deduplicated']),deduplicated);
  }
  for (const unknown of [undefined,'__proto__','<private-path>']) assert.equal(workbench.jobTriggerLabel(unknown,messages),messages['probe.unknown']);
 }
});

// Control availability follows actual phase while an unacknowledged pause remains visibly pending.
test('job controls distinguish requested pause, reached pause, cancel and terminal states', () => {
 for(const language of ['de','en']){
  const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
  const calls=[];
  const render=(state,control=null,pending=null)=>workbench.JobControls({job:{state},control,pending,sending:false,messages,send:action=>calls.push(action)});
  const requested=renderToStaticMarkup(render('scanning',{requested:'pause',version:1}));
  assert.ok(requested.includes(messages['control.pausePending']));assert.ok(requested.includes(messages['control.resume']));
  const paused=renderToStaticMarkup(render('paused',{requested:'pause',version:1}));assert.ok(!paused.includes(messages['control.pausePending']));
  const waiting=renderToStaticMarkup(render('queued'));assert.ok(!waiting.includes(`>${messages['control.pause']}</button>`));assert.ok(waiting.includes(messages['control.cancel']));
  const running=render('scanning');const buttons=React.Children.toArray(running.props.children).filter(child=>child.type==='button');for(const button of buttons)button.props.onClick();
  assert.deepEqual(calls,['pause','cancel']);
  for(const terminal of ['completed','cancelled','failed','interrupted'])assert.equal(render(terminal),null);
  const uncertain=renderToStaticMarkup(render('scanning',null,{jobId:'j',action:'pause',key:'k'}));assert.ok(uncertain.includes(messages['control.retry']));
  assert.ok(!messages['probe.complete'].includes('has been loaded'));assert.ok(!messages['probe.complete'].includes('wurde geladen'));
 }
});

// Guarded requests carry only the immutable job/action and preserve the key on explicit retry.
test('job control adapter validates receipts and propagates session and definite conflicts', async () => {
 const intent={jobId:'job',action:'pause',key:'same-key'};const signal=new AbortController().signal;const seen=[];
 const request=async(url,options)=>{seen.push({url,options});return new Response(JSON.stringify({contractVersion:1,data:{jobId:'job',action:'pause',version:1}}),{status:200});};
 for(let i=0;i<2;i++)assert.deepEqual(await jobControl.requestJobControl(intent,signal,request,'transient'),{requested:'pause',version:1});
 // Queue-only cancellation preserves its guarded action in the receipt and request.
 const queued={jobId:'waiting',action:'cancel-queued',key:'queue-key'};
 assert.deepEqual(await jobControl.requestJobControl(queued,signal,async(url,options)=>{assert.equal(JSON.parse(options.body).action,'cancel-queued');return new Response(JSON.stringify({contractVersion:1,data:{jobId:'waiting',action:'cancel-queued',version:1}}));},'transient'),{requested:'cancel',version:1});
 assert.equal(seen[0].url,'./api.cgi?action=job-control');assert.equal(seen[0].options.headers['Idempotency-Key'],seen[1].options.headers['Idempotency-Key']);
 assert.deepEqual(JSON.parse(seen[0].options.body),{contractVersion:1,jobId:'job',action:'pause'});assert.equal(seen[0].options.cache,'no-store');
 await assert.rejects(jobControl.requestJobControl(intent,signal,async()=>new Response('',{status:403}),'transient'),/SESSION_REQUIRED/);
 await assert.rejects(jobControl.requestJobControl(intent,signal,async()=>new Response(JSON.stringify({contractVersion:1,error:{code:'JOB_CONFLICT'}}),{status:409}),'transient'),/JOB_CONFLICT/);
 await assert.rejects(jobControl.requestJobControl(intent,signal,async()=>new Response('x'.repeat(4097)),'transient'),/SERVICE_RESPONSE_INVALID/);
 await assert.rejects(jobControl.requestJobControl(intent,signal,async()=>new Response(JSON.stringify({contractVersion:1,data:{jobId:'foreign',action:'pause',version:1}})),'transient'),/SERVICE_RESPONSE_INVALID/);
 assert.throws(()=>jobControl.checkedControl({requested:'__proto__',version:1}),/INVALID_CONTROL_STATUS/);
});

// Exercise the actual Workbench -> keyed ProfileView -> StorageSettings wiring with hook lifetimes.
// This local lifecycle harness verifies callback/abort behaviour, not native browser acceptance.
test('keyed profile remount retains an uncertain global retention request and permits exact retry', async()=>{
 const original={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window};
 const harness=()=>({states:[],refs:[],effects:[],render(fn,props,runEffects=false){let s=0,r=0,e=0;const scheduled=[];
  React.useState=initial=>{const i=s++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],value=>{this.states[i]=typeof value==='function'?value(this.states[i]):value;}];};
  React.useRef=initial=>{const i=r++;return this.refs[i]??=( {current:initial});};
  React.useEffect=(effect,deps)=>{const i=e++;if(runEffects&&(!this.effects[i]||deps.some((x,j)=>x!==this.effects[i].deps[j]))){scheduled.push(()=>{this.effects[i]?.cleanup?.();this.effects[i]={deps,cleanup:effect()};});}};
  const tree=fn(props);for(const effect of scheduled)effect();return tree;
 },unmount(){for(const effect of this.effects)effect?.cleanup?.();}});
 const find=(tree,predicate)=>{if(Array.isArray(tree)){for(const item of tree){const hit=find(item,predicate);if(hit)return hit;}}else if(React.isValidElement(tree)){if(predicate(tree))return tree;return find(tree.props.children,predicate);}return null;};
 const tick=()=>new Promise(resolve=>setImmediate(resolve));
 const posts=[];let oldSignal;
 const fixture={completeCount:2,dataPath:'/package',indexBytes:'1',temporaryBytes:'0',journalBytes:'0',freeBytes:'100',reserveBytes:'67108864',temporaryRequiredBytes:null,measurementState:'complete',measuredAt:'2026-10-10T01:00:00Z',retainedSnapshots:[],retainedTruncated:false,pendingCleanup:0,pendingQualification:0,unsupportedGenerations:0};
 const reply=data=>({ok:true,status:200,text:async()=>JSON.stringify({contractVersion:1,data})});
 const wb=harness(), first=harness(),second=harness();
 try{
  globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'synthetic-test-token'}}}}};
  globalThis.fetch=async(url,options)=>{if(options.method!=='POST')return reply(fixture);posts.push({body:options.body,key:options.headers['Idempotency-Key']});if(posts.length===1){oldSignal=options.signal;return new Promise((_,reject)=>options.signal.addEventListener('abort',()=>reject(new Error('aborted')),{once:true}));}return reply({completeCount:7,cleanupScheduled:true});};
  const props={language:'en',selected:'one',onSelect:()=>{},denied:()=>assert.fail('unexpected session denial')};
  wb.render(workbench.Workbench,props);wb.states[0]={dsmBuild:'90080',profiles:['one','two'].map(id=>({profile:{id,name:id},latestJobId:null}))};
  const profile=selected=>find(wb.render(workbench.Workbench,{...props,selected}),x=>x.type?.name==='ProfileView');
  const storage=element=>{const pv=harness();pv.render(element.type,element.props);pv.states[9]=false;return find(pv.render(element.type,element.props),x=>x.type?.name==='StorageSettings');};
  const p1=profile('one'),s1=storage(p1);assert.ok(s1);first.render(s1.type,s1.props,true);await tick();
  let tree=first.render(s1.type,s1.props,true);find(tree,x=>x.type==='select').props.onChange({target:{value:'7'}});
  tree=first.render(s1.type,s1.props,true);find(tree,x=>x.type==='button'&&x.props.children==='Save retention').props.onClick();await tick();
  assert.equal(posts.length,1);assert.equal(JSON.parse(posts[0].body).completeCount,7);
  first.unmount();const p2=profile('two'),s2=storage(p2);assert.notEqual(p1.key,p2.key);assert.equal(s1.props.pendingIntent,s2.props.pendingIntent);
  second.render(s2.type,s2.props,true);await tick();assert.equal(oldSignal.aborted,true);
  tree=second.render(s2.type,s2.props,true);let retry=find(tree,x=>x.type==='button'&&x.props.children==='Retry saving');assert.ok(retry);assert.equal(retry.props.disabled,false);assert.equal(find(tree,x=>x.type==='select').props.value,7);
  retry.props.onClick();await tick();assert.deepEqual(posts[1],posts[0]);assert.equal(s2.props.pendingIntent.current,null);
 }finally{first.unmount();second.unmount();React.useState=original.useState;React.useRef=original.useRef;React.useEffect=original.useEffect;globalThis.fetch=original.fetch;if(original.window===undefined)delete globalThis.window;else globalThis.window=original.window;}
});

// Service interruption offers a new scan without exposing live pause/resume controls.
test('interrupted jobs explain retained work and do not offer resume', () => {
 for(const language of ['de','en']) {
  const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
  const job={scanMode:'full',state:'interrupted',trigger:'ui',entriesVisited:'7',bytesObserved:'123'};
  const html=renderToStaticMarkup(React.createElement(workbench.ScanJobStatus,{job,deduplicated:false,messages,language}));
  assert.ok(html.includes(messages['scan.interruptedHelp']));
  assert.equal(workbench.JobControls({job,control:null,pending:null,sending:false,messages,send:()=>assert.fail('resume')}),null);
 }
});

// Selector changes invalidate old responses without retaining a growing cache of navigation results.
test('query session keys cover profile, generation and effective selectors',()=>{
 const base={profileId:'p',viewId:'v',snapshotId:'s',rootId:'n',metric:'logical',effectiveFilters:{categories:['image'],text:'ä'},sort:'name',direction:'asc',page:'',limit:50,depth:1};
 const key=querySession.queryIdentity(base);
 assert.equal(key,querySession.queryIdentity({...base,effectiveFilters:{text:'ä',categories:['image']}}));
 const session=new querySession.QuerySession();const first=session.begin(base);
 for(const [field,value]of Object.entries({profileId:'p2',viewId:'v2',snapshotId:'s2',rootId:'n2',metric:'allocated',effectiveFilters:{text:'other'},sort:'size',direction:'desc',page:'next',limit:25,depth:2})){
  assert.notEqual(key,querySession.queryIdentity({...base,[field]:value}));
  const ticket=session.begin({...base,[field]:value});assert.ok(session.accepts(ticket));assert.ok(!session.accepts(first));
 }
 for(let i=0;i<500;i++){const ticket=session.begin({...base,rootId:`n${i}`});assert.ok(session.accepts(ticket));}
 session.invalidate();assert.ok(!session.accepts(first));
 assert.throws(()=>querySession.queryIdentity({...base,effectiveFilters:{text:'x'.repeat(20000)}}));
});
// The actual metadata adapter binds all reader IDs, bounds bytes and rejects mixed or denied responses.
test('leased metadata request is bounded and generation-qualified',async()=>{
 const input={contractVersion:1,profileId:'p',viewId:'v',snapshotId:'s',nodeId:'n'};
 const value={snapshotId:'s',nodeId:'n',name:{text:'ä%2F',encoding:'utf8'},nasPath:{text:'/recorded/ä%2F',encoding:'utf8'},fileStationPath:null};
 const signal=new AbortController().signal;
 let calls=0;const request=async(url,options)=>{calls++;assert.equal(url,'./api.cgi?action=leased-paths');assert.equal(options.method,'POST');assert.deepEqual(JSON.parse(options.body),input);return new Response(JSON.stringify({contractVersion:1,data:value}));};
 assert.deepEqual(await workbench.readLeasedPaths(input,signal,request,'token'),value);
 await assert.rejects(workbench.readLeasedPaths(input,signal,request,null),session.SessionDenied);assert.equal(calls,1);
 for(const status of [401,403])await assert.rejects(workbench.readLeasedPaths(input,signal,async()=>new Response('',{status}),'token'),session.SessionDenied);
 for(const data of [{...value,snapshotId:'other'},{...value,nodeId:'other'},{...value,fileStationPath:'/guessed'}])await assert.rejects(workbench.readLeasedPaths(input,signal,async()=>new Response(JSON.stringify({contractVersion:1,data})),'token'));
 await assert.rejects(workbench.readLeasedPaths(input,signal,async()=>new Response('x'.repeat(65537)),'token'));
});
// Historical metadata stays available while current filesystem handoff remains separately disabled.
test('historical table separates metadata details from current File Station actions',()=>{
 const messages=JSON.parse(readFileSync(new URL('../src/locales/de.json',import.meta.url),'utf8'));
 const snapshot={nodes:[{id:'n',snapshotId:'old',originalName:'old',relativePath:'.',kind:'directory',logicalBytes:null,allocatedBytes:null,linkCount:null}],truncated:false};
 const html=renderToStaticMarkup(React.createElement(workbench.IndexTable,{snapshot,messages,open:()=>{},details:()=>{},disabled:true,detailsDisabled:false}));
 assert.ok(html.includes(`<button>${messages['paths.show']}</button>`));assert.ok(html.includes(`<button disabled="">${messages['probe.open']}</button>`));
});

// Captured path details do not imply that a current File Station mapping was checked.
test('recorded-only detail panel makes no current availability claim',()=>{
 const messages=JSON.parse(readFileSync(new URL('../src/locales/en.json',import.meta.url),'utf8'));
 const paths={snapshotId:'s',nodeId:'n',name:{text:'old',encoding:'utf8'},nasPath:{text:'/captured/old',encoding:'utf8'},fileStationPath:null};
 const html=renderToStaticMarkup(React.createElement(nodeDetails.NodeDetails,{paths,messages,recordedOnly:true}));
 assert.ok(html.includes(messages['paths.snapshotNotice']));assert.ok(!html.includes(messages['probe.fileStationUnavailable']));assert.ok(!html.includes(messages['paths.currentNotice']));
});

// Historical guidance retains recorded details while qualifying current File Station actions separately.
test('historical action guidance preserves recorded path availability in both languages',()=>{
 for(const [language,available]of [['de','Auswahl und erfassten Pfaddetails bleiben nutzbar'],['en','selection and recorded path details remain usable']]){
  const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
  assert.ok(messages['view.historicalActions'].includes(available));
  assert.ok(messages['view.historicalActions'].includes(language==='de'?'File-Station-Aktionen':'File Station actions'));
 }
});

// The real gate publishes a restored cross-profile pin and semantic view in one reducer event.
test('authenticated gate atomically accepts restored profile, lease and full view',()=>{
 const view={profileId:'restored',snapshotId:'snapshot',rootId:'root',selectedNodeId:'node',map:'radial',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'size',direction:'desc'};
 const pin={profileId:'restored',snapshotId:'snapshot',viewId:'lease',nodeId:'node'};
 let state=gateState({session:'authenticated',selected:'old'},{analysis:{profileId:'restored',pin,view}});
 assert.equal(state.selected,'restored');assert.deepEqual(state.pin,pin);assert.deepEqual(state.view,view);
 state=gateState(state,{session:'checking'});assert.deepEqual(state.view,view);
 state=gateState(state,{session:'denied'});assert.equal(state.view,undefined);assert.equal(state.pin,undefined);
});

// Hook-driven Workbench bootstrap observes actual request order and forbids pre-restore fallback writes.
for(const expired of [false,true])test(`Workbench restores preferences before fallback without autosave (expired remembered lease: ${expired})`,async()=>{
 const original={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window};
 const states=[],refs=[],effects=[];let mounted=true;const selected=[],published=[],calls=[];
 const render=()=>{let si=0,ri=0,ei=0;const scheduled=[];React.useState=initial=>{const i=si++;if(!(i in states))states[i]=typeof initial==='function'?initial():initial;return[states[i],value=>{if(mounted)states[i]=typeof value==='function'?value(states[i]):value;}];};React.useRef=initial=>refs[ri++]??={current:initial};React.useEffect=(fn,deps)=>{const i=ei++;if(!effects[i])scheduled.push(()=>effects[i]={cleanup:fn(),deps});};const tree=workbench.Workbench({language:'en',selected:'',...(expired?{pin:{profileId:'saved',snapshotId:'s',viewId:'expired',nodeId:null},rememberedView:{profileId:'saved',snapshotId:'s',rootId:'root',selectedNodeId:null,map:'radial',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'size',direction:'desc'}}:{}),onSelect:id=>selected.push(id),onAnalysis:(...args)=>published.push(args),denied:()=>assert.fail('denied')});scheduled.forEach(fn=>fn());return tree;};
 const tick=()=>new Promise(resolve=>setImmediate(resolve));
 try{
  globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};
  const view={profileId:'saved',snapshotId:'s',rootId:'root',selectedNodeId:null,map:'radial',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'size',direction:'desc'};
  globalThis.fetch=async(url,options)=>{const action=new URL(url,'https://local.invalid').searchParams.get('action');calls.push({action,method:options.method});let data;
   if(action==='preferences')data={version:2,display:{units:'decimal',colorMode:'folder',detail:'auto',labels:true,percentages:true,hierarchyBorders:'normal',reducedMotion:'auto'},dsmUserId:'admin',revision:3,lastView:view,language:'auto',theme:'auto',preferredProfileId:'saved',map:'radial',locale:null,timeZone:null};
   else if(action==='profiles')data={dsmBuild:'90080',profiles:[{profile:{id:'first',name:'First'}},{profile:{id:'saved',name:'Saved'}}]};
   else if(action==='view')return new Response(JSON.stringify({contractVersion:1,error:{code:'VIEW_EXPIRED'}}),{status:409});
   else if(action==='view-restore'){assert.deepEqual(selected,[]);const id=JSON.parse(options.body).viewId;data={preferenceRevision:3,profileId:'saved',resolvedView:view,reasons:[],reader:{view:{...storageFixture(),snapshotId:'s',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'Saved'},lease:{id,snapshotId:'s',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}},navigation:{snapshotId:'s',rootId:'root',parentId:null,selectedNodeId:null,reasons:[],breadcrumb:[{id:'root',name:'Root',kind:'nas'}]}};}
   else assert.fail(`unexpected ${action}`);
   return new Response(JSON.stringify({contractVersion:1,data}));
  };
  render();await tick();await tick();render();assert.deepEqual(calls.map(x=>x.action),(expired?['regional-context','preferences','profiles','view','view-restore']:['regional-context','preferences','profiles','view-restore']));assert.equal(published.length,1);assert.equal(published[0][0],'saved');assert.equal(published[0][2].map,'radial');assert.deepEqual(selected,[]);assert.ok(!calls.some(x=>x.action==='preferences'&&x.method==='POST'));
 }finally{mounted=false;effects.forEach(e=>e?.cleanup?.());Object.assign(React,{useState:original.useState,useRef:original.useRef,useEffect:original.useEffect});globalThis.fetch=original.fetch;globalThis.window=original.window;}
});

// Initial loading and a terminal latest-job poll can open the same idempotent server lease.
for(const reverse of [false,true])test(`overlapping ProfileView loads retain coalesced lease (reverse: ${reverse})`,async()=>{
 const original={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window};
 const make=()=>({states:[],refs:[],cleanups:[],render(fn,props,effects=false){let si=0,ri=0;const scheduled=[];React.useState=initial=>{const i=si++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=initial=>this.refs[ri++]??={current:initial};React.useEffect=fn=>{if(effects)scheduled.push(fn);};const tree=fn(props);for(const fn of scheduled)this.cleanups.push(fn());return tree;}});
 const find=tree=>{if(Array.isArray(tree))return tree.map(find).find(Boolean);if(React.isValidElement(tree)){if(tree.type?.name==='ProfileView')return tree;return find(tree.props.children);}return null;};
 const tick=()=>new Promise(r=>setImmediate(r));const opens=[],releases=[];const wb=make(),pv=make();let navigated=false;
 try{
  globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};
  globalThis.fetch=async(url,options)=>{const action=new URL(url,'https://local.invalid').searchParams.get('action');const body=options.body?JSON.parse(options.body):{};
   if(action==='view'&&body.operation==='open')return new Promise(resolve=>opens.push({body,resolve}));
   if(action==='view'&&body.operation==='release'){releases.push(body.viewId);return new Response(JSON.stringify({contractVersion:1,data:{}}));}
   if(action==='job')return new Response(JSON.stringify({contractVersion:1,data:{job:{id:'j',profileId:'p',state:'completed',scanMode:'full',resultSnapshotId:null},snapshotState:'complete',control:null}}));
   if(action==='view-navigation'){assert.equal(releases.length,0,'shared open was released before navigation');navigated=true;return new Response(JSON.stringify({contractVersion:1,data:{snapshotId:'s',rootId:'nas',parentId:null,selectedNodeId:null,reasons:[],breadcrumb:[{id:'nas',name:'NAS',kind:'nas'}]}}));}
   throw Error(`unexpected ${action}`);
  };
  const props={language:'en',selected:'p',onSelect:()=>{},denied:()=>assert.fail('denied')};wb.render(workbench.Workbench,props);wb.states[0]={dsmBuild:'90080',profiles:[{profile:{id:'p',name:'P'},latestJobId:'j'}]};const element=find(wb.render(workbench.Workbench,props));assert.ok(element);pv.render(element.type,element.props,true);await tick();assert.equal(opens.length,2);assert.equal(opens[0].body.viewId,opens[1].body.viewId);
  const reply=o=>new Response(JSON.stringify({contractVersion:1,data:{view:{...storageFixture(),snapshotId:'s',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'P'},lease:{id:o.body.viewId,snapshotId:'s',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}}}));
  const order=reverse?[1,0]:[0,1];opens[order[0]].resolve(reply(opens[order[0]]));await tick();assert.deepEqual(releases,[]);opens[order[1]].resolve(reply(opens[order[1]]));await tick();await tick();assert.equal(navigated,true);assert.deepEqual(releases,[]);
 }finally{pv.cleanups.forEach(fn=>fn?.());Object.assign(React,{useState:original.useState,useRef:original.useRef,useEffect:original.useEffect});globalThis.fetch=original.fetch;globalThis.window=original.window;}
});

// Exercise real ProfileView effects: a delayed maintenance read must not supersede a user drill.
for (const actionKind of ['navigation','adopt','partial']) for (const order of ['read-first','navigation-first','read-error']) test(`explicit ${actionKind} owns publication over maintenance (${order})`, async()=>{
 const original={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window,document:globalThis.document,setInterval:globalThis.setInterval,clearInterval:globalThis.clearInterval};
 const make=()=>({states:[],refs:[],cleanups:[],render(fn,props,effects=false){let si=0,ri=0;const scheduled=[];React.useState=initial=>{const i=si++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=initial=>this.refs[ri++]??={current:initial};React.useEffect=fn=>{if(effects)scheduled.push(fn);};const tree=fn(props);for(const fn of scheduled)this.cleanups.push(fn());return tree;}});
 const find=(tree,name)=>{if(Array.isArray(tree))return tree.map(x=>find(x,name)).find(Boolean);if(React.isValidElement(tree)){if(tree.type?.name===name)return tree;return find(tree.props.children,name);}return null;};
 const tick=()=>new Promise(r=>setImmediate(r));const response=data=>new Response(JSON.stringify({contractVersion:1,data}));const wb=make(),pv=make();let interval,delayedRead,delayedNav;let transitioning=false;const calls=[],releases=[];
 const reply=id=>({view:{...storageFixture(),snapshotId:'s',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'P'},lease:{id,snapshotId:'s',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});
 const nav=body=>({snapshotId:body.snapshotId,rootId:body.rootId,parentId:null,selectedNodeId:body.selectedNodeId,reasons:[],breadcrumb:[{id:body.rootId,name:'Synthetic',kind:'nas'}]});
 try{
  globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};globalThis.document={visibilityState:'visible'};
  globalThis.setInterval=fn=>{interval=fn;return 1;};globalThis.clearInterval=()=>{};
  globalThis.fetch=async(url,options)=>{const action=new URL(url,'https://local.invalid').searchParams.get('action'),body=options.body?JSON.parse(options.body):{};calls.push({action,body});
   if(action==='job')return response({job:{id:'j',profileId:'p',state:'completed',scanMode:'full',resultSnapshotId:'partial',profileRevision:1},snapshotState:'partial',snapshotEndedAt:'2026-10-10T00:01:00Z',control:null});
   if(action==='scan-boundaries')return response({jobId:'j',snapshotId:'partial',state:'partial',endedAt:'2026-10-10T00:01:00Z',profileRevision:1,boundaries:[],count:'0',truncated:false});
   if(action==='view'&&(body.operation==='open'||body.operation==='switch')){const data=reply(body.newViewId??body.viewId);if(body.jobId||body.operation==='switch'){data.view.snapshotId=body.jobId?'partial':'new';data.lease.snapshotId=data.view.snapshotId;data.view.state=body.jobId?'partial':'complete';}return response(data);}
   if(action==='view'&&body.operation==='read')return new Promise((resolve,reject)=>{delayedRead={resolve:()=>resolve(response(reply(body.viewId))),reject};});
   if(action==='view'&&body.operation==='release'){releases.push(body.viewId);return response({});}
   if(action==='view-navigation'){if(transitioning)return new Promise(resolve=>{delayedNav=()=>resolve(response(nav(body)));});return response(nav(body));}
   throw Error(`unexpected ${action}`);
  };
  const props={language:'en',selected:'p',onSelect:()=>{},denied:()=>assert.fail('denied')};wb.render(workbench.Workbench,props);wb.states[0]={dsmBuild:'90080',profiles:[{profile:{id:'p',name:'P',active:true,scope:{kind:'volumes',targetIds:['v']}},latestJobId:actionKind==='partial'?'j':null}]};
  const element=()=>find(wb.render(workbench.Workbench,props),'ProfileView');let e=element();pv.render(e.type,e.props,true);await tick();await tick();e=element();let tree=pv.render(e.type,e.props);const controller=e.props.controller;
  assert.equal(controller.current.state.view.rootId,'nas');const initialNavigations=calls.filter(x=>x.action==='view-navigation').length;interval();await tick();assert.ok(delayedRead);
  transitioning=true;if(actionKind==='navigation')find(tree,'NavigationBar').props.onBreadcrumb('volume');else if(actionKind==='adopt')find(tree,'StorageSettings').props.onChooseSnapshot('new');else find(tree,'SnapshotStatus').props.onOpenPartial();await tick();assert.ok(delayedNav);
  if(order==='navigation-first'){delayedNav();await tick();delayedRead.resolve();}
  else {if(order==='read-error')delayedRead.reject(Error('late read failure'));else delayedRead.resolve();await tick();delayedNav();}
  await tick();await tick();assert.equal(controller.current.state.view.rootId,actionKind==='navigation'?'volume':'nas');assert.equal(controller.current.state.view.snapshotId,actionKind==='navigation'?'s':actionKind==='adopt'?'new':'partial');
  assert.deepEqual(calls.filter(x=>x.action==='view-navigation').slice(initialNavigations).map(x=>x.body.rootId),[actionKind==='navigation'?'volume':'nas']);assert.equal(new Set(releases).size,actionKind==='navigation'?0:1);
  e=element();tree=pv.render(e.type,e.props);assert.equal(find(tree,'NavigationBar').props.disabled,false);assert.equal(renderToStaticMarkup(tree).includes('late read failure'),false);
 }finally{pv.cleanups.forEach(fn=>fn?.());Object.assign(React,{useState:original.useState,useRef:original.useRef,useEffect:original.useEffect});for(const key of ['fetch','window','document','setInterval','clearInterval'])globalThis[key]=original[key];}
});

// Real ProfileView effects retry a failed read without inventing a navigation transition.
test('explicit refresh retries failed hierarchy with unchanged qualified view',async()=>{
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window,document:globalThis.document,setInterval:globalThis.setInterval,clearInterval:globalThis.clearInterval,ResizeObserver:globalThis.ResizeObserver};
 const make=()=>({states:[],refs:[],effects:[],render(fn,props,run=false){let si=0,ri=0,ei=0;const scheduled=[];React.useState=initial=>{const i=si++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=initial=>this.refs[ri++]??={current:initial};React.useEffect=(effect,deps)=>{const i=ei++;if(run&&(!this.effects[i]||deps.some((v,j)=>v!==this.effects[i].deps[j])))scheduled.push(()=>{this.effects[i]?.cleanup?.();this.effects[i]={deps,cleanup:effect()};});};const tree=fn(props);scheduled.forEach(f=>f());return tree;}});
 const find=(tree,test)=>{if(Array.isArray(tree))return tree.map(x=>find(x,test)).find(Boolean);if(React.isValidElement(tree)){if(test(tree))return tree;return find(tree.props.children,test);}return null;};
 const tick=()=>new Promise(r=>setImmediate(r)),response=data=>new Response(JSON.stringify({contractVersion:1,data}));
 const viewport={},observers=[];let autoMeasure=true;
 const wb=make(),pv=make();let hierarchyCalls=0,failRefresh=false;const hierarchySignals=[];
 try{
  globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};globalThis.document={visibilityState:'visible'};globalThis.setInterval=()=>1;globalThis.clearInterval=()=>{};
  globalThis.ResizeObserver=class{constructor(cb){this.cb=cb;observers.push(this);}observe(target){this.target=target;if(autoMeasure)this.cb([{target,contentRect:{width:900,height:500}}]);}disconnect(){this.disconnected=true;}};
  globalThis.fetch=async(url,options)=>{const action=new URL(url,'https://local.invalid').searchParams.get('action'),b=JSON.parse(options.body??'{}');
   if(action==='view'&&b.operation==='read'&&failRefresh)return new Response('{}',{status:503});
   if(action==='view')return response({view:{...storageFixture(),snapshotId:'s',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'P'},lease:{id:b.viewId,snapshotId:'s',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});
   if(action==='view-navigation')return response({snapshotId:b.snapshotId,rootId:b.rootId,parentId:null,selectedNodeId:b.selectedNodeId,reasons:[],breadcrumb:[{id:'nas',name:'NAS',kind:'nas'}]});
   if(action==='hierarchy'){hierarchyCalls++;hierarchySignals.push(options.signal);if(hierarchyCalls===1)return new Response(JSON.stringify({contractVersion:1,error:{code:'QUERY_BUDGET_EXCEEDED'}}),{status:503});const summary={knownBytes:'0',unknownFileCount:'0',fileCount:'0',directoryCount:'0',specialCount:'0'};return response(completeHierarchy(b.query,[{id:'nas',parentId:null,realNodeId:null,name:'Recovered hierarchy',kind:'nas',virtual:true,summary,hasMore:false}]));}
   throw Error(`unexpected ${action}`);
  };
  const props={language:'en',selected:'p',onSelect:()=>{},denied:()=>assert.fail('denied')};wb.render(workbench.Workbench,props);wb.states[0]={dsmBuild:'90080',profiles:[{profile:{id:'p',name:'P',active:true,scope:{kind:'volumes',targetIds:['v']}},latestJobId:null}]};
  const element=()=>find(wb.render(workbench.Workbench,props),e=>e.type?.name==='ProfileView');const render=()=>{const e=element();const tree=pv.render(e.type,e.props,true);find(tree,e=>e.props.className==='map-viewport')?.props.ref(viewport);return tree;};
  render();await tick();await tick();render();await tick();await tick();render();await tick();let tree=render();await tick();tree=render();assert.equal(hierarchyCalls,1);
  const controller=element().props.controller,before=JSON.stringify(controller.current.state),lease=controller.current.pin.viewId;
  const controls=find(tree,e=>e.type?.name==='ScanControls');
  const refresh=find(controls.type(controls.props),e=>e.type==='button'&&e.props.children==='Load snapshot');
  assert.ok(refresh,'real refresh control');
  failRefresh=true;await refresh.props.onClick();await tick();await tick();render();await tick();assert.equal(hierarchyCalls,1,'unqualified refresh must not trigger a hierarchy retry');
  failRefresh=false;const latestControls=find(render(),e=>e.type?.name==='ScanControls');latestControls.props.reload();await tick();render();await tick();await tick();tree=render();
  assert.equal(hierarchyCalls,2);assert.equal(JSON.stringify(controller.current.state),before);assert.equal(controller.current.pin.viewId,lease);assert.equal(hierarchySignals[0].aborted,true);
  assert.ok(renderToStaticMarkup(tree).includes('Recovered hierarchy'));
 }finally{pv.effects.forEach(e=>e?.cleanup?.());Object.assign(React,{useState:saved.useState,useRef:saved.useRef,useEffect:saved.useEffect});for(const key of ['fetch','window','document','setInterval','clearInterval','ResizeObserver'])globalThis[key]=saved[key];}
});

// Real ProfileView callbacks qualify competing row intents without releasing another owner's busy lock.
for(const next of ['selection','drill','adopt-lock'])for(const order of ['old-first','new-first','old-error'])test(`row ${next} supersedes selection with ${order}`,async()=>{
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window,document:globalThis.document,setInterval:globalThis.setInterval,clearInterval:globalThis.clearInterval,ResizeObserver:globalThis.ResizeObserver};
 const make=()=>({states:[],refs:[],effects:[],render(fn,props){let si=0,ri=0,ei=0;const scheduled=[];React.useState=initial=>{const i=si++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=initial=>this.refs[ri++]??={current:initial};React.useEffect=(effect,deps)=>{const i=ei++;if(!this.effects[i]||deps.some((v,j)=>v!==this.effects[i].deps[j]))scheduled.push(()=>{this.effects[i]?.cleanup?.();this.effects[i]={deps,cleanup:effect()};});};const tree=fn(props);scheduled.forEach(f=>f());return tree;}});
 const find=(tree,name)=>{if(Array.isArray(tree))return tree.map(x=>find(x,name)).find(Boolean);if(React.isValidElement(tree)){if(tree.type?.name===name)return tree;return find(tree.props.children,name);}return null;};
 const tick=()=>new Promise(r=>setImmediate(r)),response=data=>new Response(JSON.stringify({contractVersion:1,data}));const wb=make(),pv=make(),waiters=[],writes=[];let delaying=false,hierarchyCalls=0;
 const summary={knownBytes:'0',unknownFileCount:'0',fileCount:'0',directoryCount:'0',specialCount:'0'};
 const nodes=['a','b'].map(id=>({id,realNodeId:id,parentId:'nas',name:id,kind:'directory',virtual:false,summary:{...summary,directoryCount:'1'},hasMore:false}));
 const nav=b=>({snapshotId:b.snapshotId,rootId:b.rootId,parentId:null,selectedNodeId:b.selectedNodeId,reasons:[],breadcrumb:[{id:b.rootId,name:'Synthetic',kind:'nas'}]});
 try{
  globalThis.ResizeObserver=MeasuredViewportObserver;globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};globalThis.document={visibilityState:'visible'};globalThis.setInterval=()=>1;globalThis.clearInterval=()=>{};
  globalThis.fetch=async(url,options)=>{const action=new URL(url,'https://local.invalid').searchParams.get('action'),b=JSON.parse(options.body??'{}');
   if(action==='view')return response({view:{...storageFixture(),snapshotId:'s',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'P'},lease:{id:b.viewId,snapshotId:'s',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});
   if(action==='view-navigation'){if(delaying)return new Promise((resolve,reject)=>waiters.push({resolve:()=>resolve(response(nav(b))),reject}));return response(nav(b));}
   if(action==='hierarchy'){hierarchyCalls++;return response(completeHierarchy(b.query,b.query.rootId==='nas'?[{id:'nas',parentId:null,realNodeId:null,name:'NAS',kind:'nas',virtual:true,summary:{...summary,directoryCount:'2'},hasMore:false},...nodes]:[{...nodes.find(n=>n.id===b.query.rootId),parentId:null}]));}
   throw Error(`unexpected ${action}`);
  };
  const props={language:'en',selected:'p',onSelect:()=>{},denied:()=>assert.fail('denied')};
  // Parent bootstrap is suppressed; the real ProfileView effects and controller remain active.
  wb.render=function(fn,p){let si=0,ri=0;React.useState=initial=>{const i=si++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=initial=>this.refs[ri++]??={current:initial};React.useEffect=()=>{};return fn(p);};
  wb.render(workbench.Workbench,props);
  wb.states[0]={dsmBuild:'90080',profiles:[{profile:{id:'p',name:'P',active:true,scope:{kind:'volumes',targetIds:['v']}},latestJobId:null}]};
  const element=()=>find(wb.render(workbench.Workbench,props),'ProfileView');
  const viewport={};const render=()=>{const e=element();let tree;for(let i=0;i<3;i++){tree=pv.render(e.type,{...e.props,saveView:()=>writes.push(structuredClone(e.props.controller.current.state.view))});attachFixtureViewport(tree,viewport);}return tree;};
  render();await tick();await tick();render();await tick();let tree=render();const controller=element().props.controller;
  const hierarchyBefore=hierarchyCalls;
  delaying=true;
  if(next==='adopt-lock'){
   find(tree,'StorageSettings').props.onChooseSnapshot('s');await tick();assert.equal(waiters.length,1);
   const blocked=find(render(),'AnalysisNodeList');assert.equal(blocked.props.disabled,true);
   blocked.props.onSelect(nodes[0]);blocked.props.onOpen(nodes[1]);await tick();assert.equal(waiters.length,1,'generic adoption cannot be superseded by row callbacks');
   waiters[0].resolve();await tick();await tick();assert.equal(controller.current.state.view.rootId,'nas');return;
  }
  find(tree,'AnalysisNodeList').props.onSelect(nodes[0]);await tick();assert.equal(waiters.length,1);
  tree=render();assert.equal(find(tree,'AnalysisNodeList').props.disabled,false);const row=find(tree,'AnalysisNodeList');(next==='drill'?row.props.onOpen:row.props.onSelect)(nodes[1]);await tick();assert.equal(waiters.length,2);
  if(order==='new-first'){waiters[1].resolve();await tick();waiters[0].resolve();}else{if(order==='old-error')waiters[0].reject(Error('late selection failure'));else waiters[0].resolve();await tick();assert.equal(find(render(),'NavigationBar').props.disabled,true,'old completion must not release new owner');waiters[1].resolve();}
  await tick();await tick();tree=render();assert.equal(controller.current.state.view.rootId,next==='drill'?'b':'nas');assert.equal(controller.current.state.view.selectedNodeId,next==='drill'?null:'b');assert.equal(find(tree,'NavigationBar').props.disabled,false);await tick();render();assert.equal(hierarchyCalls,hierarchyBefore+(next==='drill'?1:0),'selection preserves hierarchy while drill reads new root');assert.equal(writes.length,1);assert.deepEqual(writes[0],controller.current.state.view);assert.ok(!renderToStaticMarkup(tree).includes('late selection failure'));
 }finally{pv.effects.forEach(e=>e?.cleanup?.());wb.effects.forEach(e=>e?.cleanup?.());Object.assign(React,{useState:saved.useState,useRef:saved.useRef,useEffect:saved.useEffect});for(const k of ['fetch','window','document','setInterval','clearInterval','ResizeObserver'])globalThis[k]=saved[k];}
});

// Run the actual ProfileView lifecycle and route status actions through its qualified reader controller.
for(const scenario of ['historical-file-not-preview','historical-late-200','historical-late-403','new-complete','first-partial','bounded-search','filtered-search-hits','empty','source-gap'])test(`snapshot status integrates ${scenario} without automatic adoption`,async()=>{
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window,document:globalThis.document,setInterval:globalThis.setInterval,clearInterval:globalThis.clearInterval,ResizeObserver:globalThis.ResizeObserver};
 const make=()=>({states:[],refs:[],effects:[],render(fn,props,run=true){let si=0,ri=0,ei=0;const scheduled=[];React.useState=initial=>{const i=si++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=initial=>this.refs[ri++]??={current:initial};React.useEffect=(effect,deps)=>{const i=ei++;if(run&&(!this.effects[i]||deps.some((v,j)=>v!==this.effects[i].deps[j])))scheduled.push(()=>{this.effects[i]?.cleanup?.();this.effects[i]={deps,cleanup:effect()};});};const tree=fn(props);scheduled.forEach(f=>f());return tree;}});
 const find=(tree,name)=>{if(Array.isArray(tree))return tree.map(x=>find(x,name)).find(Boolean);if(React.isValidElement(tree)){if(tree.type?.name===name)return tree;return find(tree.props.children,name);}return null;};
 let settleFileStation,launches=0;
 const tick=()=>new Promise(r=>setImmediate(r)),response=data=>new Response(JSON.stringify({contractVersion:1,data})),wb=make(),pv=make(),calls=[];
 const summary={knownBytes:scenario==='filtered-search-hits'?'8':'0',unknownFileCount:'0',fileCount:scenario==='filtered-search-hits'?'1':'0',directoryCount:'0',specialCount:'0'};
 const captured=()=>({...storageFixture(),snapshotId:'old',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:2,profileChanged:true,capturedProfileName:'P',...(scenario==='source-gap'?{scanBoundaries:[{targetId:'v',nodeId:null,reasonCode:'SOURCE_NOT_MOUNTED',relativePath:'.'}],scanBoundaryCount:'1'}:{})});
 try{
  globalThis.ResizeObserver=MeasuredViewportObserver;globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'},AppLaunch:()=>{launches++;}}}}};globalThis.document={visibilityState:'visible'};globalThis.setInterval=()=>1;globalThis.clearInterval=()=>{};
  globalThis.fetch=async(url,options)=>{const action=new URL(url,'https://local.invalid').searchParams.get('action'),b=JSON.parse(options.body??'{}');calls.push({action,b,parameters:new URL(url,'https://local.invalid').searchParams});
   if(action==='filestation')return scenario==='historical-file-not-preview'?response({fileStationPath:'/Share/deep.txt',kind:'file'}):new Promise(resolve=>{settleFileStation=resolve;});
   if(action==='session')return response({authenticated:true});
   if(action==='view'){
    if(b.operation==='release')return response({});
    const view=captured();if(scenario==='first-partial'&&!b.jobId)Object.assign(view,{snapshotId:null,state:null,startedAt:null,endedAt:null,profileRevision:null,capturedProfileName:null,profileChanged:false});
    if(b.jobId)Object.assign(view,{snapshotId:'partial',state:'partial'});if(b.operation==='switch')view.snapshotId='new';
    return response({view,lease:view.snapshotId?{id:b.newViewId??b.viewId,snapshotId:view.snapshotId,expiresAt:'2026-10-20T00:00:00Z'}:null,newCompleteSnapshotId:scenario==='new-complete'&&view.snapshotId!=='new'?'new':null,selection:{nodeId:null,fallback:b.operation==='switch'?'ancestor':'none'}});
   }
   if(action==='job')return response({job:{id:'j',profileId:'p',state:'completed',scanMode:'full',resultSnapshotId:'partial',profileRevision:1},snapshotState:'partial',snapshotEndedAt:'2026-10-10T00:01:00Z',control:null});
   if(action==='scan-boundaries')return response({jobId:'j',snapshotId:'partial',state:'partial',endedAt:'2026-10-10T00:01:00Z',profileRevision:1,boundaries:[],count:'0',truncated:false});
   if(action==='view-navigation')return response({snapshotId:b.snapshotId,rootId:b.rootId,parentId:null,selectedNodeId:b.selectedNodeId,reasons:[],breadcrumb:[{id:b.rootId,name:'Synthetic',kind:'nas'}]});
   if(action==='hierarchy')return response(completeHierarchy(b.query,[{id:b.query.rootId,realNodeId:null,parentId:null,name:'NAS',kind:'nas',virtual:true,summary,hasMore:false},...(scenario==='filtered-search-hits'?[{id:'f',realNodeId:'f',parentId:b.query.rootId,name:'Found',kind:'file',virtual:false,summary,hasMore:false}]:[])]));
   if(action==='search')return response({...b.query,selectionOrder:`${b.query.sort}-${b.query.direction}-recorded-rowid`,items:scenario==='filtered-search-hits'?[{snapshotId:b.query.snapshotId,nodeId:'f',parentId:'nas',name:'Found',kind:'file',summary}]:[],hasMore:scenario==='bounded-search',workLimited:scenario==='bounded-search'});
   throw Error(`unexpected ${action}`);
  };
  const props={language:'en',selected:'p',onSelect:()=>{},denied:()=>assert.fail('denied')};wb.render(workbench.Workbench,props,false);
  wb.states[0]={dsmBuild:'90080',profiles:[{profile:{id:'p',name:'P',revision:2,freshnessSeconds:60,active:true,scope:{kind:'volumes',targetIds:['v']}},latestJobId:scenario==='first-partial'?'j':null}]};
  const element=()=>find(wb.render(workbench.Workbench,props,false),'ProfileView');const viewport={};const render=()=>{const e=element();let tree;for(let i=0;i<3;i++){tree=pv.render(e.type,e.props);attachFixtureViewport(tree,viewport);}return tree;};
  render();await tick();await tick();render();await tick();let tree=render();const controller=element().props.controller;
  assert.equal(calls.filter(c=>c.action==='view'&&c.b.operation==='switch').length,0);
  if(scenario.includes('search')){await controller.move({...controller.current.state.view,search:{text:'find',field:'both',scope:'recursive'},filter:{categories:scenario==='filtered-search-hits'?['image']:[]}});render();await tick();tree=render();}
  let status=find(tree,'SnapshotStatus').props;
  assert.equal(status.currentRevision,2);assert.equal(status.freshnessSeconds,60);
  if(scenario==='historical-file-not-preview'){
   // This file is absent from the bounded preview; the qualified target decides the parent hint.
   find(tree,'SearchResultList').props.onFileStation({realNodeId:'deep-file'});await tick();await tick();tree=render();assert.equal(launches,1);
   const message=JSON.parse(readFileSync(new URL('../src/locales/en.json',import.meta.url),'utf8'))['probe.parentRequested'];
   const text=t=>Array.isArray(t)?t.map(text).join(' '):React.isValidElement(t)?text(t.props.children):typeof t==='string'?t:'';assert.ok(text(tree).includes(message));
  }else if(scenario.startsWith('historical-late')){
   // Exercise the actual host callback: invalidation suppresses both late target and late auth errors.
   find(tree,'SearchResultList').props.onFileStation({realNodeId:'historical-file'});await tick();
   const call=calls.find(c=>c.action==='filestation');assert.ok(call);assert.equal(call.parameters.get('viewId'),controller.current.pin.viewId);assert.equal(call.parameters.get('snapshotId'),'old');
   find(tree,'IntegritySettings').props.onSnapshotUnavailable('p','old','corrupt');
   settleFileStation(scenario.endsWith('403')?new Response('',{status:403}):response({fileStationPath:'/Share/file',kind:'file'}));
   await tick();await tick();tree=render();assert.equal(calls.filter(c=>c.action==='session').length,0);assert.equal(find(tree,'SnapshotStatus').props.snapshot,null);
  }else if(scenario==='new-complete'){
   assert.equal(status.snapshot.snapshotId,'old');assert.equal(status.newCompleteSnapshotId,'new');
   // A newer publication leaves the qualified old lease eligible for current-object checks.
   assert.equal(find(tree,'AnalysisNodeList').props.fileStationDisabled,false);
   status.onAdopt();await tick();await tick();tree=render();status=find(tree,'SnapshotStatus').props;
   assert.equal(status.snapshot.snapshotId,'new');assert.equal(status.selectionFallback,'ancestor');assert.equal(calls.filter(c=>c.action==='view'&&c.b.operation==='switch').length,1);
  }else if(scenario==='first-partial'){
   assert.equal(status.snapshot,null);assert.equal(status.contentState,'no-snapshot');assert.equal(status.partialAvailable,true);assert.equal(status.completeAvailable,false);
   status.onOpenPartial();await tick();await tick();tree=render();status=find(tree,'SnapshotStatus').props;assert.equal(status.snapshot.state,'partial');assert.equal(status.completeAvailable,false);
   status.onBackComplete();await tick();await tick();status=find(render(),'SnapshotStatus').props;assert.equal(status.snapshot,null);assert.equal(status.contentState,'no-snapshot');
  }else if(scenario==='bounded-search')assert.notEqual(status.contentState,'bounded-empty','a result-page window cannot classify the independent hierarchy');
  else if(scenario==='filtered-search-hits')assert.equal(status.contentState,'data');
  else if(scenario==='empty')assert.equal(status.contentState,'empty');
  else {assert.equal(status.sourceGap,true);assert.notEqual(status.contentState,'empty');}
  assert.equal(calls.filter(c=>c.action==='scan'||c.action==='preferences').length,0,'status rendering never starts a scan or writes preferences');
 }finally{pv.effects.forEach(e=>e?.cleanup?.());wb.effects.forEach(e=>e?.cleanup?.());Object.assign(React,{useState:saved.useState,useRef:saved.useRef,useEffect:saved.useEffect});for(const k of ['fetch','window','document','setInterval','clearInterval','ResizeObserver'])globalThis[k]=saved[k];}
});

// Exercise the actual ProfileView lifecycle with synthetic transport and an explicit timer queue.
async function pollingFixture(check) {
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window,setTimeout:globalThis.setTimeout,clearTimeout:globalThis.clearTimeout,setInterval:globalThis.setInterval,clearInterval:globalThis.clearInterval};
 const make=()=>({states:[],refs:[],cleanups:[],render(fn,props,effects=false){let si=0,ri=0;const pending=[];React.useState=initial=>{const i=si++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=initial=>this.refs[ri++]??={current:initial};React.useEffect=fn=>{if(effects)pending.push(fn);};const tree=fn(props);pending.forEach(fn=>this.cleanups.push(fn()));return tree;}});
 // Presentational slots retain the same real JobControls/ScanJobStatus elements.
 const find=(tree,name)=>{if(Array.isArray(tree))return tree.map(t=>find(t,name)).find(Boolean);if(React.isValidElement(tree)){if(tree.type?.name===name)return tree;if(tree.type?.name==='ScanStatusPanel')return find(tree.type(tree.props),name);return find(tree.props.children,name)??find(tree.props.controls,name);}return null;};
 const wb=make(),pv=make(),timers=new Map(),calls=[];let timerID=0,denials=0;const tick=()=>new Promise(r=>setImmediate(r));
 const response=data=>new Response(JSON.stringify({contractVersion:1,data}));
 const outcome=(state='aggregating',id='j')=>({job:{id,profileId:'p',state,scanMode:'full',trigger:'ui',createdAt:'2026-10-10T00:00:00Z',resultSnapshotId:null},snapshotState:state==='completed'?'complete':'',control:null});
 const f={calls,timers,tick,response,outcome,jobRead:async()=>response(outcome()),viewRead:null,denials:()=>denials};
 try {
  globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};globalThis.setInterval=()=>1;globalThis.clearInterval=()=>{};
  globalThis.setTimeout=(fn,ms)=>{const id=++timerID;timers.set(id,{fn,ms});return id;};globalThis.clearTimeout=id=>timers.delete(id);
  globalThis.fetch=async(url,options)=>{const action=new URL(url,'https://local.invalid').searchParams.get('action'),body=JSON.parse(options.body??'{}');calls.push({action,body,signal:options.signal,headers:options.headers});
   if(action==='status-snapshot')return f.statusRead?f.statusRead(options):response({observedAt:'2026-10-10T00:00:00Z',active:null,queued:[]});
   if(action==='history')return f.historyRead?f.historyRead(options):response({jobs:[],truncated:false});
   if(action==='progress'){const id=new URL(url,'https://local.invalid').searchParams.get('jobId');return response({jobId:id,profileId:'p',state:'scanning',trigger:'ui',mode:'full',createdAt:'2026-10-10T00:00:00Z',startedAt:null,endedAt:null,entries:'0',files:'0',directories:'0',specials:'0',unknownLogical:'0',knownLogicalBytes:'0',warnings:'0',observedAt:null,elapsedSeconds:null,exactTotal:null,estimate:null,targetId:'',measured:false,publishedComplete:false,errorCodes:[]});}
   if(action==='job')return f.jobRead(options);
   if(action==='scan')return f.scanRead ? f.scanRead(options) : response({...outcome('scanning','new-job'),deduplicated:false});
   if(action==='job-control')return f.controlRead(options);
   if(action==='view'){if(f.viewRead)return f.viewRead(options);return response({view:{...storageFixture(),snapshotId:null,nodes:[],truncated:false,state:null,startedAt:null,endedAt:null,profileRevision:null,currentProfileRevision:1,profileChanged:false,capturedProfileName:null},lease:null,newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});}
   throw Error(`unexpected ${action}`);
  };
  const props={language:'en',selected:'p',onSelect:()=>{},denied:()=>denials++};wb.render(workbench.Workbench,props);wb.states[0]={dsmBuild:'90080',profiles:[{profile:{id:'p',name:'P',active:true},latestJobId:'j'}]};const element=find(wb.render(workbench.Workbench,props),'ProfileView');
  f.renderWith=extra=>pv.render(element.type,{...element.props,...extra});f.busy=value=>pv.states[9]=value;f.mount=()=>pv.render(element.type,element.props,true);f.tree=()=>pv.render(element.type,element.props);f.find=name=>find(f.tree(),name);f.status=()=>f.find('ScanJobStatus')?.props.job.state;
  // Expose the actual parent-owned notice boundary without mounting a second poller.
  f.notices=()=>find(wb.render(workbench.Workbench,props),'LocalNotices').props;
  f.notify=element.props.onNotice;f.deny=element.props.denied;
  f.unmount=()=>pv.cleanups.splice(0).forEach(fn=>fn?.());
  f.next=async()=>{assert.equal(timers.size,1,'one pending read timer');const [id,t]=timers.entries().next().value;timers.delete(id);t.fn();await tick();await tick();return t.ms;};
  await check(f);
 }finally{pv.cleanups.forEach(fn=>fn?.());Object.assign(React,{useState:saved.useState,useRef:saved.useRef,useEffect:saved.useEffect});for(const key of ['fetch','window','setTimeout','clearTimeout','setInterval','clearInterval'])globalThis[key]=saved[key];}
}

for(const fault of ['html503','network','timeout'])test(`job polling recovers once after ${fault} without mutation`,()=>pollingFixture(async f=>{
 let n=0;f.jobRead=async()=>{if(++n===2){if(fault==='html503')return new Response('<html>unavailable</html>',{status:503});if(fault==='timeout')throw new DOMException('timeout','TimeoutError');throw new TypeError('network');}return f.response(f.outcome(n>=3?'completed':'aggregating'));};
 f.mount();await f.tick();assert.equal(f.status(),'aggregating');await f.next();assert.match(renderToStaticMarkup(f.tree()),/Job display may be out of date/);await f.next();assert.equal(f.status(),'completed');assert.equal(f.timers.size,1);assert.equal(f.calls.filter(x=>x.action==='scan').length,0);
}));

test('job polling caps retries and explicit read retries no mutation',()=>pollingFixture(async f=>{
 f.jobRead=async()=>new Response('<html>unavailable</html>',{status:503});f.mount();await f.tick();const delays=[];while(f.timers.size)delays.push(await f.next());assert.deepEqual(delays,[1000,2000,4000,8000,15000,30000]);
 assert.match(renderToStaticMarkup(f.tree()),/Automatic reads stopped/);assert.equal(f.calls.filter(x=>x.action==='job').length,7);
 f.jobRead=async()=>f.response(f.outcome('failed'));const findButton=t=>{if(Array.isArray(t))return t.map(findButton).find(Boolean);if(React.isValidElement(t)){if(t.type==='button'&&t.props.children==='Read job status again')return t;return findButton(t.props.children);}return null;};findButton(f.tree()).props.onClick();await f.tick();assert.equal(f.status(),'failed');assert.equal(f.timers.size,1);
}));

for(const status of [401,403,404,400])test(`job polling stops definitive ${status}`,()=>pollingFixture(async f=>{
 f.jobRead=async()=>new Response('',{status});f.mount();await f.tick();assert.equal(f.timers.size,0);assert.equal(f.denials(),status===401||status===403?1:0);
}));

test('confirmed terminal job survives reload failure and retries only the read',()=>pollingFixture(async f=>{
 let views=0;f.jobRead=async()=>f.response(f.outcome('completed'));f.viewRead=async()=>{views++;throw new TypeError('network');};f.mount();await f.tick();assert.equal(f.status(),'completed');assert.equal(f.timers.size,1);await f.next();assert.equal(f.status(),'completed');assert.equal(f.calls.filter(x=>x.action==='job').length,1);assert.ok(views>=3);
}));

for(const late of ['active','denied','error'])test(`new scan discards superseded poll ${late}`,()=>pollingFixture(async f=>{
 let finish;f.jobRead=()=>new Promise((resolve,reject)=>finish={resolve,reject});f.mount();await f.tick();const old=finish;
 // Initial loading is unlocked by a temporary poll failure; then start an explicit new intent.
 old.resolve(new Response('',{status:503}));await f.tick();await f.next();const stale=finish;
 f.find('ScanControls').props.start('full');await f.tick();const fresh=finish;fresh.resolve(f.response(f.outcome('completed','new-job')));await f.tick();await f.tick();assert.equal(f.status(),'completed');
 if(late==='active')stale.resolve(f.response(f.outcome('scanning')));else if(late==='denied')stale.resolve(new Response('',{status:401}));else stale.reject(new TypeError('late'));
 await f.tick();assert.equal(f.find('ScanJobStatus').props.job.id,'new-job');assert.equal(f.status(),'completed');assert.equal(f.denials(),0);assert.equal(f.timers.size,1);
}));

test('unmount invalidates in-flight poll success and auth failure',()=>pollingFixture(async f=>{
 let resolve;f.jobRead=()=>new Promise(r=>resolve=r);f.mount();await f.tick();f.unmount();resolve(new Response('',{status:401}));await f.tick();assert.equal(f.denials(),0);assert.equal(f.timers.size,0);
}));

for(const state of ['failed','cancelled','interrupted','completed'])test(`terminal ${state} ends job reads`,()=>pollingFixture(async f=>{
 f.jobRead=async()=>f.response(f.outcome(state));f.mount();await f.tick();await f.tick();assert.equal(f.status(),state);assert.equal(f.timers.size,1);await f.next();assert.equal(f.calls.filter(x=>x.action==='job').length,1);
}));

test('pause and resume states are not numerically ordered',()=>pollingFixture(async f=>{
 const states=['scanning','paused','scanning','completed'];f.jobRead=async()=>f.response(f.outcome(states.shift()));f.mount();await f.tick();assert.equal(f.status(),'scanning');await f.next();assert.equal(f.status(),'paused');await f.next();assert.equal(f.status(),'scanning');await f.next();assert.equal(f.status(),'completed');assert.equal(f.timers.size,1);
}));

for(const value of [{job:{id:'other',profileId:'p',state:'scanning'}},{job:{id:'j',profileId:'other',state:'scanning'}},{job:{id:'j',profileId:'p',state:'invented',scanMode:'full'}}])test(`invalid poll identity/state stops (${JSON.stringify(value)})`,()=>pollingFixture(async f=>{
 f.jobRead=async()=>f.response(value);f.mount();await f.tick();assert.equal(f.status(),undefined);assert.equal(f.timers.size,0);
}));

test('unqualified reader HTML does not downgrade terminal and requires explicit read',()=>pollingFixture(async f=>{
 f.jobRead=async()=>f.response(f.outcome('completed'));f.viewRead=async()=>new Response('<html>temporarily unavailable</html>',{status:503});f.mount();await f.tick();await f.tick();assert.equal(f.status(),'completed');assert.equal(f.timers.size,0);assert.match(renderToStaticMarkup(f.tree()),/Read job status again/);
}));

// An old control completion cannot resurrect polling while a newer start is outcome-unknown.
for(const late of ['receipt','denied'])test(`pending scan admission rejects old control ${late}`,()=>pollingFixture(async f=>{
 let controlResolve,scanReject;f.controlRead=()=>new Promise(r=>controlResolve=r);f.scanRead=()=>new Promise((_,r)=>scanReject=r);
 f.mount();await f.tick();f.find('JobControls').props.send('pause');await f.tick();assert.ok(controlResolve);
 // A temporary read failure unlocks the user controls while the old control transport is pending.
 f.jobRead=async()=>new Response('',{status:503});await f.next();f.find('ScanControls').props.start('full');await f.tick();assert.ok(scanReject);
 const before=f.calls.filter(x=>x.action==='job').length;controlResolve(late==='denied'?new Response('',{status:403}):f.response({jobId:'j',action:'pause',version:1}));await f.tick();await f.tick();
 assert.equal(f.calls.filter(x=>x.action==='job').length,before);assert.equal(f.timers.size,0);assert.equal(f.denials(),0);assert.equal(f.status(),'aggregating');
 scanReject(new TypeError('unknown delivery'));await f.tick();const first=f.calls.find(x=>x.action==='scan');
 f.scanRead=async()=>f.response({...f.outcome('completed','new-job'),deduplicated:false});f.jobRead=async()=>f.response(f.outcome('completed','new-job'));
 f.find('ScanControls').props.start('full');await f.tick();await f.tick();const starts=f.calls.filter(x=>x.action==='scan');assert.equal(starts.length,2);assert.deepEqual(starts[1].body,first.body);assert.equal(starts[1].headers['Idempotency-Key'],first.headers['Idempotency-Key']);assert.equal(f.find('ScanJobStatus').props.job.id,'new-job');
}));

// A completed selected job does not stop the one observer from discovering external work.
test('shared observer discovers external CLI and completed history jobs without POST',()=>pollingFixture(async f=>{
 f.jobRead=async options=>f.response(f.outcome('completed',f.calls.filter(c=>c.action==='job').length===1?'j':'external'));
 f.mount();await f.tick();await f.tick();assert.equal(f.status(),'completed');
 f.statusRead=async()=>f.response({observedAt:'2026-10-10T00:01:00Z',active:null,queued:[{jobId:'external',profileId:'p',profileRevision:2,scanMode:'rebuild',trigger:'cli',state:'queued',acceptedAt:'2026-10-10T00:01:00Z',startedAt:null,queuePosition:1}]});
 await f.next();await f.next();assert.equal(f.find('ScanJobStatus').props.job.id,'external');assert.equal(f.calls.filter(x=>x.action==='job').length,2);assert.equal(f.calls.filter(x=>['scan','job-control'].includes(x.action)).length,0);
}));

test('global read failure preserves terminal confirmation and marks progress stale',()=>pollingFixture(async f=>{
 f.jobRead=async()=>f.response(f.outcome('completed'));f.mount();await f.tick();await f.tick();
 const before=f.find('ProgressHistory').props;f.statusRead=async()=>new Response('unavailable',{status:503});await f.next();
 assert.equal(f.status(),'completed');assert.equal(f.calls.filter(x=>x.action==='job').length,1);assert.equal(f.find('ProgressHistory').props.failed,true);assert.deepEqual(f.find('ProgressHistory').props.history,before.history);
}));

// Discovery changes control ownership even when the surrounding scheduler object remains alive.
for(const late of ['success','denied'])test(`external job discovery rejects previous control ${late}`,()=>pollingFixture(async f=>{
 let resolve;f.controlRead=()=>new Promise(r=>resolve=r);f.mount();await f.tick();f.find('JobControls').props.send('pause');await f.tick();assert.ok(resolve);
 f.statusRead=async()=>f.response({observedAt:'2026-10-10T00:01:00Z',active:null,queued:[{jobId:'external',profileId:'p',profileRevision:2,scanMode:'rebuild',trigger:'cli',state:'queued',acceptedAt:'2026-10-10T00:01:00Z',startedAt:null,queuePosition:1}]});
 await f.next();f.jobRead=async()=>f.response(f.outcome('scanning','external'));await f.next();assert.equal(f.find('ScanJobStatus').props.job.id,'external');
 const reads=f.calls.filter(x=>x.action==='job').length;resolve(late==='denied'?new Response('',{status:403}):f.response({jobId:'j',action:'pause',version:1}));await f.tick();assert.equal(f.denials(),0);assert.equal(f.calls.filter(x=>x.action==='job').length,reads);assert.equal(f.find('ScanJobStatus').props.job.id,'external');assert.equal(f.timers.size,1);
}));

// Test the real Workbench bootstrap, without mounting child feature effects or any backend writes.
async function bootstrapFixture(language,check) {
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window};
 const states=[],refs=[],cleanups=[],calls=[],publications=[];let first=true,mounted=true,denials=0;
 const response=data=>new Response(JSON.stringify({contractVersion:1,data}));
 const view={profileId:'saved',snapshotId:'s',rootId:'nas',selectedNodeId:null,map:'radial',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'size',direction:'desc'};
 const prefs={version:2,display:{units:'decimal',colorMode:'folder',detail:'auto',labels:true,percentages:true,hierarchyBorders:'normal',reducedMotion:'auto'},dsmUserId:'admin',revision:3,lastView:view,language:'auto',theme:'auto',preferredProfileId:'saved',map:'radial',locale:null,timeZone:null};
 const f={calls,publications,view,prefs,response,denials:()=>denials,tick:()=>new Promise(r=>setImmediate(r)),intercept:null};
 const find=(tree,match)=>{if(Array.isArray(tree))return tree.map(t=>find(t,match)).find(Boolean);if(React.isValidElement(tree)){if(match(tree))return tree;return find(tree.props.children,match);}return null;};
 f.render=()=>{let si=0,ri=0;const effects=[];React.useState=initial=>{const i=si++;if(!(i in states))states[i]=typeof initial==='function'?initial():initial;return[states[i],v=>{if(mounted)states[i]=typeof v==='function'?v(states[i]):v;}];};React.useRef=initial=>refs[ri++]??={current:initial};React.useEffect=fn=>{if(first)effects.push(fn);};const tree=workbench.Workbench({language,selected:'',onSelect:id=>publications.push({selected:id}),onAnalysis:(...x)=>publications.push(x),denied:()=>denials++});first=false;effects.forEach(fn=>cleanups.push(fn()));return tree;};
 f.find=name=>find(f.render(),x=>x.type?.name===name);
 f.retry=()=>find(f.render(),x=>x.type==='button'&&x.props.children===(language==='de'?'Einstieg erneut laden':'Retry opening workspace'))??null;
 f.unmount=()=>{mounted=false;cleanups.splice(0).forEach(fn=>fn?.());};
 try {
  globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};
  globalThis.fetch=async(url,options)=>{const action=new URL(url,'https://local.invalid').searchParams.get('action');const b=JSON.parse(options.body??'{}');calls.push({action,method:options.method??'GET',body:b,signal:options.signal});const injected=await f.intercept?.(action,options);if(injected)return injected;
   if(action==='preferences')return response(prefs);
   if(action==='profiles')return response({dsmBuild:'90080',profiles:[{profile:{id:'saved',name:'Saved',active:true}}]});
   if(action==='view-restore')return response({preferenceRevision:3,profileId:'saved',resolvedView:view,reasons:[],reader:{view:{...storageFixture(),snapshotId:'s',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'Saved'},lease:{id:b.viewId,snapshotId:'s',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}},navigation:{snapshotId:'s',rootId:'nas',parentId:null,selectedNodeId:null,reasons:[],breadcrumb:[{id:'nas',name:'NAS',kind:'nas'}]}});
   throw Error(`unexpected ${action}`);
  };
  await check(f);
 }finally{f.unmount();Object.assign(React,{useState:saved.useState,useRef:saved.useRef,useEffect:saved.useEffect});globalThis.fetch=saved.fetch;globalThis.window=saved.window;}
}

for(const language of ['de','en'])for(const stage of ['preferences','profiles','view-restore'])test(`bootstrap explicit ${language} retry after ${stage} failure`,()=>bootstrapFixture(language,async f=>{
 let failed=false;f.intercept=async action=>{if(action===stage&&!failed){failed=true;return new Response('<html>Unavailable</html>',{status:503});}};
 f.render();await f.tick();await f.tick();assert.ok(f.retry());assert.equal(f.publications.length,0);const initial=f.calls.length;await f.tick();assert.equal(f.calls.length,initial,'no automatic retry');
 const button=f.retry();button.props.onClick();button.props.onClick();await f.tick();await f.tick();assert.equal(f.retry(),null);assert.equal(f.publications.length,1);assert.equal(f.calls.filter(x=>x.action==='preferences').length,2);
 assert.deepEqual(f.calls.slice(initial).map(x=>x.action),['preferences','profiles','view-restore']);assert.ok(f.calls.every(x=>x.method==='GET'||x.action==='view-restore'));
 const tree=f.render();assert.equal(f.find('AnalysisShell').props.picker.type,'label');const html=renderToStaticMarkup(tree);assert.doesNotMatch(html,/Saved view unavailable|Gespeicherte Ansicht nicht verfügbar/);
 if(stage==='view-restore'){const restores=f.calls.filter(x=>x.action==='view-restore');assert.notEqual(restores[0].body.viewId,restores[1].body.viewId,'retry receives fresh qualified lease identity');}
}));

for(const stage of ['preferences','profiles','view-restore'])for(const result of ['success','auth','network'])test(`bootstrap ignores unmounted ${stage} ${result}`,()=>bootstrapFixture('en',async f=>{
 let settle;f.intercept=(action)=>action===stage?new Promise((resolve,reject)=>settle={resolve,reject}):null;
 f.render();await f.tick();assert.ok(settle);const count=f.calls.length;f.unmount();if(result==='network')settle.reject(new TypeError('late'));else settle.resolve(result==='auth'?new Response('',{status:401}):undefined);
 await f.tick();await f.tick();assert.equal(f.denials(),0);assert.equal(f.publications.length,0);assert.equal(f.calls.length,count,'no next stage after unmount');
}));

for(const status of [401,403])test(`bootstrap ${status} goes through session gate without retry`,()=>bootstrapFixture('en',async f=>{
 f.intercept=action=>action==='profiles'?new Response('',{status}):null;f.render();await f.tick();await f.tick();assert.equal(f.denials(),1);assert.equal(f.retry(),null);assert.equal(f.publications.length,0);assert.deepEqual(f.calls.map(x=>x.action),['regional-context','preferences','profiles']);
}));

// The absolute bootstrap deadline releases retry even when a transport ignores its signal.
for(const stage of ['preferences','profiles','view-restore'])for(const status of [401,403])test(`bootstrap deadline releases hung ${stage} and ignores late ${status}`,()=>bootstrapFixture('en',async f=>{
 const timeout=AbortSignal.timeout;const deadlines=[];
 AbortSignal.timeout=ms=>{if(ms!==20000)return timeout(ms);const c=new AbortController();deadlines.push(c);return c.signal;};
 try{
  let finish;f.intercept=action=>action===stage?new Promise(resolve=>finish=resolve):null;
  f.render();await f.tick();assert.ok(finish);assert.equal(f.retry(),null);
  deadlines[0].abort(new DOMException('Deadline reached','TimeoutError'));await f.tick();await f.tick();
  assert.ok(f.retry(),'bounded failure makes explicit retry available');assert.equal(f.denials(),0);assert.equal(f.publications.length,0);
  f.intercept=null;f.retry().props.onClick();await f.tick();await f.tick();assert.equal(f.retry(),null);assert.equal(f.publications.length,1);
  finish(new Response('',{status}));await f.tick();await f.tick();assert.equal(f.denials(),0);assert.equal(f.publications.length,1);assert.equal(f.retry(),null);
 }finally{AbortSignal.timeout=timeout;}
}));

// Exercise the production count formatter used by the map's historical unknown-size notice.
test('unknown-size map notice selects DE EN singular only for exact one',()=>{
 for(const [language,expected] of [['de',['Dateien mit unbekannter Größe','Datei mit unbekannter Größe','Dateien mit unbekannter Größe']],['en',['files with unknown size','file with unknown size','files with unknown size']]]){
  for(const count of [0n,1n,2n])assert.equal(workbench.unknownSizeCountLabel(count,language),expected[Number(count)]);
  assert.equal(workbench.unknownSizeCountLabel(9007199254740993n,language),expected[2]);
 }
});

// A definitive invalid input must not trap the dirty form behind a global unresolved-write lock.
test('regional invalid zone stays editable and corrected save preserves current view without scan',()=>bootstrapFixture('en',async f=>{
 f.render();await f.tick();await f.tick();let form=f.find('RegionalSettings');assert.equal(form.props.disabled,false);form.props.onDirty(true);
 const posts=()=>f.calls.filter(x=>x.method==='POST'&&x.action==='preferences');
 await assert.rejects(form.props.onSave({language:'de',locale:'de-DE',timeZone:'Europe/Typo'}),/INVALID_REQUEST/);form=f.find('RegionalSettings');assert.equal(form.props.disabled,false);assert.equal(posts().length,0);
 f.intercept=(action,options)=>action==='preferences'&&options.method==='POST'?f.response({...f.prefs,...JSON.parse(options.body).appearance,revision:4}):null;
 await form.props.onSave({language:'de',locale:'de-DE',timeZone:'Europe/Amsterdam'});form=f.find('RegionalSettings');assert.equal(form.props.disabled,false);assert.equal(form.props.value.timeZone,'Europe/Amsterdam');assert.deepEqual(form.props.value.lastView,f.view);assert.equal(posts().length,1);assert.ok(!f.calls.some(x=>x.action==='scan'));
}));

// Theme confirmation shares the real Workbench preference owner and leaves its qualified view intact.
test('personal theme save preserves confirmed analysis and routes acknowledged theme to both canvas host modes',()=>bootstrapFixture('en',async f=>{
 f.intercept=async(action,options)=>{if(action==='preferences'&&options.method==='POST'){const body=JSON.parse(options.body);assert.equal(body.group,'appearance');assert.equal(body.appearance.theme,'dark');return f.response({...f.prefs,...body.appearance,revision:4});}};
 f.render();await f.tick();await f.tick();const before=f.publications.length;
 await f.find('ThemeSettings').props.onSave('dark');await f.tick();
 assert.equal(f.find('ThemeSettings').props.value.theme,'dark');assert.equal(f.render().props.children[1].props.value.theme,'dark');
 assert.equal(f.publications.length,before);const writes=f.calls.filter(c=>c.method==='POST'&&c.action==='preferences');assert.equal(writes.length,1);assert.deepEqual(writes[0].body.appearance,{theme:'dark',language:'auto',map:'radial',locale:null,timeZone:null});assert.ok(!f.calls.some(c=>c.action==='scan'));
}));

// A confirmed application-language change invalidates an older manual help-language override without remounting.
test('local help follows confirmed language changes and permits a temporary explicit help choice',()=>{
 const old=React.useState;let state;React.useState=initial=>[state??=typeof initial==='function'?initial():initial,value=>state=value];
 const render=language=>localHelp.LocalHelp({language});const selector=tree=>React.Children.toArray(tree.props.children[1].props.children).find(n=>n?.type==='select');
 try{let tree=render('de');assert.equal(tree.props.lang,'de');tree=render('en');assert.equal(tree.props.lang,'en');const select=selector(tree);select.props.onChange({target:{value:'de'}});assert.equal(render('en').props.lang,'de');assert.equal(render('de').props.lang,'de');assert.equal(render('en').props.lang,'en');
 // Choosing English manually then a German app transition follows German immediately.
 tree=render('en');selector(tree).props.onChange({target:{value:'en'}});assert.equal(render('de').props.lang,'de');
 }finally{React.useState=old;}
});

// Settings reuse the centrally bound detail identity; another profile or a partial snapshot cannot enable launching.
test('File Station settings host binds historical selection and disables partial or busy views',()=>pollingFixture(async f=>{
 const find=(tree)=>Array.isArray(tree)?tree.map(find).find(Boolean):React.isValidElement(tree)?tree.type?.name==='FileStationIntegrationTest'?tree:find(tree.props.children):null;
 const pin={profileId:'p',viewId:'lease',snapshotId:'s',nodeId:'file'};
 const view={profileId:'p',snapshotId:'s',rootId:'nas',selectedNodeId:'file',map:'treemap',metric:'logical',filterMode:'highlight',filter:{categories:[]},search:{text:'',field:'both',scope:'recursive'},sort:'name',direction:'asc'};
 const reader={view:{...storageFixture(),snapshotId:'s',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'P'},lease:{id:'lease',snapshotId:'s',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:'file',fallback:'none'}};
 const analysis={state:{view,back:[],forward:[],epoch:7},pin,reader,navigation:null};
 f.renderWith({area:'settings',analysis});f.busy(false);let component=find(f.renderWith({area:'settings',analysis}));
 assert.deepEqual(component.props.selection,{profileId:'p',viewId:'lease',snapshotId:'s',rootId:'nas',nodeId:'file',metric:'logical',epoch:7});assert.equal(component.props.disabled,false);assert.equal(typeof component.props.onTest,'function');
 f.busy(true);assert.equal(find(f.renderWith({area:'settings',analysis})).props.disabled,true);f.busy(false);
 assert.equal(find(f.renderWith({area:'settings',analysis:{...analysis,reader:{...reader,view:{...reader.view,state:'partial'}}}})).props.disabled,true);
 assert.equal(find(f.renderWith({area:'settings',analysis:{...analysis,state:{...analysis.state,view:{...view,profileId:'foreign'}}}})).props.selection,null);
 assert.equal(find(f.renderWith({area:'overview',analysis})),undefined);assert.equal(f.calls.length,0,'rendering host issues no launch or mutation');
}));

// The composed status area shares the existing serial observer and never labels failed reads as current.
test('status panel receives qualified snapshots and preserves stale observations without controls',()=>pollingFixture(async f=>{
 let unavailable=false;
 const job={jobId:'queued-other',profileId:'other',profileRevision:2,scanMode:'reconcile',trigger:'scheduler',state:'queued',acceptedAt:'2026-10-10T00:00:00Z',startedAt:null,queuePosition:1};
 f.statusRead=async()=>unavailable?new Response('',{status:503}):f.response({observedAt:'2026-10-10T00:00:00Z',active:null,queued:[job]});
 f.mount();await f.tick();await f.tick();let panel=f.find('ScanStatusPanel');
 assert.equal(panel.props.health,'current');assert.equal(panel.props.selectedProfileName,'P');assert.equal(panel.props.snapshot.queued[0].jobId,'queued-other');assert.ok(panel.props.controls);assert.equal(panel.props.progress.history.length,0);
 assert.match(renderToStaticMarkup(panel),/Current job status/);assert.equal(f.timers.size,1);
 unavailable=true;await f.next();panel=f.find('ScanStatusPanel');assert.equal(panel.props.health,'retrying');assert.equal(panel.props.snapshot.queued[0].jobId,'queued-other');
 assert.match(renderToStaticMarkup(panel),/fieldset disabled/);assert.match(renderToStaticMarkup(panel),/Earlier observation/);
 assert.equal(f.calls.filter(x=>x.action==='scan'||x.action==='preferences').length,0);
}));

// Profile routing is supplied by the Workbench; the status presenter never infers an ID from a label.
test('status profile routing keeps the existing area and central selection contract',()=>pollingFixture(async f=>{
 f.mount();await f.tick();await f.tick();let selected=null;
 const tree=f.renderWith({profileNames:{p:'P',q:'Same label'},onStatusProfile:id=>selected=id});
 const find=t=>Array.isArray(t)?t.map(find).find(Boolean):React.isValidElement(t)?t.type?.name==='ScanStatusPanel'?t:find(t.props.children):null;
 const panel=find(tree);assert.deepEqual(panel.props.profileNames,{p:'P',q:'Same label'});panel.props.onSelectProfile('q');assert.equal(selected,'q');
 assert.equal(f.calls.filter(x=>x.action==='scan').length,0);
}));

// The background observer stays mounted, but its actions cannot change a setup session.
test('unselected status stays hidden outside status and cannot navigate during setup',()=>bootstrapFixture('en',async f=>{
 f.render();await f.tick();await f.tick();
 const find=(t,p)=>Array.isArray(t)?t.map(x=>find(x,p)).find(Boolean):React.isValidElement(t)?p(t)?t:find(t.props.children,p):null;
 const wrapper=()=>find(f.render(),t=>t.type==='div'&&t.props.children?.type?.name==='UnselectedStatus');
 f.find('AnalysisShell').props.onArea('settings');assert.equal(wrapper().props.hidden,true);
 let before=f.publications.length;wrapper().props.children.props.onSelectProfile('saved');assert.equal(f.publications.length,before);
 f.find('AnalysisShell').props.onArea('status');assert.equal(wrapper().props.hidden,false);
 const open=find(f.render(),t=>t.type==='button'&&t.props.children==='Open setup');open.props.onClick();
 assert.ok(f.find('OnboardingWizard'));assert.equal(wrapper().props.hidden,true);before=f.publications.length;
 wrapper().props.children.props.onSelectProfile('saved');
 assert.equal(f.publications.length,before);assert.ok(f.find('OnboardingWizard'));assert.equal(f.calls.filter(x=>x.action==='scan').length,0);
}));

// Display confirmation updates presentation only; the restored query and scan state remain untouched.
test('personal display host adopts only confirmed defaults and blocks dirty navigation',()=>bootstrapFixture('en',async f=>{
 let finish;f.intercept=async(action,o)=>action==='preferences'&&o.method==='POST'?new Promise(resolve=>finish=()=>resolve(f.response({...f.prefs,display:JSON.parse(o.body).display,revision:4}))):null;
 f.render();await f.tick();await f.tick();let form=f.find('PersonalDisplaySettings');const old=form.props.value.display,before=f.publications.length;
 form.props.onDirty(true);f.find('AnalysisShell').props.onArea('profiles');assert.equal(f.find('AnalysisShell').props.area,'overview');
 const changed={...old,labels:false,units:'binary',detail:4,colorMode:'mtime'},saving=form.props.onSave({group:'display',display:changed});await f.tick();assert.deepEqual(f.find('PersonalDisplaySettings').props.value.display,old);finish();await saving;
 assert.deepEqual(f.find('PersonalDisplaySettings').props.value.display,changed);assert.equal(f.publications.length,before);assert.ok(!f.calls.some(x=>x.action==='scan'));
 form=f.find('PersonalDisplaySettings');form.props.onDirty(false);f.find('AnalysisShell').props.onArea('profiles');assert.equal(f.find('AnalysisShell').props.area,'profiles');
}));

// Settings probes use the saved profile scope, not the historical snapshot or an editor draft.
test('permission settings host binds current profile revision and stays explicit',()=>pollingFixture(async f=>{
 const find=t=>Array.isArray(t)?t.map(find).find(Boolean):React.isValidElement(t)?t.type?.name==='PermissionStatus'?t:find(t.props.children):null;
 const profile={id:'p',revision:3,scope:{kind:'shares',targetIds:['own-share']}};
 f.busy(false);let panel=find(f.renderWith({area:'settings',profile}));assert.deepEqual(panel.props.scope,profile.scope);assert.equal(panel.props.identity,'p:3');assert.equal(panel.props.disabled,false);
 f.busy(true);assert.equal(find(f.renderWith({area:'settings',profile})).props.disabled,true);
 assert.equal(find(f.renderWith({area:'overview',profile})),undefined);assert.equal(f.calls.length,0,'rendering never probes sources or starts a scan');
}));

// The real host mounts private export state only inside Settings and shares its session owner.
test('configuration export host is explicit, session-bound and unmounts on settings leave',()=>bootstrapFixture('en',async f=>{
 f.render();assert.ok(!f.find('ConfigurationExport'));await f.tick();await f.tick();
 f.find('AnalysisShell').props.onArea('settings');const panel=f.find('ConfigurationExport');assert.ok(panel);assert.equal(panel.props.language,'en');assert.equal(panel.props.disabled,false);assert.equal(typeof panel.props.sessionIdentity,'string');assert.ok(panel.props.sessionIdentity.length>0);
 assert.equal(f.calls.filter(c=>c.action==='configuration-export').length,0,'entering settings never exports automatically');
 let release;f.intercept=(action,options)=>action==='preferences'&&options.method==='POST'?new Promise(resolve=>release=()=>resolve(f.response({...f.prefs,...JSON.parse(options.body).appearance,revision:4}))):null;
 const saving=f.find('ThemeSettings').props.onSave('dark');await f.tick();assert.equal(f.find('ConfigurationExport').props.disabled,true);assert.equal(f.find('ConfigurationExport').props.sessionIdentity,panel.props.sessionIdentity);release();await saving;assert.equal(f.find('ConfigurationExport').props.disabled,false);
 f.find('AnalysisShell').props.onArea('overview');assert.ok(!f.find('ConfigurationExport'),'leaving settings unmounts the private download owner');
 f.find('AnalysisShell').props.onArea('settings');f.find('ConfigurationExport').props.onSessionDenied();assert.equal(f.denials(),1);assert.equal(f.calls.filter(c=>c.action==='configuration-export').length,0);
}));

// A confirmed import refreshes shared settings only and never repeats the committed mutation.
test('configuration import host guards draft navigation and refreshes without selection or scan',()=>bootstrapFixture('en',async f=>{
 f.render();await f.tick();await f.tick();assert.ok(!f.find('ConfigurationImport'));f.find('AnalysisShell').props.onArea('settings');let panel=f.find('ConfigurationImport');assert.ok(panel);assert.equal(panel.props.disabled,false);assert.equal(f.calls.filter(x=>x.action?.startsWith('configuration-import')).length,0);
 panel.props.onDirty(true);f.find('AnalysisShell').props.onArea('profiles');assert.equal(f.find('AnalysisShell').props.area,'settings');assert.equal(f.find('ConfigurationImport').props.disabled,false);assert.equal(f.find('ThemeSettings').props.disabled,true);
 const publications=f.publications.length,old=f.find('PersonalDisplaySettings').props.value.display,changed={...old,units:'binary'};let finish;
 f.intercept=(action)=>action==='preferences'?new Promise(resolve=>finish=()=>resolve(f.response({...f.prefs,revision:4,display:changed}))):null;
 const handoff=panel.props.onApplied({created:[],tasksRequireReview:true});await f.tick();assert.deepEqual(f.find('PersonalDisplaySettings').props.value.display,old);finish();await handoff;
 assert.deepEqual(f.find('PersonalDisplaySettings').props.value.display,changed);assert.equal(f.publications.length,publications);assert.equal(f.calls.filter(x=>x.action==='scan'||x.action==='configuration-import-apply').length,0);
 panel=f.find('ConfigurationImport');panel.props.onDirty(false);f.find('AnalysisShell').props.onArea('overview');assert.ok(!f.find('ConfigurationImport'));
}));

// The actual Settings host composes the bundled help without adding any status or external read.
test('offline help settings host follows confirmed language and performs no help request',()=>bootstrapFixture('en',async f=>{
 f.render();await f.tick();await f.tick();const before=f.calls.length;f.find('AnalysisShell').props.onArea('settings');const panel=f.find('HelpSettings');assert.ok(panel);assert.equal(panel.props.language,'en');const html=renderToStaticMarkup(panel);assert.match(html,/Local user guide/);assert.match(html,/About We.Rock.DiskMap/);assert.match(html,/control\.db/);assert.equal(f.calls.length,before);assert.equal(f.calls.filter(x=>x.action==='scan').length,0);
}));

// Terminal polling and the central session owner share one bounded notice lifecycle.
test('local notices stay quiet on success and deduplicate failed jobs across host callbacks',()=>pollingFixture(async f=>{
 f.jobRead=async()=>f.response({...f.outcome('completed'),coverageErrors:0});f.mount();await f.tick();await f.tick();
 assert.equal(f.notices().notices.length,0);const before=f.calls.length;
 const failed={...f.outcome('failed','failed-job'),coverageErrors:0};f.notify(failed);assert.equal(f.notices().notices.length,1);
 f.notify(failed);assert.equal(f.notices().notices.length,1);const notice=f.notices().notices[0];f.notices().onDismiss(notice.id);
 f.notify(failed);assert.equal(f.notices().notices.length,0);assert.equal(f.calls.length,before,'notice updates never request data or mutations');
}));

// Denial revokes admission immediately, before a parent render can unmount the old profile.
test('local notices clear on session denial and reject a stale profile callback',()=>pollingFixture(async f=>{
 const outcome={...f.outcome('interrupted'),coverageErrors:0};f.notify(outcome);assert.equal(f.notices().notices.length,1);
 const stale=f.notify;f.deny();assert.equal(f.denials(),1);assert.equal(f.notices().notices.length,0);
 stale({...outcome,job:{...outcome.job,id:'late-job'}});assert.equal(f.notices().notices.length,0);assert.equal(f.calls.length,0);
}));

// Specific storage recovery reaches the actual job presenter without consuming private warning text.
test('job status exposes fixed bilingual index-space recovery without a restart action',()=>{
 for(const language of ['de','en']) {
  const messages=JSON.parse(readFileSync(new URL(`../src/locales/${language}.json`,import.meta.url),'utf8'));
  const job={state:'failed',scanMode:'full',trigger:'ui',entriesVisited:'7',bytesObserved:'123',warnings:[{code:'INDEX_SPACE_LOW',message:'/private/password/token'}]};
  const html=renderToStaticMarkup(React.createElement(workbench.ScanJobStatus,{job,deduplicated:false,messages,language}));
  assert.match(html,language==='de'?/Speicher|Platz/:/space|storage/i);assert.doesNotMatch(html,/private|password|token|<button|<form/);
 }
});

// The real ProfileView query effect consumes only observer-qualified content boxes.
test('first viewport measurement gates queries and later resizes coalesce',async()=>{
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window,document:globalThis.document,setInterval:globalThis.setInterval,clearInterval:globalThis.clearInterval,ResizeObserver:globalThis.ResizeObserver};
 const make=()=>({states:[],refs:[],effects:[],render(fn,props,run=false){let si=0,ri=0,ei=0;const scheduled=[];React.useState=initial=>{const i=si++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=initial=>this.refs[ri++]??={current:initial};React.useEffect=(effect,deps)=>{const i=ei++;if(run&&(!this.effects[i]||deps.some((v,j)=>v!==this.effects[i].deps[j])))scheduled.push(()=>{this.effects[i]?.cleanup?.();this.effects[i]={deps,cleanup:effect()};});};const tree=fn(props);scheduled.forEach(f=>f());return tree;}});
 const find=(tree,test)=>{if(Array.isArray(tree))return tree.map(x=>find(x,test)).find(Boolean);if(React.isValidElement(tree)){if(test(tree))return tree;return find(tree.props.children,test);}return null;};
 const tick=()=>new Promise(r=>setImmediate(r)),response=data=>new Response(JSON.stringify({contractVersion:1,data}));
 const viewport={},observers=[];let autoMeasure=false;
 const wb=make(),pv=make();let hierarchyCalls=0,failRefresh=false;const hierarchySignals=[],queries=[];
 try{
  globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};globalThis.document={visibilityState:'visible'};globalThis.setInterval=()=>1;globalThis.clearInterval=()=>{};
  globalThis.ResizeObserver=class{constructor(cb){this.cb=cb;observers.push(this);}observe(target){this.target=target;if(autoMeasure)this.cb([{target,contentRect:{width:900,height:500}}]);}disconnect(){this.disconnected=true;}};
  globalThis.fetch=async(url,options)=>{const action=new URL(url,'https://local.invalid').searchParams.get('action'),b=JSON.parse(options.body??'{}');
   if(action==='view'&&b.operation==='read'&&failRefresh)return new Response('{}',{status:503});
   if(action==='view'){const snapshotId=b.operation==='switch'?'next':'s';return response({view:{...storageFixture(),snapshotId,nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'P'},lease:{id:b.newViewId??b.viewId,snapshotId,expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});}
   if(action==='view-navigation')return response({snapshotId:b.snapshotId,rootId:b.rootId,parentId:null,selectedNodeId:b.selectedNodeId,reasons:[],breadcrumb:[{id:'nas',name:'NAS',kind:'nas'}]});
   if(action==='hierarchy'){hierarchyCalls++;hierarchySignals.push(options.signal);queries.push(b.query);const summary={knownBytes:'0',unknownFileCount:'0',fileCount:'0',directoryCount:'0',specialCount:'0'};return response(completeHierarchy(b.query,[{id:'nas',parentId:null,realNodeId:null,name:'Recovered hierarchy',kind:'nas',virtual:true,summary,hasMore:false}]));}
   throw Error(`unexpected ${action}`);
  };
  const props={language:'en',selected:'p',onSelect:()=>{},denied:()=>assert.fail('denied')};wb.render(workbench.Workbench,props);wb.states[0]={dsmBuild:'90080',profiles:[{profile:{id:'p',name:'P',active:true,scope:{kind:'volumes',targetIds:['v']}},latestJobId:null}]};
  const element=()=>find(wb.render(workbench.Workbench,props),e=>e.type?.name==='ProfileView');const render=()=>{const e=element();const tree=pv.render(e.type,e.props,true);find(tree,e=>e.props.className==='map-viewport')?.props.ref(viewport);return tree;};
  render();await tick();await tick();render();render();await tick();
  assert.equal(hierarchyCalls,0,'unmeasured viewport must not issue placeholder query');
  const emit=(observer,width,height)=>observer.cb([{target:observer.target,contentRect:{width,height}}]);
  let observer=observers.at(-1);assert.ok(observer);
  emit(observer,0,0);render();await tick();assert.equal(hierarchyCalls,0);
  emit(observer,900,500);render();await tick();render();assert.equal(hierarchyCalls,1);assert.equal(queries[0].elementBudget,2812);
  emit(observer,900,500);await new Promise(r=>setTimeout(r,170));render();await tick();assert.equal(hierarchyCalls,1);
  emit(observer,1000,500);emit(observer,1100,500);render();await tick();assert.equal(hierarchyCalls,1);
  await new Promise(r=>setTimeout(r,170));render();await tick();assert.equal(hierarchyCalls,2);assert.equal(queries[1].elementBudget,3437);
  // Zero size cancels a queued resize and requires a fresh positive measurement.
  emit(observer,1200,500);emit(observer,0,0);render();await new Promise(r=>setTimeout(r,170));render();await tick();assert.equal(hierarchyCalls,2);
  emit(observer,1100,500);render();await tick();assert.equal(hierarchyCalls,3);
  // A replaced DOM element cannot inherit the previous observer or its delayed callback.
  let tree=render();const replacement={};find(tree,e=>e.props.className==='map-viewport').props.ref(replacement);
  const e=element();tree=pv.render(e.type,e.props,true);assert.equal(observer.disconnected,true);
  emit(observer,1600,700);await new Promise(r=>setTimeout(r,170));pv.render(e.type,e.props,true);await tick();assert.equal(hierarchyCalls,3);
  observer=observers.at(-1);emit(observer,800,400);pv.render(e.type,e.props,true);await tick();assert.equal(hierarchyCalls,4);assert.equal(queries[3].elementBudget,2000);
  // Hidden panels cancel queued work; showing requires a new observer admission.
  emit(observer,1500,700);pv.render(e.type,{...e.props,area:'settings'},true);
  emit(observer,1600,700);await new Promise(r=>setTimeout(r,170));await tick();assert.equal(hierarchyCalls,4);
  pv.render(e.type,e.props,true);await tick();assert.equal(hierarchyCalls,4);
  observer=observers.at(-1);emit(observer,16000,16000);pv.render(e.type,e.props,true);await tick();assert.equal(hierarchyCalls,5);assert.equal(queries[4].elementBudget,10000);
  const controller=element().props.controller;await controller.move({...controller.current.state.view,map:'radial'});
  const radial=element();pv.render(radial.type,radial.props,true);await tick();assert.equal(queries.at(-1).elementBudget,2000);
  // A real snapshot switch invalidates both the pending resize and its old observer.
  const beforeSwitch=hierarchyCalls,oldObserver=observer;
  emit(oldObserver,1200,400);
  const radialTree=pv.render(radial.type,radial.props,true);
  find(radialTree,node=>node.type?.name==='StorageSettings').props.onChooseSnapshot('next');
  await tick();await tick();
  let switched=element();pv.render(switched.type,switched.props,true);
  assert.equal(controller.current.state.view.snapshotId,'next');assert.equal(oldObserver.disconnected,true);
  assert.equal(hierarchyCalls,beforeSwitch,'snapshot admission cannot reuse the previous measurement');
  emit(oldObserver,1300,400);
  await new Promise(resolve=>setTimeout(resolve,170));
  switched=element();pv.render(switched.type,switched.props,true);await tick();
  assert.equal(hierarchyCalls,beforeSwitch,'old timer and callback cannot qualify the new snapshot');
  observer=observers.at(-1);assert.notEqual(observer,oldObserver);
  emit(observer,800,400);pv.render(switched.type,switched.props,true);await tick();
  assert.equal(hierarchyCalls,beforeSwitch+1);assert.equal(queries.at(-1).snapshotId,'next');
  assert.equal(queries.at(-1).elementBudget,533,'new snapshot uses its own first content box');
  // Cleanup rejects late callbacks even when their dimensions are otherwise valid.
  const count=hierarchyCalls;pv.effects.forEach(effect=>effect?.cleanup?.());emit(observer,900,500);await new Promise(r=>setTimeout(r,170));assert.equal(hierarchyCalls,count);


 }finally{pv.effects.forEach(e=>e?.cleanup?.());Object.assign(React,{useState:saved.useState,useRef:saved.useRef,useEffect:saved.useEffect});for(const key of ['fetch','window','document','setInterval','clearInterval','ResizeObserver'])globalThis[key]=saved[key];}
});

// Hold real hierarchy transports while exercising the ProfileView admission and stale-owner guards.
for(const outcome of ['success','error','pending','allocated'])test(`hierarchy first attempt releases secondary readers: ${outcome}`,async()=>{
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window,document:globalThis.document,setInterval:globalThis.setInterval,clearInterval:globalThis.clearInterval,ResizeObserver:globalThis.ResizeObserver};
 const make=()=>({states:[],refs:[],effects:[],render(fn,props,run=false){let si=0,ri=0,ei=0;const scheduled=[];React.useState=initial=>{const i=si++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=initial=>this.refs[ri++]??={current:initial};React.useEffect=(effect,deps)=>{const i=ei++;if(run&&(!this.effects[i]||deps.some((v,j)=>v!==this.effects[i].deps[j])))scheduled.push(()=>{this.effects[i]?.cleanup?.();this.effects[i]={deps,cleanup:effect()};});};const tree=fn(props);scheduled.forEach(f=>f());return tree;}});
 const find=(tree,name)=>{if(Array.isArray(tree))return tree.map(x=>find(x,name)).find(Boolean);if(React.isValidElement(tree)){if(tree.type?.name===name)return tree;return find(tree.props.children,name);}return null;};
 const tick=()=>new Promise(r=>setImmediate(r)),response=data=>new Response(JSON.stringify({contractVersion:1,data}));
 const wb=make(),pv=make(),held=[],events=[],observers=[],viewport={};let area='overview';
 const summary={knownBytes:'0',unknownFileCount:'0',fileCount:'0',directoryCount:'0',specialCount:'0'};
 const settle=(entry,kind='success')=>entry.resolve(kind==='error'?new Response('{}',{status:503}):response(kind==='pending'?{state:'pending',query:entry.query}:completeHierarchy(entry.query,[{id:entry.query.rootId,parentId:null,realNodeId:null,name:'NAS',kind:'nas',virtual:true,summary,hasMore:false}])));
 try{
  globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};globalThis.document={visibilityState:'visible'};globalThis.setInterval=()=>1;globalThis.clearInterval=()=>{};
  globalThis.ResizeObserver=class{constructor(cb){this.cb=cb;observers.push(this);}observe(target){this.target=target;}disconnect(){}};
  globalThis.fetch=async(url,options)=>{const action=new URL(url,'https://local.invalid').searchParams.get('action'),b=JSON.parse(options.body??'{}');events.push(action);
   if(action==='view')return response({view:{...storageFixture(),snapshotId:b.snapshotId??'s',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'P'},lease:{id:b.viewId,snapshotId:b.snapshotId??'s',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});
   if(action==='view-navigation')return response({snapshotId:b.snapshotId,rootId:b.rootId,parentId:null,selectedNodeId:b.selectedNodeId,reasons:[],breadcrumb:[{id:b.rootId,name:'NAS',kind:'nas'}]});
   if(action==='hierarchy')return new Promise(resolve=>held.push({resolve,query:b.query,signal:options.signal}));
   if(action==='size-metrics')return response({snapshotId:b.snapshotId,rootId:b.rootId,defaultMetric:'logical',logical:{selectable:true,method:'st_size',reason:null,qualification:null,knownBytes:'0',unknownFileCount:'0'},allocated:{selectable:true,method:'st_blocks*512',qualification:'g06-dsm90080-kvmx64-st-blocks512-v1',reason:null,knownBytes:'0',unknownFileCount:'0'}});
   throw Error(`unexpected ${action}`);
  };
  const props={language:'en',selected:'p',onSelect:()=>{},denied:()=>assert.fail('denied')};wb.render(workbench.Workbench,props);wb.states[0]={dsmBuild:'90080',profiles:[{profile:{id:'p',name:'P',active:true,scope:{kind:'volumes',targetIds:['v']}},latestJobId:null}]};
  const element=()=>find(wb.render(workbench.Workbench,props),'ProfileView');
  const render=()=>{const e=element(),tree=pv.render(e.type,{...e.props,area},true);attachFixtureViewport(tree,viewport);return tree;};
  const pump=async()=>{render();await tick();render();await tick();return render();};
  await pump();const controller=element().props.controller;
  if(outcome==='allocated'){await controller.move({...controller.current.state.view,metric:'allocated'});await pump();}
  assert.equal(events.filter(x=>x==='size-metrics').length,0,'no secondary or primary read without a measured viewport');
  let o=observers.at(-1);o.cb([{target:o.target,contentRect:{width:900,height:500}}]);let tree=await pump();
  assert.equal(held.length,1);assert.equal(find(tree,'SearchResultList').props.disabled,true);
  assert.equal(events.filter(x=>x==='size-metrics').length,outcome==='allocated'?1:0,'only allocated primary qualification may precede hierarchy');
  settle(held[0],outcome==='allocated'?'success':outcome);await tick();tree=await pump();
  assert.equal(find(tree,'SearchResultList').props.disabled,false,'success, error and pending all release the independent list');
  assert.equal(events.filter(x=>x==='size-metrics').length,outcome==='allocated'?2:1);
  if(outcome==='success'){
   // Selection alone has no hierarchy request and must retain the completed admission.
   await controller.move({...controller.current.state.view,selectedNodeId:'selected'});tree=await pump();assert.equal(held.length,1);assert.equal(find(tree,'SearchResultList').props.disabled,false);
   // A-B-A must not reuse A's prior completion, and a late B cannot release the new A.
   await controller.move({...controller.current.state.view,rootId:'b',selectedNodeId:null});tree=await pump();assert.equal(find(tree,'SearchResultList').props.disabled,true);
   await controller.move({...controller.current.state.view,rootId:'nas'});tree=await pump();assert.equal(held.length,3);assert.equal(find(tree,'SearchResultList').props.disabled,true);
   settle(held[1]);await tick();tree=await pump();assert.equal(find(tree,'SearchResultList').props.disabled,true);settle(held[2]);await tick();tree=await pump();assert.equal(find(tree,'SearchResultList').props.disabled,false);
   // A changed budget closes admission synchronously; hidden/showing cannot inherit it either.
   o.cb([{target:o.target,contentRect:{width:1000,height:500}}]);await new Promise(r=>setTimeout(r,170));tree=await pump();assert.equal(held.length,4);assert.equal(find(tree,'SearchResultList').props.disabled,true);
   area='settings';await pump();settle(held[3]);await tick();area='overview';tree=await pump();assert.equal(find(tree,'SearchResultList').props.disabled,true);
   o=observers.at(-1);o.cb([{target:o.target,contentRect:{width:1000,height:500}}]);tree=await pump();assert.equal(held.length,5);assert.equal(find(tree,'SearchResultList').props.disabled,true);settle(held[4]);await tick();tree=await pump();assert.equal(find(tree,'SearchResultList').props.disabled,false);
   // Explicit refresh changes the retry owner even with the same root, lease and budget.
   find(tree,'ScanControls').props.reload();await tick();tree=await pump();assert.equal(held.length,6);assert.equal(find(tree,'SearchResultList').props.disabled,true);settle(held[5]);await tick();tree=await pump();assert.equal(find(tree,'SearchResultList').props.disabled,false);
   // A new snapshot and lease require their own content box and their own successful attempt.
   await controller.move({...controller.current.state.view,snapshotId:'next'});tree=await pump();assert.equal(held.length,6);assert.equal(find(tree,'SearchResultList').props.disabled,true);
   o=observers.at(-1);o.cb([{target:o.target,contentRect:{width:1000,height:500}}]);tree=await pump();assert.equal(held.length,7);assert.equal(held[6].query.snapshotId,'next');assert.equal(find(tree,'SearchResultList').props.disabled,true);settle(held[6]);await tick();tree=await pump();assert.equal(find(tree,'SearchResultList').props.disabled,false);

  }
 }finally{pv.effects.forEach(e=>e?.cleanup?.());Object.assign(React,{useState:saved.useState,useRef:saved.useRef,useEffect:saved.useEffect});for(const k of ['fetch','window','document','setInterval','clearInterval','ResizeObserver'])globalThis[k]=saved[k];}
});

// Exercise the production host handoff, including response order and supersession.
for(const outcome of ['early','late','fallback','resize','late-denied','superseded','error','pending'])test(`same-lease hierarchy handoff: ${outcome}`,async()=>{
 const saved={useState:React.useState,useRef:React.useRef,useEffect:React.useEffect,fetch:globalThis.fetch,window:globalThis.window,document:globalThis.document,setInterval:globalThis.setInterval,clearInterval:globalThis.clearInterval,ResizeObserver:globalThis.ResizeObserver};
 const make=()=>({states:[],refs:[],effects:[],render(fn,props,run=false){let si=0,ri=0,ei=0;const scheduled=[];React.useState=initial=>{const i=si++;if(!(i in this.states))this.states[i]=typeof initial==='function'?initial():initial;return[this.states[i],v=>this.states[i]=typeof v==='function'?v(this.states[i]):v];};React.useRef=initial=>this.refs[ri++]??={current:initial};React.useEffect=(effect,deps)=>{const i=ei++;if(run&&(!this.effects[i]||deps.some((v,j)=>v!==this.effects[i].deps[j])))scheduled.push(()=>{this.effects[i]?.cleanup?.();this.effects[i]={deps,cleanup:effect()};});};const tree=fn(props);scheduled.forEach(f=>f());return tree;}});
 const find=(tree,name)=>{if(Array.isArray(tree))return tree.map(x=>find(x,name)).find(Boolean);if(React.isValidElement(tree)){if(tree.type?.name===name)return tree;return find(tree.props.children,name);}return null;};
 const tick=()=>new Promise(r=>setImmediate(r)),response=data=>new Response(JSON.stringify({contractVersion:1,data}));
 const wb=make(),pv=make(),held=[],events=[],observers=[],viewport={};let area='overview',navigationHeld=null;
 const summary={knownBytes:'0',unknownFileCount:'0',fileCount:'0',directoryCount:'0',specialCount:'0'};
 const settle=(entry,kind='success')=>entry.resolve(kind==='error'?new Response('{}',{status:503}):response(kind==='pending'?{state:'pending',query:entry.query}:completeHierarchy(entry.query,[{id:entry.query.rootId,parentId:null,realNodeId:null,name:'NAS',kind:'nas',virtual:true,summary,hasMore:false}])));
 try{
  globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},parent:{SYNO:{SDS:{Session:{SynoToken:'fixture'}}}}};globalThis.document={visibilityState:'visible'};globalThis.setInterval=()=>1;globalThis.clearInterval=()=>{};
  globalThis.ResizeObserver=class{constructor(cb){this.cb=cb;observers.push(this);}observe(target){this.target=target;}disconnect(){}};
  globalThis.fetch=async(url,options)=>{const action=new URL(url,'https://local.invalid').searchParams.get('action'),b=JSON.parse(options.body??'{}');events.push(action);
   if(action==='view')return response({view:{...storageFixture(),snapshotId:b.snapshotId??'s',nodes:[],truncated:false,profileRevision:1,currentProfileRevision:1,profileChanged:false,capturedProfileName:'P'},lease:{id:b.viewId,snapshotId:b.snapshotId??'s',expiresAt:'2026-10-20T00:00:00Z'},newCompleteSnapshotId:null,selection:{nodeId:null,fallback:'none'}});
   if(action==='view-navigation'){if(b.rootId==='child'&&b.selectedNodeId===null)return new Promise(resolve=>navigationHeld={resolve,body:b});return response({snapshotId:b.snapshotId,rootId:b.rootId,parentId:null,selectedNodeId:b.selectedNodeId,reasons:[],breadcrumb:[{id:b.rootId,name:'NAS',kind:'nas'}]});}
   if(action==='hierarchy')return new Promise(resolve=>held.push({resolve,query:b.query,signal:options.signal}));
   if(action==='size-metrics')return response({snapshotId:b.snapshotId,rootId:b.rootId,defaultMetric:'logical',logical:{selectable:true,method:'st_size',reason:null,qualification:null,knownBytes:'0',unknownFileCount:'0'},allocated:{selectable:true,method:'st_blocks*512',qualification:'g06-dsm90080-kvmx64-st-blocks512-v1',reason:null,knownBytes:'0',unknownFileCount:'0'}});
   throw Error(`unexpected ${action}`);
  };
  const props={language:'en',selected:'p',onSelect:()=>{},denied:()=>assert.fail('denied')};wb.render(workbench.Workbench,props);wb.states[0]={dsmBuild:'90080',profiles:[{profile:{id:'p',name:'P',active:true,scope:{kind:'volumes',targetIds:['v']}},latestJobId:null}]};
  const element=()=>find(wb.render(workbench.Workbench,props),'ProfileView');
  const render=()=>{const e=element(),tree=pv.render(e.type,{...e.props,area},true);attachFixtureViewport(tree,viewport);return tree;};
  const pump=async()=>{render();await tick();render();await tick();return render();};
  await pump();const controller=element().props.controller;
  let o=observers.at(-1);o.cb([{target:o.target,contentRect:{width:900,height:500}}]);let tree=await pump();
  assert.equal(held.length,1);
  held[0].resolve(response(completeHierarchy(held[0].query,[
   {id:'nas',parentId:null,realNodeId:null,name:'NAS',kind:'nas',virtual:true,summary:{...summary,directoryCount:'1'},hasMore:false},
   {id:'child',parentId:'nas',realNodeId:'child',name:'Child',kind:'directory',virtual:false,summary:{...summary,directoryCount:'1'},hasMore:false}
  ])));await tick();tree=await pump();
  find(tree,'AnalysisNodeList').props.onOpen({id:'child',kind:'directory',realNodeId:'child'});await tick();
  assert.ok(navigationHeld);assert.equal(held.length,2,'hierarchy starts while navigation is pending');assert.equal(controller.current.state.view.rootId,'nas');
  const pending=held[1];
  if(outcome==='early')settle(pending);
  if(outcome==='resize'){o.cb([{target:o.target,contentRect:{width:1000,height:500}}]);await new Promise(r=>setTimeout(r,170));await pump();}
  navigationHeld.resolve(response({snapshotId:'s',rootId:outcome==='fallback'?'nas':'child',parentId:'nas',selectedNodeId:null,reasons:outcome==='fallback'?['ROOT_UNAVAILABLE']:[],breadcrumb:outcome==='fallback'?[{id:'nas',name:'NAS',kind:'nas'}]:[{id:'nas',name:'NAS',kind:'nas'},{id:'child',name:'Child',kind:'directory'}]}));await tick();tree=await pump();
  if(['fallback','resize'].includes(outcome)){
   assert.equal(pending.signal.aborted,true);pending.resolve(new Response('{}',{status:403}));await tick();await pump();
   if(outcome==='resize')assert.ok(held.some((x,i)=>i>1&&x.query.rootId==='child'),'new budget uses normal read');
  }else if(outcome==='superseded'){
   await controller.move({...controller.current.state.view,rootId:'nas'});await pump();assert.equal(pending.signal.aborted,true);
   pending.resolve(new Response('{}',{status:403}));await tick();await pump();assert.equal(controller.current.state.view.rootId,'nas');
  }else if(outcome==='late-denied'){
   pv.effects.forEach(e=>e?.cleanup?.());assert.equal(pending.signal.aborted,true);pending.resolve(new Response('{}',{status:403}));await tick();
  }else{
   assert.equal(held.length,2,'confirmed effect consumes the single promise');
   if(outcome==='late'){
    assert.equal(find(tree,'SearchResultList').props.disabled,true);
    // Selection cancellation must not revoke an already transferred hierarchy owner.
    await controller.move({...controller.current.state.view,selectedNodeId:'child'});await pump();assert.equal(pending.signal.aborted,false);settle(pending);
   }
   if(outcome==='error'||outcome==='pending')settle(pending,outcome);
   await tick();tree=await pump();assert.equal(held.length,2);assert.equal(find(tree,'SearchResultList').props.disabled,false);
  }

 }finally{pv.effects.forEach(e=>e?.cleanup?.());Object.assign(React,{useState:saved.useState,useRef:saved.useRef,useEffect:saved.useEffect});for(const k of ['fetch','window','document','setInterval','clearInterval','ResizeObserver'])globalThis[k]=saved[k];}
});
