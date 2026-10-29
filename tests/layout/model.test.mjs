import test from 'node:test';
import assert from 'node:assert/strict';
import {BoundedHierarchy,totals} from '../../web/src/layout/model.ts';

// Aggregates must stay under the correct parent while retaining exact sum and unknown count.
test('parent-local aggregation conserves values and membership',()=>{
 const m=new BoundedHierarchy(0);
 [{id:'0',parentId:null,kind:'directory',logicalBytes:'0'},{id:'1',parentId:'0',kind:'directory',logicalBytes:'0'},
 {id:'2',parentId:'0',kind:'file',logicalBytes:'9007199254740993'},
 {id:'3',parentId:'1',kind:'file',logicalBytes:null},{id:'4',parentId:'1',kind:'file',logicalBytes:'0'}].forEach(n=>m.push(n));
 const root=m.finish();assert.deepEqual(totals(root),{knownBytes:'9007199254740993',unknownCount:1,memberCount:3,nodes:4});
 assert.equal(root.children[0].children[0].parentId,'1');assert.equal(root.children[1].parentId,'0');assert(!('relativePath' in root.children[1]));
});
// A missing parent, unsupported record or directory explosion fails before rendering.
test('invalid inputs and unbounded directories fail closed',()=>{
 assert.throws(()=>new BoundedHierarchy(-1),/BUDGET/);
 const m=new BoundedHierarchy(0,1);m.push({id:'0',parentId:null,kind:'directory',logicalBytes:'0'});
 assert.throws(()=>m.push({id:'1',parentId:'0',kind:'directory',logicalBytes:'0'}),/DIRECTORY_BUDGET/);
 const foreign=new BoundedHierarchy(0);assert.throws(()=>foreign.push({id:'0',parentId:'x',kind:'file',logicalBytes:'1'}),/PARENT_MISSING/);
});
