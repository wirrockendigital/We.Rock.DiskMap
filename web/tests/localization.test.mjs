import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync,readdirSync,mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {fileURLToPath} from 'node:url';
import {join,relative} from 'node:path';
import vm from 'node:vm';
const root=fileURLToPath(new URL('../../',import.meta.url));
// Extract balanced literal catalogues without treating braces inside text as structure.
function objectAt(text,start){let depth=0,quote=null,escape=false;for(let i=start;i<text.length;i++){const c=text[i];if(quote){if(escape)escape=false;else if(c==='\\')escape=true;else if(c===quote)quote=null;continue;}if(c==='"'||c==="'"||c==='`'){quote=c;continue;}if(c==='{')depth++;if(c==='}'&&!--depth)return text.slice(start,i+1);}throw new Error('Unclosed catalogue');}
function placeholders(text){return [...text.matchAll(/\{([a-zA-Z][\w.]*)\}/g)].map(x=>x[1]).sort();}
function equalCatalogue(de,en,path){assert.deepEqual(Object.keys(de).sort(),Object.keys(en).sort(),path);for(const key of Object.keys(de)){const a=de[key],b=en[key],label=`${path}.${key}`;assert.equal(typeof a,typeof b,label);if(typeof a==='string'){assert.ok(a.trim()&&b.trim(),label);assert.deepEqual(placeholders(a),placeholders(b),label);}else{assert.ok(a&&b&&typeof a==='object',label);equalCatalogue(a,b,label);}}}
function files(dir){return readdirSync(dir,{withFileTypes:true}).flatMap(e=>e.isDirectory()?files(join(dir,e.name)):[join(dir,e.name)]);}
test('main and inline DE/EN catalogues preserve keys and interpolation slots',()=>{
 equalCatalogue(JSON.parse(readFileSync(join(root,'web/src/locales/de.json'))),JSON.parse(readFileSync(join(root,'web/src/locales/en.json'))),'locales');
 let checked=0;
 for(const path of files(join(root,'web/src')).filter(x=>/\.tsx?$/.test(x))){const text=readFileSync(path,'utf8');for(const match of text.matchAll(/\{\s*de\s*:\s*\{/g)){const literal=objectAt(text,match.index);if(!/\ben\s*:/.test(literal))continue;const value=vm.runInNewContext(`(${literal})`,Object.create(null),{timeout:100});equalCatalogue(value.de,value.en,relative(root,path));checked++;}}
 assert.ok(checked>=15,`Only ${checked} inline catalogues inspected`);
});
// DSM uses its documented language codes; the default remains the English package description.
test('package description offers German and English with the current application version',()=>{const info=readFileSync(join(root,'packaging/INFO'),'utf8');const field=key=>info.match(new RegExp(`^${key}="([^"]+)"$`,'m'))?.[1];assert.equal(field('description'),field('description_enu'));assert.match(field('description_ger'),/DSM-Administratoren/);assert.match(field('description_ger'),/Paketkonto/);assert.match(field('description_enu'),/administrators only/);assert.equal(field('version'),JSON.parse(readFileSync(join(root,'web/package.json'),'utf8')).version+'-1');});

// Render production count text, including the one-entry server remainder that local display groups cannot represent.
test('group count text handles zero singular and plural in DE and EN',async t=>{
 const web=join(root,'web');mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/localization-'));
 try{
  writeFileSync(join(tmp,'package.json'),' {"type":"commonjs"}');cpSync(join(web,'src'),join(tmp,'src'),{recursive:true});
  execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'src/visualization/shared/GroupInspectionPanel.tsx')],{stdio:'pipe'});
  const {GroupInspectionPanel}=createRequire(import.meta.url)(join(tmp,'out/visualization/shared/GroupInspectionPanel.js'));
  for(const [language,expected] of [['de',['0 direkte Elemente','1 direktes Element','2 direkte Elemente']],['en',['0 direct entries','1 direct entry','2 direct entries']]]){
   for(const count of [0,1,2])await t.test(`${language} count ${count}`,()=>{
    const model={snapshotId:'s',rootId:'root',metric:'logical',mode:'highlight',nodes:{group:{id:'group',kind:'group',weight:0n,continuation:{rootId:'root',cursor:'cursor',directChildren:BigInt(count)}}}};
    const html=renderToStaticMarkup(React.createElement(GroupInspectionPanel,{model,target:{kind:'model',nodeId:'group'},identity:'test',language,disabled:false,onClose(){},onSelect(){},onOpen(){},loadPage:async()=>assert.fail('SSR must not request data')}));
    assert.ok(html.includes(expected[count]),`Expected ${expected[count]} in production group count text`);
   });
  }
 }finally{rmSync(tmp,{recursive:true,force:true});}
});
