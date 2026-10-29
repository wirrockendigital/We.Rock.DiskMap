import {useEffect,useRef} from 'react';
import type {Language} from './model';
// The destructive choice is always explicit and focuses Cancel, including keyboard-driven use.
export function IndexResetDialog({name,language,onCancel,onConfirm,rebuild=false}:{rebuild?:boolean;name:string;language:Language;onCancel:()=>void;onConfirm:()=>void}){
 const ref=useRef<HTMLDialogElement>(null),de=language==='de';
 useEffect(()=>{const origin=document.activeElement as HTMLElement|null;ref.current?.showModal();return()=>{ref.current?.close();origin?.focus();};},[]);
 return <dialog ref={ref} className="disk-dialog disk-delete-dialog" aria-labelledby="index-reset-title" onCancel={event=>{event.preventDefault();onCancel();}}>
  <header className="disk-dialog-header"><h2 id="index-reset-title">{rebuild?(de?'Index neu aufbauen?':'Rebuild index?'):(de?'Index löschen?':'Delete index?')}</h2></header>
  <div className="disk-delete-content"><p className="disk-delete-name">{name}</p><p>{rebuild?(de?'Bist du sicher? Der Bereich wird vollständig neu gescannt und sein gespeicherter Index ersetzt. Deine Dateien bleiben unverändert.':'Are you sure? This area will be scanned completely and its recorded index replaced. Your files remain unchanged.'):(de?'Bist du sicher? Gespeicherte Scanergebnisse für diesen Bereich werden aus allen Scanbeständen entfernt. Deine Dateien und Einstellungen bleiben erhalten. Anschließend kannst du den Bereich neu scannen.':'Are you sure? Recorded results for this area will be removed from all scans. Your files and settings remain unchanged. You can scan the area again afterwards.')}</p></div>
  <footer className="disk-delete-actions"><button autoFocus onClick={onCancel}>{de?'Abbrechen':'Cancel'}</button><button className="disk-delete-confirm" onClick={onConfirm}>{rebuild?(de?'Neu scannen':'Scan again'):(de?'Index löschen':'Delete index')}</button></footer>
 </dialog>;
}
