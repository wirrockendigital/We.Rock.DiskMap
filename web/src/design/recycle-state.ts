import type {Scope} from '../contracts';
import type {RecycleState,ScopeInventory} from '../scope-selection';
// Folder navigation inherits its share's bin; NAS/volume absence requires evidence for every share.
export function recycleStateForScope(scope:Scope|undefined,inventory:ScopeInventory|null|undefined):RecycleState{
 if(!scope||!inventory)return 'unknown';
 const shares=inventory.shares.filter(s=>scope.kind==='nas'||(scope.kind==='volumes'?scope.targetIds.includes(s.volumeId??''):scope.targetIds.includes(s.id)));
 if(!shares.length)return 'unknown';
 const states=shares.map(s=>s.recycleState??'unknown');
 if(states.includes('denied'))return 'denied';
 if(states.includes('unknown'))return 'unknown';
 return states.includes('readable')?'readable':'missing';
}
