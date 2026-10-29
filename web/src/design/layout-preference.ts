// Store only presentation flags, scoped to the authenticated DSM account.
export interface SidebarPreference {left:boolean;right:boolean}
export const defaultSidebars=():SidebarPreference=>({left:true,right:true});
const key=(userId:string)=>'we-rock-diskmap.sidebars.v1:'+encodeURIComponent(userId);
export function readSidebars(userId:string,storage:Pick<Storage,'getItem'>):SidebarPreference {
 try{const value=JSON.parse(storage.getItem(key(userId))??'null');return value&&typeof value.left==='boolean'&&typeof value.right==='boolean'?{left:value.left,right:value.right}:defaultSidebars();}catch{return defaultSidebars();}
}
// Never persist names, paths, snapshot contents or session credentials here.
export function writeSidebars(userId:string,value:SidebarPreference,storage:Pick<Storage,'setItem'>){
 if(!userId||typeof value.left!=='boolean'||typeof value.right!=='boolean')throw Error('INVALID_PREFERENCE');storage.setItem(key(userId),JSON.stringify(value));
}
