import assert from 'node:assert/strict';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
import {rolldown} from 'rolldown';
// A zero-size other-area descendant must not overwrite its containing volume's exact total.
const dir=await mkdtemp(join(tmpdir(),'diskmap-source-size-'));
try{
 const bundle=await rolldown({cwd:new URL('../',import.meta.url).pathname,input:'src/design/source-sizes.ts',platform:'node'});await bundle.write({file:join(dir,'sizes.mjs'),format:'esm'});await bundle.close();
 const {recordedSourceSizes}=await import(pathToFileURL(join(dir,'sizes.mjs')));
 const file={id:'f',name:'movie.mov',sourceKind:'file',bytesExact:'70'};
 const folder={id:'d',name:'Media',sourceKind:'directory',bytesExact:'70',children:[file]};
 const share={id:'s',name:'Data',shareId:'share',sourceKind:'share',bytesExact:'70',children:[folder]};
 const other={id:'o',name:'Other',sourceKind:'other',bytesExact:'30',children:[{id:'r',name:'.',sourceKind:'directory',bytesExact:'30',children:[{id:'g',name:'Remaining',sourceKind:'group',bytesExact:'0'}]}]};
 const volume={id:'v',name:'Volume 2',volumeId:'vol2',sourceKind:'volume',bytesExact:'100',children:[share,other]};
 const root={id:'nas',name:'NAS',sourceKind:'nas',bytesExact:'100',children:[volume]};
 const inventory={volumes:[{id:'vol2',name:'volume2'}],shares:[{id:'share',name:'Data',volumeId:'vol2'}]};
 for(const [current,path] of [[root,[root]],[volume,[root,volume]],[folder,[root,volume,share,folder]]]){
  const sizes=recordedSourceSizes(current,path,inventory);assert.equal(sizes.get('source:volumes:vol2:.'),'100');assert.equal(sizes.get('source:shares:share:.'),'70');assert.equal(sizes.get('source:folder:share:Media'),'70');assert(!sizes.has('source:folder:share:Media/movie.mov'));
 }
 console.log('PASS exact source sizes, zero residual, other areas, file exclusion and selected subtree');
}finally{await rm(dir,{recursive:true,force:true});}
