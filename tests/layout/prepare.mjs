import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { writeFile } from 'node:fs/promises';
import assert from 'node:assert/strict';
import { BoundedHierarchy, totals } from '../../web/src/layout/model.ts';
import references from '../scale/reference-manifests.json' with { type:'json' };

// Aggregate the real scale generator stream outside the browser with fixed retained state.
const executable=process.env.SCALE_GENERATOR || '/tmp/diskmap-scalefixtures';
const prepared=[];
for(const shape of ['wide','deep','skewed']) {
 const child=spawn(executable,['-count','10000000','-shape',shape,'-seed','42'],{stdio:['ignore','pipe','pipe']});
 let error='';child.stderr.on('data',data=>error+=data);
 const completion=new Promise((resolve,reject)=>{child.on('error',reject);child.on('exit',code=>code===0?resolve():reject(new Error(error)))});
 const models=[new BoundedHierarchy(1400),new BoundedHierarchy(9400)];let records=0;
 for await(const line of createInterface({input:child.stdout,crlfDelay:Infinity})){const n=JSON.parse(line);models.forEach(m=>m.push(n));records++;}
 await completion;assert.equal(records,10000000);
 const trees=models.map(m=>m.finish());const a=totals(trees[0]),b=totals(trees[1]);
 const expected=references.find(m=>m.config.shape===shape&&m.count===10000000);
 assert.equal(a.knownBytes,expected.knownSum);assert.equal(b.knownBytes,expected.knownSum);assert.equal(a.unknownCount,expected.unknown);assert.equal(a.memberCount,b.memberCount);
 assert(a.nodes<=2000);assert(b.nodes<=10000);prepared.push({shape,trees,summary:a});
 console.log(JSON.stringify({shape,records,radialNodes:a.nodes,treemapNodes:b.nodes,sum:a.knownBytes,rss:process.memoryUsage().rss}));
}
await writeFile('/tmp/diskmap-layout-prepared.json',JSON.stringify(prepared));
