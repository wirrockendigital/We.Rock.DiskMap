// Original outline icons keep NAS, volume and folder identities visually distinct.
export function StorageIcon({kind}:{kind:'nas'|'volume'|'folder'|'file'}){
 return <svg className="disk-storage-icon" data-kind={kind} width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
  {kind==='nas'?<><path d="M6 3h12l2 18H4L6 3Z"/><path d="M8 7h8M8 11h8M8 15h5"/><circle cx="16" cy="17" r=".8"/></>:kind==='volume'?<><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 4 16 4 16 0V5M4 12c0 4 16 4 16 0"/></>:kind==='file'?<><path d="M6 3h8l4 4v14H6Z"/><path d="M14 3v5h4"/></>:<path d="M3 6h7l2 3h9v11H3V6Z"/>}
 </svg>;
}
