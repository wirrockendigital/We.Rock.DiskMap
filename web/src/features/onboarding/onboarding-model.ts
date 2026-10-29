import type {ProfileConfiguration} from '../../profile-actions';
import {newProfileConfiguration,profileConfiguration} from '../../profile-actions';
import type {Scope} from '../../contracts';
import type {ScopeCheck} from '../../scope-selection';

// Stored drafts are incomplete form state, not permission to execute a profile or scan.
import type {OnboardingDraft as WizardDraft} from './onboarding-types';
export type {OnboardingDraft as WizardDraft} from './onboarding-types';
export const newWizardDraft=():WizardDraft=>({step:1,language:'auto',configuration:null,profileId:null,updateChoice:'undecided'});
export function draftConfiguration(draft:WizardDraft):ProfileConfiguration{const c={...newProfileConfiguration(),...draft.configuration};if(c.scope.kind==='')c.scope={kind:'shares',targetIds:c.scope.targetIds};if(c.resourceMode==='')c.resourceMode='fast';return profileConfiguration(c as ProfileConfiguration);}
// Missing numeric form input remains omitted rather than becoming JSON null or an invented zero.
export function storedConfiguration(c:ProfileConfiguration):Partial<ProfileConfiguration>{const next:Partial<ProfileConfiguration>=profileConfiguration(c);if(!Number.isSafeInteger(next.freshnessSeconds))delete next.freshnessSeconds;return next;}
export function scopeKey(scope:Scope):string{return JSON.stringify([scope.kind,[...scope.targetIds].sort()]);}
export function readableScope(report:ScopeCheck,scope:Scope):boolean{return scopeKey(report.scope)===scopeKey(scope)&&report.roots.length>0&&report.issues.length===0&&report.roots.every(r=>r.state==='reachable');}
