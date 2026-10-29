import type {Scope} from './contracts';

export type RecycleState='readable'|'denied'|'missing'|'unknown';
export type ScopeState = 'reachable'|'unmounted'|'locked'|'unreadable'|'missing'|'unverified';
export interface ScopeInventory {
 volumes:{id:string;name:string;state:ScopeState}[];
 shares:{id:string;name:string;volumeId:string|null;state:ScopeState;readable:boolean;recycleState?:RecycleState}[];
 observedAt:string;
}
export interface ScopeCheck {
 scope:Scope;checkedAt:string;
 roots:{targetId:string;kind:'volume'|'share';volumeId:string|null;state:ScopeState}[];
 coveredTargetIds:string[];
 issues:{targetId:string;code:string;state:ScopeState}[];
}
export interface ScopeSelectionState {nas:boolean;volumeIds:string[];shareIds:string[]}
export type CheckState = 'checked'|'mixed'|'unchecked';
export const emptySelection=():ScopeSelectionState=>({nas:false,volumeIds:[],shareIds:[]});
const id=/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
const states:ScopeState[]=['reachable','unmounted','locked','unreadable','missing','unverified'];
const timestamp=(x:unknown):x is string=>typeof x==='string'&&x.length<=64&&Number.isFinite(Date.parse(x));
const label=(x:unknown):x is string=>typeof x==='string'&&x.length>0&&x.length<=1024&&!/[\u0000-\u001f\u007f]/u.test(x);
// The browser receives identities and observed states, never physical paths or root bindings.
export function checkedScopeInventory(value:unknown):ScopeInventory {
 const v=value as ScopeInventory;
 if(!v||!Array.isArray(v.volumes)||v.volumes.length>512||!Array.isArray(v.shares)||v.shares.length>1024||!timestamp(v.observedAt))throw new Error('INVALID_SCOPE_INVENTORY');
 const ids=new Set<string>(),volumes=new Set<string>();
 for(const x of v.volumes){if(!x||typeof x.id!=='string'||!id.test(x.id)||ids.has(x.id)||!label(x.name)||!states.includes(x.state))throw new Error('INVALID_SCOPE_INVENTORY');ids.add(x.id);volumes.add(x.id);}
 for(const x of v.shares){if(!x||typeof x.id!=='string'||!id.test(x.id)||ids.has(x.id)||!label(x.name)||!states.includes(x.state)||typeof x.readable!=='boolean'||(x.recycleState!==undefined&&!['readable','denied','missing','unknown'].includes(x.recycleState))||(x.volumeId!==null&&(typeof x.volumeId!=='string'||!volumes.has(x.volumeId)))||(x.state==='reachable'&&(!x.readable||x.volumeId===null))||(x.state!=='reachable'&&x.readable))throw new Error('INVALID_SCOPE_INVENTORY');ids.add(x.id);}
 return v;
}
// Whole-volume intent is distinct from selecting every currently listed share.
export function normalizedSelection(s:ScopeSelectionState,i:ScopeInventory):Scope|null {
 if(s.nas)return {kind:'nas',targetIds:[]};
 const volumes=[...new Set(s.volumeIds)].sort(),allShares=[...new Set(s.shareIds)].sort();
 if(volumes.some(v=>!i.volumes.some(x=>x.id===v))||allShares.some(v=>!i.shares.some(x=>x.id===v)))throw new Error('SELECTION_TARGET_MISSING');
 const shares=allShares.filter(v=>!volumes.includes(i.shares.find(x=>x.id===v)!.volumeId??''));
 const targetIds=[...volumes,...shares].sort();
 return targetIds.length?{kind:volumes.length?(shares.length?'mixed':'volumes'):'shares',targetIds}:null;
}
// Partial means selected descendants without whole-container intent, including all known shares.
export function selectionCheckState(s:ScopeSelectionState,i:ScopeInventory,kind:'nas'|'volume'|'share',target=''):CheckState {
 if(s.nas)return 'checked';
 if(kind==='nas')return s.volumeIds.length||s.shareIds.length?'mixed':'unchecked';
 if(kind==='volume')return s.volumeIds.includes(target)?'checked':i.shares.some(x=>x.volumeId===target&&s.shareIds.includes(x.id))?'mixed':'unchecked';
 const share=i.shares.find(x=>x.id===target);
 return s.shareIds.includes(target)||(share?.volumeId!==null&&s.volumeIds.includes(share?.volumeId??''))?'checked':'unchecked';
}
// Unchecking a descendant deliberately reduces whole-container intent to explicit remaining targets.
export function toggleSelection(s:ScopeSelectionState,i:ScopeInventory,kind:'nas'|'volume'|'share',target:string,checked:boolean):{selection:ScopeSelectionState;reduced:boolean} {
 if(kind==='nas')return {selection:checked?{nas:true,volumeIds:[],shareIds:[]}:emptySelection(),reduced:false};
 if(kind==='volume'&&!i.volumes.some(x=>x.id===target)||kind==='share'&&!i.shares.some(x=>x.id===target))throw new Error('UNKNOWN_SCOPE_TARGET');
 const n={nas:false,volumeIds:s.nas?i.volumes.map(x=>x.id):[...s.volumeIds],shareIds:[...s.shareIds]};let reduced=s.nas;
 if(kind==='volume'){
  n.volumeIds=n.volumeIds.filter(x=>x!==target);n.shareIds=n.shareIds.filter(x=>i.shares.find(y=>y.id===x)?.volumeId!==target);
  if(checked)n.volumeIds.push(target);
 }else{
  const share=i.shares.find(x=>x.id===target)!;
  if(!checked&&share.volumeId&&n.volumeIds.includes(share.volumeId)){
   reduced=true;n.volumeIds=n.volumeIds.filter(x=>x!==share.volumeId);
   n.shareIds.push(...i.shares.filter(x=>x.volumeId===share.volumeId&&x.id!==target).map(x=>x.id));
  }
  n.shareIds=n.shareIds.filter(x=>x!==target);if(checked&&!n.volumeIds.includes(share.volumeId??''))n.shareIds.push(target);
 }
 n.volumeIds=[...new Set(n.volumeIds)].sort();n.shareIds=[...new Set(n.shareIds)].sort();return {selection:n,reduced};
}
// Canonical request identity binds an asynchronous check to exactly the chosen intent.
export function scopeSelectionKey(s:Scope):string {return JSON.stringify({kind:s.kind,targetIds:[...s.targetIds].sort(),...(s.relativePath!==undefined?{relativePath:s.relativePath}:{})});}
// A check cannot silently qualify a different selection, duplicate roots, or a foreign target.
export function checkedScopeCheck(value:unknown,scope:Scope,i:ScopeInventory):ScopeCheck {
 const v=value as ScopeCheck;
 if(!v||!v.scope||!Array.isArray(v.scope.targetIds)||scopeSelectionKey(v.scope)!==scopeSelectionKey(scope)||!timestamp(v.checkedAt)||!Array.isArray(v.roots)||v.roots.length>1536||!Array.isArray(v.coveredTargetIds)||v.coveredTargetIds.length>1536||!Array.isArray(v.issues)||v.issues.length>3072)throw new Error('INVALID_SCOPE_CHECK');
 const selected=new Set(scope.kind==='nas'?i.volumes.map(x=>x.id):scope.targetIds);
 // NAS and volume selections expand to their authorized shares, never bare volume roots.
 const expected=i.shares.filter(x=>selected.has(x.id)||selected.has(x.volumeId??'')).map(x=>x.id);
 const roots=new Set<string>();
 for(const r of v.roots){const volume=i.volumes.find(x=>x.id===r?.targetId),share=i.shares.find(x=>x.id===r?.targetId);if(!r||roots.has(r.targetId)||!expected.includes(r.targetId)||!states.includes(r.state)||r.kind!==(volume?'volume':'share')||r.volumeId!==(volume?volume.id:share?.volumeId))throw new Error('INVALID_SCOPE_CHECK');roots.add(r.targetId);}
 if(roots.size!==expected.length)throw new Error('INVALID_SCOPE_CHECK');
 const allowed=new Set([...selected,...i.shares.filter(x=>selected.has(x.volumeId??'')||scope.kind==='nas').map(x=>x.id)]);
 if(v.coveredTargetIds.length!==new Set(v.coveredTargetIds).size||v.coveredTargetIds.some(x=>!allowed.has(x))||[...allowed].some(x=>!v.coveredTargetIds.includes(x)))throw new Error('INVALID_SCOPE_CHECK');
 for(const x of v.issues){if(!x||!allowed.has(x.targetId)||typeof x.code!=='string'||!/^SCOPE_[A-Z_]{1,64}$/.test(x.code)||!states.includes(x.state))throw new Error('INVALID_SCOPE_CHECK');}
 return v;
}
