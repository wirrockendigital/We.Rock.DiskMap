import {RegionalTime,RegionalSize,RegionalNumber,RegionalPercentage} from './regional-format';
import type {ReactNode} from 'react';
import { SourceAvailabilityNotice } from './SourceAvailabilityNotice';
import { useEffect, useRef, useState } from 'react';
import { detailsMessages } from './details-messages';
import { requestNodeDetail, detailSelectionKey, settleDetail } from './node-details-client';
import type { DetailSelection, HistoricalDetail, DetailMetric, DetailOperation, DetailPage } from './node-details-client';
import type { SizeUnit } from './size-metrics';
import type { ContentAggregate } from './contracts';
import { SessionDenied } from './session';
import en from './locales/en.json';
import type { Node } from './contracts';
import de from './locales/de.json';

export interface PathText { text: string; encoding: 'utf8' | 'escaped-bytes' }
export interface NodePaths { snapshotId: string; nodeId: string; name: PathText; nasPath: PathText; fileStationPath: string | null }

// This boundary accepts labelled text only; it never decodes URI escapes or interprets shell syntax.
export function checkedPaths(value: NodePaths, snapshotId: string, nodeId: string): NodePaths {
  if (!value || value.snapshotId !== snapshotId || value.nodeId !== nodeId) throw new Error('PATH_SCOPE_MISMATCH');
  for (const text of [value.name, value.nasPath]) {
    if (!text || typeof text.text !== 'string' || text.text.length > 32768 || !['utf8', 'escaped-bytes'].includes(text.encoding)) throw new Error('PATH_TEXT_INVALID');
  }
  if (value.fileStationPath !== null && (typeof value.fileStationPath !== 'string' || !value.fileStationPath.startsWith('/') || value.fileStationPath.length > 4096)) throw new Error('PATH_TEXT_INVALID');
  return value;
}

// A user-initiated write copies exactly one explicit text value and never reads the clipboard.
export async function copyPathText(text: string, clipboard: Pick<Clipboard, 'writeText'> | undefined = navigator.clipboard): Promise<void> {
  if (!clipboard || typeof clipboard.writeText !== 'function') throw new Error('CLIPBOARD_UNAVAILABLE');
  try { await clipboard.writeText(text); } catch { throw new Error('CLIPBOARD_DENIED'); }
}

// Plain React text and readonly fields preserve names without creating links, HTML or commands.
export function NodeDetails({ paths, node, messages, recordedOnly = false }: { recordedOnly?: boolean; paths: NodePaths; node?: Node; messages: typeof de }) {
  const [status, setStatus] = useState('');
  const copy = async (text: string) => {
    try { await copyPathText(text); setStatus(messages['paths.copied']); }
    catch { setStatus(messages['paths.copyFailed']); }
  };
  const item = (label: string, value: PathText, button: string) => <div><label>{label}<textarea readOnly value={value.text} /></label>
    {value.encoding === 'escaped-bytes' && <p>{messages['paths.escaped']}</p>}
    <button onClick={() => { void copy(value.text); }}>{value.encoding === 'escaped-bytes' ? messages['paths.copyRepresentation'] : button}</button></div>;
  return <section aria-label={messages['paths.title']}><h3>{messages['paths.title']}</h3>
    {node?.kind === 'file' && node.linkCount !== null && node.linkCount > 1 && <p>{messages['node.hardlink']}</p>}
    {node?.kind === 'file' && <p>{messages['node.reflink']}</p>}
    {node?.kind === 'symlink' && <p>{messages['node.symlink']}</p>}
    {node && ['fifo', 'socket', 'blockDevice', 'characterDevice'].includes(node.kind) && <p>{messages['node.special']}</p>}
    {item(messages['paths.name'], paths.name, messages['paths.copyName'])}
    <p>{messages['paths.snapshotNotice']}</p>
    {item(messages['paths.nas'], paths.nasPath, messages['paths.copyNAS'])}
    {!recordedOnly && <><p>{messages['paths.currentNotice']}</p>
    {paths.fileStationPath === null ? <p>{messages['probe.fileStationUnavailable']}</p> : item(messages['paths.fileStation'], { text: paths.fileStationPath, encoding: 'utf8' }, messages['paths.copyFileStation'])}</>}
    {status && <p role="status">{status}</p>}
  </section>;
}

