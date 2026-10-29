import type { Profile, Scope } from '../../contracts';
import type { ProfileConfiguration } from '../../profile-actions';

// Draft values may be incomplete; only the separate create request is executable configuration.
export interface OnboardingConfiguration {
 name?:string; scope?:Omit<Scope,'kind'>&{kind:Scope['kind']|''}; active?:boolean;
 exclusions?:Profile['exclusions']; resourceMode?:'gentle'|'balanced'|'fast'|'custom'|'';
 limits?:Profile['limits']; freshnessSeconds?:number;
}
export interface OnboardingDraft {step:number;language:'auto'|'de'|'en';configuration:OnboardingConfiguration|null;profileId:string|null;updateChoice:'undecided'|'manual'|'dsm-help'}
export interface OnboardingCreateRequest {contractVersion:1;operation:'create';configuration:ProfileConfiguration}
export interface PendingOnboardingCreate {key:string;canonicalPayload:string}
export interface OnboardingState {version:1;revision:number;draft:OnboardingDraft|null;pendingProfileCreate:PendingOnboardingCreate|null}
export interface OnboardingCreateReceipt {operation:'create';profileId:string;profile:Profile;cleanupScheduled:false;tasksRequireReview:false}
export interface OnboardingRecovery extends PendingOnboardingCreate {status:'pending'|'committed'|'profile-unavailable'|'rejected';rejectionCode?:'INVALID_REQUEST'|'JOB_CONFLICT'|'PROFILE_REBIND_REQUIRED';receipt:OnboardingCreateReceipt|null;profileState:'unknown'|'active'|'missing'|'inactive'|'changed'}
export interface OnboardingResponse extends Omit<OnboardingState,'pendingProfileCreate'> {pendingProfileCreate:OnboardingRecovery|null;profileState:'none'|'active'|'inactive'|'missing'}
export type OnboardingChange={operation:'replace';draft:OnboardingDraft}|{operation:'prepareProfileCreate';create:{key:string;request:OnboardingCreateRequest}}|{operation:'acknowledgeProfileCreate'|'acknowledgeProfileRejection'|'reset'};
export interface OnboardingRequest {contractVersion:1;expectedRevision:number;change:OnboardingChange}
