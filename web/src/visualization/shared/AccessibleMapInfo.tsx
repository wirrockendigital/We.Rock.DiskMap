import {RegionalNumber} from '../../regional-format';
import {nodeKindLabel} from './labels';
import type {MapModel} from './model';
import {keyboardTarget} from './interaction';

// The host owns selection and focus; this bounded text mirror creates no second authoritative view state.
export function AccessibleMapInfo({model,selectedId,focusedId,onSelect,onFocus,onOpen,onGroup,disabled=false,language}:{model:MapModel;selectedId:string|null;focusedId:string|null;onSelect:(id:string)=>void;onFocus:(id:string)=>void;onOpen:(id:string)=>void;onGroup:(id:string)=>void;disabled?:boolean;language:'de'|'en'}) {
 const de=language==='de',selected=selectedId?model.nodes[selectedId]:undefined;
 const focus=focusedId&&model.nodes[focusedId]?focusedId:model.order[0];
 // Model remainder groups use inspection only, never node selection/navigation callbacks.
 const activate=(id:string,open=false)=>{if(disabled)return;const node=model.nodes[id];if(!node)return;if(node.kind==='group')onGroup(id);else if(open)onOpen(id);else onSelect(id);};
 return <section aria-label={de?'Karteninformationen':'Map information'}>
  <p>{de?'Pfeiltasten bewegen den Fokus; Eingabe wählt aus. „Hier öffnen“ navigiert.':'Arrow keys move focus; Enter selects. “Open here” navigates.'}</p>
  <div role="group" aria-label={de?'Kartenfokus':'Map focus'} aria-disabled={disabled} tabIndex={disabled?-1:0} style={{outline:'2px solid currentColor'}} onKeyDown={event=>{if(disabled)return;if(event.key==='Enter'||event.key===' '){event.preventDefault();if(focus)activate(focus);return;}if(['ArrowDown','ArrowUp','ArrowLeft','ArrowRight','Home','End'].includes(event.key)){event.preventDefault();const next=keyboardTarget(model,focus,event.key);if(next)onFocus(next);}}}>
   <p aria-live="polite">{focus&&model.nodes[focus]?<>{model.nodes[focus].name} — {nodeKindLabel(model.nodes[focus].kind,language)} — <RegionalNumber value={model.nodes[focus].weight} language={language}/> B</>:de?'Kein Kartenfokus':'No map focus'}</p>
   {focus&&<button type="button" disabled={disabled} onClick={()=>activate(focus)}>{de?'Auswählen':'Select'}</button>}
  </div>
  {selected&&<div aria-live="polite"><p>{selected.name} — {nodeKindLabel(selected.kind,language)} — <RegionalNumber value={selected.weight} language={language}/> B</p>{['nas','volume','share','other','directory','group'].includes(selected.kind)&&<button type="button" disabled={disabled} onClick={()=>activate(selected.id,true)}>{selected.kind==='group'?(de?'Darstellungsgruppe anzeigen':'Inspect display group'):(de?'Hier öffnen':'Open here')}</button>}</div>}
 </section>;
}
