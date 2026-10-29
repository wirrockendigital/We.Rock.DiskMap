import React from 'react';
import {createRoot} from 'react-dom/client';
import {useSourceNavigation} from '../../src/design/useSourceNavigation';
import {defaultWorkspace} from '../../src/design/workspace-state';
import {formatUnitBytes,nextSizeUnit} from '../../src/design/size-unit';
// Controlled read-only transport reproduces delayed roots, partial batches and stale child reads.
window.SYNO={SDS:{Session:{SynoToken:'fixture'}}};
window.observationRequests=[];
window.fetch=async(url,options)=>{
 if(String(url).includes('scope-folders'))return Response.json({contractVersion:1,data:{shareId:'photo',relativePath:'.',checkedAt:new Date().toISOString(),entries:[],nextCursor:null}});
 if(!String(url).includes('scope-observations'))throw Error('Unexpected request '+url);
 return new Promise((resolve,reject)=>window.observationRequests.push({scopes:JSON.parse(options.body).scopes,resolve,reject}));
};
window.reply=(index,photoBytes='10000000000',endedAt='2026-10-15T00:00:00Z',status=200)=>{
 const request=window.observationRequests[index];request.resolve(Response.json({contractVersion:1,data:request.scopes.map(s=>({...s,bytes:s.shareId==='photo'?photoBytes:'1000000000',recycleBytes:'0',endedAt,state:'complete',snapshotId:endedAt.replace(/[^0-9]/g,''),reasons:[],reasonCount:0,unreadableRecycle:false,unreadableSystem:false}))},{status}));
};
const shares=Array.from({length:33},(_,i)=>({id:i?'s'+i:'photo',name:i?'Share '+i:'photo',volumeId:i===32?'v2':'v1',readable:true,state:'reachable'}));
const recorded={id:'recorded-nas',name:'NAS',kind:'volume',sourceKind:'nas',bytesExact:'42000000000',children:[{id:'old-v1',name:'volume1',kind:'volume',sourceKind:'volume',volumeId:'v1',bytesExact:'41000000000',children:[{id:'old-photo',name:'photo',kind:'share',sourceKind:'share',shareId:'photo',bytesExact:'10000000000'}]}]};
const sources={volumes:[{id:'v1',name:'volume1',state:'reachable'},{id:'v2',name:'volume2',state:'reachable'}],shares};
function App(){
 const [revision,setRevision]=React.useState(0),[unit,setUnit]=React.useState('GB');
 const api={sources,profiles:[],summaries:[{profileId:'profile',endedAt:'2026-10-'+String(15+revision)+'T00:00:00Z'}],summariesReady:true,layoutReady:false,workspace:defaultWorkspace(),path:[recorded],current:recorded,analysis:{reader:null},profileId:'profile'};
 const source=useSourceNavigation(api,()=>{throw Error('Unexpected denial');});
 window.startChild=()=>source.read('source:shares:photo:.','',new AbortController().signal).catch(error=>window.childError=error.message);
 const rows=[source.root,...source.root.children,...source.root.children.flatMap(n=>n.children)];
 return <><button onClick={()=>setRevision(r=>r+1)}>Scan completed</button><button onClick={()=>setUnit(nextSizeUnit(unit))}>Unit {unit}</button><table><tbody>{rows.map(n=><tr key={n.id} data-id={n.id}><td>{n.name}</td><td data-bytes={source.size(n)??'unknown'}>{formatUnitBytes(source.size(n),unit,'en')}</td></tr>)}</tbody></table></>;
}
createRoot(document.getElementById('root')).render(<App/>);
