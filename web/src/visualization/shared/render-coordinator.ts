// One scheduled frame and one cooperative task own rendering; obsolete results and errors are discarded.
export class LatestRenderCoordinator<Input,Output> {
 private epoch=0;private disposed=false;private running=false;private pending:{value:Input;epoch:number}|null=null;private abort:AbortController|null=null;private frame:number|null=null;
 constructor(private readonly work:(input:Input,signal:AbortSignal)=>Promise<Output>,private readonly publish:(output:Output)=>void,private readonly failure:(error:unknown)=>void,private readonly schedule:(callback:()=>void)=>number=callback=>requestAnimationFrame(callback),private readonly unschedule:(id:number)=>void=id=>cancelAnimationFrame(id)){}
 request(value:Input):void {
  if(this.disposed)return;
  this.pending={value,epoch:++this.epoch};this.abort?.abort();this.queue();
 }
 private queue():void {
  if(this.disposed||this.running||this.frame!==null||!this.pending)return;
  this.frame=this.schedule(()=>{this.frame=null;void this.run();});
 }
 private async run():Promise<void> {
  const task=this.pending;if(!task||this.disposed)return;this.pending=null;this.running=true;
  const abort=new AbortController();this.abort=abort;
  try{const result=await this.work(task.value,abort.signal);if(!this.disposed&&!abort.signal.aborted&&this.epoch===task.epoch)this.publish(result);}
  catch(error){if(!this.disposed&&!abort.signal.aborted&&this.epoch===task.epoch)this.failure(error);}
  finally{this.running=false;if(this.abort===abort)this.abort=null;this.queue();}
 }
 // Disposal is permanent; even an abort-ignoring task can no longer publish or schedule work.
 dispose():void {this.disposed=true;this.epoch++;this.pending=null;this.abort?.abort();if(this.frame!==null)this.unschedule(this.frame);this.frame=null;}
}
