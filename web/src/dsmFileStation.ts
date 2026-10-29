import { currentToken, sessionRequest } from './session';

// Targets must originate from the authenticated server's current share mapping.
export interface FileStationTarget {
  fileStationPath: string;
  kind: 'directory' | 'file';
  parentPath?: string;
  // A missing file may expose only its verified surviving parent, with an explicit notice.
  noticeCode?: 'FILE_REMOVED_PARENT_AVAILABLE';
}
// Typed local failures carry only fixed codes, never upstream DSM diagnostics or paths.
export class FileStationFailure extends Error {
  constructor(readonly code: 'BUILD_UNSUPPORTED' | 'UNAVAILABLE' | 'TARGET_INVALID' | 'LAUNCH_FAILED') { super(`FILE_STATION_${code}`); }
}
type DSMHost = Window & { SYNO?: { SDS?: { AppLaunch?: unknown } } };
export interface FileStationOptions {
  dsmBuild: string;
  signal: AbortSignal;
  host?: Window;
  request?: typeof fetch;
}

// This is a build-specific launcher contract; highlighting is not yet verified.
export const fileStationCapability = Object.freeze({ dsmBuild: '90080', highlighting: false });

// Preserve literal Unicode and percent characters while rejecting ambiguous path structure.
function validPath(path: unknown): path is string {
  return typeof path === 'string' && path.startsWith('/') && path.length > 1
    && !/[\u0000-\u001f\u007f]/.test(path)
    && path.slice(1).split('/').every(part => part !== '' && part !== '.' && part !== '..');
}

// Structural checks supplement, but never replace, server authorization and scope validation.
function folderFor(target: FileStationTarget): string {
  if (!target || !validPath(target.fileStationPath)) throw new FileStationFailure('TARGET_INVALID');
  // Reject unknown notices and contradictory file targets before any DSM navigation.
  if (target.noticeCode !== undefined && (target.noticeCode !== 'FILE_REMOVED_PARENT_AVAILABLE' || target.kind !== 'directory')) {
    throw new FileStationFailure('TARGET_INVALID');
  }
  if (target.kind === 'directory') {
    if (target.parentPath !== undefined) throw new FileStationFailure('TARGET_INVALID');
    return target.fileStationPath;
  }
  if (target.kind !== 'file') throw new FileStationFailure('TARGET_INVALID');
  const parent = target.fileStationPath.slice(0, target.fileStationPath.lastIndexOf('/'));
  if (!validPath(parent) || (target.parentPath !== undefined && target.parentPath !== parent)) {
    throw new FileStationFailure('TARGET_INVALID');
  }
  return parent;
}

// Resolve the current same-origin DSM parent without an alternate URL or popup fallback.
function launcher(host: Window): { owner: object; launch: (name: string, parameters: { opendir: string }) => unknown } {
  try {
    if (host.parent === host) throw new Error('standalone');
    const owner = (host.parent as DSMHost).SYNO?.SDS;
    if (!owner || typeof owner.AppLaunch !== 'function') throw new Error('missing');
    return { owner, launch: owner.AppLaunch as (name: string, parameters: { opendir: string }) => unknown };
  } catch { throw new FileStationFailure('UNAVAILABLE'); }
}

// Recheck the DSM session before requesting navigation; the result does not confirm the visible folder.
export async function openFileStation(target: FileStationTarget, options: FileStationOptions): Promise<{ requestedPath: string; highlighted: false }> {
  if (options.dsmBuild !== fileStationCapability.dsmBuild) throw new FileStationFailure('BUILD_UNSUPPORTED');
  const openedPath = folderFor(target);
  const host = options.host ?? window;
  options.signal.throwIfAborted();
  launcher(host);
  await sessionRequest('session', options.signal, currentToken(host), options.request ?? fetch);
  options.signal.throwIfAborted();
  const current = launcher(host);
  try { current.launch.call(current.owner, 'SYNO.SDS.App.FileStation3.Instance', { opendir: openedPath }); }
  catch { throw new FileStationFailure('LAUNCH_FAILED'); }
  return { requestedPath: openedPath, highlighted: false };
}
