import React from 'react';
import {createRoot} from 'react-dom/client';
import {DirectoryTree} from '../../src/design/DirectoryTree';
import {useSourceNavigation} from '../../src/design/useSourceNavigation';
import {defaultWorkspace} from '../../src/design/workspace-state';

// Exercise the real hook and tree across preference restoration and scan revisions without DSM.
window.SYNO={SDS:{Session:{SynoToken:'fixture'}}};
window.folderReads=[];
window.fetch=async(url,options)=>{
 const body=JSON.parse(options.body);
 if(String(url).includes('scope-observations'))return Response.json({contractVersion:1,data:body.scopes.map(s=>({...s,bytes:'1000',recycleBytes:'0',endedAt:'2026-10-18T00:00:00Z',state:'complete',snapshotId:'snapshot',reasons:[],reasonCount:0,unreadableRecycle:false,unreadableSystem:false}))});
 if(String(url).includes('scope-folders')){
  window.folderReads.push(body);
  return Response.json({contractVersion:1,data:{shareId:body.shareId,relativePath:body.relativePath,checkedAt:new Date().toISOString(),entries:[{name:'Child',relativePath:body.relativePath==='.'?'Child':body.relativePath+'/Child'}],nextCursor:null}});
 }
 throw Error('Unexpected request '+url);
};
const sources={volumes:[{id:'v1',name:'volume1',state:'reachable'},{id:'v2',name:'volume2',state:'reachable'}],shares:[{id:'a',name:'Share A',volumeId:'v1',readable:true},{id:'b',name:'Share B',volumeId:'v1',readable:true},{id:'c',name:'Share C',volumeId:'v2',readable:true}]};
const kind=new URLSearchParams(location.search).get('source')??'nas';
const saved=kind==='nas'?{kind:'nas',targetIds:[]}:kind==='volumes'?{kind:'volumes',targetIds:['v1']}:kind==='folder'?{kind:'folder',targetIds:['a'],relativePath:'Child'}:{kind:'shares',targetIds:['a']};
function App(){
 const [ready,setReady]=React.useState(false),[revision,setRevision]=React.useState(0);
 const [workspace,setWorkspace]=React.useState(()=>({...defaultWorkspace(),source:saved,expanded:['source:volumes:v1:.','source:volumes:v2:.']}));
 const api={sources,profiles:[],summaries:[{profileId:'profile',endedAt:String(revision)}],summariesReady:true,layoutReady:ready,workspace,path:[],analysis:{reader:null},profileId:'profile',loading:false,setWorkspace:patch=>setWorkspace(w=>({...w,...patch})),openScope:async()=>true};
 const source=useSourceNavigation(api,()=>{throw Error('Unexpected denial');});
 return <><button onClick={()=>setReady(true)}>Restore preferences</button><button onClick={()=>setRevision(r=>r+1)}>Scan completed</button><div data-selected={source.selected.scope.kind}/><DirectoryTree key={source.revision} root={source.root} path={source.path} selectedId={source.selected.id} workspace={workspace} onWorkspace={api.setWorkspace} language="en" load={source.read} onNavigate={source.navigate} size={source.size}/></>;
}
createRoot(document.getElementById('root')).render(<App/>);
