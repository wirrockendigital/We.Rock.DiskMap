import {snapshotClient,SnapshotAccessDenied} from './snapshot-usage';
import {SessionDenied} from './session';

export type DriveState='readable'|'missing'|'denied'|'unknown';
export interface DriveVersioningStatus {state:DriveState;teamFolder:boolean|null;maxVersions:number|null;personal?:boolean}
export interface DriveVersioningInventory {state:'readable'|'denied'|'unknown';shares:Map<string,DriveVersioningStatus>}
// Missing and malformed observations never imply that versioning is disabled.
export const unknownDriveStatus=():DriveVersioningStatus=>({state:'unknown',teamFolder:null,maxVersions:null});
const record=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);

// Read one bounded, paginated inventory through the existing session-bound DSM transport.
export async function readDriveVersioning(signal:AbortSignal,request:typeof fetch=fetch):Promise<DriveVersioningInventory>{
 try{
  const call=await snapshotClient('Drive',signal,request);
  const info=await call('/webapi/query.cgi?api=SYNO.API.Info&version=1&method=query&query=SYNO.SynologyDrive.Share');
  const api=info['SYNO.SynologyDrive.Share'];
  if(!record(api)||api.path!=='entry.cgi'||typeof api.minVersion!=='number'||typeof api.maxVersion!=='number'||api.minVersion>1||api.maxVersion<1)throw Error('DRIVE_API_UNAVAILABLE');
  const shares=new Map<string,DriveVersioningStatus>();let expected:number|undefined,offset=0;
  do{
   const data=await call('/webapi/entry.cgi',new URLSearchParams({api:'SYNO.SynologyDrive.Share',version:'1',method:'list',offset:String(offset),limit:'100',sort_by:JSON.stringify('share_name'),sort_direction:JSON.stringify('ASC')}));
   if(!Number.isSafeInteger(data.total)||(data.total as number)<0||(data.total as number)>1024||!Array.isArray(data.items)||data.items.length>100||expected!==undefined&&data.total!==expected)throw Error('INVALID_DRIVE_STATUS');
   expected=data.total as number;
   if(offset+data.items.length>expected||offset<expected&&!data.items.length)throw Error('INVALID_DRIVE_PAGE');
   for(const item of data.items){
    if(!record(item)||typeof item.share_name!=='string'||!item.share_name||item.share_name.length>256||/[\x00-\x1f]/.test(item.share_name)||shares.has(item.share_name)||typeof item.share_enable!=='boolean')throw Error('INVALID_DRIVE_SHARE');
    let status:DriveVersioningStatus=unknownDriveStatus();
    if(!item.share_enable)status={state:'missing',teamFolder:false,maxVersions:null};
    else if(Number.isSafeInteger(item.rotate_cnt)&&(item.rotate_cnt as number)>=0&&(item.rotate_cnt as number)<=32)status={state:item.rotate_cnt===0?'missing':'readable',teamFolder:true,maxVersions:item.rotate_cnt as number};
    shares.set(item.share_name,status);
   }
   offset+=data.items.length;
  }while(offset<expected);
  // My Drive settings cover personal Drive directories, not the entire homes share.
  const personal=shares.get('homes/mydrive');if(personal)shares.set('homes',{...personal,personal:true});
  return {state:'readable',shares};
 }catch(error){
  signal.throwIfAborted();if(error instanceof SessionDenied)throw error;
  return {state:error instanceof SnapshotAccessDenied?'denied':'unknown',shares:new Map()};
 }
}
