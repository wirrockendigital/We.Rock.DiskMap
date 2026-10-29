import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync,mkdirSync,mkdtempSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import vm from 'node:vm';
import * as jsx from 'react/jsx-runtime';
// Inspect the compiled production icons with deterministic observations, including stale sessions.
const web=fileURLToPath(new URL('../',import.meta.url)),tmp=(mkdirSync(join(web,'.test-build'),{recursive:true}),mkdtempSync(join(web,'.test-build/share-status-')));
let source;
try{
 execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','esnext','--moduleResolution','bundler','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--rootDir',join(web,'src'),'--outDir',tmp,join(web,'src/design/ShareStatusIcons.tsx')],{stdio:'pipe'});
 source=readFileSync(join(tmp,'design/ShareStatusIcons.js'),'utf8').replace(/import \{([^}]+)\} from ["']([^"']+)["'];/g,(_,names,path)=>`const {${names.replace(/ as /g,':')}}=require('${path}');`).replace(/export /g,'')+'\nObject.assign(exports,{StatusIcon,driveDescription,ShareStatusIcons});';
}finally{rmSync(tmp,{recursive:true,force:true});}
function component(observed=null){
 const exports={};vm.runInNewContext(source,{exports,window:{},require:name=>{
  if(name==='react')return {useState:()=>[observed,()=>{}],useEffect:()=>{}};
  if(name==='react/jsx-runtime')return jsx;
  if(name==='../session')return {currentToken:()=> 'current'};
  if(name==='../snapshot-status-cache')return {};
  if(name==='../snapshot-usage')return {unknownSnapshotStatus:()=>({state:'unknown',supported:null,scheduled:null,count:null})};
  if(name==='../drive-versioning')return {};
  if(name==='@radix-ui/react-icons')return {TrashIcon:()=>null,LayersIcon:()=>null,CounterClockwiseClockIcon:()=>null};
  throw Error(name);
 }});return exports;
}
test('all status icons expose accessible explanations without mutation buttons',()=>{
 const c=component();for(const kind of ['recycle','snapshot','drive'])for(const state of ['readable','missing','denied','unknown']){
  const icon=c.StatusIcon({kind,state,title:'Status explanation'});
  assert.equal(icon.type,'span');assert.equal(icon.props.role,'img');assert.equal(icon.props.tabIndex,0);
  assert.equal(icon.props['data-state'],state);assert.equal(icon.props['aria-label'],icon.props.title);
  assert.equal(Boolean(icon.props.children[1]),state==='unknown');
 }
});
test('Drive descriptions distinguish every state in both languages and qualify My Drive',()=>{
 const {driveDescription:d}=component();
 for(const lang of ['de','en']){
  const texts=[{state:'readable',teamFolder:true,maxVersions:8},{state:'missing',teamFolder:true,maxVersions:0},{state:'missing',teamFolder:false,maxVersions:null},{state:'unknown'},{state:'denied'}].map(s=>d(s,lang));
  assert.equal(new Set(texts).size,5);assert.match(texts[0],/8/);assert.match(d({state:'readable',teamFolder:true,maxVersions:8,personal:true},lang),/My Drive/);
 }
});
test('share changes and session changes immediately discard stale snapshot status',()=>{
 const props={share:{id:'s',name:'Docs',recycleState:'denied'},drive:{state:'missing',teamFolder:true,maxVersions:0},language:'de'};
 for(const observed of [null,{name:'Old',token:'current',status:{state:'missing'}},{name:'Docs',token:'previous',status:{state:'readable'}}]){
  const icons=component(observed).ShareStatusIcons(props).props.children;
  assert.equal(icons[0].props.state,'denied');assert.equal(icons[1].props.state,'unknown');assert.equal(icons.length,3);
 }
 const icons=component({name:'Docs',token:'current',status:{state:'readable',supported:true,scheduled:false,count:5}}).ShareStatusIcons(props).props.children;
 assert.equal(icons[1].props.state,'readable');assert.match(icons[1].props.title,/5 Snapshots/);
});
