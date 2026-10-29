import {useEffect, useId, useRef, useState} from 'react';
import {ArchiveIcon, ArrowLeftIcon, ChevronRightIcon, CubeIcon} from '@radix-ui/react-icons';
import {pathTo, type Language, type StorageNode} from './model';

export interface FolderPickerProps {
  open: boolean;
  nodes: StorageNode[];
  initialId: string;
  language: Language;
  onCancel: () => void;
  onChoose: (node: StorageNode) => void;
  loadChildren?: (node: StorageNode) => Promise<StorageNode[]>;
}

// This picker selects presentation nodes; the caller owns the subsequent scan action.
export function FolderPicker({open, nodes, initialId, language, onCancel, onChoose, loadChildren}: FolderPickerProps) {
  const dialog = useRef<HTMLDialogElement>(null);
  const initialized = useRef(false);
  const [loaded,setLoaded] = useState<Record<string,StorageNode[]>>({});
  const [loading,setLoading] = useState(false),[error,setError]=useState('');
  // Merge only explicitly browsed directories into the bounded picker tree.
  const merge=(node:StorageNode):StorageNode=>({...node,children:(loaded[node.id]??node.children)?.map(merge)});
  const tree=nodes.map(merge);
  const [currentId, setCurrentId] = useState('');
  const [selectedId, setSelectedId] = useState('');
  const titleId = useId();
  const de = language === 'de';
  // The NAS location owns the volume tree without duplicating node identities.
  const volumes = nodes.flatMap(node => node.id==='scope-nas'?[node,...(node.children??[])]:[node]).filter(node => node.kind === 'volume');
  const currentPath = pathTo(tree, currentId);
  const current = currentPath.at(-1);
  const selectedPath = pathTo(tree, selectedId);
  const selected = selectedPath.at(-1);
  const folders = (current?.children ?? []).filter(node => node.kind !== 'file');
  const selectionPath = selectedPath.map(node => node.name).join(' / ');

  // Initialize once per opening so parent renders cannot erase an in-progress choice.
  useEffect(() => {
    if (!open) { initialized.current = false; return; }
    if (initialized.current) return;
    const requested = pathTo(tree, initialId).filter(node => node.kind !== 'file').at(-1);
    const initial = requested ?? nodes.find(node => node.kind === 'volume');
    setCurrentId(initial?.id ?? '');
    setSelectedId(initial?.id ?? '');
    initialized.current = true;
  }, [open, nodes, initialId]);

  // Native modal focus containment and Escape semantics remain owned by the dialog.
  useEffect(() => {
    if (!open) return;
    const element = dialog.current;
    if (!element) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (!element.open) element.showModal();
    return () => {
      if (element.open) element.close();
      if (previous?.isConnected) previous.focus({preventScroll: true});
    };
  }, [open]);

  const enter = (node: StorageNode) => {
    if (node.kind === 'file') return;
    setCurrentId(node.id);
    setSelectedId(node.id);
  };
  // Each directory load is discarded when the user leaves or closes the picker.
  useEffect(()=>{
    if(!open||!loadChildren||!current||current.kind==='volume')return;
    let active=true;setLoading(true);setError('');
    loadChildren(current).then(children=>{if(active)setLoaded(previous=>({...previous,[current.id]:children}));})
      .catch(()=>{if(active)setError(de?'Ordner konnte nicht geladen werden.':'Could not load folder.');})
      .finally(()=>{if(active)setLoading(false);});
    return()=>{active=false;};
  },[open,currentId,loadChildren]);
  const up = currentPath.at(-2);

  return <dialog ref={dialog} className="disk-dialog" aria-labelledby={titleId}
    onCancel={event => { event.preventDefault(); onCancel(); }}>
    <header className="disk-dialog-header">
      <h2 id={titleId}>{de ? 'Was möchtest du scannen?' : 'What would you like to scan?'}</h2>
      <p>{de ? 'NAS, Volume oder Ordner auswählen.' : 'Choose NAS, volume or folder.'}</p>
    </header>
    <div className="disk-picker-body">
      <nav className="disk-picker-locations" aria-label="Volumes">
        {volumes.map(volume => <button key={volume.id} type="button"
          className={currentPath.filter(node=>node.kind==='volume').at(-1)?.id === volume.id ? 'is-selected' : undefined}
          aria-current={currentPath.filter(node=>node.kind==='volume').at(-1)?.id === volume.id ? 'location' : undefined}
          onClick={() => enter(volume)}><CubeIcon aria-hidden="true"/>{volume.name}</button>)}
      </nav>
      <section className="disk-picker-content" aria-label={de ? 'Ordnerauswahl' : 'Folder selection'}>
        <nav className="disk-picker-path" aria-label={de ? 'Aktueller Ordner' : 'Current folder'}>
          <button type="button" className="disk-icon-button" disabled={!up}
            aria-label={de ? 'Übergeordneter Ordner' : 'Parent folder'} onClick={() => { if (up) enter(up); }}>
            <ArrowLeftIcon aria-hidden="true"/>
          </button>
          {currentPath.map((node, index) => <span key={node.id}>
            {index > 0 && <ChevronRightIcon aria-hidden="true"/>}
            <button type="button" aria-current={node.id === currentId ? 'location' : undefined}
              onClick={() => enter(node)}>{node.name}</button>
          </span>)}
        </nav>
        <div className="disk-picker-list">
          {folders.map(node => <div key={node.id} className={`disk-picker-row${selectedId === node.id ? ' is-selected' : ''}`}>
            <button type="button" aria-pressed={selectedId === node.id}
              onClick={() => setSelectedId(node.id)} onDoubleClick={() => enter(node)}
              onKeyDown={event => {
                // Enter opens the focused folder; Space retains ordinary button selection.
                if (event.key === 'Enter') { event.preventDefault(); enter(node); }
              }}><ArchiveIcon aria-hidden="true"/>{node.name}</button>
            <button type="button" className="disk-icon-button"
              aria-label={`${de ? 'Ordner öffnen' : 'Open folder'}: ${node.name}`}
              onClick={() => enter(node)}><ChevronRightIcon aria-hidden="true"/></button>
          </div>)}
          {loading&&<p role="status">{de?'Ordner werden geladen …':'Loading folders …'}</p>}
          {error&&<p role="alert">{error}</p>}
          {!folders.length && !loading && !error && <p>{current
            ? (de ? 'Keine weiteren Ordner. Du kannst diesen Ordner auswählen.' : 'No further folders. You can select this folder.')
            : (de ? 'Keine Volumes verfügbar.' : 'No volumes available.')}</p>}
        </div>
      </section>
    </div>
    <footer className="disk-picker-footer">
      <p title={selectionPath} style={{minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap'}}>{selectionPath || (de ? 'Keine Auswahl' : 'Nothing selected')}</p>
      <button type="button" onClick={onCancel}>{de ? 'Abbrechen' : 'Cancel'}</button>
      <button type="button" className="disk-primary-button" disabled={loading||!!error||!selected || selected.kind === 'file'||selected.unavailable}
        onClick={() => { if (selected && selected.kind !== 'file') onChoose(selected); }}>
        {de ? 'Auswählen & scannen' : 'Select & scan'}
      </button>
    </footer>
  </dialog>;
}
