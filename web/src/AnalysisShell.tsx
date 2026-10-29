import { useState, type ReactNode } from 'react';
import type { Profile } from './contracts';
import de from './locales/de.json';

export type AnalysisArea = 'overview' | 'profiles' | 'status' | 'settings';
type Messages = typeof de;
const areas: AnalysisArea[] = ['overview', 'profiles', 'status', 'settings'];

// Small decorative symbols reinforce navigation without replacing accessible text.
function AreaIcon({area}:{area:AnalysisArea}) {
  const paths:Record<AnalysisArea,string> = {
    overview:'M3 3h7v18H3z M13 3h8v8h-8z M13 14h8v7h-8z',
    profiles:'M3 6h7l2 3h9v11H3z M3 6V4h7l2 2',
    status:'M4 20V10 M12 20V4 M20 20v-7',
    settings:'M4 7h16 M4 17h16 M9 4v6 M16 14v6',
  };
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={paths[area]}/></svg>;
}

// Validate remembered selection against the current catalogue before mounting a profile reader.
export function resolveProfileSelection(profiles: readonly Pick<Profile, 'id' | 'active'>[], remembered: string): string {
  if (profiles.some(profile => profile.id === remembered)) return remembered;
  return profiles.find(profile => profile.active)?.id ?? profiles[0]?.id ?? '';
}

// Primary navigation changes visibility only; reader and mutation owners stay mounted.
export function AnalysisShell({ area, onArea, picker, messages, children }: {
  area: AnalysisArea; onArea: (area: AnalysisArea) => void; picker: ReactNode; messages: Messages; children: ReactNode;
}) {
  return <section className="analysis-shell" aria-label={messages['shell.title']} data-shell-version="1">
    <aside className="workspace-sidebar">
    <nav className="analysis-nav" aria-label={messages['shell.navigation']}>
      {areas.map(value => <button key={value} aria-current={area === value ? 'page' : undefined} onClick={() => onArea(value)}><AreaIcon area={value}/>{messages[`shell.${value}`]}</button>)}
    </nav>
    <header className="analysis-source">{picker}<details className="shell-help"><summary>{messages['shell.help']}</summary><p>{messages[`shell.help.${area}`]}</p></details></header>
    </aside>
    <div className="workspace-content">{children}</div>
  </section>;
}

// Side panels are real collapsible regions; the centre remains available without a manufactured map.
export function AnalysisWorkspace({ messages, orientation, details, children }: {
  messages: Messages; orientation: ReactNode; details: ReactNode; children: ReactNode;
}) {
  // Optional panels start folded so the first view gives the map the full working width.
  const [treeOpen, setTreeOpen] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  return <div className="analysis-workspace">
    <div className="analysis-panel-controls">
      <button aria-expanded={treeOpen} aria-controls="analysis-orientation" onClick={() => setTreeOpen(value => !value)}>{messages['shell.tree']}</button>
      <button aria-expanded={detailsOpen} aria-controls="analysis-details" onClick={() => setDetailsOpen(value => !value)}>{messages['shell.details']}</button>
    </div>
    <div className={`analysis-columns${treeOpen ? ' with-tree' : ''}${detailsOpen ? ' with-details' : ''}`}>
      <aside id="analysis-orientation" hidden={!treeOpen} aria-label={messages['shell.tree']}><h3>{messages['shell.tree']}</h3>{orientation}</aside>
      <div className="analysis-main" aria-label={messages['shell.main']}>{children}</div>
      <aside id="analysis-details" hidden={!detailsOpen} aria-label={messages['shell.details']}><h3>{messages['shell.details']}</h3>{details}</aside>
    </div>
  </div>;
}
