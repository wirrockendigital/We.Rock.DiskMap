import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import {mkdirSync,mkdtempSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import * as jsx from 'react/jsx-runtime';
// Compile the production icon and inspect its rendered contract with deterministic hook state.
const web=fileURLToPath(new URL('../',import.meta.url)),tmp=(mkdirSync(join(web,'.test-build'),{recursive:true}),mkdtempSync(join(web,'.test-build/snapshot-icon-')));
let source;
try{
 execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','esnext','--moduleResolution','bundler','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--rootDir',join(web,'src'),'--outDir',tmp,join(web,'src/design/SnapshotUsageButton.tsx')],{stdio:'pipe'});
 source=readFileSync(join(tmp,'design/SnapshotUsageButton.js'),'utf8').replace(/import \{([^}]+)\} from ["']([^"']+)["'];/g,(_,names,path)=>`const {${names.replace(/ as /g,':')}}=require('${path}');`).replace('export function SnapshotUsageButton','function SnapshotUsageButton')+'\nexports.SnapshotUsageButton=SnapshotUsageButton;';
}finally{rmSync(tmp,{recursive:true,force:true});}

function icon(state,share='video',observedShare=share,language='de'){
 let index=0;const values=[false,null,false,false,{share:observedShare,status:{state,supported:true,scheduled:false,count:state==='readable'?19:0}}];
 const exports={};vm.runInNewContext(source,{exports,AbortController,require:name=>{
  if(name==='react')return {useState:()=>[values[index++],()=>{}],useEffect:()=>{},useRef:()=>({current:null})};
  if(name==='react/jsx-runtime')return jsx;
  if(name==='../snapshot-usage')return {unknownSnapshotStatus:()=>({state:'unknown',supported:null,scheduled:null,count:null})};
  if(name==='./model')return {};
  if(name==='./size-unit')return {useSizeFormatter:()=>()=>''};
  if(name==='@radix-ui/react-icons')return {LayersIcon:()=>null,Cross2Icon:()=>null};
  throw Error(name);
 }});
 return exports.SnapshotUsageButton({share,language}).props.children[0];
}
test('snapshot icon uses the recycle state classes and accessible disabled explanations',()=>{
 for(const state of ['readable','missing','denied','unknown'])for(const language of ['de','en']){
  const wrapper=icon(state,'video','video',language),button=wrapper.props.children;
  assert.equal(wrapper.props.className,'disk-recycle-'+state);
  assert.equal(wrapper.props['data-snapshot-state'],state);
  assert.equal(button.props.disabled,state==='missing'||state==='denied');
  assert.ok(button.props['aria-label'].includes(wrapper.props.title));
 }
 assert.match(icon('readable').props.title,/deaktiviert.*19/);
 assert.match(icon('missing').props.title,/keine Snapshots/);
 assert.match(icon('denied').props.title,/Keine Berechtigung/);
});
test('a changed share immediately discards the previous share color',()=>{
 assert.equal(icon('missing','new-share','video').props['data-snapshot-state'],'unknown');

 const wrapper=icon('readable','', 'video');assert.equal(wrapper.props.children.props.disabled,true);
});
