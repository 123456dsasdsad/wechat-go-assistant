import datetime,importlib.util,pathlib,sys,tempfile,unittest
from unittest.mock import patch
from provider_config import selected_provider_url

spec=importlib.util.spec_from_file_location('runner',pathlib.Path(__file__).with_name('campus-maintenance.py'))
runner=importlib.util.module_from_spec(spec);spec.loader.exec_module(runner)

class CampusMaintenanceTest(unittest.TestCase):
    def test_selected_provider_never_uses_another_providers_url(self):
        text='''model_provider = "chosen"
# base_url = "http://wrong-comment"
[model_providers.other]
base_url = "http://wrong-provider"
[model_providers."chosen"]
base_url = 'http://127.0.0.1:1234/v1' # local tunnel
[misc]
base_url = "http://wrong-table"
'''
        self.assertEqual(selected_provider_url(text),'http://127.0.0.1:1234/v1')
        self.assertEqual(selected_provider_url(text.replace('"chosen"',"'missing'",1)),'')

    def test_release_failure_does_not_skip_account_report(self):
        calls=[]
        with tempfile.TemporaryDirectory() as root, patch.object(runner,'ROOT',pathlib.Path(root)), patch.object(sys,'argv',['runner','morning']), patch.object(runner,'updates',side_effect=RuntimeError('offline')), patch.object(runner,'invoke',side_effect=lambda *args:calls.append(args)):
            with self.assertRaises(RuntimeError):runner.run()
        self.assertIn(('accounts','-if-changed'),calls)

    def test_retry_refreshes_late_cloud_snapshot_without_forcing_resend(self):
        calls=[]
        with tempfile.TemporaryDirectory() as root, patch.object(runner,'ROOT',pathlib.Path(root)), patch.object(sys,'argv',['runner','retry']), patch.object(runner,'invoke',side_effect=lambda *args:calls.append(args)), patch.object(runner,'update_requests',return_value=[]), patch.object(runner,'account_followup_due',return_value=True):
            runner.run()
        self.assertEqual(calls,[('retry-publish',),('accounts','-if-changed')])

    def test_account_followups_wait_until_seven_beijing_on_any_host_timezone(self):
        utc=datetime.timezone.utc
        self.assertFalse(runner.account_followup_due(datetime.datetime(2026,10,8,16,0,tzinfo=utc)))
        self.assertFalse(runner.account_followup_due(datetime.datetime(2026,10,8,22,59,tzinfo=utc)))
        self.assertTrue(runner.account_followup_due(datetime.datetime(2026,10,8,23,0,tzinfo=utc)))
        calls=[]
        with tempfile.TemporaryDirectory() as root, patch.object(runner,'ROOT',pathlib.Path(root)), patch.object(sys,'argv',['runner','retry']), patch.object(runner,'invoke',side_effect=lambda *args:calls.append(args)), patch.object(runner,'update_requests',return_value=[]), patch.object(runner,'account_followup_due',return_value=False):
            runner.run()
        self.assertEqual(calls,[('retry-publish',)])

    def test_requested_update_is_completed_only_after_native_runner_success(self):
        for failed in [False,True]:
            with tempfile.TemporaryDirectory() as root, patch.object(runner,'ROOT',pathlib.Path(root)), patch.object(sys,'argv',['runner','retry']), patch.object(runner,'invoke'), patch.object(runner,'update_requests',return_value=[{'id':'request'}]), patch.object(runner,'updates',side_effect=RuntimeError('offline') if failed else None) as update, patch.object(runner,'complete_update_request') as complete:
                if failed:
                    with self.assertRaises(RuntimeError):runner.run()
                    complete.assert_not_called()
                else:
                    runner.run();complete.assert_called_once_with({'id':'request'})
                update.assert_called_once()

if __name__=='__main__':unittest.main()
