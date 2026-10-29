export type DetailLevel='auto'|'max'|1|2|3|4|5|6|7|8;
export type MapKind='treemap'|'radial';
// Request limits depend on CSS space, never the number of indexed source objects.
export function renderBudget(input:{kind:MapKind;detail:DetailLevel;width:number;height:number}) {
 const {kind,detail,width,height}=input;
 if(!['treemap','radial'].includes(kind)||![width,height].every(n=>Number.isFinite(n)&&n>=0&&n<=16384)||!(detail==='auto'||detail==='max'||Number.isInteger(detail)&&Number(detail)>=1&&Number(detail)<=8))throw Error('INVALID_RENDER_BUDGET');
 const cap=kind==='treemap'?10000:2000;
 const elementBudget=detail==='max'?cap:Math.min(cap,Math.max(1,Math.floor(width*height/(kind==='treemap'?160:600))));
 const depth=detail==='max'?8:detail==='auto'?(Math.min(width,height)<400?2:Math.min(width,height)<800?4:6):detail;
 return Object.freeze({depth,elementBudget});
}
// Animation is optional presentation; reduced motion always suppresses its duration.
export function animationDuration(reducedMotion:boolean):number{return reducedMotion?0:120;}
