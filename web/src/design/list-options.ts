import type {StorageNode} from './model';
// List controls affect the complete index query, never only the current chart budget.
export interface ListOptions {text:string;recursive:boolean;kind:'all'|'file'|'directory';category:string;sort:'name'|'size'|'type';direction:'asc'|'desc';foldersFirst:boolean}
export const defaultListOptions=():ListOptions=>({text:'',recursive:false,kind:'all',category:'',sort:'size',direction:'desc',foldersFirst:true});
export const categoryLabels:Record<string,[string,string]>={image:['Bilder','Images'],video:['Videos','Videos'],audio:['Audio','Audio'],document:['Dokumente','Documents'],archive:['Archive','Archives'],diskImage:['Datenträgerabbilder','Disk images'],database:['Datenbanken','Databases'],otherUnknown:['Sonstiges','Other']};
export interface FileListPage {items:StorageNode[];nextCursor:string|null}
export type FileListLoader=(options:ListOptions,cursor:string,signal:AbortSignal)=>Promise<FileListPage>;
