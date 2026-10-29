import type { AnalysisNode } from '../../analysis-query';
import type { DetailSelection } from '../../node-details-client';

export type InteractionIdentity = Omit<DetailSelection, 'nodeId'>;
// Both map hit tests and accessible list rows must carry the same qualified identity.
export const interactionKey = (identity: InteractionIdentity): string => JSON.stringify(identity);
export const selectionID = (node: AnalysisNode): string => node.realNodeId ?? node.id;
export function nodeCapabilities(node: AnalysisNode) {
 // Source containers remain navigable projections without filesystem capabilities.
 const sourceContainer = ['share','other'].includes(node.kind);
 const real = !sourceContainer && !node.virtual && node.realNodeId !== null;
 const selectable = ['file','directory','symlink','fifo','socket','blockDevice','characterDevice','nas','volume','share','other'].includes(node.kind);
 return { selectable, open: selectable && ['directory','nas','volume','share','other'].includes(node.kind), doubleClick: selectable && node.kind==='directory', fileStation: real && ['file','directory'].includes(node.kind), copy: real && selectable };
}
export interface InteractionCallbacks { select:(node:AnalysisNode)=>void; open:(node:AnalysisNode)=>void; current:()=>boolean }
// Selection is immediate. The central controller owns superseding selection-only requests
// so a subsequent folder double click can navigate without relying on OS click timing.
// The adapter never stores selection, geometry, paths or application state.
export function nodeInteraction(node:AnalysisNode, callbacks:InteractionCallbacks) {
 const select=()=>{if(callbacks.current()&&nodeCapabilities(node).selectable)callbacks.select(node);};
 return {
  click:select,
  select,
  doubleClick:()=>{if(callbacks.current()){const caps=nodeCapabilities(node);if(caps.doubleClick)callbacks.open(node);else if(caps.selectable)callbacks.select(node);}},
  open:()=>{if(callbacks.current()&&nodeCapabilities(node).open)callbacks.open(node);},
 };
}
