import React from 'react';
import {createRoot} from 'react-dom/client';
import {DiskMapWorkspace} from '../../src/design/DiskMapWorkspace';
import {defaultWorkspace} from '../../src/design/workspace-state';
// Exercise the actual workspace surface with controlled inventory outcomes, without DSM ACL changes.
export function mount(){
 function App(){
  const [setup,setSetup]=React.useState('permissions'),[busy,setBusy]=React.useState(false),[failed,setFailed]=React.useState(false);
  const shares=['Dokumente','Fotos','Videos','Projekte'].map((name,i)=>({id:'s'+i,name,kind:'share',unavailable:true}));
  const nas={id:'nas',name:'NAS',kind:'volume',children:setup==='first'?[{id:'v1',name:'Volume 1',kind:'volume',children:shares}]:[]};
  const noop=()=>{};window.setupSetMode=setSetup;
  const refresh=()=>{window.setupChecks=(window.setupChecks??0)+1;setBusy(true);setFailed(false);setTimeout(()=>{setBusy(false);if(window.setupFail)setFailed(true);else setSetup('first');},200);};
  return <DiskMapWorkspace volumes={[nas]} initialId="nas" live={{setup,inventoryRefreshing:busy,inventoryError:failed,refreshInventory:refresh,treeRoot:nas,treeNavigate:n=>{window.setupSelected=n.name;},workspace:defaultWorkspace(),layoutReady:true,sidebars:{left:false,right:false},current:null,path:[nas],entries:[],volumes:[nas],loading:false,busy:false,error:'',statusText:'',canBack:false,navigate:noop,back:noop,search:noop,scan:()=>{throw Error('Unexpected scan');},rescan:()=>{throw Error('Unexpected scan');},canRescan:false,debug:'',sourceName:'NAS',map:'treemap'}}/>;
 }
 const old=document.getElementById('root');old.hidden=true;const host=document.createElement('div');host.id='setup-test';host.style.height='100dvh';document.body.append(host);createRoot(host).render(<App/>);
}
