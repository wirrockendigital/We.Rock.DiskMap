import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// Execute the shipped launcher in an isolated DSM component harness, not a second implementation.
test('native launcher composes one resizable DSM window with a local frame and no network side effect', () => {
  const source=readFileSync(new URL('../../packaging/ui/DiskMap.js',import.meta.url),'utf8');
  const context={SYNO:{SDS:{App:{}},namespace:name=>{assert.equal(name,'SYNO.SDS.App.DiskMap');context.SYNO.SDS.App.DiskMap={};}},Vue:{extend:value=>value}};
  vm.runInNewContext(source,context,{timeout:1000});
  // Resolve the main registered class explicitly, independently of auxiliary windows.
  const component=context.SYNO.SDS.App.DiskMap.Instance;
  const render=()=>component.render((tag,attributes,children)=>({tag,attributes,children}));
  for(let reopened=0;reopened<2;reopened++) {
    const instance=render();assert.equal(instance.tag,'v-app-instance');
    const window=instance.children[0];assert.equal(window.tag,'v-app-window');
    assert.equal(window.attributes.props.resizable,true);
    assert.ok(window.attributes.props.width>=640);assert.ok(window.attributes.props.height>=480);
    const frame=window.children[0];assert.equal(frame.tag,'iframe');
    assert.equal(frame.attributes.attrs.src,'/webman/3rdparty/we-rock-diskmap/index.html');
    assert.equal(frame.attributes.attrs.title,'We.Rock.DiskMap');
    assert.equal(frame.attributes.style.width,'100%');assert.equal(frame.attributes.style.height,'100%');
    assert.equal(window.attributes.on,undefined);
  }
  // No lifecycle callback sends cancel/stop requests when DSM destroys its window.
  assert.deepEqual(Object.keys(component),['render']);
});

// The actual title-bar callback launches one local help app, using DSM's language.
test('native question mark opens bundled help with no online catalogue dependency', () => {
  const source=readFileSync(new URL('../../packaging/ui/DiskMap.js',import.meta.url),'utf8');
  let language='ger';const launched=[];
  const context={SYNO:{SDS:{App:{},Utils:{getHelpLanguage:()=>language},AppLaunch:id=>launched.push(id)},namespace:()=>{context.SYNO.SDS.App.DiskMap={};}},Vue:{extend:value=>value}};
  vm.runInNewContext(source,context,{timeout:1000});
  const h=(tag,attributes,children)=>({tag,attributes,children});
  const main=context.SYNO.SDS.App.DiskMap.Instance.render(h);
  assert.equal(launched.length,0);
  main.children[0].attributes.props.onHelp();
  assert.deepEqual(launched,['SYNO.SDS.App.DiskMap.HelpInstance']);
  for(const [requested,bundled] of [['ger','ger'],['enu','enu'],['fre','enu']]){
    language=requested;
    const help=context.SYNO.SDS.App.DiskMap.HelpInstance.render(h);
    assert.equal(help.attributes.props.className,launched[0]);
    const window=help.children[0];assert.equal(window.attributes.props.showHelp,false);
    assert.equal(window.attributes.props.resizable,true);
    assert.equal(window.children[0].attributes.attrs.src,`/webman/3rdparty/we-rock-diskmap/help/${bundled}/index.html`);
  }
  const config=JSON.parse(readFileSync(new URL('../../packaging/ui/config',import.meta.url),'utf8'));
  const help=config['DiskMap.js'][launched[0]];
  assert.equal(help.hidden,true);assert.equal(help.allowMultiInstance,false);assert.equal(help.allUsers,false);
});

// Registration permissions complement server authorization and never replace the CGI session guard.
test('DSM registration is admin-only, single-instance and has no extra port or external URL', () => {
  const config=JSON.parse(readFileSync(new URL('../../packaging/ui/config',import.meta.url),'utf8'));
  const entry=config['DiskMap.js']['SYNO.SDS.App.DiskMap.Instance'];
  assert.equal(entry.type,'app');assert.equal(entry.allUsers,false);assert.equal(entry.allowMultiInstance,false);
  assert.equal(entry.hidden,false);assert.equal(entry.appWindow,'SYNO.SDS.App.DiskMap.Instance');
  assert.equal(entry.port,undefined);assert.equal(entry.url,undefined);
});
