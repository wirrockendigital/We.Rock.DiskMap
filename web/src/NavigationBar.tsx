import type { QueryProvenance } from './query-provenance';
import type { PreferenceView } from './contracts';

// Navigation identities are supplied by the qualified server response, never derived from names.
export interface NavigationState {
  snapshotId: string; rootId: string; parentId: string | null; selectedNodeId: string | null;
  breadcrumb: { id: string; name: string; kind: 'nas' | 'volume' | 'share' | 'other' | 'directory'; provenance?: QueryProvenance }[]; reasons: string[];
}
export interface NavigationBarProps {
  language: 'de' | 'en'; view: PreferenceView; navigation: NavigationState | null;
  canBack: boolean; canForward: boolean; disabled?: boolean;
  onBack: () => void; onForward: () => void; onRoot: () => void; onParent: () => void;
  onBreadcrumb: (id: string) => void; onMap: (map: PreferenceView['map']) => void;
}
// This toolbar performs no fetching or preference writes and cannot cross snapshot boundaries itself.
export function NavigationBar(p: NavigationBarProps) {
  const de = p.language === 'de';
  const nav = p.navigation?.snapshotId === p.view.snapshotId && p.navigation.rootId === p.view.rootId ? p.navigation : null;
  return <nav className="view-navigation" aria-label={de ? 'Analysenavigation' : 'Analysis navigation'}>
    <div className="view-history">
    <button type="button" disabled={p.disabled || !p.canBack} onClick={p.onBack}>{de ? 'Zurück' : 'Back'}</button>
    <button type="button" disabled={p.disabled || !p.canForward} onClick={p.onForward}>{de ? 'Vorwärts' : 'Forward'}</button>
    <button type="button" disabled={p.disabled || !nav?.parentId} onClick={p.onParent}>{de ? 'Eine Ebene höher' : 'Up'}</button>
    <button type="button" disabled={p.disabled || !nav} onClick={p.onRoot}>{de ? 'Profilwurzel' : 'Profile root'}</button>
    </div>
    <ol aria-label={de ? 'Position' : 'Location'}>{nav?.breadcrumb.map(item => <li key={item.id}>
      <button type="button" disabled={p.disabled || item.id === nav.rootId} aria-current={item.id === nav.rootId ? 'location' : undefined}
        onClick={() => p.onBreadcrumb(item.id)}>{item.name}</button></li>)}</ol>
    <div className="map-switch">
    <button type="button" disabled={p.disabled} aria-pressed={p.view.map === 'treemap'} onClick={() => p.onMap('treemap')}>Treemap</button>
    <button type="button" disabled={p.disabled} aria-pressed={p.view.map === 'radial'} onClick={() => p.onMap('radial')}>{de ? 'Radialkarte' : 'Radial map'}</button>
    </div>
  </nav>;
}
