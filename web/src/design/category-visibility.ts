import type {StorageNode} from './model';
// Use the same category names for maps, navigation and recorded result lists.
export const isRecycleName=(name:string)=>name.toLowerCase()==='#recycle';
export const isSystemName=(name:string)=>/^(?:@eadir|\.ds_store|\.smb.*|\.appledouble|\._.*|\.spotlight-v100|\.trashes|\.fseventsd)$/i.test(name);
export interface CategoryVisibility {showRecycle?:boolean;showSystem?:boolean}
// Full recorded paths preserve category inheritance in recursive search results.
export function categoryVisible(node:Pick<StorageNode,'name'|'path'>,visibility:CategoryVisibility={},ancestorPath=''):boolean{
 const parts=[...ancestorPath.split('/'),...(node.path??'').split('/'),node.name];
 return parts.every(name=>(visibility.showRecycle||!isRecycleName(name))&&(visibility.showSystem||!isSystemName(name)));
}
