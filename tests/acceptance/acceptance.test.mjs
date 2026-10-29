import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { validate } from './validate.mjs';
import { fixture, manifest } from './fixtures.mjs';
const load=()=>JSON.parse(readFileSync(new URL('matrix.json',import.meta.url),'utf8'));

// Deliberately invalid claims must not turn a complete mapping into release acceptance.
test('complete matrix and rejected omitted, invalid-owner and evidence-free cases',()=>{
  assert.equal(validate(load()).namedRequirements,47);
  for(const mutate of [m=>m.requirements.pop(),m=>m.scenarios.pop(),m=>m.scenarios[0].owners=['WI-999'],m=>m.scenarios[0].status='passed']) {
    const data=load();mutate(data);
    assert.throws(()=>validate(data));
  }
  const data=load();data.requirements=data.requirements.filter(r=>r.id!=='SEC-01');assert.throws(()=>validate(data),/INCOMPLETE/);
});
test('synthetic fixture is deterministic and agrees with manually calculated oracle',()=>{
  const value=fixture();assert.deepEqual(manifest(),manifest());
  // Six file references: 13 + 7 + 9007199254740993 + 13 + 0, and one unknown.
  assert.equal(manifest().knownSum,'9007199254741026');assert.equal(manifest().files,6);assert.equal(manifest().unknown,1);assert.equal(manifest().zero,1);
  assert.equal(value.nodes.filter(n=>n.id.startsWith('v')&&n.shareId).length,4);
  assert.equal(value.nodes.filter(n=>n.linkGroupId).length,2);
  assert.ok(!JSON.stringify(value).includes('/volume'));
  const changed=structuredClone(value);changed.nodes.find(n=>n.id==='direct').logicalBytes='14';assert.notEqual(manifest(changed).sha256,manifest().sha256);
});
test('upload-dependent inventory cannot be fabricated',()=>{
  // Set the incomplete-upload precondition independently of the recorded live inventory.
  const data=load();data.uploadStatus='in_progress';data.sourceInventory.counts=[0,0,0,0];assert.throws(()=>validate(data),/PREMATURE_SOURCE_INVENTORY/);
});

// Artifact-bound evidence is accepted only for the exact current source and environment.
test('stale and synthetic evidence cannot satisfy a vDSM scenario',()=>{
  const data=load();const row=data.scenarios.find(s=>s.id==='T02');
  data.acceptedSourceId='test-source';data.acceptedPackageSha256='a'.repeat(64);
  data.evidence=[{id:'test-only',sourceId:'test-source',packageSha256:'a'.repeat(64),timestamp:'2026-10-09T00:00:00Z',target:'vdsm-native-spk-test',report:'test-only-report',scenarios:['T02'],result:'passed'}];
  row.status='passed';row.evidenceIds=['test-only'];assert.equal(validate(data).passed,1);
  data.evidence[0].sourceId='older-source';assert.throws(()=>validate(data),/STALE_EVIDENCE/);
  data.evidence[0].sourceId='test-source';data.evidence[0].target='synthetic';assert.throws(()=>validate(data),/WRONG_ENVIRONMENT/);
});

// Historical slices and local candidates cannot promote unfinished final-artifact scenarios.
test('candidate links remain bounded traceability rather than acceptance',()=>{
  const data=load();assert.ok(data.workItems.some(w=>w.displayId==='WI-138'));
  assert.equal(validate(data).passed,0);
  const row=data.scenarios.find(s=>s.id==='T44');assert.ok(row.evidenceCandidateIds.includes('wi054-native-d37ffee'));
  row.evidenceCandidateIds.push('missing-candidate');assert.throws(()=>validate(data),/UNKNOWN_EVIDENCE_CANDIDATE/);
  row.evidenceCandidateIds.pop();row.status='passed';assert.throws(()=>validate(data),/PASS_WITHOUT_EVIDENCE/);
});

// Consolidation records real dependency identities while leaving every acceptance gate closed.
test('owner inventory through WI160 retains bounded traceability and historical-only evidence',()=>{
  const data=load(),ids=new Set(data.workItems.map(w=>w.id));
  assert.equal(data.workItems.length,160);assert.equal(ids.size,160);
  for(let n=1;n<=160;n++)assert.ok(data.workItems.some(w=>w.displayId===`WI-${String(n).padStart(3,'0')}`));
  for(const owner of data.workItems){assert.ok(Number.isInteger(owner.revision)&&owner.revision>0);assert.ok(Array.isArray(owner.dependsOnWorkItemIds));for(const dependency of owner.dependsOnWorkItemIds)assert.ok(ids.has(dependency));}
  // Every pending case has an explicit later acceptance owner and report contract, not fabricated evidence.
  for(const scenario of data.scenarios){const a=scenario.evidenceAssignment;assert.equal(a.status,'pending');assert.ok(data.workItems.some(w=>w.displayId===a.owner&&w.id===a.workItemId));assert.equal(a.expected,scenario.expected);assert.equal(a.environment,scenario.environment);assert.ok(a.task.includes(scenario.id));assert.equal(a.reportDestination,`docs/testing/acceptance-evidence-2026-10-10.md#${scenario.id.toLowerCase()}-acceptance`);assert.equal(a.destinationState,'planned-section-not-yet-evidence');for(const field of ['inputs','expected','actual','sourceId','packageSha256','target','limitations'])assert.ok(a.requiredReceiptFields.includes(field));}
  assert.equal(data.evidence.length,0);assert.equal(data.acceptedSourceId,undefined);assert.equal(data.acceptedPackageSha256,undefined);
  assert.ok(data.scenarios.every(s=>s.status==='pending'));assert.equal(validate(data).scenarios,72);
  const diagnostic=data.evidenceCandidates.find(c=>c.id==='wi106-diagnostic-native-8034ea47');
  assert.equal(diagnostic.packageSha256,'8034ea47b647c214ca7d7dc2e083b13641fb869770f155bfa93c770c3e82bdd8');
  assert.match(diagnostic.scope,/no separate DE byte comparison/);
  assert.equal(data.evidenceCandidates.find(c=>c.id==='wi107-local-notices-candidate').packageSha256,undefined);
  assert.ok(!data.scenarios.find(s=>s.id==='T63').owners.includes('WI-159'));
});
