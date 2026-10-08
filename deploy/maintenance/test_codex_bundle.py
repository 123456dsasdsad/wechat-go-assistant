import io,json,pathlib,tarfile,tempfile,unittest
from codex_bundle import stage_bundle,switch_bundle,restore_bundle

CLI=b'''#!/bin/sh
if [ "$1" = "--version" ]; then
  test -f "$(dirname "$0")/codex-code-mode-host" || exit 2
  echo 'codex-cli 0.161.0'
else
  mkdir -p "$4"
  echo '{"methods":["turn/steer","thread/resume","turn/start"]}' > "$4/schema.json"
fi
'''

class CodexBundleTest(unittest.TestCase):
    def archive(self,root,complete=True):
        path=root/'package.tar.gz'
        with tarfile.open(path,'w:gz') as tf:
            values={'bin/codex':CLI,'codex-package.json':json.dumps({'layoutVersion':1,'version':'0.161.0','entrypoint':'bin/codex','variant':'codex'}).encode(),'codex-resources/bwrap':b'new resource'}
            if complete:values['bin/codex-code-mode-host']=b'new host'
            for name,data in values.items():
                member=tarfile.TarInfo(name);member.size=len(data);member.mode=0o700;tf.addfile(member,io.BytesIO(data))
        return path

    def test_bundle_installs_matching_helpers_and_can_restore_all_old_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=pathlib.Path(tmp);live=root/'live';live.mkdir();target=live/'codex';target.write_text('old cli');(live/'codex-code-mode-host').write_text('old host');(live/'operator-note').write_text('keep')
            staged=stage_bundle(self.archive(root),target,root,'rust-v0.161.0')
            self.assertEqual(target.read_text(),'old cli')
            backup=switch_bundle(staged,target)
            self.assertEqual((live/'codex-code-mode-host').read_text(),'new host');self.assertEqual((live/'operator-note').read_text(),'keep')
            self.assertEqual((live/'codex-resources/bwrap').read_text(),'new resource');self.assertTrue(target.is_symlink())
            restore_bundle(target,backup)
            self.assertEqual(target.read_text(),'old cli');self.assertEqual((live/'codex-code-mode-host').read_text(),'old host')

    def test_incomplete_package_never_changes_the_live_cli(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=pathlib.Path(tmp);live=root/'live';live.mkdir();target=live/'codex';target.write_text('old cli')
            with self.assertRaises(RuntimeError):stage_bundle(self.archive(root,False),target,root,'rust-v0.161.0')
            self.assertEqual(target.read_text(),'old cli')

    def test_archive_paths_cannot_escape_the_staging_directory(self):
        for name in ['bin/../../outside','/outside']:
            with self.subTest(name=name),tempfile.TemporaryDirectory() as tmp:
                root=pathlib.Path(tmp);live=root/'live';live.mkdir();target=live/'codex';target.write_text('old cli');archive=root/'bad.tar.gz'
                with tarfile.open(archive,'w:gz') as tf:
                    member=tarfile.TarInfo(name);member.size=3;tf.addfile(member,io.BytesIO(b'bad'))
                with self.assertRaises(RuntimeError):stage_bundle(archive,target,root,'rust-v0.161.0')
                self.assertFalse((root/'outside').exists());self.assertEqual(target.read_text(),'old cli')

if __name__=='__main__':unittest.main()
