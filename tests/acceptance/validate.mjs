import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// Matrix validity is structural traceability, never a claim that pending tests passed.
export function validate(matrix) {
  const named = matrix.requirements.filter(r => !r.id.startsWith('U-'));
  const expected = Object.entries({PKG:4,DSM:2,SCOPE:3,PROF:1,SCAN:6,INDEX:3,SCHED:3,VIS:2,TREE:3,RAD:3,FS:3,FIND:2,SIZE:2,EXCL:1,I18N:1,CFG:2,SEC:3,OPS:3}).flatMap(([prefix,count]) => Array.from({length:count},(_,i)=>`${prefix}-${String(i+1).padStart(2,'0')}`));
  const unique = (values, name) => { if(new Set(values).size !== values.length) throw new Error(`DUPLICATE_${name}`); };
  unique(matrix.requirements.map(r=>r.id),'REQUIREMENT'); unique(matrix.scenarios.map(s=>s.id),'SCENARIO');
  if (JSON.stringify(named.map(r=>r.id).sort()) !== JSON.stringify(expected.sort())) throw new Error('NAMED_REQUIREMENTS_INCOMPLETE');
  const cases = [...Array.from({length:64},(_,i)=>`T${String(i+1).padStart(2,'0')}`),...Array.from({length:8},(_,i)=>`G${String(i+1).padStart(2,'0')}`)];
  if(JSON.stringify(matrix.scenarios.map(s=>s.id).sort())!==JSON.stringify(cases.sort())) throw new Error('SCENARIOS_INCOMPLETE');
  const sections=JSON.parse(readFileSync(new URL('prd-sections.json',import.meta.url),'utf8')).sections.map(s=>'U-'+s.section).sort();
  if(JSON.stringify(matrix.requirements.filter(r=>r.id.startsWith('U-')).map(r=>r.id).sort())!==JSON.stringify(sections)) throw new Error('UNNUMBERED_SECTIONS_INCOMPLETE');
  const workItems = new Set(matrix.workItems.map(w=>w.displayId));
  const evidence = new Map(matrix.evidence.map(e=>[e.id,e])); unique(matrix.evidence.map(e=>e.id),'EVIDENCE');
  // Slice candidates remain traceability only; no candidate can substitute for accepted evidence.
  const candidates = new Map((matrix.evidenceCandidates??[]).map(e=>[e.id,e]));
  unique((matrix.evidenceCandidates??[]).map(e=>e.id),'EVIDENCE_CANDIDATE');
  for(const c of candidates.values()) {
    if(!c.id||!c.path||c.owners?.some(w=>!workItems.has(w))||c.scenarios?.some(s=>!cases.includes(s))) throw new Error('INVALID_EVIDENCE_CANDIDATE');
  }
  for(const row of [...matrix.requirements,...matrix.scenarios]) {
    if(!row.owners?.length || row.owners.some(w=>!workItems.has(w))) throw new Error('INVALID_OWNER');
    if(!['pending','capability_pending','passed','failed','blocked'].includes(row.status)) throw new Error('INVALID_STATUS');
    if(row.scenarios?.some(s=>!cases.includes(s))) throw new Error('INVALID_SCENARIO_REFERENCE');
    if(row.id.startsWith('U-') && (!row.sourceSection || !row.implementationPaths?.length)) throw new Error('UNNUMBERED_TRACE_MISSING');
    for(const id of row.evidenceIds) if(!evidence.has(id)) throw new Error('UNKNOWN_EVIDENCE');
    for(const id of row.evidenceCandidateIds??[]) if(!candidates.has(id)) throw new Error('UNKNOWN_EVIDENCE_CANDIDATE');
    if(row.status==='passed') {
      if(row.scenarios && row.scenarios.some(id=>matrix.scenarios.find(s=>s.id===id)?.status!=='passed')) throw new Error('REQUIREMENT_SCENARIOS_PENDING');
      if(!row.evidenceIds.length) throw new Error('PASS_WITHOUT_EVIDENCE');
      for(const id of row.evidenceIds) {
        const e=evidence.get(id);
        if(e.result!=='passed'||!e.sourceId||!e.timestamp||!/^[a-f0-9]{64}$/.test(e.packageSha256)||!e.report||!e.scenarios.includes(row.id)&&!row.scenarios?.some(s=>e.scenarios.includes(s))) throw new Error('INCOMPLETE_PASS_EVIDENCE');
        if(e.sourceId!==matrix.acceptedSourceId||e.packageSha256!==matrix.acceptedPackageSha256) throw new Error('STALE_EVIDENCE');
        if(['vdsm','synthetic-and-vdsm'].includes(row.environment) && e.target!=='vdsm-native-spk-test') throw new Error('WRONG_ENVIRONMENT');
        if(row.id==='T64' && (e.target!=='human-study'||e.participants!==5)) throw new Error('HUMAN_ACCEPTANCE_REQUIRED');
        if(row.id==='T63' && e.target!=='physical-hardware-matrix') throw new Error('HARDWARE_ACCEPTANCE_REQUIRED');
      }
    }
  }
  if(matrix.uploadStatus!=='complete' && (matrix.sourceInventory.counts!==null||matrix.sourceInventory.referenceSums!==null)) throw new Error('PREMATURE_SOURCE_INVENTORY');
  return {namedRequirements:named.length,unnumberedSections:matrix.requirements.length-named.length,scenarios:cases.length,passed:matrix.scenarios.filter(s=>s.status==='passed').length};
}
if(process.argv[1]===fileURLToPath(import.meta.url)) {
  const matrix=JSON.parse(readFileSync(new URL('matrix.json',import.meta.url),'utf8'));
  console.log(JSON.stringify(validate(matrix)));
  if(process.argv.includes('--release') && matrix.requirements.some(r=>r.priority==='P0'&&r.status!=='passed')) throw new Error('P0_ACCEPTANCE_PENDING');
}
