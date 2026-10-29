import type {MapModel} from '../shared/model';
import type {HitTarget} from '../shared/interaction';
export interface TreemapActions {onSelect?:(id:string)=>void;onFocus?:(id:string)=>void;onOpen?:(id:string)=>void;onGroup?:(target:HitTarget)=>void}
// Display and server remainder groups are inspected locally and never become backend node actions.
export function activateTreemap(model:MapModel,target:HitTarget,open:boolean,callbacks:TreemapActions):void {
 if(target.kind==='display-group'){callbacks.onGroup?.(target);return;}
 const node=model.nodes[target.nodeId];if(!node)return;
 if(node.kind==='group'){callbacks.onGroup?.(target);return;}
 callbacks.onFocus?.(node.id);
 if(open&&['nas','volume','share','other','directory'].includes(node.kind))callbacks.onOpen?.(node.id);
 else callbacks.onSelect?.(node.id);
}
