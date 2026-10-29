import assert from 'node:assert/strict';
import {mkdir,mkdtemp,rm} from 'node:fs/promises';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
import {rolldown} from 'rolldown';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Exercise the actual workspace across first scan, cached rescan, and terminal state.
await mkdir(join(new URL('../',import.meta.url).pathname,'.test-build'),{recursive:true});
const directory=await mkdtemp(join(new URL('../',import.meta.url).pathname,'.test-build/scan-presentation-'));
try {
 const bundle=await rolldown({cwd:new URL('../',import.meta.url).pathname,input:'src/design/DiskMapWorkspace.tsx',platform:'node',external:['react','react/jsx-runtime'],transform:{jsx:{runtime:'automatic'}}});
 await bundle.write({file:join(directory,'workspace.mjs'),format:'esm'});await bundle.close();
 const {DiskMapWorkspace}=await import(pathToFileURL(join(directory,'workspace.mjs')));
 const root={id:'root',name:'Test share',kind:'share',bytesExact:'100',children:[{id:'file',name:'Test.txt',kind:'file',bytesExact:'100'}]};
 const noop=()=>{};
 const base={mapRoot:root,current:root,path:[root],entries:root.children,volumes:[root],loading:false,busy:false,error:'',statusText:'Index wird aufgebaut',canBack:false,navigate:noop,back:noop,search:noop,scan:noop,rescan:noop,canRescan:false,debug:'',sourceName:'Test share',map:'treemap',cancelScan:noop,cacheNotice:'Ergebnis aus dem Cache vom 13.10.2026 um 13:34 Uhr',scanning:true,scanProgress:{label:'Karte und Suchindex werden aufgebaut',entries:'10',bytes:'100',paused:false,phase:'aggregating'}};
 const html=live=>renderToStaticMarkup(React.createElement(DiskMapWorkspace,{volumes:[root],initialId:'root',native:true,live}));
 const cached=html(base);
 assert.match(cached,/<canvas/);assert.doesNotMatch(cached,/class="disk-scan-center"/);assert.doesNotMatch(cached,/disk-cache-notice|disk-live-status/);assert.match(cached,/Meldungen/);assert.doesNotMatch(cached,/Ergebnis aus dem Cache/);assert.match(cached,/Karte und Suchindex werden aufgebaut/);
 // The independent file-list loader must not render stale legacy search rows during SSR.
 const list=html({...base,layout:'list',entries:[{id:'stale',name:'Wrong search.wav',kind:'file',bytesExact:'1'}]});assert.doesNotMatch(list,/Wrong search.wav/);
 const first=html({...base,current:null,path:[],cacheNotice:''});
 assert.match(first,/class="disk-scan-center"/);assert.doesNotMatch(first,/<canvas/);
 const complete=html({...base,scanning:false,scanProgress:undefined,statusText:'',canRescan:true});
 assert.match(complete,/<canvas/);assert.doesNotMatch(complete,/class="disk-scan-center"|im Hintergrund aktualisiert|Scanfortschritt/);
 console.log('PASS: first-scan progress, usable cached map during rescan, terminal map, cache provenance');
}finally{await rm(directory,{recursive:true,force:true});}
