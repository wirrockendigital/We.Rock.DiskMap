import {useEffect,useRef,useState,type CSSProperties,type ReactNode} from 'react';
import {BellIcon,Cross2Icon} from '@radix-ui/react-icons';

interface Notice {id:number;key:string;source:string;kind:'info'|'error';text:string;time:number;dismissed:boolean;read:boolean}
interface Props {completion?:{jobId:string;name:string;state:string}|null;language:'de'|'en';source:string;status:string;error:string;actions?:ReactNode}
// Session-local history contains observations only; dismissed toasts never delete history or replay actions.
export function NotificationCenter({language,source,status,error,actions,completion}:Props){
 const [items,setItems]=useState<Notice[]>([]),[open,setOpen]=useState(false);
 const next=useRef(0),previous=useRef({status:'',error:''}),bell=useRef<HTMLButtonElement>(null);
 const de=language==='de';
 useEffect(()=>{
  // Only actionable observations enter history; routine cache and progress state stays in the header.
  const updates:Notice[]=[];
  for(const [channel,text] of Object.entries({status,error})){
   const value=text.trim(),key=source+':'+channel+':'+value.replace(/ · [\d.,]+ (Einträge|entries)$/,'');
   const old=previous.current[channel as keyof typeof previous.current];
   previous.current[channel as keyof typeof previous.current]=value?key:'';
   if(value&&old!==key)updates.push({id:++next.current,key,source,kind:channel==='error'?'error':'info',text:value,time:Date.now(),dismissed:false,read:false});
  }
  if(updates.length)setItems(old=>[...updates.reverse(),...old].slice(0,100));
 },[source,status,error]);
 // A job identity deduplicates completion independently of the selected share.
 useEffect(()=>{
  if(!completion)return;
  const key='job:'+completion.jobId;
  const label=completion.state==='completed'?(de?'Scan abgeschlossen':'Scan completed'):completion.state==='cancelled'?(de?'Scan abgebrochen':'Scan cancelled'):(de?'Scan fehlgeschlagen':'Scan failed');
  setItems(old=>old.some(n=>n.key===key)?old:[{id:++next.current,key,source:'job',kind:completion.state==='failed'||completion.state==='interrupted'?'error' as const:'info' as const,text:completion.name+' · '+label,time:Date.now(),dismissed:false,read:false},...old].slice(0,100));
 },[completion?.jobId]);
 const visible=items.filter(n=>!n.dismissed).slice(0,3),unread=items.filter(n=>!n.read).length;
 // Informational toasts expire after ten seconds; errors remain until explicitly closed.
 useEffect(()=>{
  const timers=items.filter(n=>!n.dismissed&&n.kind==='info').map(n=>setTimeout(()=>setItems(old=>old.map(v=>v.id===n.id?{...v,dismissed:true}:v)),Math.max(0,n.time+10000-Date.now())));
  return()=>timers.forEach(clearTimeout);
 },[items]);
 const close=()=>{setOpen(false);bell.current?.focus();};
 const dismiss=(id:number)=>setItems(old=>old.map(n=>n.id===id?{...n,dismissed:true,read:true}:n));
 const card=(n:Notice,index:number,history=false)=><article key={n.id} className="disk-notice-card" data-kind={n.kind} style={!history?{'--notice-index':index} as CSSProperties:undefined} aria-hidden={!history&&index>0}>
  <header><strong>{n.kind==='error'?(de?'Fehler':'Error'):'We.Rock.DiskMap'}</strong><time>{new Date(n.time).toLocaleTimeString(language,{hour:'2-digit',minute:'2-digit'})}</time>{!history&&<button tabIndex={index? -1:0} className="disk-icon-button" aria-label={de?'Meldung schließen':'Dismiss notification'} onClick={()=>dismiss(n.id)}><Cross2Icon/></button>}</header>
  <p>{n.text}</p>
  {/* Historical entries never retain executable callbacks from an earlier source or operation. */}
  {n.source===source&&(n.text===status||n.text===error)&&actions&&<div className="disk-notice-actions" inert={!history&&index>0?true:undefined}>{actions}</div>}
 </article>;
 return <>
  <button ref={bell} className="disk-icon-button disk-notification-bell" title={de?'Meldungen':'Notifications'} aria-label={de?'Meldungen':'Notifications'} aria-expanded={open} onClick={()=>{setOpen(v=>!v);setItems(old=>old.map(n=>({...n,read:true})));}}><BellIcon/>{unread>0&&<span className="disk-notice-badge">{unread>9?'9+':unread}</span>}</button>
  {open?<section className="disk-notice-history" role="region" aria-label={de?'Letzte Meldungen':'Recent notifications'} onKeyDown={e=>{if(e.key==='Escape'){e.stopPropagation();close();}}}><header><strong>{de?'Meldungen':'Notifications'}</strong><button className="disk-icon-button" aria-label={de?'Meldungen schließen':'Close notifications'} onClick={close}><Cross2Icon/></button></header><div>{items.length?items.map((n,i)=>card(n,i,true)):<p>{de?'Noch keine Meldungen in dieser Sitzung.':'No notifications in this session.'}</p>}</div></section>:<section className="disk-notice-stack" aria-label={de?'Neue Meldungen':'New notifications'} aria-live={error?'assertive':'polite'}>{visible.map((n,i)=>card(n,i))}</section>}
 </>;
}
