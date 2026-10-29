import type { PreferenceView } from './contracts';

// Each transition replaces the complete view; history contains no browser-persisted metadata.
export interface AnalysisState { view: PreferenceView | null; back: PreferenceView[]; forward: PreferenceView[]; epoch: number }
export type AnalysisAction = { type: 'navigate' | 'replace'; view: PreferenceView | null } | { type: 'back' } | { type: 'forward' } | { type: 'reset' };
export const HISTORY_LIMIT = 64;
export const initialAnalysisState = (): AnalysisState => ({ view: null, back: [], forward: [], epoch: 0 });
export const cloneView = (view: PreferenceView): PreferenceView => structuredClone(view);
export const viewKey = (view: PreferenceView | null): string => JSON.stringify(view);

// A history move is proposed atomically; the integrator qualifies its snapshot lease before dispatch.
export function analysisReducer(state: AnalysisState, action: AnalysisAction): AnalysisState {
  if (action.type === 'reset') return { ...initialAnalysisState(), epoch: state.epoch + 1 };
  if (action.type === 'back' || action.type === 'forward') {
    const from = state[action.type];
    if (!from.length) return state;
    const opposite = action.type === 'back' ? 'forward' : 'back';
    return { ...state, view: cloneView(from[from.length - 1]), [action.type]: from.slice(0, -1),
      [opposite]: state.view ? [...state[opposite], cloneView(state.view)].slice(-HISTORY_LIMIT) : state[opposite], epoch: state.epoch + 1 };
  }
  if (viewKey(action.view) === viewKey(state.view)) return state;
  return { view: action.view ? cloneView(action.view) : null, epoch: state.epoch + 1,
    back: action.type === 'navigate' && state.view ? [...state.back, cloneView(state.view)].slice(-HISTORY_LIMIT) : state.back,
    forward: action.type === 'navigate' ? [] : state.forward };
}
export interface AnalysisTicket { epoch: number; key: string }
export const analysisTicket = (state: AnalysisState): AnalysisTicket => ({ epoch: state.epoch, key: viewKey(state.view) });
// Both resolved and rejected stale work is ignored, including after an abort-insensitive transport.
export async function settleAnalysis<T>(work: Promise<T>, ticket: AnalysisTicket, current: () => AnalysisState,
  success: (value: T) => void, failure: (error: unknown) => void): Promise<void> {
  const valid = () => { const state = current(); return state.epoch === ticket.epoch && viewKey(state.view) === ticket.key; };
  try { const value = await work; if (valid()) success(value); } catch (error) { if (valid()) failure(error); }
}
