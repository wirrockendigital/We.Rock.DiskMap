import React from 'react';
import {createRoot} from 'react-dom/client';
import {FileListPanel} from '../../src/design/FileListPanel';
// More than two batches expose dropped rows and reset errors in continuous scrolling.
export function mount(){
 const host=document.createElement('div');host.id='scroll-test';host.className='disk-workspace';host.style.cssText='position:fixed;inset:0;z-index:9999';document.body.append(host);
 const root={id:'all',name:'All',kind:'folder',bytes:120,children:Array.from({length:120},(_,i)=>({id:String(i),name:`file-${String(i).padStart(3,'0')}.zip`,kind:'file',bytes:120-i,category:'archive'}))};
 createRoot(host).render(<div className="disk-side" style={{height:450,width:390,display:'flex',flexDirection:'column'}}><FileListPanel root={root} identity="scroll-test" language="de" selected={null} onSelect={()=>{}} onHover={()=>{}} onContext={()=>{}}/></div>);
}
