// Translate presentation-only kind labels; recorded names and wire identities remain untouched.
const kinds = {
 de: {file:'Datei',directory:'Ordner',nas:'NAS',volume:'Volume',share:'Freigabebereich',other:'Weitere erfasste Volume-Bereiche',group:'Zusammengefasste Gruppe',symlink:'Symbolischer Link',fifo:'FIFO',socket:'Socket',blockDevice:'Blockgerät',characterDevice:'Zeichengerät',unknown:'Unbekannter Typ'},
 en: {file:'File',directory:'Directory',nas:'NAS',volume:'Volume',share:'Shared-folder area',other:'Other recorded volume areas',group:'Aggregated group',symlink:'Symbolic link',fifo:'FIFO',socket:'Socket',blockDevice:'Block device',characterDevice:'Character device',unknown:'Unknown type'},
};
export function nodeKindLabel(kind:string, language:'de'|'en'):string {
 const labels=kinds[language];
 return Object.hasOwn(labels,kind)?labels[kind as keyof typeof labels]:labels.unknown;
}
