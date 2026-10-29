import '../../src/design/worker-entry';
import React from 'react';
import {createRoot} from 'react-dom/client';
import '../../src/style.css';
import '../../src/design/workspace.css';
import {DiskMapWorkspace} from '../../src/design/DiskMapWorkspace';
import {defaultWorkspace} from '../../src/design/workspace-state';
// This isolated browser fixture uses production components and never contacts DSM.
window.SYNO={SDS:{Session:{SynoToken:'fixture-only'}}};
const rows=[['Docs',true,8],['dev',true,0],['Archive',false,'-']];
window.fixtureCalls=[];
window.fetch=async(url,options)=>{
 const body=options?.body,api=body?.get('api'),method=body?.get('method');window.fixtureCalls.push({api,method,url:String(url)});
 if(String(url).includes('action=session'))return Response.json({contractVersion:1,data:{authenticated:true}});
 let data;
 if(String(url).includes('SYNO.API.Info'))data={'SYNO.SynologyDrive.Share':{path:'entry.cgi',minVersion:1,maxVersion:2},'SYNO.Core.Share':{path:'entry.cgi',minVersion:1,maxVersion:1}};
 else if(api==='SYNO.SynologyDrive.Share'&&method==='list')data={total:3,items:rows.map(([share_name,share_enable,rotate_cnt])=>({share_name,share_enable,rotate_cnt}))};
 else if(api==='SYNO.Core.Share'&&method==='get'){const name=JSON.parse(body.get('name'));data={name,support_snapshot:true,snapshot_info:{snapshot_count:name==='Docs'?5:0}};}
 else throw Error('Unexpected fixture request');
 return Response.json({success:true,data});
};
const shares=rows.map(([name],i)=>({id:'s'+i,name,volumeId:'v',state:'reachable',readable:true,recycleState:i===0?'readable':i===1?'denied':'missing'}));
const folder={id:'folder',name:'Docs',kind:'folder',bytesExact:new URLSearchParams(location.search).has('zero')?'0':'81900000',children:[{id:'file',name:'example.bin',kind:'file',bytesExact:'81900000'}]};
const nodes=shares.map(s=>({id:s.id,name:s.name,kind:'share',bytesExact:['81900000','105987654321','1000000000000'][Number(s.id.slice(1))],children:s.id==='s0'?[folder]:[]}));
const volume={id:'v',name:'Volume 1',kind:'volume',bytesExact:'1106069554321',children:nodes},nas={id:'nas',name:'NAS',kind:'volume',children:[volume]};
// Real React state verifies the toggle without introducing persistence or scan effects.
function App(){
 const [map,setMap]=React.useState('treemap');
 const [workspace,setWorkspace]=React.useState({...defaultWorkspace(),...(JSON.parse(localStorage.getItem('fixture.workspace')??'null')??{}),expanded:['v','s0'],leftWidth:Number(new URLSearchParams(location.search).get('width')??290)}),[selected,setSelected]=React.useState(nodes[0]);
 const noop=()=>{};
 return <DiskMapWorkspace volumes={[nas]} initialId="nas" live={{layoutReady:true,storedLanguage:new URLSearchParams(location.search).get('lang')==='en'?'en':'de',workspace,onWorkspace:p=>setWorkspace(w=>{const next={...w,...p};localStorage.setItem('fixture.workspace',JSON.stringify(next));return next;}),shareStatusRevision:'fixture',statusShare:n=>shares.find(s=>s.id===n.id),snapshotShareName:selected.kind==='share'?selected.name:undefined,recycleState:'readable',mapRoot:selected,onMap:setMap,current:selected,path:[nas,volume,selected],entries:[],volumes:[nas],treeRoot:nas,treePath:[nas,volume],treeSelectedId:selected.id,treeNavigate:setSelected,treeRevision:'fixture',treeStamp:new URLSearchParams(location.search).has('partial')?node=>({state:node.id==='s0'||node.id==='folder'?'partial':'complete',endedAt:new Date().toISOString()}):undefined,loading:false,busy:false,error:'',statusText:'',canBack:false,navigate:setSelected,back:noop,search:noop,scan:()=>{throw Error('Unexpected scan');},rescan:()=>{throw Error('Unexpected rescan');},canRescan:false,debug:'',sourceName:'NAS',map}}/>;
}
createRoot(document.getElementById('root')).render(<App/>);
