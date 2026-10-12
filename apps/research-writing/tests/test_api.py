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

    def test_beginner_course_waits_for_answer_and_unlocks_only_after_correct_practice(self):
        root='projects/'+self.pid+'/'
        first=self.call(root+'learning')[1]
        self.assertEqual(first['completed'],[])
        self.assertNotIn('answer',first['lessons'][0])
        code,_=self.call(root+'learn',{'lesson_id':'one-question','answer':'an answer','request_id':'skip'})
        self.assertEqual(code,400)
        self.call(root+'learn',{'lesson_id':'paper-purpose','answer':'layout','request_id':'wrong'})
        self.assertEqual(self.call(root+'learning')[1]['completed'],[])
        right={'lesson_id':'paper-purpose','answer':'question','request_id':'right'}
        self.call(root+'learn',right);self.call(root+'learn',right)
        course=self.call(root+'learning')[1]
        self.assertEqual(course['completed'],['paper-purpose'])
        self.assertEqual(course['current'],'paper-purpose')
        self.assertEqual(len(course['attempts']),2)
        self.call(root+'learn',{'action':'select','lesson_id':'one-question'})
        self.assertEqual(self.call(root+'learning')[1]['current'],'one-question')

    def test_tutor_context_is_independent_and_question_cannot_mark_lesson_complete(self):
        from learning_worker import tick_learning
        root='projects/'+self.pid+'/'
        self.call(root+'learn',{'lesson_id':'paper-purpose','answer':'question','request_id':'first-lesson'})
        self.call(root+'experiments',{'rq':'UNRELATED_PRIVATE_EXPERIMENT','result':'foreign task context'})
        self.call(root+'learn',{'lesson_id':'one-question','intent':'question','answer':'研究问题与主题有何区别？','request_id':'help'})
        a=self.call(root+'learning')[1]['attempts'][0]
        input_path=repo.DB.parent/'writing-tutor'/(a['id']+'.input.json')
        raw=input_path.read_text(encoding='utf-8')
        self.assertNotIn('UNRELATED_PRIVATE_EXPERIMENT',raw)
        self.assertNotIn('foreign task context',raw)
        c=repo.connect();c.execute("UPDATE learning_attempts SET state='running',job_id='test-job' WHERE id=?",(a['id'],));c.commit();c.close()
        feedback={'request_id':a['id'],'lesson_id':a['lesson_id'],'passed':True,'feedback':'一个是领域，一个是要回答的问题。','example':'教学例子','next_tip':'试着写出自己的问题。'}
        (repo.DB.parent/'writing-tutor'/(a['id']+'.report.json')).write_text(json.dumps(feedback,ensure_ascii=False),encoding='utf-8')
        tick_learning(object())
        course=self.call(root+'learning')[1]
        self.assertFalse(course['attempts'][0]['feedback']['passed'])
        self.assertEqual(course['completed'],['paper-purpose'])
        self.assertEqual(course['current'],'one-question')

    def test_tutor_text_feedback_persists_without_browser_and_does_not_duplicate(self):
        from learning_worker import tick_learning
        root='projects/'+self.pid+'/'
        self.call(root+'learn',{'lesson_id':'paper-purpose','answer':'question','request_id':'first'})
        self.call(root+'learn',{'lesson_id':'one-question','answer':'同条件下比较两种杯子的温度变化。','request_id':'practice'})
        a=self.call(root+'learning')[1]['attempts'][0]
        c=repo.connect();c.execute("UPDATE learning_attempts SET state='running',job_id='test-job' WHERE id=?",(a['id'],));c.commit();c.close()
        report={'request_id':a['id'],'lesson_id':a['lesson_id'],'passed':True,'feedback':'问题已经清楚。','example':'示例','next_tip':'由你决定是否进入下一课。'}
        (repo.DB.parent/'writing-tutor'/(a['id']+'.report.json')).write_text(json.dumps(report),encoding='utf-8')
        tick_learning(object());tick_learning(object())
        course=self.call(root+'learning')[1]
        self.assertEqual(len(course['completed']),2)
        self.assertEqual(course['attempts'][0]['state'],'completed')
        self.assertEqual(course['attempts'][0]['answer'],'同条件下比较两种杯子的温度变化。')

    def test_overloaded_delivered_tutor_job_retries_once_then_fails_visibly(self):
        from learning_worker import tick_learning
        root='projects/'+self.pid+'/'
        self.call(root+'learn',{'lesson_id':'paper-purpose','answer':'question','request_id':'first'})
        self.call(root+'learn',{'lesson_id':'one-question','answer':'比较杯子温度变化','request_id':'overload'})
        a=self.call(root+'learning')[1]['attempts'][0]
        c=repo.connect();c.execute("UPDATE learning_attempts SET state='running',job_id='job' WHERE id=?",(a['id'],));c.commit();c.close()
        class Overloaded:
            def snapshot(self,query):
                return {'tasks':[{'id':'job','state':'delivered','error':'codex_server_overloaded'}]}
        tick_learning(Overloaded())
        attempt=self.call(root+'learning')[1]['attempts'][0]
        self.assertEqual(attempt['state'],'creating')
        self.assertEqual(attempt['fallback_count'],1)
        c=repo.connect();c.execute("UPDATE learning_attempts SET state='running',job_id='job' WHERE id=?",(a['id'],));c.commit();c.close()
        tick_learning(Overloaded())
        attempt=self.call(root+'learning')[1]['attempts'][0]
        self.assertEqual(attempt['state'],'failed')
        self.assertTrue(attempt['error'])
        self.assertEqual(attempt['answer'],'比较杯子温度变化')

    def test_tutor_waits_for_corrected_intermediate_json(self):
        from learning_worker import tick_learning,recover_feedback
        root='projects/'+self.pid+'/'
        self.call(root+'learn',{'lesson_id':'paper-purpose','answer':'question','request_id':'first'})
        self.call(root+'learn',{'lesson_id':'one-question','answer':'比较杯子温度变化','request_id':'correction'})
        a=self.call(root+'learning')[1]['attempts'][0]
        c=repo.connect();c.execute("UPDATE learning_attempts SET state='running',job_id='job' WHERE id=?",(a['id'],));c.commit();c.close()
        path=repo.DB.parent/'writing-tutor'/(a['id']+'.report.json')
        path.write_text('{"passed":true}invalid intermediate content',encoding='utf-8')
        class Running:
            def snapshot(self,query):
                return {'tasks':[{'id':'job','state':'running','error':''}]}
        tick_learning(Running())
        self.assertEqual(self.call(root+'learning')[1]['attempts'][0]['state'],'running')
        # Simulate an older service's premature failure, then a corrected output.
        c=repo.connect();c.execute("UPDATE learning_attempts SET state='failed',error='Extra data' WHERE id=?",(a['id'],));c.commit();c.close()
        report={'request_id':a['id'],'lesson_id':a['lesson_id'],'passed':True,'feedback':'入门目标已表达清楚。','example':'','next_tip':'等你决定下一步。'}
        path.write_text(json.dumps(report,ensure_ascii=False),encoding='utf-8')
        recover_feedback()
        row=self.call(root+'learning')[1]['attempts'][0]
        self.assertEqual(row['state'],'completed')
        self.assertEqual(row['error'],'')
        self.assertEqual(self.call(root+'learning')[1]['current'],'one-question')

if __name__ == '__main__':
    unittest.main()
