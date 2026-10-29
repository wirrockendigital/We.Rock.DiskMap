// State cues are additive and remain understandable without distinguishing red from green.
export function stateStyle(state:{selected?:boolean;focused?:boolean;warning?:boolean}) {
 return Object.freeze({outline:state.focused?'3px double currentColor':state.selected?'2px solid currentColor':'none',outlineOffset:state.focused?'2px':'0',icon:state.warning?'⚠':state.selected?'✓':null,pattern:state.warning?'diagonal-lines':'none',selected:!!state.selected,focused:!!state.focused,warning:!!state.warning});
}
