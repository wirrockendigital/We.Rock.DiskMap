import React,{useState} from 'react';
import {createRoot} from 'react-dom/client';
import {NotificationCenter} from '../../src/design/NotificationCenter';
// Essential errors remain available after dismissing their transient card.
function Fixture(){const [error,setError]=useState('');return <><button id="raise-error" onClick={()=>setError('Der Scan konnte nicht abgeschlossen werden.')}>Fail</button><NotificationCenter language="de" source="test" status="" error={error}/></>;}
export function mount(){const e=document.createElement('div');e.id='notification-test';e.className='disk-workspace';e.style.cssText='position:fixed;inset:0;z-index:10000';document.body.append(e);createRoot(e).render(<Fixture/>);}
