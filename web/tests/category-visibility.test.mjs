import {test} from 'node:test';
import assert from 'node:assert/strict';
import {categoryVisible} from '../src/design/category-visibility.ts';
// Names and ancestor paths must follow the same category boundary as map workers.
test('category visibility respects exact names, ancestors and independent switches',()=>{
 for(const name of ['#recycle','@eaDir','.DS_Store','.SMB-cache','._photo','.AppleDouble','.Spotlight-V100','.Trashes','.fseventsd']){
  assert.equal(categoryVisible({name}),false,name);
  assert.equal(categoryVisible({name:'child',path:`homes/eric/${name}/child`}),false,name);
  assert.equal(categoryVisible({name:'child'},{},`homes/eric/${name}`),false,name);
  assert.equal(categoryVisible({name},{showRecycle:true,showSystem:true}),true,name);
 }
 assert.equal(categoryVisible({name:'file',path:'#recycle/@eaDir/file'},{showRecycle:true}),false);
 assert.equal(categoryVisible({name:'#recycle'},{showSystem:true}),false);
 for(const name of ['recycle','#recycle-old','@eaDir-other','.DS_Store.backup','Photos','.codex'])assert.equal(categoryVisible({name}),true,name);
});
