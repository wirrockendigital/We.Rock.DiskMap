import catalogue from '../settings/help-topics.json';
// Closed recovery cases describe existing actions; they never execute scans or repairs.
export type RecoveryLanguage='de'|'en';
interface RecoverySection {title:string;paragraphs?:string[];recoveryCase?:string}
const source:Record<RecoveryLanguage,{sections:RecoverySection[]}[]>=catalogue;
// Diagnostic notices reuse the handbook text instead of maintaining a second catalogue.
export const recoveryCases:Record<string,readonly [string,string,string,string]>=Object.fromEntries(
 source.de.flatMap(topic=>topic.sections).filter(section=>'recoveryCase' in section).map(section=>{
  const translated=source.en.flatMap(topic=>topic.sections).find(value=>'recoveryCase' in value&&value.recoveryCase===section.recoveryCase)!;
  return [section.recoveryCase,[section.title,section.paragraphs![0],translated.title,translated.paragraphs![0]] as const];
 })
);
// The catalogue is local, escaped React text, with no network or mutation controls.
export function RecoveryHelp({language}:{language:RecoveryLanguage}) {
 const offset=language==='de'?0:2;
 return <section aria-label={language==='de'?'Hilfe bei Fehlern und Wiederherstellung':'Error and recovery help'}><h3>{language==='de'?'Fehler und Wiederherstellung':'Errors and recovery'}</h3>{Object.entries(recoveryCases).map(([id,text])=><details key={id}><summary>{text[offset]}</summary><p>{text[offset+1]}</p></details>)}</section>;
}
// Only closed warning codes are interpreted; messages, paths and arbitrary codes are never displayed.
export function IndexSpaceHelp({language,warnings}:{language:RecoveryLanguage;warnings:readonly {code:string}[]}) {
 const low=warnings.some(w=>w.code==='INDEX_SPACE_LOW');
 if(!low&&!warnings.some(w=>w.code==='INDEX_SPACE_UNAVAILABLE'))return null;
 const offset=language==='de'?0:2;
 return <aside role="status">{low?<><strong>{recoveryCases['index-space'][offset]}</strong><p>{recoveryCases['index-space'][offset+1]}</p></>:<p>{language==='de'?'Der freie Indexspeicher konnte nicht geprüft werden. Prüfe den Paket-Datenbereich und die lokale Diagnose. Dieser Befund beweist keinen vollen Datenträger.':'Free index storage could not be checked. Review the package data area and local diagnostics. This does not establish that the disk is full.'}</p>}</aside>;
}
