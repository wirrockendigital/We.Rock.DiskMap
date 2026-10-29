import { SessionDenied, sessionRequest, currentToken } from './session';
import type { SessionState } from './session';

type MonitorOptions = {
  visible: () => boolean;
  changed: (state: SessionState, principal?:string) => void;
  request?: (signal: AbortSignal) => Promise<unknown>;
  schedule?: (callback: () => void, delay: number) => number;
  cancel?: (timer: number) => void;
};

// Focus and visibility checks preserve an authenticated workspace until access actually fails.
export function monitorSession(options: MonitorOptions) {
  const request = options.request ?? (signal => sessionRequest('session', signal));
  const schedule = options.schedule ?? ((callback, delay) => window.setTimeout(callback, delay));
  const cancel = options.cancel ?? (timer => window.clearTimeout(timer));
  let stopped = false;
  // A local token observer clears the UI promptly without adding DSM network polling.
  let token=options.request===undefined?currentToken():null;let tokenTimer:number|undefined;
  let principal:string|undefined;
  let state: SessionState = 'checking';
  const changed = (next: SessionState) => { state = next; options.changed(next,principal); };
  let generation = 0;
  let active: AbortController | null = null;
  let poll: number | undefined;
  let deadline: number | undefined;
  const clearTimers = () => {
    if (poll !== undefined) cancel(poll);
    if (deadline !== undefined) cancel(deadline);
    poll = undefined; deadline = undefined;
  };
  const invalidate = () => {
    generation++; clearTimers(); active?.abort(); active = null;
  };
  const check = async (reentry: boolean) => {
    if (stopped) return;
    if (reentry) { invalidate(); changed('checking'); }
    if (!options.visible() || active) return;
    if (poll !== undefined) cancel(poll);
    poll = undefined;
    const current = ++generation;
    const controller = new AbortController(); active = controller;
    let expired = false;
    deadline = schedule(() => {
      if (stopped || current !== generation) return;
      expired = true; changed('unavailable'); controller.abort();
    }, 5000);
    try {
      const reply=await request(controller.signal) as {data?:{principal?:unknown}}|undefined;
      // Superseded responses must not mutate even the remembered owner identity.
      if (stopped || current !== generation || expired || !options.visible()) return;
      const next=reply?.data?.principal;
      if(typeof next==='string'&&next.length>0&&next.length<=256){
        if(principal!==undefined&&principal!==next)changed('checking');
        principal=next;
      }
      if (!stopped && current === generation && !expired && options.visible()) changed('authenticated');
    } catch (error) {
      if (!stopped && current === generation && options.visible()) changed(error instanceof SessionDenied ? 'denied' : 'unavailable');
    } finally {
      if (current === generation) {
        if (deadline !== undefined) cancel(deadline);
        deadline = undefined; active = null;
        if (!stopped && options.visible()) poll = schedule(() => { void check(false); }, 15000);
      }
    }
  };
  // Hidden documents abort pending work and have no scheduled authorization traffic.
  const visibility = () => { if (options.visible()) { void check(state !== 'authenticated'); } else { invalidate(); } };
  const observeToken=()=>{
    if(stopped)return;
    const current=currentToken();
    if(current!==token){token=current;principal=undefined;invalidate();changed('checking');void check(true);}
    tokenTimer=schedule(observeToken,250);
  };
  if(options.request===undefined) tokenTimer=schedule(observeToken,250);
  void check(true);
  return {
    // Do not unmount the click target on visible focus or restart an in-flight authorization deadline.
    focus: () => { if (options.visible()) void check(state !== 'authenticated'); },
    visibility,
    stop: () => { stopped = true; invalidate();if(tokenTimer!==undefined)cancel(tokenTimer); },
  };
}
