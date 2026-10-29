import React,{useState} from 'react';
import {createRoot} from 'react-dom/client';
import {ScanQueue} from '/src/design/ScanQueue';
// Exercise the actual dialog with server-like authoritative queue updates.
export function mount(){function Fixture(){const [waiting,setWaiting]=useState([{id:'b',name:'Videos',position:1},{id:'c',name:'Fotos',position:2}]);return <ScanQueue language="de" busy={false} queue={{active:{id:'a',name:'homes'},waiting,unavailable:false}} remove={id=>setWaiting(items=>items.filter(item=>item.id!==id))}/>;}createRoot(document.getElementById('root')).render(<Fixture/>);}
