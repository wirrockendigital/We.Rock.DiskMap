import type { PreferenceView } from '../../contracts';
import type { AnalysisSummary,AnalysisNode } from '../../analysis-query';
import type { ViewPin } from '../../snapshot-reader';
// Search metadata belongs to one immutable generation; null size is never converted to zero.
export interface RecordedClassification {category:string|null;basis:string;mappingRevision:number;folderComposition:string;folderCategories:string[];contextHints:string[]}
export interface SearchListItem {
 snapshotId:string;nodeId:string;parentId:string|null;name:string;path:string;kind:string;
 volumeId:string;shareId:string|null;modifiedAt:string|null;size:string|null;summary:AnalysisSummary;classification?:RecordedClassification|null;
}
export interface SearchListPage {
 snapshotId:string;rootId:string;metric:string;scope:string;selectionOrder:string;
 items:SearchListItem[];hasMore:boolean;nextCursor:string;workLimited:boolean;
}
export interface ListBinding {view:PreferenceView;pin:ViewPin}
// Exclude selection only: clicking a row must not reset its DOM or the current result page.
export function listIdentity({view,pin}:ListBinding):string {
 const {selectedNodeId:_,...selectors}=view;return JSON.stringify([pin.profileId,pin.viewId,pin.snapshotId,selectors]);
}
// Existing central interaction callbacks consume real nodes, never synthetic file paths.
export function resultNode(item:SearchListItem):AnalysisNode {
 return{id:item.nodeId,realNodeId:item.nodeId,parentId:item.parentId,name:item.name,kind:item.kind,virtual:false,summary:item.summary,hasMore:item.kind==='directory'};
}
const decimal=(v:unknown)=>typeof v==='string'&&v.length<=128&&/^(0|[1-9][0-9]*)$/.test(v);
const text=(v:unknown,max:number)=>typeof v==='string'&&new TextEncoder().encode(v).length<=max;
const kinds=['file','directory','symlink','fifo','socket','blockDevice','characterDevice'];
// Validate echoed identities and bounded metadata before exposing any page to the renderer.
export function checkedSearchListPage(value:unknown,{view,pin}:ListBinding):SearchListPage {
 const p=value as SearchListPage;
 if(pin.profileId!==view.profileId||pin.snapshotId!==view.snapshotId||!p||p.snapshotId!==view.snapshotId||p.rootId!==view.rootId||p.metric!==view.metric||p.scope!==view.search.scope||p.selectionOrder!==`${view.sort}-${view.direction}-recorded-rowid`||!Array.isArray(p.items)||p.items.length>50||typeof p.hasMore!=='boolean'||typeof p.workLimited!=='boolean'||!text(p.nextCursor,512)||p.hasMore!==!!p.nextCursor||p.workLimited&&!p.hasMore)throw Error('INVALID_SEARCH_PAGE');
 const ids=new Set<string>();
 for(const i of p.items){
  if(!i||i.snapshotId!==view.snapshotId||!text(i.nodeId,128)||!i.nodeId||ids.has(i.nodeId)||!(i.parentId===null||text(i.parentId,128))||!text(i.name,65536)||!text(i.path,65536)||!kinds.includes(i.kind)||!text(i.volumeId,128)||!(i.shareId===null||text(i.shareId,128))||!(i.size===null||decimal(i.size))||!(i.modifiedAt===null||text(i.modifiedAt,64)&&Number.isFinite(Date.parse(i.modifiedAt)))||!i.summary||![i.summary.knownBytes,i.summary.unknownFileCount,i.summary.fileCount,i.summary.directoryCount,i.summary.specialCount].every(decimal))throw Error('INVALID_SEARCH_PAGE');
  if(i.classification!==undefined&&i.classification!==null){const c=i.classification;const categories=['image','video','audio','document','archive','diskImage','database','otherUnknown'];if(!(c.category===null||categories.includes(c.category))||!['extension','unknown','not-applicable'].includes(c.basis)||!Number.isSafeInteger(c.mappingRevision)||c.mappingRevision<1||!['not-directory','no-regular-files','single-category','mixed'].includes(c.folderComposition)||!Array.isArray(c.folderCategories)||c.folderCategories.length>8||c.folderCategories.some(x=>!categories.includes(x))||!Array.isArray(c.contextHints)||c.contextHints.length>1||c.contextHints.some(x=>x!=='docker-directory-name')||(i.kind==='file'?(c.category===null||c.basis==='not-applicable'):(c.category!==null||c.basis!=='not-applicable')))throw Error('INVALID_SEARCH_PAGE');}
  if(!['file','directory'].includes(i.kind)&&i.size!==null)throw Error('INVALID_SEARCH_PAGE');ids.add(i.nodeId);
 }
 return p;
}
export interface PagePosition {cursor:string;number:number;back:Array<{cursor:string;number:number}>}
export const initialPagePosition=():PagePosition=>({cursor:'',number:1,back:[]});
// Only a bounded cursor history is retained; forward navigation has no total-result limit.
export function nextPagePosition(current:PagePosition,cursor:string):PagePosition {
 if(!cursor||cursor===current.cursor||!text(cursor,512))throw Error('INVALID_CURSOR');
 return{cursor,number:current.number+1,back:[...current.back,{cursor:current.cursor,number:current.number}].slice(-128)};
}
export function previousPagePosition(current:PagePosition):PagePosition {
 const previous=current.back.at(-1);return previous?{...previous,back:current.back.slice(0,-1)}:current;
}
