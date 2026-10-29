import {layout} from './chart-layout';
// A terminated worker cannot publish geometry for a replaced view or viewport.
self.onmessage=event=>{try{self.postMessage(layout(event.data.root,event.data.mode,event.data.box));}catch{self.postMessage({error:'MAP_LAYOUT_FAILED'});}};
