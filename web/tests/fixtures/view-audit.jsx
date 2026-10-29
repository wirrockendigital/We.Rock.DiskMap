import React,{useState} from 'react';
import {createRoot} from 'react-dom/client';
import {StorageChart} from '../../src/design/StorageChart';
import {FileListPanel} from '../../src/design/FileListPanel';
// Dense deterministic data exposes repaint churn and unbounded list DOM growth.
const children=Array.from({length:10000},(_,i)=>({id:`file-${i}`,name:`file-${i}.bin`,kind:'file',bytesExact:'1000'}));
const root={id:'audit',name:'Audit',kind:'folder',bytesExact:'10000000',children};
const load=async()=>({items:children,nextCursor:null});
function Audit(){const [hover,setHover]=useState(null),[mode,setMode]=useState('treemap');return <div className="disk-workspace" style={{display:'flex',height:'100vh'}}><div style={{flex:1,position:'relative'}}><button id="audit-mode" style={{position:'absolute',zIndex:10}} onClick={()=>setMode('radial')}>Radial</button><StorageChart root={root} mode={mode} language="de" hovered={hover} onHover={setHover} onOpen={()=>{}}/></div><aside className="disk-side" style={{width:390}}><FileListPanel root={root} identity="audit" language="de" load={load} selected={null} onSelect={()=>{}} onHover={()=>{}} onContext={()=>{}}/></aside></div>;}
export function mount(){const el=document.createElement('div');el.id='view-audit';el.style.cssText='position:fixed;inset:0;z-index:9999';document.body.append(el);createRoot(el).render(<Audit/>);}
