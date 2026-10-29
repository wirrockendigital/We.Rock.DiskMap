import {createContext,useContext,useEffect} from 'react';
import type {PreferenceDisplay} from '../../contracts';
// Standalone presenters use the same explicit defaults as an empty server preference row.
export const defaultDisplay:Readonly<PreferenceDisplay>=Object.freeze({units:'decimal',colorMode:'folder',detail:'auto',labels:true,percentages:true,hierarchyBorders:'normal',reducedMotion:'auto'});
export const DisplayContext=createContext<Readonly<PreferenceDisplay>>(defaultDisplay);
export const useDisplay=()=>useContext(DisplayContext);
// Reduced motion is session scoped and never overrides the operating-system reduce preference.
export function DisplayEffects(){const display=useDisplay();useEffect(()=>{if(typeof document==='undefined')return;document.documentElement.dataset.motion=display.reducedMotion;return()=>{delete document.documentElement.dataset.motion;};},[display.reducedMotion]);return null;}
