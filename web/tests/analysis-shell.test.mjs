import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,cpSync,writeFileSync,rmSync,readFileSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import React from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
// Compile the real shell, leaving application transport and authentication outside this presentation test.
const web=fileURLToPath(new URL('../',import.meta.url));mkdirSync(join(web,'.test-build'),{recursive:true});const tmp=mkdtempSync(join(web,'.test-build/shell-'));let shell;
try {
 writeFileSync(join(tmp,'package.json'),'{"type":"commonjs"}');
 for(const file of ['AnalysisShell.tsx','contracts.ts'])cpSync(join(web,'src',file),join(tmp,file));
 cpSync(join(web,'src/locales'),join(tmp,'locales'),{recursive:true});
 execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','Node16','--moduleResolution','Node16','--jsx','react-jsx','--esModuleInterop','--resolveJsonModule','--skipLibCheck','--outDir',join(tmp,'out'),join(tmp,'AnalysisShell.tsx')],{stdio:'pipe'});
 shell=createRequire(import.meta.url)(join(tmp,'out/AnalysisShell.js'));
}finally{rmSync(tmp,{recursive:true,force:true});}
const messages=language=>JSON.parse(readFileSync(join(web,'src/locales',language+'.json'),'utf8'));
// A missing remembered ID never becomes a synthetic profile or an instruction to scan.
test('catalogue validates remembered profile and falls back to a real active entry',()=>{
 const profiles=[{id:'disabled',active:false},{id:'current',active:true},{id:'other',active:true}];
 assert.equal(shell.resolveProfileSelection(profiles,'other'),'other');
 assert.equal(shell.resolveProfileSelection(profiles,'disabled'),'disabled');
 assert.equal(shell.resolveProfileSelection(profiles,'deleted'),'current');
 assert.equal(shell.resolveProfileSelection(profiles,''),'current');
 assert.equal(shell.resolveProfileSelection([], 'deleted'),'');
 assert.equal(shell.resolveProfileSelection([{id:'inactive-only',active:false}],''),'inactive-only');
});
// Each visible navigation action selects exactly one area and leaves the child owner unchanged.
test('four actual navigation choices preserve child identity and expose contextual help',()=>{
 for(const language of ['de','en'])for(const area of ['overview','profiles','status','settings']){
  const choices=[],child=React.createElement('div',{'data-reader':'unchanged'});const m=messages(language);
  const tree=shell.AnalysisShell({area,onArea:value=>choices.push(value),picker:React.createElement('span',null,'<private & long profile>'),messages:m,children:child});
  assert.equal(tree.props.children[1].props.className,'workspace-content');
  assert.equal(tree.props.children[1].props.children,child);
  const sidebar=tree.props.children[0];assert.equal(sidebar.props.className,'workspace-sidebar');
  const buttons=sidebar.props.children[0].props.children;assert.equal(buttons.length,4);
  assert.equal(sidebar.props.children[1].props.children[1].props.open,undefined);
  assert.equal(buttons.filter(button=>button.props['aria-current']==='page').length,1);
  for(const button of buttons)button.props.onClick();assert.deepEqual(choices,['overview','profiles','status','settings']);
  const html=renderToStaticMarkup(tree);assert.ok(html.includes(m['shell.help.'+area]));assert.ok(html.includes('&lt;private &amp; long profile&gt;'));
  assert.doesNotMatch(html,/<canvas|data-demo|disabled=""/);
  assert.equal((html.match(/<svg[^>]+aria-hidden="true"/g)??[]).length,4);
 }
});
// Both controls collapse and restore their own regions without removing the centre content.
test('side panels collapse independently and keep the primary analysis mounted',()=>{
 const old=React.useState,states=[];let i=0;
 React.useState=initial=>{const n=i++;if(!(n in states))states[n]=initial;return[states[n],value=>{states[n]=typeof value==='function'?value(states[n]):value;}];};
 try{
  const centre=React.createElement('p',null,'recorded entries');
  const render=()=>{i=0;return shell.AnalysisWorkspace({messages:messages('de'),orientation:'scope',details:'details',children:centre});};
  // Exercise both initially folded regions and every toggle without replacing the centre.
  let tree;
  const check=(treeOpen,detailsOpen)=>{
   tree=render();const controls=tree.props.children[0].props.children,columns=tree.props.children[1].props.children;
   assert.equal(controls[0].props['aria-expanded'],treeOpen);assert.equal(controls[1].props['aria-expanded'],detailsOpen);
   assert.equal(columns[0].props.hidden,!treeOpen);assert.equal(columns[2].props.hidden,!detailsOpen);
   assert.equal(columns[1].props.children,centre);
   assert.equal(controls[0].props['aria-controls'],columns[0].props.id);assert.equal(controls[1].props['aria-controls'],columns[2].props.id);
  };
  const toggle=index=>tree.props.children[0].props.children[index].props.onClick();
  check(false,false);toggle(0);check(true,false);toggle(1);check(true,true);
  toggle(0);check(false,true);toggle(1);check(false,false);
  toggle(1);check(false,true);toggle(0);check(true,true);
 }finally{React.useState=old;}
});
// Layout rules keep long labels and bounded tables from widening the entire DSM window.
test('desktop shell has bounded columns and long-text overflow rules',()=>{
 const css=readFileSync(join(web,'src/style.css'),'utf8');
 assert.match(css,/\.analysis-shell \[hidden\][^}]+display: none !important/);
 assert.match(css,/\.analysis-columns\.with-tree\.with-details[^}]+minmax\(0,1fr\)/);
 assert.match(css,/\.analysis-main \{ min-width: 0/);
 assert.match(css,/\.analysis-source select[^}]+max-width: 100%/);
 assert.match(css,/\.analysis-shell button[^}]+overflow-wrap: anywhere/);
});
