import type { PreferenceWorkspace, PreferenceDisplay, PreferenceRequest, PreferenceView, UserPreference } from './contracts';
import { currentToken, SessionDenied } from './session';

export type PreferenceStatus = 'idle' | 'saving' | 'uncertain' | 'conflict';
export class PreferenceError extends Error {}
type Pending = Readonly<{ body: string; key: string; revision: number }>;
// Validate remembered navigation without treating stored identities as live authorizations.
export function validView(view: unknown): boolean {
  if (view === null) return true;
  if (!view || typeof view !== 'object') return false;
  const v = view as PreferenceView;
  const id = (x: unknown) => typeof x === 'string' && /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(x);
  const node = (x: unknown) => typeof x === 'string' && /^[A-Za-z0-9_.:-]{1,160}$/.test(x);
  if (!id(v.profileId) || !id(v.snapshotId) || !node(v.rootId) || !(v.selectedNodeId === null || node(v.selectedNodeId)) ||
    !['treemap', 'radial'].includes(v.map) || !['logical', 'allocated'].includes(v.metric) ||
    !['highlight', 'hits-only'].includes(v.filterMode) || !['name', 'size', 'mtime', 'type'].includes(v.sort) ||
    !['asc', 'desc'].includes(v.direction) || !v.search || typeof v.search.text !== 'string' ||
    new TextEncoder().encode(v.search.text).length > 512 || !['name', 'path', 'both'].includes(v.search.field) ||
    !['direct', 'recursive'].includes(v.search.scope) || !v.filter || !Array.isArray(v.filter.categories)) return false;
  const f = v.filter;
  const cats = ['image', 'video', 'audio', 'document', 'archive', 'diskImage', 'database', 'otherUnknown'];
  if (f.categories.length > 8 || new Set(f.categories).size !== f.categories.length || f.categories.some(x => !cats.includes(x))) return false;
  for (const bytes of [f.minSize, f.maxSize]) if (bytes !== undefined && (typeof bytes !== 'string' || !/^(0|[1-9][0-9]{0,127})$/.test(bytes))) return false;
  if (f.minSize !== undefined && f.maxSize !== undefined && BigInt(f.minSize) > BigInt(f.maxSize)) return false;
  if (f.objectKinds !== undefined && (!Array.isArray(f.objectKinds) || f.objectKinds.length > 2 || new Set(f.objectKinds).size !== f.objectKinds.length || f.objectKinds.some(x => !['file','directory'].includes(x)))) return false;
  if (f.text !== undefined && (typeof f.text !== 'string' || new TextEncoder().encode(f.text).length > 512)) return false;
  if (f.field !== undefined && !['name','path','both'].includes(f.field)) return false;
  for (const date of [f.modifiedFrom, f.modifiedTo]) if (date !== undefined && (typeof date !== 'string' || !Number.isFinite(Date.parse(date)))) return false;
  return !(f.modifiedFrom && f.modifiedTo && Date.parse(f.modifiedFrom) > Date.parse(f.modifiedTo));
}
// Compare every stored selector while normalizing only Go's optional empty fields.
function receiptViewKey(view: PreferenceView | null): string {
  if (view === null) return 'null';
  const f = view.filter;
  return JSON.stringify([view.profileId, view.snapshotId, view.rootId, view.selectedNodeId,
    view.map, view.metric, view.filterMode, view.sort, view.direction,
    view.search.text, view.search.field, view.search.scope, f.categories,
    f.minSize ?? null, f.maxSize ?? null, f.modifiedFrom ?? null, f.modifiedTo ?? null,
    f.objectKinds ?? [], f.text ?? '', f.field ?? '']);
}
// JSON object member order is not part of a preference receipt's value.
function receiptValueKey(value:unknown):string|undefined {
 const canonical=(v:unknown):unknown=>Array.isArray(v)?v.map(canonical):v&&typeof v==='object'?Object.fromEntries(Object.entries(v).sort(([a],[b])=>a.localeCompare(b)).map(([k,x])=>[k,canonical(x)])):v;
 return JSON.stringify(canonical(value));
}
// Limit retained transport bytes before parsing; no unbounded response body is buffered.
export async function responseJSON(response: Response): Promise<unknown> {
  if (!response.body) throw new PreferenceError('INVALID_PREFERENCE_RESPONSE');
  const reader = response.body.getReader(); const chunks: Uint8Array[] = []; let length = 0;
  try {
    for (;;) { const next = await reader.read(); if (next.done) break;
      length += next.value.length;
      if (length > 65536) { await reader.cancel(); throw new PreferenceError('INVALID_PREFERENCE_RESPONSE'); }
      chunks.push(next.value);
    }
  } finally { reader.releaseLock(); }
  const bytes = new Uint8Array(length); let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
  try { return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); }
  catch { throw new PreferenceError('INVALID_PREFERENCE_RESPONSE'); }
}
// Personal zones must be IANA-style identifiers supported by this browser, never raw offsets.
function validTimeZone(value:unknown):boolean {if(value===null)return true;if(typeof value!=='string'||!value||value.length>128||!/^[A-Za-z][A-Za-z0-9_+./-]*$/.test(value)||value.includes('..'))return false;try{new Intl.DateTimeFormat('en',{timeZone:value});return true;}catch{return false;}}
// Validate every display field without coercing missing values or numeric strings.
function validDisplay(value: unknown): boolean {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const d = value as Record<string, unknown>;
  return Object.keys(d).length === 7 && ['decimal','binary'].includes(d.units as string) &&
    ['folder','filetype','mtime'].includes(d.colorMode as string) &&
    (d.detail === 'auto' || d.detail === 'max' || (typeof d.detail === 'number' && Number.isInteger(d.detail) && d.detail >= 1 && d.detail <= 8)) &&
    typeof d.labels === 'boolean' && typeof d.percentages === 'boolean' &&
    ['normal','subtle'].includes(d.hierarchyBorders as string) && ['auto','reduce'].includes(d.reducedMotion as string);
}
// Workspace data is presentation-only but still receives bounded schema validation.
export function validWorkspace(value:unknown):boolean {
 if(value===undefined||value===null)return true;
 if(typeof value!=='object')return false;const w=value as PreferenceWorkspace;
 // Stored source choices remain bounded selectors and are revalidated by the live inventory.
 if(w.source!=null){const s=w.source;if(!['nas','volumes','shares','folder'].includes(s.kind)||!Array.isArray(s.targetIds)||(s.kind==='nas'?s.targetIds.length!==0:s.targetIds.length!==1)||s.targetIds.some(id=>! /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(id))||s.kind==='folder'&&(typeof s.relativePath!=='string'||s.relativePath.length>4096||s.relativePath.split('/').some(p=>!p||p==='.'||p==='..')))return false;}
 if(w.zoom?.snapshotId!==undefined&&!/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(w.zoom.snapshotId))return false;
 if([w.scanRecycle,w.scanSystem].some(v=>v!==undefined&&typeof v!=='boolean'))return false;
 if(w.sizeUnit!==undefined&&!['TB','GB','MB','mixed','hidden'].includes(w.sizeUnit))return false;
 if(w.showShareStatus!==undefined&&typeof w.showShareStatus!=='boolean')return false;
 const integer=(v:unknown,min:number,max:number)=>typeof v==='number'&&Number.isInteger(v)&&v>=min&&v<=max;
 if(!['left','right','filterOpen','showRecycle','showSystem'].every(k=>typeof (w as unknown as Record<string,unknown>)[k]==='boolean')||!integer(w.leftWidth,180,700)||!integer(w.rightWidth,240,800)||!integer(w.treeScroll,0,1e8)||!integer(w.listScroll,0,1e8)||!['name','size'].includes(w.treeSort)||!['asc','desc'].includes(w.treeDirection)||!Array.isArray(w.expanded)||w.expanded.length>256||new Set(w.expanded).size!==w.expanded.length||w.expanded.some(s=>typeof s!=='string'||!s||new TextEncoder().encode(s).length>4096||/[\u0000\r\n]/.test(s)))return false;
 if(w.jobs!=null&&(!Array.isArray(w.jobs)||w.jobs.length>32||w.jobs.some(j=>typeof j!=='string'||!/^([A-Za-z0-9][A-Za-z0-9_-]{0,127})\|([A-Za-z0-9][A-Za-z0-9_-]{0,127})$/.test(j))))return false;
 if(w.zoom!=null&&(!/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(w.zoom.root)||!Number.isFinite(w.zoom.scale)||w.zoom.scale<1||w.zoom.scale>64||![w.zoom.x,w.zoom.y].every(n=>Number.isFinite(n)&&n<=0&&n>=-1e6)))return false;
 const l=w.list;return !!l&&typeof l.text==='string'&&new TextEncoder().encode(l.text).length<=512&&!l.text.includes('\0')&&typeof l.recursive==='boolean'&&typeof l.foldersFirst==='boolean'&&['all','file','directory'].includes(l.kind)&&['','image','video','audio','document','archive','diskImage','database','otherUnknown'].includes(l.category)&&['name','size','type'].includes(l.sort)&&['asc','desc'].includes(l.direction);
}
// Response data is never adopted on a malformed envelope or an invalid revision.
function decode(value: unknown): UserPreference {
  const envelope = value as { contractVersion?: unknown; data?: UserPreference } | null;
  const data = envelope?.data;
  if (envelope?.contractVersion !== 1 || !data || data.version !== 2 || !Number.isSafeInteger(data.revision) || data.revision < 0 || data.revision > 2147483647 ||
    typeof data.dsmUserId !== 'string' || !data.dsmUserId || new TextEncoder().encode(data.dsmUserId).length > 256 || /[\u0000-\u001f\u007f]/.test(data.dsmUserId) || !['de', 'en', 'auto'].includes(data.language) ||
    !['light', 'dark', 'auto'].includes(data.theme) || !['treemap', 'radial'].includes(data.map) ||
    !(data.preferredProfileId === null || (typeof data.preferredProfileId === 'string' && /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(data.preferredProfileId))) ||
    !(data.locale === null || ['de-DE','en-US','en-GB'].includes(data.locale)) || !validTimeZone(data.timeZone) || !validView(data.lastView) || !validDisplay(data.display) || !validWorkspace(data.workspace)) throw new PreferenceError('INVALID_PREFERENCE_RESPONSE');
  return structuredClone(data);
}

