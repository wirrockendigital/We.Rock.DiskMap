import {categoryVisible,type CategoryVisibility} from './category-visibility';
import type {ScopeObservation} from './scope-observations';
// Project saved coverage onto the visible categories without modifying the scan record.
export function visibleCoverage(observation:ScopeObservation,visibility:CategoryVisibility):ScopeObservation{
 const reasons=observation.reasons.filter(reason=>categoryVisible({name:'',path:reason.relativePath},visibility,observation.path));
 const hidden=observation.reasons.length-reasons.length;
 if(!hidden)return observation;
 // A bounded reason list cannot prove that undisclosed gaps belong to hidden categories.
 const fullyExplained=Number.isSafeInteger(observation.reasonCount)&&observation.reasonCount===observation.reasons.length;
 const reasonCount=Math.max(reasons.length,observation.reasonCount-hidden);
 return {...observation,reasons,reasonCount,state:observation.state==='partial'&&fullyExplained&&reasons.length===0?'complete':observation.state};
}
