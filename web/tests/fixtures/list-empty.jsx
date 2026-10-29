import React,{useState} from 'react';
import {createRoot} from 'react-dom/client';
import {FileListPanel} from '../../src/design/FileListPanel';
// Keep identity stable to verify readiness changes independently from navigation.
export function mount(){
 const host=document.createElement('div');host.id='empty-test';host.style.cssText='position:fixed;inset:0;z-index:99999;background:#243143';document.body.append(host);window.emptyListCalls=0;
 const load=async()=>{window.emptyListCalls++;throw Error('LIST_UNAVAILABLE');};
 function Fixture(){const [ready,setReady]=useState(false);return <><button id="ready-toggle" onClick={()=>setReady(v=>!v)}>Toggle</button><FileListPanel root={{id:'empty',name:'NAS',kind:'folder',unavailable:!ready}} identity="empty-test" language="de" load={load} selected={null} onSelect={()=>{}} onHover={()=>{}} onContext={()=>{}}/></>;}
 createRoot(host).render(<Fixture/>);
}
