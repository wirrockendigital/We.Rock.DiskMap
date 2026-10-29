import {OfflineHelpContent} from './features/settings/OfflineHelpContent';
import { useState } from 'react';
import de from './locales/de.json';
import en from './locales/en.json';

// Accept only the bundled help languages; invalid selector values preserve the current choice.
export function selectHelpLanguage(current: 'de' | 'en', selected: string): 'de' | 'en' {
  return selected === 'de' || selected === 'en' ? selected : current;
}

// Bundled help uses no remote resources or private scan metadata and remains available at the session gate.
export function LocalHelp({ language }: { language: 'de' | 'en' }) {
  // Manual help choice applies only while the confirmed application language stays unchanged.
  const [choice, setChoice] = useState({base:language,selected:language});
  const helpLanguage=choice.base===language?choice.selected:language;
  if(choice.base!==language)setChoice({base:language,selected:language});
  const messages = helpLanguage === 'en' ? en : de;
  return <details className="local-help" lang={helpLanguage}><summary>{messages['help.title']}</summary>
    <label>{messages['help.language']} <select value={helpLanguage} onChange={event => setChoice({base:language,selected:selectHelpLanguage(helpLanguage,event.target.value)})}><option value="de" lang="de">Deutsch</option><option value="en" lang="en">English</option></select></label>
    <OfflineHelpContent language={helpLanguage}/>
  </details>;
}
