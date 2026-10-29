import {StorageChart} from './StorageChart';
import {previewTree,type ScanPreview} from './scan-preview';
const ignore=()=>{};
// This surface deliberately has no action, selection, navigation, search or live-path callbacks.
export function ScanPreviewSurface({preview,language,mode}:{preview:ScanPreview;language:'de'|'en';mode:'radial'|'treemap'}){
 const root=previewTree(preview,language);if(!root)return null;
 return <section className="disk-scan-preview" aria-label={language==='de'?'Scanvorschau':'Scan preview'} style={{position:'absolute',inset:0,display:'grid',gridTemplateRows:'auto minmax(0,1fr)'}}>
  <header style={{padding:'12px 20px'}}><strong>{language==='de'?'Vorschau · bereits erfasste Daten':'Preview · already observed data'}</strong><p style={{margin:'4px 0',fontSize:12}}>{language==='de'?'Unvollständiger Ausschnitt · Gesamtgröße noch unbekannt · Nur lesbar':'Incomplete view · Total size still unknown · Read-only'}{preview.truncated?(language==='de'?' · Weitere Einträge zusammengefasst':' · Additional entries grouped'):''}</p></header>
  <StorageChart root={root} mode={mode} language={language} hovered={null} interactive={false} onHover={ignore} onOpen={ignore}/>
 </section>;
}
