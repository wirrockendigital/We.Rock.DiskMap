import type {QueryProvenance} from '../../query-provenance';
import type {RecoveryLanguage} from './RecoveryHelp';
// The recorded state is authoritative for historical wording; generic reasons never imply a specific cause.
export function sourceRecoveryHelp(state:QueryProvenance['state'],language:RecoveryLanguage):string {
 const text={
 missing:['Quelle war nicht vorhanden. Prüfe das Volume oder die Freigabe in DSM und danach den Profilumfang.','The source was missing. Check the volume or share in DSM, then the profile scope.'],
 unmounted:['Quelle war nicht eingehängt. Prüfe den Einhängestatus in DSM und danach den Profilumfang.','The source was unmounted. Check its mount status in DSM, then the profile scope.'],
 locked:['Freigabe war gesperrt. Entsperre sie in DSM und prüfe den Bereich erneut. DiskMap fragt keinen Schlüssel ab.','The share was locked. Unlock it in DSM and check the scope again. DiskMap does not request a key.'],
 unreadable:['Quelle war nicht lesbar. Prüfe in DSM die Leserechte des Paketdienstes für diesen Bereich und seine Unterordner. Keine pauschale Rechteerweiterung.','The source was unreadable. Check package-service read permissions for this scope and its descendants in DSM. Do not grant blanket access.'],
 unavailable:['Quelle war nicht qualifiziert verfügbar; die Ursache ist nicht bestätigt. Prüfe den Bereich unter Berechtigungen erneut.','The source was not qualified as available; its cause is unconfirmed. Check the scope again under Permissions.'],
 unverified:['Quellenzustand war nicht bestätigt. Prüfe den Bereich unter Berechtigungen erneut.','The source state was unverified. Check the scope again under Permissions.'],
 reachable:['Quelle war erreichbar; das bestätigt keine vollständige Erfassung ihres Inhalts. Prüfe die aufgezeichneten Erfassungsgrenzen.','The source was reachable; this does not confirm complete capture of its content. Check the recorded coverage boundaries.'],
 } as const;
 return text[state][language==='de'?0:1];
}
