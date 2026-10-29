import React from 'react';
import {createRoot} from 'react-dom/client';
import {CategoryToolbar} from '../../src/design/CategoryToolbar';
import {FileListPanel} from '../../src/design/FileListPanel';
import {DirectoryTree} from '../../src/design/DirectoryTree';
import {defaultWorkspace} from '../../src/design/workspace-state';
// Hidden branches stay cached; changing presentation never triggers a scan.
export function mount(){
 const folder=(id,name,children=[])=>({id,name,kind:'folder',children});
 const recycle=folder('bin','#recycle',[folder('inside','private')]);
 const system=folder('sys','@eaDir',[folder('thumb','thumbnails')]);
 const root=folder('root','NAS',[folder('normal','Photos'),recycle,system]);
 const files=[{id:'r',name:'deleted.zip',path:'eric/#recycle/deleted.zip',kind:'file'},{id:'s',name:'thumb.jpg',path:'eric/@eaDir/thumb.jpg',kind:'file'},{id:'d',name:'.DS_Store',kind:'file'},{id:'visible',name:'visible.pdf',path:'eric/Photos/visible.pdf',kind:'file'}];
 window.visibilityLoads=[];
 // Two completely hidden pages must not stop automatic continuation.
 const load=async(options,cursor,signal)=>{window.visibilityLoads.push(cursor);await new Promise(r=>setTimeout(r,20));signal.throwIfAborted();const i=Number(cursor)||0;return {items:files.slice(i,i+1),nextCursor:i+1<files.length?String(i+1):null};};
 function App(){const [workspace,set]=React.useState({...defaultWorkspace(),expanded:['bin','sys'],scanRecycle:false,scanSystem:false});const [available,setAvailable]=React.useState(true);const [recycleState,setRecycleState]=React.useState('readable');return <div className="disk-workspace"><div style={{width:280}}><CategoryToolbar recycleState={recycleState} root={{...root,indexedRecycle:available,indexedSystem:available,bytesExact:String(1000000000+(workspace.showRecycle?2000000000:0)+(workspace.showSystem?1000000000:0))}} workspace={workspace} language="de" onChange={patch=>set(s=>({...s,...patch}))}/></div><div style={{position:'fixed',bottom:10,left:10,display:'flex',gap:12,zIndex:999}}>{['readable','denied','missing','unknown'].map(state=><button key={state} id={'state-'+state} onClick={()=>setRecycleState(state)}>{state}</button>)}</div><button id="availability" onClick={()=>setAvailable(v=>!v)}>availability</button><button id="bins" onClick={()=>set(s=>({...s,showRecycle:!s.showRecycle}))}>bins</button><button id="systems" onClick={()=>set(s=>({...s,showSystem:!s.showSystem}))}>systems</button><div style={{width:300,height:400}}><DirectoryTree workspace={workspace} root={root} path={[root]} selectedId="root" language="de" onNavigate={()=>{}}/></div><div className="disk-side" style={{height:400,width:390,display:'flex',flexDirection:'column'}}><FileListPanel workspace={workspace} root={root} identity="visibility" language="de" load={load} selected={null} onSelect={()=>{}} onHover={()=>{}} onContext={()=>{}}/></div></div>;}
 document.getElementById('root')?.remove();const host=document.createElement('div');host.id='root';document.body.append(host);createRoot(host).render(<App/>);
}
