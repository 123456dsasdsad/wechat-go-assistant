import importlib.util,pathlib,sys,tempfile,unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('runner',pathlib.Path(__file__).with_name('campus-maintenance.py'))
runner=importlib.util.module_from_spec(spec);spec.loader.exec_module(runner)

class CampusMaintenanceTest(unittest.TestCase):
    def test_release_failure_does_not_skip_account_report(self):
        calls=[]
        with tempfile.TemporaryDirectory() as root, patch.object(runner,'ROOT',pathlib.Path(root)), patch.object(sys,'argv',['runner','morning']), patch.object(runner,'updates',side_effect=RuntimeError('offline')), patch.object(runner,'invoke',side_effect=lambda *args:calls.append(args)):
            with self.assertRaises(RuntimeError):runner.run()
        self.assertIn(('accounts',),calls)

    def test_retry_refreshes_late_cloud_snapshot_without_forcing_resend(self):
        calls=[]
        with tempfile.TemporaryDirectory() as root, patch.object(runner,'ROOT',pathlib.Path(root)), patch.object(sys,'argv',['runner','retry']), patch.object(runner,'invoke',side_effect=lambda *args:calls.append(args)):
            runner.run()
        self.assertEqual(calls,[('retry-publish',),('accounts','-if-changed')])

if __name__=='__main__':unittest.main()
