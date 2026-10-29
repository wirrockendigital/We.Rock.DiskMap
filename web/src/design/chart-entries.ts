import {sizeOf,type StorageNode} from './model';
// Match treemap leaves and residual tiles without mixing search or group-page results into the map scope.
export function chartEntries(root:StorageNode,language:'de'|'en'):StorageNode[]{
 const entries:StorageNode[]=[];
 const size=(n:StorageNode)=>BigInt(n.bytesExact??Math.max(0,Math.trunc(sizeOf(n))));
 const visit=(node:StorageNode)=>{
  const children=node.children??[],remaining=size(node)-children.reduce((sum,child)=>sum+size(child),0n);
  if(remaining>0n)entries.push(children.length?{...node,children:undefined,bytesExact:remaining.toString(),name:(language==='de'?'Weitere Inhalte in ':'Other contents in ')+node.name}:node);
  children.forEach(visit);
 };
 visit(root);return entries;
}
