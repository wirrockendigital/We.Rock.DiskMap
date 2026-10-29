// Session credentials remain in the existing DSM context and are never persisted by DiskMap.
export type SessionState = 'checking' | 'authenticated' | 'denied' | 'unavailable';
type DSMWindow = Window & { SYNO?: { SDS?: { Session?: { SynoToken?: unknown } } } };
export class SessionDenied extends Error {}

// Read the tested same-origin DSM adapter afresh for each request; inaccessible contexts fail closed.
export function currentToken(host: Window = window): string | null {
  try {
    const token = (host.parent as DSMWindow).SYNO?.SDS?.Session?.SynoToken;
    return typeof token === 'string' && token.length > 0 && token.length <= 4096 ? token : null;
  } catch { return null; }
}

// Only known same-origin CGI operations are addressable through this boundary.
export async function sessionRequest(action: 'session' | 'health', signal: AbortSignal, token = currentToken(), request: typeof fetch = fetch): Promise<unknown> {
  if (!token) throw new SessionDenied('SESSION_REQUIRED');
  const response = await request(`./api.cgi?action=${action}`, {
    credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal,
    headers: { 'X-SYNO-TOKEN': token, Accept: 'application/json' },
  });
  // A response from a superseded DSM session must never reach application state.
  if(typeof window!=='undefined'&&currentToken()!==token) throw new SessionDenied('SESSION_CHANGED');
  if (response.status === 401 || response.status === 403) throw new SessionDenied('SESSION_REQUIRED');
  if (!response.ok) throw new Error('SESSION_SERVICE_UNAVAILABLE');
  const body: unknown = await response.json();
  // Body delivery can outlive both the request headers and the DSM session that initiated it.
  if (signal.aborted || (typeof window !== 'undefined' && currentToken() !== token)) throw new SessionDenied('SESSION_CHANGED');
  if (action === 'session') {
    const envelope = body as { contractVersion?: unknown; data?: { authenticated?: unknown } } | null;
    if (envelope?.contractVersion !== 1 || envelope.data?.authenticated !== true) throw new Error('INVALID_SESSION_RESPONSE');
  }
  return body;
}
