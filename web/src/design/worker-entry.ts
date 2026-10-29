import {configureMapWorkers} from './worker-runtime';
// Static URLs let Vite emit both workers as same-origin, versioned package assets.
configureMapWorkers({packed:()=>new Worker(new URL('./packed-map.worker.ts',import.meta.url),{type:'module'}),layout:()=>new Worker(new URL('./chart-layout.worker.ts',import.meta.url),{type:'module'})});
