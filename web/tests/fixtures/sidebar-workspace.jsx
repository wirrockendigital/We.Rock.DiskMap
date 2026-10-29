import React from 'react';
import {createRoot} from 'react-dom/client';
import {defaultWorkspace} from '../../src/design/workspace-state';
import {DiskMapWorkspace} from '../../src/design/DiskMapWorkspace';
// Local browser fixture uses production components with deterministic recorded data.
export function mount(header=false){
  const media={id:'media',name:'Aufnahmen',kind:'folder',bytesExact:'100',children:[{id:'movie',name:'Interview.mov',kind:'file',bytesExact:'90'},{id:'audio',name:'Master.wav',kind:'file',bytesExact:'10'}]};
  const share={id:'share',name:'volume-2-folder-2',kind:'share',bytesExact:'100',children:[media]},volume={id:'v2',name:'Volume 2',kind:'volume',bytesExact:'100',children:[share]},nas={id:'nas',name:'NAS',kind:'folder',bytesExact:'100',children:[volume]};
  const nodes={nas,v2:volume,share,media},paths={nas:[nas],v2:[nas,volume],share:[nas,volume,share],media:[nas,volume,share,media]};
  const loader=async(id,cursor,signal)=>{await new Promise(r=>setTimeout(r,id==='share'?150:15));signal.throwIfAborted();return {rootId:id,items:nodes[id].children??[],nextCursor:null};};
  // Supply the same controlled workspace state as the native app.
  function App(){const [workspace,setWorkspace]=React.useState({...defaultWorkspace(),expanded:['v2']}),[id,setId]=React.useState('v2'),[sidebars,setSidebars]=React.useState({left:true,right:true}),[running,setRunning]=React.useState(false);const noop=()=>{};return React.createElement(DiskMapWorkspace,{volumes:[nas],initialId:'nas',live:{layoutReady:true,workspace,onWorkspace:patch=>setWorkspace(old=>({...old,...patch})),...(header?{selectedStamp:{state:'complete',endedAt:new Date(Date.now()-3*86400000).toISOString()},freshness:{value:7,unit:'days'},scanning:running,scanProgress:running?{label:'Scan läuft',entries:'42',bytes:'100',paused:false}:undefined,cancelScan:()=>setRunning(false),scanNas:()=>{window.headerAction='nas';setRunning(true);}}:{}),current:nodes[id],path:paths[id],entries:[],volumes:[nas],sidebars,onSidebars:setSidebars,readChildren:loader,snapshotKey:'fixture-generation',loading:false,busy:false,error:'',statusText:'',canBack:id!=='nas',navigate:n=>setId(n.id),back:()=>setId('nas'),search:noop,scan:()=>{throw Error('Unexpected scan')},rescan:()=>{if(header){window.headerAction=id;setRunning(true);}},canRescan:true,debug:'',sourceName:'NAS',map:'treemap'}});}
  document.getElementById('root')?.remove();const host=document.createElement('div');host.id='root';document.body.append(host);createRoot(host).render(React.createElement(App));
}
