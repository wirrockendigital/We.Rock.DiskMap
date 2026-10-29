// Entry points register bundler-resolved workers without coupling pure component tests to asset URLs.
let factories:Record<'layout'|'packed',()=>Worker>|null=null;
export function configureMapWorkers(value:Record<'layout'|'packed',()=>Worker>){factories=value;}
export function createMapWorker(kind:'layout'|'packed'):Worker{if(!factories)throw Error('MAP_WORKER_UNAVAILABLE');return factories[kind]();}
