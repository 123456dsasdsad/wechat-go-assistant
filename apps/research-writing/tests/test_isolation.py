import sys
import unittest
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from bridge import submit
from learning_worker import submit_task


class Connection:
    def execute(self, sql, args=()):
        self.sql = sql
        return self
    def fetchone(self):
        return {'title': '论文项目', 'ai_conversation': '', 'learning_conversation': ''}
    def commit(self):
        pass


class Gateway:
    base, origin, cookie = 'https://example.invalid/api', 'https://example.invalid', 'test'
    def __init__(self): self.calls = []; self.task = None
    def snapshot(self, *args): raise AssertionError('must not read global current conversation')
    def call(self, action, **fields):
        if action == 'conversation': raise AssertionError('must not change global selection')
        self.calls.append((action, fields))
        return {'conversation': '12345678'}
    def request(self, url, data, headers):
        self.task = data
        return {'id': 'job'}


class IsolationTest(unittest.TestCase):
    def test_review_uses_returned_background_id_and_preserves_profile(self):
        gateway = Gateway()
        submit(gateway, Connection(), {'id': 'a' * 24, 'project_id': 'project', 'version_id': 'version'})
        self.assertEqual(gateway.task['conversation'], '12345678')
        self.assertEqual([x[0] for x in gateway.calls], ['conversation_create', 'identity', 'model'])
        self.assertEqual(gateway.calls[1][1]['profile']['app'], 'research-writing')
        again = Gateway()
        submit(again, Connection(), {'id': 'b' * 24, 'project_id': 'project', 'version_id': 'version'})
        self.assertEqual(gateway.calls[0][1]['source'], again.calls[0][1]['source'])

    def test_tutor_has_separate_stable_conversation(self):
        gateway = Gateway()
        submit_task(gateway, Connection(), {'id': 'c' * 24, 'project_id': 'project', 'lesson_id': 'lesson'})
        self.assertEqual(gateway.task['conversation'], '12345678')
        self.assertEqual([x[0] for x in gateway.calls], ['conversation_create', 'identity', 'model'])


if __name__ == '__main__': unittest.main()
