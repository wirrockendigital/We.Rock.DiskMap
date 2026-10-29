import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
// Compile the production pure functions and preserve exact source bytes through unit changes.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/sizes-'));let sizes;
try{writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');cpSync(join(web,'src/size-metrics.ts'),join(tmp,'size-metrics.ts'));execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'size-metrics.ts')]);sizes=createRequire(import.meta.url)(join(tmp,'out/size-metrics.js'));}finally{rmSync(tmp,{recursive:true,force:true});}
// Arbitrary precision survives every supported decimal and binary unit, including rounding boundaries.
test('size unit roundtrip above 2^53 without floating point',()=>{for(const bytes of ['0','1','999999999','1000000000','1073741824','9007199254740993','1'.repeat(128)])for(const unit of ['B','GB','TB','GiB','TiB'])assert.equal(sizes.unitDecimalBytes(sizes.byteUnitDecimal(bytes,unit),unit),bytes);assert.equal(sizes.byteUnitDecimal('1073741824','GiB'),'1');assert.equal(sizes.byteUnitDecimal('1000000000','GB'),'1');});
// A rounded display keeps its original byte payload and never serves as input to subsequent conversion.
test('size display rounding and unknown remain explicit',()=>{assert.deepEqual(sizes.formatByteSize('9007199254740993','GB',2),{label:'9007199.25 GB',exactBytes:'9007199254740993',unit:'GB'});assert.equal(sizes.formatByteSize('999999999','GB',2).label,'1.00 GB');assert.equal(sizes.formatByteSize(null,'GiB').label,null);assert.equal(sizes.byteUnitDecimal(null,'GB'),null);assert.equal(sizes.formatByteSize('0','B',0).label,'0 B');});
// Invalid wire inputs, units and fractional-byte values are rejected rather than silently coerced.
test('size validation rejects lossy and noncanonical inputs',()=>{for(const value of [9007199254740992,-1,'-1','01','1e3','1.1',''])assert.throws(()=>sizes.exactBytes(value));assert.throws(()=>sizes.unitDecimalBytes('0.0000000001','GB'));assert.throws(()=>sizes.formatByteSize('1','MB'));assert.throws(()=>sizes.formatByteSize('1','GB',7));assert.throws(()=>sizes.unitDecimalBytes(' 1','GiB'));});
