import {test} from 'node:test';
import assert from 'node:assert/strict';
import {recycleStateForScope} from '../src/design/recycle-state.ts';
// Presence cannot be inferred from a missing index or an incomplete set of share observations.
test('recycle state is share-bound and aggregates only qualified evidence',()=>{
 const inventory={shares:[{id:'a',volumeId:'v1',recycleState:'readable'},{id:'b',volumeId:'v1',recycleState:'missing'},{id:'c',volumeId:'v2',recycleState:'denied'}]};
 assert.equal(recycleStateForScope({kind:'folder',targetIds:['a'],relativePath:'nested'},inventory),'readable');
 assert.equal(recycleStateForScope({kind:'shares',targetIds:['b']},inventory),'missing');
 assert.equal(recycleStateForScope({kind:'volumes',targetIds:['v1']},inventory),'readable');
 assert.equal(recycleStateForScope({kind:'nas',targetIds:[]},inventory),'denied');
 assert.equal(recycleStateForScope({kind:'shares',targetIds:['unknown']},inventory),'unknown');
 assert.equal(recycleStateForScope({kind:'nas',targetIds:[]},{shares:[...inventory.shares.slice(0,2),{id:'x'}]}),'unknown');
 assert.equal(recycleStateForScope(undefined,inventory),'unknown');
});
