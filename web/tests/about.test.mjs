import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync, mkdirSync, cpSync, writeFileSync, readFileSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {createAboutMetadata} from '../../scripts/about-metadata.mjs';

const root = fileURLToPath(new URL('../../', import.meta.url));
const web = join(root, 'web');
// Synthetic cache fixtures keep metadata tests independent of a machine's Go installation.
function fixture() {
  mkdirSync(join(web, '.test-build'), {recursive:true});
  const dir = mkdtempSync(join(web, '.test-build/about-inputs-'));
  const put = (path, text) => {mkdirSync(join(dir, path, '..'), {recursive:true});writeFileSync(join(dir, path), text);};
  put('web/package.json', JSON.stringify({version:'1.2.3',dependencies:{react:'1.0.0'}}));
  put('web/package-lock.json', JSON.stringify({version:'1.2.3',packages:{'':{version:'1.2.3'},'node_modules/react':{version:'1.0.0'},'node_modules/scheduler':{version:'2.0.0'}}}));
  for(const [name,version] of [['react','1.0.0'],['scheduler','2.0.0']]) {put(`web/node_modules/${name}/package.json`,JSON.stringify({name,version}));put(`web/node_modules/${name}/LICENSE`,'Copyright notice <literal>');}
  put('packaging/INFO','version="1.2.3-4"\n');put('LICENSE','MIT License fixture');
  put('go.mod','module example.org/app\n\ngo 1.27.2\nrequire (\n example.org/lib v1.0.0\n)\n');
  put('cache/example.org/lib@v1.0.0/LICENSE','BSD notice');put('toolchain/LICENSE','Go notice');
  return {dir,put,options:{goCache:join(dir,'cache'),goRoot:join(dir,'toolchain')}};
}

// Source and package versions must agree before any bundle can claim their identity.
test('About metadata binds local versions, complete notices and source identity',()=>{
  const f=fixture();try {
    const data=createAboutMetadata(f.dir,{...f.options,sourceId:`sha256:${'a'.repeat(64)}`});
    assert.equal(data.packageVersion,'1.2.3-4');assert.equal(data.appVersion,'1.2.3');
    assert.equal(data.components.find(x=>x.name==='scheduler').version,'2.0.0');
    assert.equal(data.licenses.length,5);assert.ok(data.licenses.some(x=>x.text==='Copyright notice <literal>'));
    assert.equal(createAboutMetadata(f.dir,f.options).sourceId,null);
    assert.throws(()=>createAboutMetadata(f.dir,{...f.options,sourceId:'/private/source'}),/ABOUT_SOURCE_INVALID/);
    f.put('packaging/INFO','version="1.2.4-4"\n');assert.throws(()=>createAboutMetadata(f.dir,f.options),/ABOUT_VERSION_MISMATCH/);
  }finally{rmSync(f.dir,{recursive:true,force:true});}
});

// Lock drift and missing notices are build failures rather than partial license claims.
test('About metadata rejects installed component drift and missing license text',()=>{
  const f=fixture();try{
    f.put('web/node_modules/react/package.json','{"name":"react","version":"9.0.0"}');
    assert.throws(()=>createAboutMetadata(f.dir,f.options),/ABOUT_COMPONENT_MISMATCH/);
    f.put('web/node_modules/react/package.json','{"name":"react","version":"1.0.0"}');
    f.put('web/node_modules/react/LICENSE','');assert.throws(()=>createAboutMetadata(f.dir,f.options),/ABOUT_LICENSE_INVALID/);
  }finally{rmSync(f.dir,{recursive:true,force:true});}
});

// Compile the actual pure component, then deny fetch while rendering both languages.
test('About panel is bilingual, offline, literal and honest about unknown build/runtime state',()=>{
  const f=fixture();const tmp=mkdtempSync(join(web,'.test-build/about-component-'));const previous=globalThis.fetch;
  try{
    writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');
    for(const name of ['AboutPanel.tsx','AboutData.ts'])cpSync(join(web,'src',name),join(tmp,name));
    execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'AboutPanel.tsx')]);
    const {AboutPanel}=createRequire(import.meta.url)(join(tmp,'out/AboutPanel.js'));
    globalThis.fetch=()=>{throw new Error('NETWORK_FORBIDDEN');};
    for(const language of ['de','en']){
      const html=renderToStaticMarkup(React.createElement(AboutPanel,{language,data:createAboutMetadata(f.dir,f.options)}));
      assert.ok(html.includes(language==='de'?'Über We.Rock.DiskMap':'About We.Rock.DiskMap'));
      assert.ok(html.includes(language==='de'?'Unbekannt':'Unknown'));assert.ok(html.includes('&lt;literal&gt;'));
      assert.ok(html.includes('90080'));assert.ok(!html.includes('<script'));assert.ok(!html.includes('href='));
      assert.ok(html.includes(language==='de'?'keine allgemeine':'not universal'));
      assert.ok(renderToStaticMarkup(React.createElement(AboutPanel,{language,data:null})).includes(language==='de'?'Unbekannt':'Unknown'));
    }
  }finally{globalThis.fetch=previous;rmSync(f.dir,{recursive:true,force:true});rmSync(tmp,{recursive:true,force:true});}
});

// Production authority files stay consistent and the build passes only its freshly generated identity.
test('Repository About inputs and build integration are authoritative and noncircular',()=>{
  const pkg=JSON.parse(readFileSync(join(web,'package.json'),'utf8'));
  const info=readFileSync(join(root,'packaging/INFO'),'utf8');
  assert.ok(info.includes(`version="${pkg.version}-`));
  const script=readFileSync(join(root,'scripts/build.sh'),'utf8');
  assert.ok(script.indexOf('source_manifest.py > dist/source-before.json')<script.indexOf('DISKMAP_BUILD_SOURCE_ID='));
  const manifest=readFileSync(join(root,'scripts/source_manifest.py'),'utf8');
  assert.ok(manifest.includes("'scripts/about-metadata.mjs'"));assert.ok(!manifest.includes("'dist/source-before.json'"));
});
