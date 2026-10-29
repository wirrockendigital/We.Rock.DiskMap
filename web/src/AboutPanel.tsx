import {aboutCompatibility, bundledAbout, type AboutMetadata} from './AboutData';

const copy = {
  de: {title: 'Über We.Rock.DiskMap', app: 'Appversion', pkg: 'Paketversion', source: 'Build-Quellidentität', unknown: 'Unbekannt', components: 'Gebündelte Komponenten', licenses: 'Lizenztexte', matrix: 'Dokumentierte DSM-Teilprüfungen', probe: 'File-Station-Ordnerübergabe und lokale Aufgabenplaner-Aufrufe wurden auf diesem Testsystem geprüft. Die optionale Dateimarkierung ist nicht verfügbar.', limitation: 'Dies ist keine allgemeine DSM-Kompatibilitäts- oder Releasefreigabe. Andere Builds sind hier nicht qualifiziert. Aktuelle Fähigkeiten und Dienstzustände zeigt die Dienstdiagnose.', local: 'Diese Informationen sind lokal gebündelt. Es findet keine Internetabfrage statt.', versions: 'Komponentenversionen beschreiben die Buildinputs, nicht den aktuellen Dienstzustand.', evidence: 'Nachweise im Quellpaket', ui: 'Oberfläche', backend: 'Backend', toolchain: 'Buildwerkzeug'},
  en: {title: 'About We.Rock.DiskMap', app: 'App version', pkg: 'Package version', source: 'Build source identity', unknown: 'Unknown', components: 'Bundled components', licenses: 'License notices', matrix: 'Documented DSM partial checks', probe: 'File Station folder navigation and local Task Scheduler calls were tested on this test system. Optional file highlighting is unavailable.', limitation: 'This is not universal DSM compatibility or release approval. Other builds are not qualified here. Service diagnostics shows current capabilities and service states.', local: 'This information is bundled locally. No internet request is made.', versions: 'Component versions describe build inputs, not current service health.', evidence: 'Evidence in the source package', ui: 'Interface', backend: 'Backend', toolchain: 'Build tool'},
} as const;

// A pure, offline component leaves session, navigation and help ownership with its host.
export function AboutPanel({language, data = bundledAbout}: {language: 'de' | 'en'; data?: AboutMetadata | null}) {
  const m = copy[language];
  return <section aria-label={m.title}>
    <h2>{m.title}</h2><p>{m.local}</p>
    <dl><dt>{m.app}</dt><dd>{data?.appVersion ?? m.unknown}</dd>
      <dt>{m.pkg}</dt><dd>{data?.packageVersion ?? m.unknown}</dd>
      <dt>{m.source}</dt><dd style={{overflowWrap: 'anywhere'}}>{data?.sourceId ?? m.unknown}</dd></dl>
    <h3>{m.components}</h3><p>{m.versions}</p>
    {data ? <ul>{data.components.map(c => <li key={c.name}>{c.name} {c.version} — {m[c.kind]}</li>)}</ul> : <p>{m.unknown}</p>}
    <h3>{m.matrix}</h3><p>{aboutCompatibility.platform} — {aboutCompatibility.build}</p><p>{m.probe}</p><p>{m.limitation}</p>
    <details><summary>{m.evidence}</summary><ul>{aboutCompatibility.evidence.map(path => <li key={path}><code>{path}</code></li>)}</ul></details>
    <details><summary>{m.licenses}</summary>{data ? data.licenses.map(l => <details key={`${l.component}/${l.name}`}><summary>{l.component} — {l.name}</summary><pre style={{whiteSpace: 'pre-wrap', overflowWrap: 'anywhere'}}>{l.text}</pre></details>) : <p>{m.unknown}</p>}</details>
  </section>;
}
