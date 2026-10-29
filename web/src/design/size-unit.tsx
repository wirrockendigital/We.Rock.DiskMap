import {createContext,useCallback,useContext} from 'react';
import {sizeOf,type Language,type StorageNode} from './model';

export type SizeUnit='TB'|'GB'|'MB'|'mixed'|'hidden';
export const SizeUnitContext=createContext<SizeUnit>('mixed');
const units:SizeUnit[]=['TB','GB','MB','mixed','hidden'];
// Unit selection affects presentation only; recorded bytes and sorting stay exact.
export function nextSizeUnit(unit:SizeUnit):SizeUnit{return units[(units.indexOf(unit)+1)%units.length];}
export function formatUnitBytes(bytes:string|undefined,unit:SizeUnit,language:Language):string{
 if(unit==='hidden')return '';
 if(bytes===undefined)return '—';
 const value=BigInt(bytes);
 // Mixed selects one decimal unit per value and keeps KB as its smallest unit.
 const selected=unit==='mixed'?(value>=1000000000000n?'TB':value>=1000000000n?'GB':value>=1000000n?'MB':'KB'):unit;
 const divisor={TB:1000000000000n,GB:1000000000n,MB:1000000n,KB:1000n}[selected];
 // Round every value consistently to one decimal place without comparison prefixes.
 const decimal=language==='de'?',':'.';
 const tenths=(value*10n+divisor/2n)/divisor;
 return new Intl.NumberFormat(language).format(tenths/10n)+decimal+String(tenths%10n)+' '+selected;
}
// One context keeps tree, file list, canvas labels and footer in the same unit.
export function useSizeFormatter(){
 const unit=useContext(SizeUnitContext);
 return useCallback((node:StorageNode,language:Language='de')=>formatUnitBytes(node.bytesExact??(node.unavailable?undefined:String(Math.round(sizeOf(node)))),unit,language),[unit]);
}