// Exact integer division rounds only the percentage label; its source byte values remain visible.
export function detailPercentage(numerator:string,denominator:string):string|null {
 const n=BigInt(numerator),d=BigInt(denominator);if(d===0n)return null;const p=(n*10000n+d/2n)/d;return `${p/100n}.${(p%100n).toString().padStart(2,'0')} %`;
}
export interface HistoricalNodeDetailsProps { language:'de'|'en'; selection:DetailSelection|null; disabled?:boolean; onSessionDenied:()=>void; onUnavailable?:(code:string)=>void; fileStationDisabled?:boolean; onFileStation?:(selection:DetailSelection)=>void }
// Selection is read-only input from the central controller; only transient request and paging state live here.
export function HistoricalNodeDetails({language,selection,disabled=false,onSessionDenied,onUnavailable,fileStationDisabled=false,onFileStation}:HistoricalNodeDetailsProps){
 const key=selection?detailSelectionKey(selection):'';
 const current=useRef(key);current.current=disabled?'':key;
 const callbacks=useRef({onSessionDenied,onUnavailable,fileStationDisabled,onFileStation});callbacks.current={onSessionDenied,onUnavailable,fileStationDisabled,onFileStation};
 const [result,setResult]=useState<{key:string;value:HistoricalDetail}|null>(null);
 const [error,setError]=useState<{key:string;code:string}|null>(null);
 const [retry,setRetry]=useState(0);
 const [unit,setUnit]=useState<SizeUnit>('GiB');
 const m=detailsMessages[language];
 useEffect(()=>{
  const abort=new AbortController();setResult(null);setError(null);
  if(!selection||disabled)return()=>abort.abort();
  const signal=AbortSignal.any([abort.signal,AbortSignal.timeout(8000)]);
  void settleDetail(requestNodeDetail(selection,'details','',signal),abort.signal,key,()=>current.current,value=>setResult({key,value}),e=>{
   if(e instanceof SessionDenied){callbacks.current.onSessionDenied();return;}
   const code=e instanceof Error?e.message:'DETAIL_UNAVAILABLE';setError({key,code});
   if(['VIEW_EXPIRED','SNAPSHOT_MISMATCH','INDEX_REFRESH_REQUIRED'].includes(code))callbacks.current.onUnavailable?.(code);
  });
  return()=>abort.abort();
 },[key,disabled,retry]);
 const value=result?.key===key&&!disabled?result.value:null;
 if(!selection)return <section className="detail-panel"><p>{m.select}</p></section>;
 if(disabled)return <section className="detail-panel" aria-busy="true"><p>{m.unavailable}</p></section>;
 if(!value)return <section className="detail-panel" aria-label={m.title} aria-busy={!error||error.key!==key}>{error?.key===key?<><p role="alert">{detailErrorMessage(error.code,language)}</p><button onClick={()=>setRetry(x=>x+1)}>{m.retry}</button></>:<p role="status">{m.loading}</p>}</section>;
 return <section className="detail-panel" aria-label={m.title}><h3>{m.title}</h3>
  <label>{m.unit} <select value={unit} onChange={e=>{if(['B','GB','GiB','TB','TiB'].includes(e.target.value))setUnit(e.target.value as SizeUnit);}}>{['B','GB','GiB','TB','TiB'].map(u=><option key={u}>{u}</option>)}</select></label>
  <DetailFacts value={value} language={language} unit={unit}/>
  {/* Historical metadata supplies identity only; the host performs a fresh leased target check. */}
  {onFileStation&&value.detail.snapshotState==='complete'&&!value.detail.virtual&&value.detail.node&&['file','directory'].includes(value.detail.kind)&&<button type="button" disabled={fileStationDisabled} onClick={()=>{if(current.current===key&&!callbacks.current.fileStationDisabled)callbacks.current.onFileStation?.(selection);}}>{(language==='de'?de:en)[value.detail.kind==='file'?'probe.showFile':'probe.open']}</button>}
  {(value.detail.kind==='directory'||value.detail.virtual)&&<>{(['direct-folders','recursive-files'] as const).map(operation=><DetailRanking key={key+operation} selection={selection} operation={operation} language={language} unit={unit} onSessionDenied={onSessionDenied} onUnavailable={onUnavailable}/>)}</>}
 </section>;
}
// Fixed messages never display transport errors or raw private response bodies.
export function detailErrorMessage(code:string,language:'de'|'en'):string{const m=detailsMessages[language];return code==='QUERY_BUDGET_EXCEEDED'?m.budget:code==='VIEW_EXPIRED'?m.expired:code==='METRIC_UNAVAILABLE'?m.metricUnavailable:m.unavailable;}
// All displayed counts remain decimal strings, including values above JavaScript's exact-number limit.
export function DetailFacts({value,language,unit='GiB'}:{value:HistoricalDetail;language:'de'|'en';unit?:SizeUnit}){
 const d=value.detail,n=d.node,m=detailsMessages[language];const messages=language==='de'?de:en;
 const size=(bytes:string|null)=>bytes===null?m.unknown:<><RegionalSize value={bytes} unit={unit} language={language}/> ({bytes} B)</>;
 const row=(label:string,v:ReactNode)=><div key={label}><dt>{label}</dt><dd>{v??m.unknown}</dd></div>;
 const measurement=(label:string,v:DetailMetric)=><section><h4>{label}</h4><p>{v.selectable?size(n&&n.kind!=='directory'?(label===m.logical?n.logicalBytes:n.allocatedBytes):v.knownBytes):m.unknown}</p>{v.selectable&&<p>{m.unknownFiles}: {v.unknownFileCount}</p>}<p>{m.method}: {v.method??m.unknown}</p>{v.qualification&&<p>{m.qualification}: {v.qualification}</p>}</section>;
 const counts=(title:string,c:ContentAggregate)=><section><h4>{title}</h4><dl>{row(m.files,<RegionalNumber value={c.fileCount} language={language}/>)}{row(m.directories,<RegionalNumber value={c.directoryCount} language={language}/>)}{row(m.specials,<RegionalNumber value={c.specialCount} language={language}/>)}{row(m.logical,size(c.logical.knownBytes))}{row(m.unknownFiles+' · '+m.logical,c.logical.unknownFileCount)}{row(m.allocated,d.allocated.selectable?size(c.allocated.knownBytes):m.unknown)}{d.allocated.selectable&&row(m.unknownFiles+' · '+m.allocated,c.allocated.unknownFileCount)}</dl></section>;
 return <>
  <label>{m.name}<textarea readOnly value={d.name}/></label>
  <dl>{row(m.type,m.kinds[d.kind as keyof typeof m.kinds]??m.unknown)}{row(m.volume,n?.volumeId)}{row(m.share,n?.shareId)}{row(m.modified,n?.modifiedAt?<RegionalTime value={n.modifiedAt} language={language}/>:null)}{row(m.owner,n?.ownerId)}{row(m.group,d.groupId)}{row(m.links,n?.linkCount)}{row(m.snapshot,d.snapshotId)}{row(m.revision,d.profileRevision)}{row(m.capture,<><RegionalTime value={d.capture.startedAt} language={language}/> — <RegionalTime value={d.capture.endedAt} language={language}/></>)}{row(m.classification,d.classification?.category?m.categories[d.classification.category as keyof typeof m.categories]:m.missingClassification)}</dl>
  <SourceAvailabilityNotice value={d.sourceAvailability} language={language}/>
  <p>{d.snapshotState==='partial'?m.partial:m.complete}</p><p>{m.basis}</p>
  {measurement(m.logical,d.logical)}{measurement(m.allocated,d.allocated)}<p>{m.blockHint}</p>
  <section><h4>{m.fraction}</h4><p>{d.denominator.knownBytes==='0'?m.undefinedFraction:<RegionalPercentage value={detailPercentage(d.numerator.knownBytes,d.denominator.knownBytes)!} language={language}/>}</p><p><RegionalNumber value={d.numerator.knownBytes} language={language}/> B / <RegionalNumber value={d.denominator.knownBytes} language={language}/> B · {d.metric==='logical'?m.logical:m.allocated}</p><p>{m.knownBasis} {m.unknownFiles}: <RegionalNumber value={d.numerator.unknownFileCount} language={language}/> / <RegionalNumber value={d.denominator.unknownFileCount} language={language}/></p></section>
  {d.aggregate&&<>{d.aggregate.direct?counts(m.direct,d.aggregate.direct):<p>{m.virtualDirect}</p>}{counts(m.subtree,d.aggregate.subtree)}<section><h4>{m.types}</h4>{Object.keys(d.aggregate.subtree.types).length===0?<p>{m.noFiles}</p>:<div className="detail-table"><table><thead><tr><th>{m.type}</th><th>{m.files}</th><th>{m.logical}</th><th>{m.allocated}</th><th>{m.unknownFiles}</th></tr></thead><tbody>{Object.entries(d.aggregate.subtree.types).map(([category,t])=><tr key={category}><th>{m.categories[category as keyof typeof m.categories]}</th><td><RegionalNumber value={t.fileCount} language={language}/></td><td>{size(t.logical.knownBytes)}</td><td>{d.allocated.selectable?size(t.allocated.knownBytes):m.unknown}</td><td>{m.logical}: <RegionalNumber value={t.logical.unknownFileCount} language={language}/>; {m.allocated}: {d.allocated.selectable?t.allocated.unknownFileCount:m.unknown}</td></tr>)}</tbody></table></div>}</section></>}
  {value.paths?<><NodeDetails paths={value.paths} node={n??undefined} messages={messages} recordedOnly/><p>{m.noFileStation}</p></>:<p>{m.noPaths}</p>}
  <section><h4>{m.coverage}</h4><p>{m.coverageHint}</p><p>{m.boundaries}: <RegionalNumber value={d.boundaryCount} language={language}/></p>{d.coverage.length===0&&<p>{m.noCoverage}</p>}<ul>{d.coverage.map((c,i)=><li key={c.targetId+i}>{c.targetId} · {c.state==='complete'?m.complete:m.partial} · {m.errors}: <RegionalNumber value={c.errors} language={language}/></li>)}</ul>{d.coverageTruncated&&<p>{m.truncated}</p>}</section>
 </>;
}
// Each ranking retains one bounded page; advancing never accumulates an unbounded client list.
function DetailRanking({selection,operation,language,unit,onSessionDenied,onUnavailable}:HistoricalNodeDetailsProps&{selection:DetailSelection;operation:Exclude<DetailOperation,'details'>;unit:SizeUnit}){
 const m=detailsMessages[language],key=detailSelectionKey(selection);const active=useRef(key);active.current=key;
 const abort=useRef<AbortController|null>(null);const serial=useRef(0);
 const [page,setPage]=useState<DetailPage|null>(null),[loading,setLoading]=useState(false),[error,setError]=useState('');const [cursor,setCursor]=useState('');
 useEffect(()=>()=>{active.current='';abort.current?.abort();},[]);
 const load=(next:string)=>{
  abort.current?.abort();const controller=new AbortController();abort.current=controller;const request=++serial.current;setLoading(true);setError('');setPage(null);setCursor(next);
  void settleDetail(requestNodeDetail(selection,operation,next,AbortSignal.any([controller.signal,AbortSignal.timeout(8000)])),controller.signal,key,()=>request===serial.current?active.current:'',value=>{setPage(value.detail.ranking);setLoading(false);},e=>{setLoading(false);if(e instanceof SessionDenied){onSessionDenied();return;}const code=e instanceof Error?e.message:'DETAIL_UNAVAILABLE';setError(code);if(['VIEW_EXPIRED','SNAPSHOT_MISMATCH','INDEX_REFRESH_REQUIRED'].includes(code))onUnavailable?.(code);});
 };
 return <section aria-busy={loading}><h4>{operation==='direct-folders'?m.folders:m.recursive}</h4><p>{operation==='direct-folders'?m.disjoint:m.nonAdditive}</p><p>{selection.metric==='logical'?m.logical:m.allocated} · {m.basis}</p>
  {!page&&!loading&&<button onClick={()=>load(cursor)}>{error?m.retry:m.load}</button>}{loading&&<p role="status">{m.loading}</p>}{error&&<p role="alert">{detailErrorMessage(error,language)}</p>}
  {page&&<>{page.workLimited&&<p role="status">{m.limited}</p>}{page.items.length===0&&!page.workLimited&&<p>{m.empty}</p>}<ol>{page.items.map(item=><li key={item.nodeId}><strong>{item.name}</strong><p>{item.path}</p><p>{item.size===null?m.unknown:<><RegionalSize value={item.size} unit={unit} language={language}/> (<RegionalNumber value={item.size} language={language}/> B)</>}</p>{item.summary.unknownFileCount!=='0'&&<p>{m.unknownFiles}: <RegionalNumber value={item.summary.unknownFileCount} language={language}/></p>}</li>)}</ol><p>{page.hasMore?m.more:m.end}</p>{page.hasMore&&<button onClick={()=>load(page.nextCursor)}>{m.next}</button>}{cursor&&<button onClick={()=>load('')}>{m.first}</button>}</>}
 </section>;
}
