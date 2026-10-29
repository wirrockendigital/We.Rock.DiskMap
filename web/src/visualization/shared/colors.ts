import type {MapModel} from './model';

export type ColorMode='folder'|'filetype'|'mtime';
export type ColorToken=Readonly<{fill:string;text:string;pattern:'none'|'mixed'|'unknown';key:string;labelKey:string}>;
// Dark categorical fills retain readable white labels; hue is never a state-only signal.
export const palette=Object.freeze(['#164e63','#5b21b6','#92400e','#065f46','#9d174d','#1e40af','#4b5563','#713f12']);
export const typeKeys=Object.freeze(['image','video','audio','document','archive','diskImage','database','otherUnknown']);
const token=(key:string,index:number,pattern:ColorToken['pattern']='none'):ColorToken=>Object.freeze({fill:palette[index],text:'#ffffff',pattern,key,labelKey:key});
// Qualified source keys, not snapshot IDs or result positions, determine branch hues.
function hash(value:string):number{let n=2166136261;for(const c of value){n^=c.codePointAt(0)!;n=Math.imul(n,16777619);}return n>>>0;}
export function colorForNode(model:MapModel,nodeId:string,mode:ColorMode,referenceTime:string):ColorToken {
 const node=model.nodes[nodeId];if(!node)throw Error('INVALID_COLOR_NODE');
 if(mode==='folder'){
  // Missing historical qualification remains unknown; never derive a key from display paths.
  return node.colorBranchKey?token('branch',hash(node.colorBranchKey)%palette.length):token('unknown',6,'unknown');
 }
 if(mode==='filetype'){
  const c=node.classification as {category?:unknown;folderComposition?:unknown;folderCategories?:unknown}|null;
  if(node.kind==='directory'){
   if(c?.folderComposition==='mixed')return token('mixed',6,'mixed');
   if(c?.folderComposition==='single-category'&&Array.isArray(c.folderCategories)&&c.folderCategories.length===1){const i=typeKeys.indexOf(c.folderCategories[0]);if(i>=0)return token(typeKeys[i],i);}
   return token('unknown',6,'unknown');
  }
  const i=node.kind==='file'&&typeof c?.category==='string'?typeKeys.indexOf(c.category):-1;
  return i>=0?token(typeKeys[i],i):token('unknown',6,'unknown');
 }
 if(mode==='mtime'){
  const reference=Date.parse(referenceTime);if(!Number.isFinite(reference))throw Error('INVALID_COLOR_REFERENCE');
  const time=node.modifiedAt===null?NaN:Date.parse(node.modifiedAt);
  if(!Number.isFinite(time))return token('unknown',6,'unknown');
  const age=reference-time;
  if(age<0)return token('future',4,'unknown');
  if(age<30*86400000)return token('recent',0);
  if(age<365*86400000)return token('year',1);
  return token('older',2);
 }
 throw Error('INVALID_COLOR_MODE');
}
