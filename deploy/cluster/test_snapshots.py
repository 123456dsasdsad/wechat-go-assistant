from contextlib import closing
import json
import sqlite3
import tempfile
import unittest
from pathlib import Path
from snapshot import create_snapshot, verify_snapshot, safe_relative
from reconcile import reconcile
from restore import restore_snapshot


class SnapshotsTest(unittest.TestCase):
    def test_live_wal_snapshot_is_complete_and_detects_tampering(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source = root / 'source'; source.mkdir()
            with closing(sqlite3.connect(source / 'state.sqlite')) as db:
                db.execute('PRAGMA journal_mode=WAL')
                db.execute('CREATE TABLE values_test(value TEXT)')
                db.execute("INSERT INTO values_test VALUES('acknowledged')"); db.commit()
                manifest = create_snapshot('ass', {'state': source}, root / 'snapshot')
                self.assertEqual(len(manifest['files']), 1)
                saved = root / 'snapshot/data/state/state.sqlite'
                with closing(sqlite3.connect(saved)) as copied:
                    self.assertEqual(copied.execute('SELECT value FROM values_test').fetchone()[0], 'acknowledged')
                self.assertEqual(verify_snapshot(root / 'snapshot')['node'], 'ass')
                saved.write_bytes(b'corrupted')
                with self.assertRaises(ValueError): verify_snapshot(root / 'snapshot')

    def test_reconcile_preserves_new_secondary_data_and_reports_conflicts(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); a = root / 'a'; b = root / 'b'; a.mkdir(); b.mkdir()
            (a / 'thread.jsonl').write_text('old', encoding='utf-8')
            (b / 'thread.jsonl').write_text('new', encoding='utf-8')
            (b / 'new-task.txt').write_text('new task', encoding='utf-8')
            create_snapshot('campus', {'sessions': a}, root / 'campus', True)
            create_snapshot('ass', {'sessions': b}, root / 'ass', True)
            report = reconcile(root / 'campus', root / 'ass')
            self.assertEqual(len(report['conflicts']), 1)
            self.assertEqual(report['secondary_only'], ['sessions/new-task.txt'])
            self.assertFalse(report['promotion_ready'])
            self.assertEqual((b / 'thread.jsonl').read_text(), 'new')

    def test_rejects_paths_outside_snapshot(self):
        for path in ('../secret', '/etc/passwd', 'C:/secret', 'a/../secret', 'a\\secret'):
            with self.subTest(path=path), self.assertRaises(ValueError): safe_relative(path)

    def test_restore_preserves_database_and_refuses_existing_destination(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source = root / 'source'; source.mkdir()
            with closing(sqlite3.connect(source / 'state.sqlite')) as db:
                db.execute('CREATE TABLE tasks(id TEXT)')
                db.execute("INSERT INTO tasks VALUES('confirmed-task')"); db.commit()
            create_snapshot('ass', {'state': source}, root / 'snapshot', True)
            restore_snapshot(root / 'snapshot', root / 'restored')
            with closing(sqlite3.connect(root / 'restored/data/state/state.sqlite')) as db:
                self.assertEqual(db.execute('SELECT id FROM tasks').fetchone()[0], 'confirmed-task')
            with self.assertRaises(FileExistsError):
                restore_snapshot(root / 'snapshot', root / 'restored')


if __name__ == '__main__': unittest.main()
