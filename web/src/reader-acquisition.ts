// Coalesced server opens share one resource until every local participant settles.
export class ReaderAcquisitions {
  private readonly entries = new Map<string, {users:number; retained:boolean}>();
  begin(id:string, discard:()=>void): (retained:boolean)=>void {
    const entry=this.entries.get(id)??{users:0,retained:false};
    entry.users++;this.entries.set(id,entry);
    let finished=false;
    return retained=>{
      // Each participant may relinquish its claim exactly once.
      if(finished)return;
      finished=true;entry.retained ||= retained;
      if(--entry.users!==0)return;
      this.entries.delete(id);
      if(!entry.retained)discard();
    };
  }
}
