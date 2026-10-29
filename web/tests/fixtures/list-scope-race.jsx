import React,{useState} from 'react';
import {createRoot} from 'react-dom/client';
import {FileListPanel} from '../../src/design/FileListPanel';
// Delayed responses must not append one folder's rows to another folder's list.
function Fixture(){const [scope,setScope]=useState('A');const load=React.useCallback(async(options,cursor)=>{await new Promise(r=>setTimeout(r,100));const offset=Number(cursor)||0;return {items:Array.from({length:50},(_,i)=>({id:scope+(offset+i),name:scope+'-'+(offset+i),kind:'file',bytes:1})),nextCursor:offset===0?'50':null};},[scope]);return <><button id="scope-switch" onClick={()=>setScope('B')}>Next scope</button><aside className="disk-side" style={{height:450,width:390}}><FileListPanel root={{id:scope,name:scope,kind:'folder'}} identity={scope} language="de" load={load} selected={null} onSelect={()=>{}} onHover={()=>{}} onContext={()=>{}}/></aside></>;}
export function mount(){const e=document.createElement('div');e.id='scope-race';e.className='disk-workspace';e.style.cssText='position:fixed;inset:0;z-index:10000;display:block';document.body.append(e);createRoot(e).render(<Fixture/>);}
