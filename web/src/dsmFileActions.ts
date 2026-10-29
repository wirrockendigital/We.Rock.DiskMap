import { openFileStation, type FileStationOptions, type FileStationTarget } from './dsmFileStation';
import { currentToken, sessionRequest, SessionDenied } from './session';

// Only server-resolved live targets may enter this boundary; historical paths are not authority.
export type VerifiedFileTarget = FileStationTarget & { kind: 'file'; noticeCode?: never };
export type DSMFileActionOptions = FileStationOptions;
export class DSMFileActionFailure extends Error {
  constructor(readonly code: 'TARGET_INVALID' | 'UNAVAILABLE' | 'REJECTED' | 'OUTCOME_UNKNOWN' | 'VIEWER_UNAVAILABLE') { super(`DSM_FILE_ACTION_${code}`); }
}
// No public, verified image/text launcher contract is available; never guess AppLaunch parameters.
export const dsmViewerCapability = Object.freeze({ image: false, text: false });
export const showInFileStation = openFileStation;
export async function openInDSMViewer(_target: VerifiedFileTarget, options: DSMFileActionOptions): Promise<never> {
  options.signal.throwIfAborted();
  throw new DSMFileActionFailure('VIEWER_UNAVAILABLE');
}
// The API is path-based, not inode-conditional. The caller must freshly resolve the exact confirmed file.
function filePath(target: VerifiedFileTarget): string {
  const path = target?.fileStationPath;
  if (target?.kind !== 'file' || target.noticeCode !== undefined || typeof path !== 'string'
    || new TextEncoder().encode(path).length > 4096 || !path.startsWith('/') || /[\u0000-\u001f\u007f]/.test(path)
    || path.slice(1).split('/').length < 2 || path.slice(1).split('/').some(p => !p || p === '.' || p === '..')
    || (target.parentPath !== undefined && target.parentPath !== path.slice(0, path.lastIndexOf('/')))) {
    throw new DSMFileActionFailure('TARGET_INVALID');
  }
  return path;
}
type Dict = Record<string, unknown>;
const object = (x: unknown): x is Dict => !!x && typeof x === 'object' && !Array.isArray(x);
// Each operation owns one deadline and session identity; credentials never enter receipts or URLs.
function operation(options: DSMFileActionOptions) {
  const host = options.host ?? window;
  const token = currentToken(host);
  if (!token) throw new SessionDenied('SESSION_REQUIRED');
  const signal = AbortSignal.any([options.signal, AbortSignal.timeout(10000)]);
  const check = () => {
    signal.throwIfAborted();
    if (currentToken(host) !== token) throw new SessionDenied('SESSION_REQUIRED');
  };
  const request = options.request ?? ((input, init) => fetch(input, init));
  return { signal, token, check, request };
}
// Read at most64KiB; upstream messages and paths are never returned as diagnostics.
async function envelope(response: Response, check: () => void): Promise<Dict> {
  check();
  if (response.status === 401 || response.status === 403) throw new SessionDenied('SESSION_REQUIRED');
  if (!response.ok || !response.body) throw new DSMFileActionFailure('UNAVAILABLE');
  const reader = response.body.getReader(); const chunks: Uint8Array[] = []; let size = 0;
  try {
    while (true) {
      const next = await reader.read(); check();
      if (next.done) break;
      size += next.value.byteLength;
      if (size > 65536) throw new DSMFileActionFailure('UNAVAILABLE');
      chunks.push(next.value);
    }
  } finally { void reader.cancel().catch(() => {}); reader.releaseLock(); }
  const bytes = new Uint8Array(size); let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
  let value: unknown;
  try { value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); }
  catch { throw new DSMFileActionFailure('UNAVAILABLE'); }
  if (!object(value) || typeof value.success !== 'boolean') throw new DSMFileActionFailure('UNAVAILABLE');
  if (!value.success) throw new DSMFileActionFailure('REJECTED');
  if (!object(value.data)) throw new DSMFileActionFailure('UNAVAILABLE');
  return value.data;
}
// Official File Station API Guide: SYNO.API.Info discovery; Delete v2 start/status (§90–92).
// https://global.download.synology.com/download/Document/Software/DeveloperGuide/Package/FileStation/All/enu/Synology_File_Station_API_Guide.pdf
async function discover(op: ReturnType<typeof operation>): Promise<string> {
  const response = await op.request('/webapi/query.cgi?api=SYNO.API.Info&version=1&method=query&query=SYNO.FileStation.Delete', {
    credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal: op.signal,
    headers: { 'X-SYNO-TOKEN': op.token, Accept: 'application/json' },
  });
  const data = await envelope(response, op.check); const api = data['SYNO.FileStation.Delete'];
  if (!object(api) || api.path !== 'entry.cgi' || !Number.isInteger(api.minVersion) || !Number.isInteger(api.maxVersion)
    || (api.minVersion as number) > 2 || (api.maxVersion as number) < 2) throw new DSMFileActionFailure('UNAVAILABLE');
  return '/webapi/entry.cgi';
}
// Start exactly one nonrecursive file deletion. No mutation retry and no success before task completion.
export async function deleteSingleFile(target: VerifiedFileTarget, options: DSMFileActionOptions & { confirmed: true }): Promise<{ taskId: string; state: 'started' }> {
  const path = filePath(target);
  if (options.confirmed !== true) throw new DSMFileActionFailure('TARGET_INVALID');
  const op = operation(options);
  await sessionRequest('session', op.signal, op.token, op.request); op.check();
  const endpoint = await discover(op); op.check();
  const body = new URLSearchParams({ api: 'SYNO.FileStation.Delete', version: '2', method: 'start', path: JSON.stringify([path]), recursive: 'false', accurate_progress: 'false' });
  try {
    // After dispatch, cancellation/transport/invalid receipt is uncertain, not a rejected deletion.
    const response = await op.request(endpoint, { method: 'POST', body, credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal: op.signal,
      headers: { 'X-SYNO-TOKEN': op.token, Accept: 'application/json' } });
    const data = await envelope(response, op.check); op.check();
    if (typeof data.taskid !== 'string' || !/^[A-Za-z0-9_-]{1,256}$/.test(data.taskid)) throw new DSMFileActionFailure('UNAVAILABLE');
    return { taskId: data.taskid, state: 'started' };
  } catch (error) {
    if (error instanceof DSMFileActionFailure && error.code === 'REJECTED') throw error;
    throw new DSMFileActionFailure('OUTCOME_UNKNOWN');
  }
}
// Observe only a task returned by start in the same UI session. Polling policy belongs to the caller.
export async function readDeleteStatus(taskId: string, options: DSMFileActionOptions): Promise<{ finished: boolean }> {
  if (!/^[A-Za-z0-9_-]{1,256}$/.test(taskId)) throw new DSMFileActionFailure('TARGET_INVALID');
  const op = operation(options);
  await sessionRequest('session', op.signal, op.token, op.request); op.check();
  const endpoint = await discover(op); op.check();
  const body = new URLSearchParams({ api: 'SYNO.FileStation.Delete', version: '2', method: 'status', taskid: JSON.stringify(taskId) });
  const response = await op.request(endpoint, { method: 'POST', body, credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal: op.signal,
    headers: { 'X-SYNO-TOKEN': op.token, Accept: 'application/json' } });
  const data = await envelope(response, op.check); op.check();
  if (typeof data.finished !== 'boolean') throw new DSMFileActionFailure('UNAVAILABLE');
  return { finished: data.finished };
}
