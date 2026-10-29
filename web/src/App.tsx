import {resolveLanguage} from './dsm-capabilities';
import { useCallback, useEffect, useReducer, useState } from 'react';
import de from './locales/de.json';
import {NativeDiskMap} from './design/NativeDiskMap';
import {currentToken} from './session';
import type { PreferenceView } from './contracts';
import type { ViewPin } from './snapshot-reader';
import en from './locales/en.json';
import { monitorSession } from './session-monitor';
import type { SessionState } from './session';

interface GateState { session: SessionState; selected: string; pin?: ViewPin; view?:PreferenceView }
type GateEvent = { session: SessionState } | { selected: string } | {pin:ViewPin|null} | {analysis:{profileId:string;pin:ViewPin|null;view:PreferenceView|null}};

// Only a profile ID survives a temporary authorization check; denied access clears it immediately.
export function gateState(state: GateState, event: GateEvent): GateState {
  if ('analysis' in event) return state.session==='authenticated'?{...state,selected:event.analysis.profileId,pin:event.analysis.pin??undefined,view:event.analysis.view??undefined}:state;
  if ('pin' in event && event.pin===null) return {session:state.session,selected:state.selected};
  if ('pin' in event) return state.session === 'authenticated' && event.pin!.profileId === state.selected ? {...state,pin:event.pin!}:state;
  if ('selected' in event) return state.session === 'authenticated' ? (event.selected===state.selected ? state : {session:state.session,selected:event.selected}) : state;
  return event.session === 'denied' || event.session === 'unavailable' ? {session:event.session,selected:''} : {...state,session:event.session};
}

// The gate hides data on entry or failure while successful background checks preserve the view.
export function App({ language }: { language?: 'de' | 'en' }) {
  // A confirmed personal choice changes all application text without remounting the session.
  const [choice,setChoice] = useState<'auto'|'de'|'en'|null>(null);
  const resolvedLanguage = resolveLanguage(choice??language??'auto',typeof navigator==='undefined'?undefined:navigator.language).language;
  const messages = resolvedLanguage === 'en' ? en : de;
  const [gate, dispatch] = useReducer(gateState, { session: 'checking', selected: '' });
  const session = gate.session;
  const [ownerIdentity,setOwnerIdentity]=useState('');
  const denied=useCallback(()=>dispatch({session:'denied'}),[]);
  useEffect(() => {
    const monitor = monitorSession({ visible: () => document.visibilityState === 'visible', changed: (session,principal) => {if(principal)setOwnerIdentity(principal);if(session==='denied'||session==='unavailable')setChoice(null);dispatch({ session });} });
    window.addEventListener('focus', monitor.focus);
    document.addEventListener('visibilitychange', monitor.visibility);
    return () => {
      monitor.stop();
      window.removeEventListener('focus', monitor.focus);
      document.removeEventListener('visibilitychange', monitor.visibility);
    };
  }, []);
  // Keep the existing live DSM session gate around the new presentation surface.
  if(session==='authenticated') return <NativeDiskMap key={ownerIdentity} sessionIdentity={ownerIdentity+':'+(currentToken()??'')} onSessionDenied={denied}/>;
  return <main className="disk-session-gate" lang={resolvedLanguage}>
    <h1>We.Rock.DiskMap</h1><h2>{messages['session.title']}</h2>
    <p role="status">{session==='checking'?messages['session.checking']:session==='denied'?messages['session.denied']:messages['session.unavailable']}</p>
    {session!=='checking'&&<a href="/webman/index.cgi" target="_top">{messages['session.login']}</a>}
  </main>;
}
