import {test} from 'node:test';
import assert from 'node:assert/strict';
import {rolldown} from 'rolldown';
import vm from 'node:vm';
// Render the production component with deterministic hooks and an explicitly delivered viewport.
test('chart waits for measurement and does not restart unchanged size or zoom',async()=>{
 const build=await rolldown({input:new URL('../src/design/StorageChart.tsx',import.meta.url).pathname,platform:'node',plugins:[{name:'viewport-fixture',resolveId(id){if(['react','react/jsx-runtime'].includes(id))return {id,external:true};},load(id){if(id.endsWith('/size-unit.tsx'))return 'export const useSizeFormatter=()=>()=>"";';if(id.endsWith('/worker-runtime.ts'))return 'export const createMapWorker=()=>{throw Error("Unexpected preview worker")};';}}]});
 const {output}=await build.generate({format:'cjs'});await build.close();
 let cursor=0,dirty=false,resize;const slots=[],pending=[],calls=[];
 const same=(a,b)=>a&&b&&a.length===b.length&&a.every((v,i)=>Object.is(v,b[i]));
 const react={useState(initial){const at=cursor++;slots[at]??={value:initial};return [slots[at].value,next=>{const value=typeof next==='function'?next(slots[at].value):next;if(!Object.is(value,slots[at].value)){slots[at].value=value;dirty=true;}}];},useRef(initial){const at=cursor++;slots[at]??={current:at===0?{}:initial};return slots[at];},useMemo(fn,deps){const at=cursor++;if(!same(slots[at]?.deps,deps))slots[at]={deps,value:fn()};return slots[at].value;},useEffect(fn,deps){const at=cursor++;if(!same(slots[at]?.deps,deps)){pending.push(()=>{slots[at]?.cleanup?.();slots[at]={deps,cleanup:fn()};});}}};
 const jsx={jsx:(type,props)=>({type,props}),jsxs:(type,props)=>({type,props})};
 const module={exports:{}};
 vm.runInNewContext(output[0].code,{module,exports:module.exports,require:id=>{if(id==='react')return react;if(id==='react/jsx-runtime')return jsx;throw Error(id);},AbortController,clearTimeout,Map,Set,Math,ResizeObserver:class{constructor(fn){resize=fn;}observe(){}disconnect(){}}});
 const props={root:{id:'root',name:'Root',kind:'folder'},mode:'treemap',language:'de',hovered:null,onHover(){},onOpen(){},loadGeometry:(_root,_mode,box)=>{calls.push({...box});return new Promise(()=>{});}};
 const render=()=>{for(let n=0;n<20;n++){dirty=false;cursor=0;module.exports.StorageChart(props);while(pending.length)pending.shift()();if(!dirty)return;}throw Error('Render loop');};
 try{
  render();assert.deepEqual(calls,[],'no placeholder layout before measurement');
  resize([{contentRect:{width:1280,height:800}}]);render();assert.deepEqual(calls,[{width:1280,height:800}]);
  resize([{contentRect:{width:1280,height:800}}]);render();assert.equal(calls.length,1,'same size and initial zoom must not restart layout');
  resize([{contentRect:{width:1024,height:768}}]);render();assert.deepEqual(calls.at(-1),{width:1024,height:768});assert.equal(calls.length,2);
 }finally{for(const slot of slots)slot?.cleanup?.();}
});
