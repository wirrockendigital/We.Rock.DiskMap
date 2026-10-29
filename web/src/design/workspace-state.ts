import type {PreferenceWorkspace} from '../contracts';
import {defaultListOptions} from './list-options';
// Each authenticated owner receives a fresh default object; file data never enters browser storage.
export const defaultWorkspace=():PreferenceWorkspace=>({sizeUnit:'mixed',showShareStatus:true,scanRecycle:true,scanSystem:true,jobs:[],zoom:null,left:true,right:true,leftWidth:290,rightWidth:390,expanded:[],treeSort:'name',treeDirection:'asc',treeScroll:0,listScroll:0,filterOpen:false,list:defaultListOptions(),showRecycle:false,showSystem:false});
