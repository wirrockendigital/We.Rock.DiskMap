// One scheduler owns a serial observation round, bounded retry and visibility suspension.
export class StatusObserver {
  private timer: ReturnType<typeof setTimeout> | undefined;
  private controller: AbortController | null = null;
  private stopped = false;
  private hidden = false;
  private resume = false;
  private failures = 0;
  private epoch = 0;
  private readonly delays = [1000, 2000, 4000, 8000, 15000, 30000];
  constructor(private readonly round:(signal:AbortSignal)=>Promise<void>, private readonly health:(state:'current'|'retrying'|'stopped')=>void,
    private readonly retryable:(error:unknown)=>boolean, private readonly failed:(error:unknown)=>void) {}
  // Replacement cancels transport and invalidates even an abort-ignoring completion.
  stop(){this.resume=false;this.stopped=true;++this.epoch;this.controller?.abort();clearTimeout(this.timer);this.timer=undefined;}
  start(){this.stop();this.stopped=false;this.failures=0;this.resume=this.hidden;if(!this.hidden)void this.run();}
  visibility(hidden:boolean){if(this.hidden===hidden)return;this.hidden=hidden;if(hidden){const resume=!this.stopped;this.stop();this.resume=resume;}else if(this.resume){this.stopped=false;void this.run();}}
  private schedule(ms:number){this.timer=setTimeout(()=>{this.timer=undefined;void this.run();},ms);}
  private async run(){
    if(this.stopped||this.hidden)return;
    const epoch=++this.epoch, request=new AbortController();this.controller=request;
    const current=()=>!this.stopped&&!this.hidden&&!request.signal.aborted&&epoch===this.epoch;
    // The full round has one deadline; a transport ignoring Abort cannot stall the scheduler.
    const signal=AbortSignal.any([request.signal,AbortSignal.timeout(20000)]);
    let abort:()=>void=()=>{};
    try{
      await Promise.race([this.round(signal),new Promise<never>((_,reject)=>{abort=()=>reject(signal.reason);signal.addEventListener('abort',abort,{once:true});})]);
      if(!current())return;this.failures=0;this.health('current');this.schedule(1500);
    }catch(error){
      if(!current())return;
      // Deadline revokes authority before a simultaneous old authentication error is interpreted.
      const qualified=signal.aborted?signal.reason:error;
      if(this.retryable(qualified)&&this.failures<this.delays.length){this.health('retrying');this.schedule(this.delays[this.failures++]);}
      else{this.stopped=true;this.health('stopped');this.failed(qualified);}
    }finally{signal.removeEventListener('abort',abort);}
  }
}
