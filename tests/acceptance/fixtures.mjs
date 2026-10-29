import { createHash } from 'node:crypto';

// Synthetic metadata describes topology and errors; it never creates NAS files or ACLs.
export function fixture() {
  const nodes=[];
  const add=(id,parentId,kind,logicalBytes,extra={})=>nodes.push({id,parentId,kind,logicalBytes,...extra});
  add('nas',null,'directory','0');
  for(let volume=1;volume<=2;volume++) {
    add(`v${volume}`,'nas','directory','0',{volumeId:`synthetic-volume-${volume}`});
    for(let share=1;share<=2;share++) add(`v${volume}s${share}`,`v${volume}`,'directory','0',{shareId:`synthetic-share-${volume}-${share}`});
  }
  add('direct','v1s1','file','13',{name:'ä-😀-#-%-\"-<script>.txt',linkGroupId:'synthetic-hardlink'});
  add('nested','v1s1','directory','0'); add('tiny','nested','file','7');
  add('huge','v2s1','file','9007199254740993');
  add('hardlink','v2s1','file','13',{linkGroupId:'synthetic-hardlink'});
  add('zero','v2s2','file','0'); add('unknown','v2s2','file',null);
  add('loop','nested','symlink','0',{target:'nested',follow:false});
  return {formatVersion:1,synthetic:true,nodes,coverage:[{rootId:'v1s2',state:'readable',empty:true},{rootId:'unknown',state:'error',reason:'permission-denied'},{rootId:'nested',state:'excluded',reason:'foreign-mount-not-entered'}]};
}
// Stable JSON property order and no clock or randomness make byte hashes repeatable.
export function manifest(value=fixture()) {
  const files=value.nodes.filter(n=>n.kind==='file');
  return {sha256:createHash('sha256').update(JSON.stringify(value)).digest('hex'),files:files.length,
    knownSum:files.reduce((sum,n)=>sum+(n.logicalBytes===null?0n:BigInt(n.logicalBytes)),0n).toString(),
    unknown:files.filter(n=>n.logicalBytes===null).length,zero:files.filter(n=>n.logicalBytes==='0').length};
}
