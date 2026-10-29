"""Exercise the real uninstall hook and native wizard contract without NAS actions."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT/'scripts'))
spec = importlib.util.spec_from_file_location('package_spk', ROOT/'scripts/package-spk.py')
package = importlib.util.module_from_spec(spec)
spec.loader.exec_module(package)


class UninstallTests(unittest.TestCase):
    """Silent/upgrade paths cannot accidentally inherit a destructive choice."""
    def test_hook_choices(self):
        with tempfile.TemporaryDirectory() as temp:
            base = Path(temp)
            (base/'bin').mkdir()
            helper = base/'bin/diskmapd'
            helper.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$RECORD"\nexit "${FAIL:-0}"\n')
            helper.chmod(0o700)
            for status, value, expected, called in [('UNINSTALL', None, 0, False), ('UNINSTALL', 'false', 0, False), ('UPGRADE', 'true', 0, False), ('UNINSTALL', 'true', 0, True), ('UNINSTALL', 'yes', 1, False)]:
                record = base/'record'
                record.unlink(missing_ok=True)
                env = dict(os.environ, SYNOPKG_PKG_STATUS=status, SYNOPKG_PKGVAR=str(base/'state'), SYNOPKG_PKGDEST=temp, RECORD=str(record))
                env.pop('diskmap_remove_data', None)
                if value is not None:
                    env['diskmap_remove_data'] = value
                result = subprocess.run(['sh', str(ROOT/'packaging/scripts/preuninst')], env=env, capture_output=True)
                self.assertEqual(result.returncode, expected)
                self.assertEqual(record.exists(), called)
                if called:
                    self.assertEqual(record.read_text().splitlines(), [f'--socket={base}/state/run/control.sock', '--uninstall-data'])
                    env['FAIL'] = '1'
                    self.assertEqual(subprocess.run(['sh', str(ROOT/'packaging/scripts/preuninst')], env=env, capture_output=True).returncode, 1)

    def test_native_wizard_module(self):
        name, raw, mode = package.uninstall_wizard(ROOT/'packaging')
        self.assertEqual((name, mode), ('WIZARD_UIFILES/uninstall_uifile', 0o644))
        self.assertEqual(raw, package.uninstall_wizard(ROOT/'packaging')[1])
        ui = json.loads(raw)[0]
        self.assertEqual(ui['custom_render_name'], 'diskmap_remove')
        # Evaluate the actual JSONP export with DSM hook doubles, not an invented second UI.
        program = r'''
const vm=require('node:vm');let loaded;
const sandbox={Vue:{ref:v=>({value:v})},_S:()=>process.argv[1],SYNO:{SDS:{PkgManApp:{Custom:{useHook:Object.assign(()=>({getNext:()=>'',checkState:()=>{}}),{props:{}}),JsonpLoader:{load:v=>loaded=v}}}}}};
vm.runInNewContext(process.argv[2],sandbox);const component=loaded.default.render;const values=component.setup({getOwner(){return {}}});
if(!values.headline)throw Error('missing title');
const receipts=[];
for(const selected of [false,true]){
  values.remove.value=selected;
  // Reproduce the observed named-field merge and doubly JSON-encoded DSM parameter.
  const merged=Object.assign({},values.getValues());
  const encoded=JSON.stringify(JSON.stringify(merged));
  const decoded=JSON.parse(JSON.parse(encoded));
  if(Object.keys(decoded).join(',')!=='diskmap_remove_data'||decoded.diskmap_remove_data!==selected)throw Error('incorrect named choice');
  receipts.push({selected,encoded,environment:{diskmap_remove_data:String(decoded.diskmap_remove_data)}});
}
process.stdout.write(JSON.stringify(receipts));
'''
        for language in ['ger', 'enu']:
            result = subprocess.run(['node', '-e', program, language, ui['custom_render_fn']], check=True, capture_output=True, text=True)
            receipts = json.loads(result.stdout)
            self.assertEqual([r['selected'] for r in receipts], [False, True])
            # Model only the named environment transfer; native DSM transfer remains a separate gate.
            with tempfile.TemporaryDirectory() as temp:
                base = Path(temp)
                (base/'bin').mkdir()
                helper = base/'bin/diskmapd'
                helper.write_text('#!/bin/sh\nprintf "%s" "$diskmap_remove_data" > "$RECORD"\n')
                helper.chmod(0o700)
                for receipt in receipts:
                    decoded = json.loads(json.loads(receipt['encoded']))
                    self.assertEqual(decoded, {'diskmap_remove_data': receipt['selected']})
                    record = base/'choice'
                    record.unlink(missing_ok=True)
                    env = dict(os.environ, SYNOPKG_PKG_STATUS='UNINSTALL', SYNOPKG_PKGVAR=str(base/'state'), SYNOPKG_PKGDEST=temp, RECORD=str(record))
                    env.update(receipt['environment'])
                    hook = subprocess.run(['sh', str(ROOT/'packaging/scripts/preuninst')], env=env, capture_output=True)
                    self.assertEqual(hook.returncode, 0)
                    self.assertEqual(record.exists(), receipt['selected'])
                    if receipt['selected']:
                        self.assertEqual(record.read_text(), 'true')
