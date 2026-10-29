import {test} from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync,readFileSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
const root=fileURLToPath(new URL('../../',import.meta.url)),web=join(root,'web'),temp=mkdtempSync(join(web,'.help-test-'));let Help,Local,topics;
// Compile and load the actual closed help module graph while every network entry point throws.
const saved={fetch:globalThis.fetch,XMLHttpRequest:globalThis.XMLHttpRequest,WebSocket:globalThis.WebSocket,EventSource:globalThis.EventSource};let network=0;
const denied=()=>{network++;throw Error('OFFLINE_NETWORK_FORBIDDEN');};Object.assign(globalThis,{fetch:denied,XMLHttpRequest:class{constructor(){denied();}},WebSocket:class{constructor(){denied();}},EventSource:class{constructor(){denied();}}});
try{mkdirSync(join(temp,'features/settings'),{recursive:true});writeFileSync(join(temp,'package.json'),'{"type":"commonjs"}');for(const f of ['AboutPanel.tsx','AboutData.ts','LocalHelp.tsx','features/settings/OfflineHelpContent.tsx','features/settings/help-topics.json','features/settings/HelpSettings.tsx'])cpSync(join(web,'src',f),join(temp,f));cpSync(join(web,'src/locales'),join(temp,'locales'),{recursive:true});cpSync(join(web,'src/features/recovery'),join(temp,'features/recovery'),{recursive:true});execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--strict','--skipLibCheck','--esModuleInterop','--resolveJsonModule','--jsx','react-jsx','--target','ES2022','--module','Node16','--moduleResolution','Node16','--outDir',join(temp,'out'),join(temp,'LocalHelp.tsx'),join(temp,'features/settings/HelpSettings.tsx')],{stdio:'pipe'});const require=createRequire(import.meta.url);Help=require(join(temp,'out/features/settings/HelpSettings.js')).HelpSettings;Local=require(join(temp,'out/LocalHelp.js')).LocalHelp;topics=require(join(temp,'out/features/settings/OfflineHelpContent.js')).helpTopics;}finally{rmSync(temp,{recursive:true,force:true});Object.assign(globalThis,saved);}
test('both full catalogues have identical unique topic coverage and meaningful translated paragraphs',()=>{
 const required=['window','scan','navigation','filters','filestation','measurement','profiles','recycle','categories','snapshot-report','snapshots','storage','settings','support','photos','session','configuration','scheduler','recovery'];
 assert.deepEqual(topics('de').map(x=>x.id),required);assert.deepEqual(topics('en').map(x=>x.id),required);
 for(const language of ['de','en'])for(const t of topics(language)){assert.ok(t.title.length>4);assert.ok(t.paragraphs.length>=1);assert.ok(t.paragraphs.every(p=>p.length>=30));assert.ok(new TextEncoder().encode(t.paragraphs.join('')).length<6000);}
 for(let i=0;i<required.length;i++)assert.notEqual(topics('de')[i].title,topics('en')[i].title);
});
test('real settings and session-gate help render offline with no external resources or mutations',()=>{
 const fetch=globalThis.fetch;globalThis.fetch=denied;
 try{for(const language of ['de','en'])for(const Component of [Help,Local]){const html=renderToStaticMarkup(React.createElement(Component,{language}));for(const t of topics(language))assert.ok(html.includes(t.title));assert.doesNotMatch(html,/<(?:img|iframe|script|link)\b|(?:href|src)="(?:https?:|\/\/)|<form\b/);assert.match(html,/4 MiB/);assert.match(html,/SYNOPKG_PKGVAR/);assert.match(html,/control\.db/);assert.match(html,/generations\//);assert.match(html,/upgrades\//);assert.match(html,/run\/control\.sock/);assert.match(html,/diskmapd\.pid/);assert.ok(new TextEncoder().encode(html).length<80000);}
 assert.equal(network,0);}finally{globalThis.fetch=fetch;}
});
test('about composition retains unknown versions and partial compatibility rather than fabricating a live capability',()=>{
 for(const language of ['de','en']){const html=renderToStaticMarkup(React.createElement(Help,{language}));assert.match(html,language==='de'?/Unbekannt/:/Unknown/);assert.match(html,/90080/);assert.match(html,language==='de'?/keine allgemeine DSM-Kompatibilitäts- oder Releasefreigabe/:/not universal DSM compatibility or release approval/);assert.match(html,language==='de'?/keine Appversionsnummern/:/not application versions/);assert.match(html,language==='de'?/gesonderte Zustimmung/:/separate consent/);assert.match(html,language==='de'?/nicht automatisch reversibel/:/not automatically reversible/);}
});
test('documented package-relative data names are grounded in actual runtime and migration sources',()=>{
 const manager=readFileSync(join(root,'internal/jobs/manager.go'),'utf8'),migration=readFileSync(join(root,'internal/migration/upgrade.go'),'utf8'),lifecycle=readFileSync(join(root,'packaging/scripts/start-stop-status'),'utf8');
 assert.match(manager,/"control\.db"/);assert.match(manager,/"generations"/);assert.match(migration,/"upgrades"/);assert.match(lifecycle,/SYNOPKG_PKGVAR/);assert.match(lifecycle,/run\/control\.sock/);assert.match(lifecycle,/diskmapd\.pid/);
});
