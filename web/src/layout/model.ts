// Bounded semantic hierarchy shared by both chart geometries.
export interface MapNode {
  id: string; parentId: string | null; kind: 'directory' | 'file' | 'aggregate';
  knownBytes: string; unknownCount: number; memberCount: number; children?: MapNode[];
}
export interface InputNode { id: string; parentId: string | null; kind: string; logicalBytes: string | null }

// Streaming aggregation runs before browser delivery and retains parent-local groups.
export class BoundedHierarchy {
  private directories = new Map<string, MapNode>();
  private aggregates = new Map<string, MapNode>();
  private root: MapNode | undefined;
  private retained = 0;
  private seen = 0;
  private readonly fileBudget: number;
  private readonly directoryBudget: number;
  constructor(fileBudget: number, directoryBudget = 300) {
    this.fileBudget=fileBudget;this.directoryBudget=directoryBudget;
    if (!Number.isInteger(fileBudget) || fileBudget < 0 || directoryBudget < 1) throw new Error('INVALID_BUDGET');
  }
  // Parent-before-child input allows bounded validation without a full source tree.
  push(input: InputNode): void {
    if (input.id !== String(this.seen++)) throw new Error('INVALID_SEQUENCE');
    if (input.logicalBytes !== null && !/^(0|[1-9][0-9]*)$/.test(input.logicalBytes)) throw new Error('INVALID_BYTES');
    const parent = input.parentId === null ? undefined : this.directories.get(input.parentId);
    if (input.parentId !== null && !parent) throw new Error('PARENT_MISSING');
    if (input.parentId === null && this.root) throw new Error('MULTIPLE_ROOTS');
    if (input.kind === 'directory') {
      if (this.directories.size >= this.directoryBudget) throw new Error('DIRECTORY_BUDGET');
      const node: MapNode = {id: input.id, parentId: input.parentId, kind:'directory', knownBytes:'0', unknownCount:0, memberCount:0, children:[]};
      this.directories.set(input.id,node); if(parent) parent.children!.push(node); else this.root=node;
    } else {
      if (input.kind !== 'file' || !parent) throw new Error('INVALID_FILE');
      let node: MapNode;
      if(this.retained < this.fileBudget) {
        node={id:input.id,parentId:input.parentId,kind:'file',knownBytes:'0',unknownCount:0,memberCount:0};
        parent.children!.push(node);this.retained++;
      } else {
        node=this.aggregates.get(parent.id)!;
        if(!node){node={id:`aggregate:${parent.id}`,parentId:parent.id,kind:'aggregate',knownBytes:'0',unknownCount:0,memberCount:0};parent.children!.push(node);this.aggregates.set(parent.id,node);}
      }
      node.memberCount++;if(input.logicalBytes===null)node.unknownCount++;else node.knownBytes=(BigInt(node.knownBytes)+BigInt(input.logicalBytes)).toString();
    }
  }
  // The response remains bounded even for ten million streamed input records.
  finish(): MapNode { if(!this.root) throw new Error('EMPTY_HIERARCHY');return this.root; }
}

// Exact totals remain independent from floating-point chart coordinates.
export function totals(root: MapNode): {knownBytes:string;unknownCount:number;memberCount:number;nodes:number} {
  let sum=0n,unknownCount=0,memberCount=0,nodes=0;const stack=[root];
  while(stack.length){const n=stack.pop()!;nodes++;if(n.children)stack.push(...n.children);else{sum+=BigInt(n.knownBytes);unknownCount+=n.unknownCount;memberCount+=n.memberCount;}}
  return {knownBytes:sum.toString(),unknownCount,memberCount,nodes};
}
