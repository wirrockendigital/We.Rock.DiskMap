import {AboutPanel} from '../../AboutPanel';
import {OfflineHelpContent,type HelpLanguage} from './OfflineHelpContent';
// The settings host supplies language only: this surface cannot fetch, scan, mutate or expose private state.
export function HelpSettings({language}:{language:HelpLanguage}){
 return <section className="workspace-panel settings-panel help-settings" aria-label={language==='de'?'Hilfe und Informationen':'Help and information'}><h2>{language==='de'?'Lokale Bedienungshilfe':'Local user guide'}</h2><OfflineHelpContent language={language}/><AboutPanel language={language}/></section>;
}
