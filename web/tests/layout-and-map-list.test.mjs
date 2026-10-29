import assert from 'node:assert/strict';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
import {rolldown} from 'rolldown';
// Exercise account separation and map-derived rows with actual production functions.
const dir=await mkdtemp(join(tmpdir(),'diskmap-layout-'));
try{
 for(const name of ['layout-preference','chart-entries']){const b=await rolldown({cwd:new URL('../',import.meta.url).pathname,input:`src/design/${name}.ts`,platform:'node'});await b.write({file:join(dir,name+'.mjs'),format:'esm'});await b.close();}
 const {readSidebars,writeSidebars}=await import(pathToFileURL(join(dir,'layout-preference.mjs')));
 const saved=new Map(),storage={getItem:k=>saved.get(k)??null,setItem:(k,v)=>saved.set(k,v)};
 assert.deepEqual(readSidebars('account-a',storage),{left:true,right:true});writeSidebars('account-a',{left:false,right:true},storage);
 assert.deepEqual(readSidebars('account-a',storage),{left:false,right:true});assert.deepEqual(readSidebars('account-b',storage),{left:true,right:true});
 writeSidebars('account-b',{left:true,right:false},storage);assert.deepEqual(readSidebars('account-a',storage),{left:false,right:true});assert.deepEqual(readSidebars('account-b',storage),{left:true,right:false});
 assert.throws(()=>writeSidebars('',{left:true,right:true},storage));assert.throws(()=>writeSidebars('account-a',{left:'invalid',right:true},storage));
 const {chartEntries}=await import(pathToFileURL(join(dir,'chart-entries.mjs')));
 const movie={id:'movie',name:'Movie.mov',kind:'file',bytesExact:'9300000000'},song={id:'song',name:'Song.wav',kind:'file',bytesExact:'60000000'};
 const root={id:'root',name:'volume1',kind:'volume',bytesExact:'9400000000',children:[{id:'folder',name:'Media',kind:'folder',bytesExact:'9360000000',children:[movie,song]}]};
 const rows=chartEntries(root,'de');assert(rows.some(n=>n.id==='movie'));assert(rows.some(n=>n.id==='song'));assert.equal(rows.reduce((sum,n)=>sum+BigInt(n.bytesExact),0n),9400000000n);assert.equal(rows.find(n=>n.id==='root').bytesExact,'40000000');
 console.log('PASS: account-isolated layout reload, invalid values, nested movie/audio rows and exact residual total');
}finally{await rm(dir,{recursive:true,force:true});}
