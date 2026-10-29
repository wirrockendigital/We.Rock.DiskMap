import type {NativeStorageNode} from './useNativeDiskMap';
import type {Scope} from '../contracts';
import type {ScopeInventory} from '../scope-selection';
const sourceId=(s:Scope)=>'source:'+s.kind+':'+s.targetIds.join(',')+':'+(s.relativePath??'.');
// Only actual navigable containers own a displayed size; descendants cannot overwrite a volume.
export function recordedSourceSizes(current:NativeStorageNode|null,path:NativeStorageNode[],inventory:ScopeInventory|null):Map<string,string>{
 const sizes=new Map<string,string>();
 const recordSize=(node:NativeStorageNode,ancestors:NativeStorageNode[])=>{
  // File and residual-group leaves never contribute a navigation folder identity.
  if(node.sourceKind==='file'||node.sourceKind==='group')return;
  const chain=[...ancestors,node],shareNode=chain.find(n=>n.sourceKind==='share'),volumeNode=chain.find(n=>n.sourceKind==='volume');
  let scope:Scope|undefined;
  if(shareNode&&['share','directory'].includes(node.sourceKind??'')){const share=inventory?.shares.find(s=>s.id===shareNode.shareId||s.name===shareNode.name);if(share){const tail=chain.slice(chain.indexOf(shareNode)+1).map(n=>n.name).filter(n=>n!=='.');scope=tail.length?{kind:'folder',targetIds:[share.id],relativePath:tail.join('/')}:{kind:'shares',targetIds:[share.id]};}}
  else if(volumeNode&&node.sourceKind==='volume'){const v=inventory?.volumes.find(v=>v.id===volumeNode.volumeId||v.name===volumeNode.name);if(v)scope={kind:'volumes',targetIds:[v.id]};}
  else if(node.sourceKind==='nas')scope={kind:'nas',targetIds:[]};
  if(scope&&node.bytesExact!==undefined)sizes.set(sourceId(scope),node.bytesExact);
  for(const child of node.children??[])recordSize(child as NativeStorageNode,chain);
 };
 if(current){for(let i=0;i<path.length-1;i++){const n=path[i];recordSize({...n,children:undefined},path.slice(0,i));}recordSize(current,path.slice(0,-1));}
 return sizes;
}
