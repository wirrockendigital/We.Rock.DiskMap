import React from 'react';
import {createRoot} from 'react-dom/client';
import {DiskMapWorkspace} from '../../src/design/DiskMapWorkspace';
import {defaultWorkspace} from '../../src/design/workspace-state';
import {PackedMap} from '../../src/design/packed-map';
// Keep recorded totals positive while the real packed map hides its only system file.
export function mount(){
 const current={id:'root',name:'System-only share',kind:'share',bytesExact:'82'},map=new PackedMap(2);
 map.append([{id:'root',parent:null,name:current.name,kind:'share',bytes:null,buckets:['0','0','0','0']},{id:'1',parent:'root',name:'.DS_Store',kind:'file',bytes:null,buckets:['0','82','0','0']}]);map.seal('root');
 function App(){const [workspace,set]=React.useState(defaultWorkspace()),[mode,setMode]=React.useState('treemap');map.visibility(workspace.showRecycle,workspace.showSystem);const noop=()=>{};
  return <DiskMapWorkspace volumes={[current]} initialId="root" live={{workspace,onWorkspace:patch=>set(old=>({...old,...patch})),current,mapRoot:map.summary('root'),path:[current],entries:[],volumes:[current],layoutReady:true,sidebars:{left:false,right:true},loading:false,busy:false,error:'',statusText:'',canBack:false,navigate:noop,back:noop,search:noop,scan:noop,rescan:noop,canRescan:false,debug:'',sourceName:current.name,map:mode,onMap:setMode}}/>;
 }
 document.getElementById('root')?.remove();const host=document.createElement('div');host.id='root';document.body.append(host);createRoot(host).render(<App/>);
}
