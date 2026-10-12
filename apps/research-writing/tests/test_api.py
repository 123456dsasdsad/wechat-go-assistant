import json
import sqlite3
import sys
import tempfile
import threading
import unittest
import urllib.request
import urllib.error
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import app
import repository as repo
from bridge import validate_report, tick

class APITest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        repo.DB = Path(self.tmp.name) / 'research.db'
        repo.initialize()
        self.server = app.ThreadingHTTPServer(('127.0.0.1', 0), app.Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.base = 'http://127.0.0.1:' + str(self.server.server_port) + '/api/'
        self.pid = self.call('projects', {'title': 'integration-test', 'field': 'test'})[1]['id']

    def tearDown(self):
        self.server.shutdown(); self.server.server_close(); self.tmp.cleanup()

    def call(self, path, body=None):
        req = urllib.request.Request(self.base + path, data=json.dumps(body).encode() if body is not None else None, headers={'Content-Type': 'application/json'})
        try:
            with urllib.request.urlopen(req) as r:
                return r.status, json.load(r)
        except urllib.error.HTTPError as e:
            return e.code, json.load(e)

    def test_conflict_preserves_both_versions_and_retry_is_idempotent(self):
        path = 'projects/' + self.pid + '/drafts'
        first = {'section': 'method', 'content': 'device A text', 'revision': 0, 'request_id': 'first'}
        self.assertEqual(self.call(path, first)[0], 200)
        self.assertEqual(self.call(path, first)[0], 200)
        stale = {**first, 'content': 'device B stale text', 'request_id': 'second'}
        code, result = self.call(path, stale)
        self.assertEqual(code, 409); self.assertEqual(result['current']['content'], 'device A text')
        stale['revision'] = 1
        self.assertEqual(self.call(path, stale)[0], 200)
        data = self.call('projects/' + self.pid)[1]
        self.assertEqual(len(data['draft_versions']), 2)
        self.assertEqual(data['drafts'][0]['revision'], 2)
        self.assertEqual(data['draft_versions'][1]['content'], 'device A text')

    def test_record_edit_rule_review_resolution_and_archive_restore(self):
        root = 'projects/' + self.pid + '/'
        rid = self.call(root + 'reading_cards', {'title': 'paper', 'method': 'old'})[1]['id']
        self.call(root + 'reading_cards', {'id': rid, 'title': 'paper', 'method': 'new'})
        report = self.call(root + 'chapter-review', {'section': 'setup', 'content': '', 'revision': 0, 'request_id': 'rule'})[1]
        self.assertEqual(self.call(root + 'review-resolution', {'id': report['review_id'], 'issue_index': 0, 'resolved': True})[0], 200)
        data = self.call('projects/' + self.pid)[1]
        self.assertEqual(data['reading_cards'][0]['method'], 'new')
        self.assertEqual(data['chapter_reviews'][0]['resolved'], [0])
        self.assertEqual(data['chapter_reviews'][0]['source'], 'rule')
        self.assertEqual(len(data['sections']), 14)
        self.call(root + 'archive', {'archived': True})
        self.assertNotIn(self.pid, [p['id'] for p in self.call('projects')[1]])
        self.call(root + 'archive', {'archived': False})
        self.assertIn(self.pid, [p['id'] for p in self.call('projects')[1]])

    def test_ai_snapshot_is_version_bound_and_duplicate_does_not_queue_twice(self):
        body = {'section': 'method', 'content': 'model review text', 'revision': 0, 'request_id': 'ai-request'}
        root = 'projects/' + self.pid + '/'
        result = self.call(root + 'ai-review', body)[1]
        duplicate = self.call(root + 'ai-review', body)[1]
        self.assertEqual(result['id'], duplicate['id'])
        self.assertEqual(result['version']['id'], duplicate['version']['id'])
        data = self.call('projects/' + self.pid)[1]
        self.assertEqual(len(data['ai_reviews']), 1)
        job = data['ai_reviews'][0]
        snapshot = json.loads((repo.DB.parent / 'ai-reviews' / (job['id'] + '.input.json')).read_text(encoding='utf-8'))
        self.call(root + 'drafts', {'section': 'method', 'content': 'newer text', 'revision': 1, 'request_id': 'newer'})
        self.assertEqual(snapshot['content'], 'model review text')
        with self.assertRaises(ValueError):
            validate_report({'request_id': job['id'], 'version_id': 'wrong', 'score': 80, 'summary': 'review'}, job)
        report = validate_report({'request_id': job['id'], 'version_id': job['version_id'], 'score': 80, 'summary': 'review', 'issues': [], 'strengths': [], 'suggestions': []}, job)
        self.assertEqual(report['score'], 80)

    def test_foreign_project_records_and_unknown_sections_are_rejected(self):
        other = self.call('projects', {'title': 'other'})[1]['id']
        rid = self.call('projects/' + other + '/claims', {'claim': 'other claim'})[1]['id']
        self.assertEqual(self.call('projects/' + self.pid + '/claims', {'id': rid, 'claim': 'overwrite'})[0], 400)
        self.assertEqual(self.call('projects/' + self.pid + '/drafts', {'section': 'unknown', 'content': 'x'})[0], 400)

    def test_background_report_does_not_overwrite_newer_draft(self):
        root = 'projects/' + self.pid + '/'
        self.call(root + 'ai-review', {'section': 'method', 'content': 'old snapshot', 'revision': 0, 'request_id': 'background'})
        with repo.connect() as c:
            c.execute("UPDATE projects SET ai_conversation='dedicated-review' WHERE id=?", (self.pid,))
            c.execute("UPDATE ai_reviews SET state='running',job_id='queue-job' WHERE project_id=?", (self.pid,))
        # Close this fixture connection explicitly (sqlite context managers only commit).
        c.close()
        self.call(root + 'drafts', {'section': 'method', 'content': 'newer draft', 'revision': 1, 'request_id': 'second-version'})
        job = self.call('projects/' + self.pid)[1]['ai_reviews'][0]
        report = {'request_id': job['id'], 'version_id': job['version_id'], 'score': 78, 'summary': 'old version review', 'issues': [], 'strengths': [], 'suggestions': []}
        (repo.DB.parent / 'ai-reviews' / (job['id'] + '.report.json')).write_text(json.dumps(report), encoding='utf-8')
        tick(object())  # File ingestion must survive without an open browser or network.
        data = self.call('projects/' + self.pid)[1]
        self.assertEqual(data['ai_reviews'][0]['state'], 'completed')
        self.assertEqual(data['chapter_reviews'][0]['draft_version_id'], job['version_id'])
        self.assertEqual(data['chapter_reviews'][0]['source'], 'ai')
        self.assertEqual(data['drafts'][0]['content'], 'newer draft')
        tick(object())
        self.assertEqual(len(self.call('projects/' + self.pid)[1]['chapter_reviews']), 1)

    def test_existing_database_migration_preserves_content(self):
        from legacy import SCHEMA
        old = Path(self.tmp.name) / 'legacy.db'
        c = sqlite3.connect(old); c.executescript(SCHEMA)
        c.execute("INSERT INTO projects(id,title) VALUES('original','original research')")
        c.execute("INSERT INTO drafts(id,project_id,section,content) VALUES('draft','original','method','existing manuscript')")
        c.commit(); c.close()
        repo.DB = old
        repo.initialize()
        c = repo.connect()
        try:
            data = repo.get_project(c, 'original')
            self.assertEqual(data['drafts'][0]['content'], 'existing manuscript')
            self.assertEqual(data['drafts'][0]['revision'], 0)
        finally:
            c.close()

if __name__ == '__main__':
    unittest.main()
