import {CheckCircledIcon,LockClosedIcon,ReloadIcon,ChevronRightIcon} from '@radix-ui/react-icons';
import type {Language,StorageNode} from './model';

// A compact outline matches the directory tree without adding an icon dependency.
function FolderIcon(){return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><path d="M3 6h7l2 3h9v12H3Z"/></svg>;}

// Setup explains package access without changing DSM permissions or starting work implicitly.
export function SetupSurface({language,mode,refreshing,failed,refresh,shares,select}:{language:Language;mode:'permissions'|'first';refreshing:boolean;failed:boolean;refresh:()=>void;shares:StorageNode[];select:(node:StorageNode)=>void}){
 const de=language==='de',ready=mode==='first';
 const steps=de?[
  ['Freigabe öffnen','DSM → Systemsteuerung → Freigegebener Ordner. Gewünschte Freigabe auswählen und „Bearbeiten“ öffnen.'],
  ['Paketkonto auswählen','Unter „Berechtigungen“ zu „Interner Systembenutzer“ wechseln und we-rock-diskmap auswählen.'],
  ['Leserechte erlauben','„Nur Lesen“ erlauben und speichern. Für weitere gewünschte Freigaben wiederholen.'],
 ]:[
  ['Open a shared folder','DSM → Control Panel → Shared Folder. Select the intended share and choose Edit.'],
  ['Select the package account','Under Permissions, switch to System internal user and select we-rock-diskmap.'],
  ['Allow read access','Allow Read only and save. Repeat for other shares you want to scan.'],
 ];
 return <div className="disk-setup-scroll"><section className="disk-setup" aria-labelledby="disk-setup-title">
  <div className="disk-setup-symbol" data-ready={ready}>{ready?<CheckCircledIcon/>:<LockClosedIcon/>}</div>
  <p className="disk-setup-eyebrow">{de?'WILLKOMMEN BEI WE.ROCK.DISKMAP':'WELCOME TO WE.ROCK.DISKMAP'}</p>
  <h1 id="disk-setup-title">{ready?(de?'Bereit für deinen ersten Scan':'Ready for your first scan'):(de?'Gib deinem Speicher eine Karte':'Give your storage a map')}</h1>
  <p className="disk-setup-intro">{ready?(de?'Deine Freigaben sind erreichbar. Wähle einen Ordner aus und starte anschließend seinen ersten Scan.':'Your shared folders are accessible. Choose a folder, then start its first scan.'):(de?'Noch keine Freigaben zugänglich. Gib dem Paketkonto Leserechte für die Ordner, die du analysieren möchtest.':'No shared folders are accessible yet. Grant the package account read access to the folders you want to analyze.')}</p>
  {ready?<div className="disk-setup-shares" aria-label={de?'Freigabe auswählen':'Choose a shared folder'}>{shares.map(n=><button key={n.id} onClick={()=>select(n)}><FolderIcon/><span>{n.name}</span><ChevronRightIcon/></button>)}</div>:<ol className="disk-setup-steps">{steps.map(([title,description],i)=><li key={title}><span className="disk-setup-step">{i+1}</span><div><h2>{title}</h2><p>{description}</p></div></li>)}</ol>}
  {ready?<details className="disk-setup-access"><summary>{de?'Eine Freigabe fehlt?':'Missing a shared folder?'}</summary><p>{de?'Erlaube dem internen Systembenutzer we-rock-diskmap in den DSM-Berechtigungen dieser Freigabe „Nur Lesen“. Prüfe danach die Freigaben erneut.':'Allow Read only for the system internal user we-rock-diskmap in that share’s DSM permissions, then check the shares again.'}</p></details>:<p className="disk-setup-note"><LockClosedIcon/>{de?'Die App ändert keine Berechtigungen. Unterordner mit eigenen Rechten und geschützte Papierkörbe benötigen gegebenenfalls zusätzliche Leserechte.':'The app does not change permissions. Subfolders with custom permissions and protected recycle bins may need additional read access.'}</p>}
  <div className="disk-setup-actions"><button className="disk-primary-button" disabled={refreshing} onClick={refresh}><ReloadIcon className={refreshing?'disk-spin':''}/>{refreshing?(de?'Freigaben werden geprüft …':'Checking shares …'):(de?'Freigaben erneut prüfen':'Check shared folders again')}</button><span>{de?'Nur Freigaben prüfen · kein Scan':'Check shares only · no scan'}</span></div>
  {failed&&<p className="disk-setup-error" role="alert">{de?'Freigaben konnten nicht geprüft werden. Bitte versuche es erneut. Dies bedeutet nicht, dass keine Freigaben vorhanden sind.':'Could not check shared folders. Please try again. This does not mean that no shares exist.'}</p>}
 </section></div>;
}
