import React from 'react';
import {createRoot} from 'react-dom/client';
import {DiskMapWorkspace} from '../../src/design/DiskMapWorkspace';
import {defaultWorkspace} from '../../src/design/workspace-state';
// Deliberately resolve the old selection last to exercise stale-response cancellation.
const files=['a','b'].map(id=>({id,name:id+'.mov',kind:'file',bytesExact:'100'}));
const root={id:'root',name:'Videos',kind:'folder',bytesExact:'200',children:files};
const readSelectedPath=async id=>{await new Promise(r=>setTimeout(r,id==='a'?350:20));return '/volume1/video/nested/'+id+'.mov';};
export function mount(){
 function App(){const [selectedId,selectFile]=React.useState(null);const noop=()=>{};return <><button id="select-a" onClick={()=>selectFile('a')}>A</button><button id="select-b" onClick={()=>selectFile('b')}>B</button><DiskMapWorkspace volumes={[root]} initialId="root" live={{current:root,mapRoot:root,path:[root],entries:[],volumes:[root],workspace:{...defaultWorkspace(),right:true},layoutReady:true,selectedId,selectFile,readSelectedPath,snapshotKey:'test',readFileList:async()=>({items:[],nextCursor:null}),selectedStamp:{state:'complete',endedAt:'2026-10-15T12:00:00Z'},loading:false,busy:false,error:'',statusText:'',canBack:false,navigate:noop,back:noop,search:noop,scan:noop,rescan:noop,canRescan:false,debug:'',sourceName:'Videos',map:'treemap'}}/></>;}
 document.getElementById('root')?.remove();const host=document.createElement('div');host.id='root';document.body.append(host);createRoot(host).render(<App/>);
}
