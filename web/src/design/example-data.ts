import type {StorageNode} from './model';

// This explicitly local example catalogue never represents the live NAS inventory.
const gb = 1e9;
function collection(id: string, name: string, total: number, groups: string[], extension: string): StorageNode {
  const weights = groups.map((_, i) => 1 / (i + 1.25));
  const sum = weights.reduce((a, b) => a + b, 0);
  return {id, name, kind: 'folder', children: groups.map((group, i) => ({
    id: `${id}-${i}`, name: group, kind: 'folder', children: Array.from({length: 5 + i % 4}, (_, j) => ({
      id: `${id}-${i}-${j}`, name: `${group} ${String(j + 1).padStart(2, '0')}.${extension}`, kind: 'file',
      bytes: total * gb * weights[i] / sum / (5 + i % 4),
    })),
  }))};
}
export const exampleVolumes: StorageNode[] = [
  {id: 'volume-1', name: 'Volume 1', kind: 'volume', capacity: 4e12, children: [
    {id: 'media', name: 'Medien', kind: 'share', children: [
      collection('films', 'Filme', 486, ['Spielfilme', 'Dokumentationen', 'Serien', 'Kurzfilme', 'Archiv'], 'mkv'),
      collection('photos', 'Fotos', 324, ['2026', '2025', '2024', '2023', 'Familie', 'Reisen', 'Favoriten'], 'jpg'),
      collection('projects', 'Projekte', 198, ['Website', 'Kurzfilm', 'Design', 'Kunden', 'Material'], 'mov'),
      collection('music', 'Musik', 136, ['Alben', 'Live', 'Playlists', 'Klassik', 'Soundtracks'], 'flac'),
      collection('recordings', 'Aufnahmen', 94, ['Kamera', 'Drohne', 'Studio', 'Unterwegs'], 'mp4'),
      collection('other', 'Sonstiges', 42, ['Downloads', 'Vorlagen', 'Sammlung'], 'zip'),
    ]},
    {id: 'documents', name: 'Dokumente', kind: 'share', children: [
      collection('work', 'Arbeit', 42, ['Rechnungen', 'Verträge', 'Entwürfe'], 'pdf'),
      collection('personal', 'Privat', 18, ['Haushalt', 'Unterlagen', 'Archiv'], 'pdf'),
      {id: 'empty-folder', name: 'Neuer Ordner', kind: 'folder', children: []},
    ]},
  ]},
  {id: 'volume-2', name: 'Volume 2', kind: 'volume', capacity: 8e12, children: [
    {id: 'backups', name: 'Backups', kind: 'share', children: [collection('mac', 'MacBook', 840, ['Aktuell', 'September', 'August'], 'backup'), collection('archive', 'Archiv', 610, ['2025', '2024', '2023'], 'zip')]},
    {id: 'shared', name: 'Gemeinsam', kind: 'share', children: [collection('team', 'Team', 128, ['Projekte', 'Vorlagen', 'Austausch'], 'pdf')]},
  ]},
];