// Only one last-view intent may be outstanding; uncertain retries reuse identical serialized bytes.
export class PreferenceClient {
  private value: UserPreference | null = null;
  private pending: Pending | null = null;
  private generation = 0;
  private disposed = false;
  private controller: AbortController | null = null;
  status: PreferenceStatus = 'idle';
  // Invoke native fetch as a global function, never with this client as its receiver.
  constructor(private request: typeof fetch = (input, init) => fetch(input, init), private token: () => string | null = currentToken,
    private key: () => string = () => crypto.randomUUID()) {}
  get preference(): UserPreference | null { return this.value ? structuredClone(this.value) : null; }
  // Reload is an explicit conflict resolution step; uncertain mutations must first be replayed.
  async read(signal?: AbortSignal): Promise<UserPreference> {
    this.available();
    if (this.pending || this.controller) throw new PreferenceError('PREFERENCE_PENDING');
    const epoch = ++this.generation;
    const controller = this.controller = new AbortController();
    // Settle even if a transport ignores cancellation; late replies cannot update the cache.
    const forwardAbort = () => controller.abort(signal?.reason);
    signal?.addEventListener('abort', forwardAbort, { once: true });
    if (signal?.aborted) forwardAbort();
    let abortRead!: () => void;
    const aborted = new Promise<never>((_, reject) => {
      abortRead = () => reject(controller.signal.reason ?? new PreferenceError('PREFERENCE_READ_ABORTED'));
      controller.signal.addEventListener('abort', abortRead, { once: true });
    });
    try {
      controller.signal.throwIfAborted();
      const response = await Promise.race([this.request('./api.cgi?action=preferences', this.options(controller.signal)), aborted]);
      this.current(epoch); controller.signal.throwIfAborted(); this.auth(response);
      if (!response.ok) throw new PreferenceError('PREFERENCE_UNAVAILABLE');
      const raw = await Promise.race([responseJSON(response), aborted]);
      this.current(epoch); controller.signal.throwIfAborted();
      const value = decode(raw);
      this.value = value; this.status = 'idle';
      return structuredClone(value);
    } catch (error) { this.current(epoch); controller.signal.throwIfAborted(); throw error; }
    finally {
      signal?.removeEventListener('abort', forwardAbort);
      controller.signal.removeEventListener('abort', abortRead);
      if (epoch === this.generation) this.controller = null;
    }
  }
  // Adoption is deliberate, never an automatic conflict overwrite or a cancellation of an uncertain write.
  adopt(value: UserPreference): void {
    this.available();
    if (this.pending || this.controller) throw new PreferenceError('PREFERENCE_PENDING');
    this.value = decode({ contractVersion: 1, data: value }); this.status = 'idle'; ++this.generation;
  }
  async writeLastView(view: PreferenceView | null): Promise<UserPreference> {
    this.available();
    if (this.pending || this.controller) throw new PreferenceError('PREFERENCE_PENDING');
    if (this.status === 'conflict' || !this.value) throw new PreferenceError('PREFERENCE_RELOAD_REQUIRED');
    if (!validView(view)) throw new PreferenceError('INVALID_REQUEST');
    const payload: PreferenceRequest = { contractVersion: 2, expectedRevision: this.value.revision, group: 'lastView', view: { value: view } };
    const body = JSON.stringify(payload);
    if (new TextEncoder().encode(body).length > 16384) throw new PreferenceError('INVALID_REQUEST');
    this.pending = Object.freeze({ body, key: this.key(), revision: this.value.revision });
    return this.send();
  }
  // Language changes preserve every other appearance field and reuse an uncertain exact intent.
  async writeLanguage(language: 'auto' | 'de' | 'en'): Promise<UserPreference> {
    this.available();
    if (!['auto', 'de', 'en'].includes(language)) throw new PreferenceError('INVALID_REQUEST');
    if (this.pending) {
      const intent = JSON.parse(this.pending.body) as PreferenceRequest;
      if (this.status === 'uncertain' && intent.group === 'appearance' && intent.appearance.language === language) return this.retry();
      throw new PreferenceError('PREFERENCE_PENDING');
    }
    if (this.controller) throw new PreferenceError('PREFERENCE_PENDING');
    if (this.status === 'conflict' || !this.value) throw new PreferenceError('PREFERENCE_RELOAD_REQUIRED');
    const {theme, map, locale, timeZone} = this.value;
    const payload: PreferenceRequest = {contractVersion: 2, expectedRevision: this.value.revision,
      group: 'appearance', appearance: {language, theme, map, locale, timeZone}};
    this.pending = Object.freeze({body: JSON.stringify(payload), key: this.key(), revision: this.value.revision});
    return this.send();
  }
  // Regional settings share the existing appearance CAS and preserve unrelated personal fields.
  async writeRegional(change: Pick<UserPreference,'language'|'locale'|'timeZone'>): Promise<UserPreference> {
    this.available();
    if (!['auto','de','en'].includes(change.language) || change.locale!==null&&!['de-DE','en-US','en-GB'].includes(change.locale)) throw new PreferenceError('INVALID_REQUEST');
    if(!validTimeZone(change.timeZone))throw new PreferenceError('INVALID_REQUEST');
    if(this.pending){const intent=JSON.parse(this.pending.body) as PreferenceRequest;
      if(this.status==='uncertain'&&intent.group==='appearance'&&Object.entries(change).every(([key,value])=>intent.appearance[key as keyof typeof change]===value))return this.retry();
      throw new PreferenceError('PREFERENCE_PENDING');}
    if(this.controller)throw new PreferenceError('PREFERENCE_PENDING');
    if(this.status==='conflict'||!this.value)throw new PreferenceError('PREFERENCE_RELOAD_REQUIRED');
    const {theme,map}=this.value;
    const payload:PreferenceRequest={contractVersion:2,expectedRevision:this.value.revision,group:'appearance',appearance:{...change,theme,map}};
    this.pending=Object.freeze({body:JSON.stringify(payload),key:this.key(),revision:this.value.revision});return this.send();
  }
  // Theme changes share the appearance CAS and never replace an unresolved different intent.
  async writeTheme(theme: UserPreference['theme']): Promise<UserPreference> {
    this.available();
    if (!['auto','light','dark'].includes(theme)) throw new PreferenceError('INVALID_REQUEST');
    if(this.pending){const intent=JSON.parse(this.pending.body) as PreferenceRequest;
      if(this.status==='uncertain'&&intent.group==='appearance'&&intent.appearance.theme===theme)return this.retry();
      throw new PreferenceError('PREFERENCE_PENDING');}
    if(this.controller)throw new PreferenceError('PREFERENCE_PENDING');
    if(this.status==='conflict'||!this.value)throw new PreferenceError('PREFERENCE_RELOAD_REQUIRED');
    const {language,map,locale,timeZone}=this.value;
    const payload:PreferenceRequest={contractVersion:2,expectedRevision:this.value.revision,group:'appearance',appearance:{theme,language,map,locale,timeZone}};
    this.pending=Object.freeze({body:JSON.stringify(payload),key:this.key(),revision:this.value.revision});return this.send();
  }
  // Settings groups share the same single outstanding CAS intent as navigation.
  private async writeSetting(change:Omit<PreferenceRequest,'contractVersion'|'expectedRevision'>):Promise<UserPreference>{
    this.available();
    if(this.pending){const intent=JSON.parse(this.pending.body);const {contractVersion:_,expectedRevision:__,...old}=intent;
      if(this.status==='uncertain'&&JSON.stringify(old)===JSON.stringify(change))return this.retry();throw new PreferenceError('PREFERENCE_PENDING');}
    if(this.controller)throw new PreferenceError('PREFERENCE_PENDING');
    if(this.status==='conflict'||!this.value)throw new PreferenceError('PREFERENCE_RELOAD_REQUIRED');
    this.pending=Object.freeze({body:JSON.stringify({contractVersion:2,expectedRevision:this.value.revision,...change}),key:this.key(),revision:this.value.revision});return this.send();
  }
  // Workspace updates use the same revision and replay discipline as the other preference groups.
  async writeWorkspace(workspace:PreferenceWorkspace):Promise<UserPreference>{
    return this.writeSetting({group:'workspace',workspace} as PreferenceRequest);
  }
  async writeDisplay(display:PreferenceDisplay):Promise<UserPreference>{
    if(!validDisplay(display))throw new PreferenceError('INVALID_REQUEST');
    return this.writeSetting({group:'display',display} as PreferenceRequest);
  }
  async writePreferredProfile(profileId:string|null):Promise<UserPreference>{
    if(profileId!==null&&!/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(profileId))throw new PreferenceError('INVALID_REQUEST');
    return this.writeSetting({group:'preferredProfile',preferredProfile:{profileId}} as PreferenceRequest);
  }
  async writeMap(map:UserPreference['map']):Promise<UserPreference>{
    this.available();if(!['treemap','radial'].includes(map)||!this.value)throw new PreferenceError('INVALID_REQUEST');
    const {language,theme,locale,timeZone}=this.value;
    return this.writeSetting({group:'appearance',appearance:{language,theme,locale,timeZone,map}} as PreferenceRequest);
  }
  async retry(): Promise<UserPreference> {
    this.available();
    if (!this.pending || this.status !== 'uncertain') throw new PreferenceError('PREFERENCE_RETRY_UNAVAILABLE');
    return this.send();
  }
  // Disposal invalidates both success and error completions; it does not assert that a write was cancelled remotely.
  dispose(): void { this.disposed = true; ++this.generation; this.controller?.abort(); this.controller = null; }
  private available(): void { if (this.disposed) throw new PreferenceError('PREFERENCE_DISPOSED'); }
  private current(epoch: number): void { this.available(); if (epoch !== this.generation) throw new PreferenceError('STALE_PREFERENCE_RESPONSE'); }
  private auth(response: Response): void { if (response.status === 401 || response.status === 403) throw new SessionDenied('SESSION_REQUIRED'); }
  private options(signal: AbortSignal): RequestInit {
    const token = this.token(); if (!token) throw new SessionDenied('SESSION_REQUIRED');
    return { signal, credentials: 'same-origin', cache: 'no-store', redirect: 'error', headers: { 'X-SYNO-TOKEN': token, Accept: 'application/json' } };
  }
  private async send(): Promise<UserPreference> {
    const intent = this.pending!; const epoch = ++this.generation;
    const controller = this.controller = new AbortController(); this.status = 'saving';
    try {
      const options = this.options(controller.signal);
      const response = await this.request('./api.cgi?action=preferences', { ...options, method: 'POST', body: intent.body,
        headers: { ...options.headers, 'Content-Type': 'application/json', 'Idempotency-Key': intent.key } });
      this.current(epoch); this.auth(response);
      if (!response.ok) {
        const raw = await responseJSON(response).catch(() => null) as { contractVersion?: number; error?: { code?: string } } | null;
        this.current(epoch);
        const code = raw?.contractVersion === 1 ? raw.error?.code : undefined;
        if (response.status === 409 && (code === 'PREFERENCE_CONFLICT' || code === 'IDEMPOTENCY_CONFLICT')) {
          this.pending = null; this.status = 'conflict'; throw new PreferenceError(code);
        }
        if (response.status === 400 && code === 'INVALID_REQUEST') { this.pending = null; this.status = 'idle'; throw new PreferenceError(code); }
        throw new PreferenceError('PREFERENCE_UNAVAILABLE');
      }
      const raw = await responseJSON(response); this.current(epoch); const value = decode(raw);
      const submitted = JSON.parse(intent.body) as PreferenceRequest;
      // Receipts must confirm the submitted group; a different valid preference is not success.
      const matches = submitted.group === 'workspace' ? !!value.workspace&&Object.entries(submitted.workspace).every(([k,v])=>k==='source'&&v==null?value.workspace!.source==null:receiptValueKey(value.workspace![k as keyof PreferenceWorkspace])===receiptValueKey(v)) : submitted.group === 'lastView'
        ? receiptViewKey(value.lastView) === receiptViewKey(submitted.view.value)
        : submitted.group === 'display' ? Object.entries(submitted.display).every(([key,expected])=>value.display[key as keyof PreferenceDisplay]===expected)
        : submitted.group === 'preferredProfile' ? value.preferredProfileId===submitted.preferredProfile.profileId
        : submitted.group === 'appearance' && Object.entries(submitted.appearance).every(([key, expected]) => value[key as keyof UserPreference] === expected);
      if (value.dsmUserId !== this.value?.dsmUserId || value.revision !== intent.revision + 1 || !matches) throw new PreferenceError('INVALID_PREFERENCE_RESPONSE');
      this.value = value; this.pending = null; this.status = 'idle'; return structuredClone(value);
    } catch (error) {
      this.current(epoch);
      if (!this.disposed && epoch === this.generation && this.pending) this.status = 'uncertain';
      throw error;
    } finally { if (epoch === this.generation) this.controller = null; }
  }
}
