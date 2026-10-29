import {useEffect,useRef,useState} from 'react';
import {Cross2Icon,ListBulletIcon} from '@radix-ui/react-icons';
import type {Language} from './model';
export interface ScanQueueState {active:{id:string;name:string}|null;waiting:{id:string;name:string;position:number}[];unavailable:boolean}
// The server owns queue order and removal; the dialog never simulates a local scheduler.
export function ScanQueue({queue,busy,remove,language}:{queue:ScanQueueState;busy:boolean;remove:(id:string)=>void;language:Language}){
 const [open,setOpen]=useState(false),ref=useRef<HTMLDialogElement>(null),de=language==='de';
 useEffect(()=>{if(!open)return;const origin=document.activeElement as HTMLElement|null;ref.current?.showModal();return()=>{ref.current?.close();origin?.focus();};},[open]);
 return <><button className="disk-icon-button disk-queue-toggle" aria-label={de?'Scanwarteschlange':'Scan queue'} title={de?'Scanwarteschlange':'Scan queue'} onClick={()=>setOpen(true)}><ListBulletIcon/>{queue.waiting.length>0&&<span>{queue.waiting.length}</span>}</button>
 {open&&<dialog ref={ref} className="disk-dialog disk-queue-dialog" aria-labelledby="scan-queue-title" onCancel={e=>{e.preventDefault();setOpen(false);}}><header className="disk-dialog-header"><h2 id="scan-queue-title">{de?'Scanwarteschlange':'Scan queue'}</h2><button className="disk-icon-button" aria-label={de?'Schließen':'Close'} onClick={()=>setOpen(false)}><Cross2Icon/></button></header>
 <div className="disk-delete-content">{queue.unavailable&&<p role="status">{de?'Warteschlangenstatus momentan nicht verfügbar.':'Queue status is currently unavailable.'}</p>}
 {queue.active&&<div className="disk-queue-active"><small>{de?'Wird gerade gescannt':'Scanning now'}</small><strong>{queue.active.name}</strong></div>}
 <h3>{de?'Wartend':'Waiting'} · {queue.waiting.length}</h3>
 {queue.waiting.length?<ol className="disk-queue-items">{queue.waiting.map(job=><li key={job.id}><span title={job.name}>{job.name}</span><button className="disk-icon-button" disabled={busy||queue.unavailable} aria-label={(de?'Aus Warteschlange entfernen: ':'Remove from queue: ')+job.name} onClick={()=>remove(job.id)}><Cross2Icon/></button></li>)}</ol>:<p>{de?'Keine weiteren Scans vorgemerkt.':'No additional scans queued.'}</p>}
 <small>{de?'Die Scans werden nacheinander ausgeführt. Der laufende Scan bleibt beim Entfernen eines wartenden Auftrags unverändert.':'Scans run one at a time. Removing a queued job does not affect the active scan.'}</small></div></dialog>}</>;
}
