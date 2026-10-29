import { currentToken, SessionDenied } from './session';

// A retry must retain this intent's key; creating a new key means a new user intent.
export interface ScanIntent { contractVersion: 1; profileId: string; mode: 'full' | 'rebuild' | 'reconcile' }

// Bind a retry key to its original mode; uncertain delivery never changes the requested operation.
export interface PendingScan { key: string; mode: ScanIntent['mode'] }
export function scanIntent(previous: PendingScan | null, mode: ScanIntent['mode'], key: () => string = () => crypto.randomUUID()): PendingScan {
  if (!['full', 'rebuild', 'reconcile'].includes(mode)) throw new Error('INVALID_REQUEST');
  if (previous) {
    if (previous.mode !== mode) throw new Error('SCAN_INTENT_PENDING');
    return previous;
  }
  return { key: key(), mode };
}

// Submit no source paths, passwords or tokens in the URL; never retry mutations implicitly.
export async function requestScan(intent: ScanIntent, idempotencyKey: string, signal: AbortSignal, request: typeof fetch = fetch, token = currentToken()): Promise<unknown> {
  const validID = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
  if (intent.contractVersion !== 1 || !validID.test(intent.profileId) || !['full', 'rebuild', 'reconcile'].includes(intent.mode) || !validID.test(idempotencyKey)) throw new Error('INVALID_REQUEST');
  if (!token) throw new SessionDenied('SESSION_REQUIRED');
  signal.throwIfAborted();
  const response = await request('./api.cgi?action=scan', {
    method: 'POST', credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal,
    headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-SYNO-TOKEN': token, 'Idempotency-Key': idempotencyKey },
    body: JSON.stringify({ contractVersion: 1, profileId: intent.profileId, mode: intent.mode }),
  });
  if (response.status === 401 || response.status === 403) throw new SessionDenied('SESSION_REQUIRED');
  if (response.status === 409) {
    // Preserve only the documented conflict codes, never server diagnostics.
    const body: unknown = await response.json();
    const envelope = body as { contractVersion?: unknown; error?: { code?: unknown } } | null;
    const code = envelope?.error?.code;
    if (envelope?.contractVersion !== 1 || (code !== 'IDEMPOTENCY_CONFLICT' && code !== 'JOB_CONFLICT' && code !== 'QUEUE_FULL')) throw new Error('SERVICE_RESPONSE_INVALID');
    throw new Error(code);
  }
  if (!response.ok) throw new Error('ACTION_UNAVAILABLE');
  return response.json();
}
