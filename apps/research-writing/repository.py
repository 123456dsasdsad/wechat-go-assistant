"""SQLite storage shared by the website and the asynchronous review worker."""
import json
import os
import sqlite3
import uuid
from contextlib import closing
from pathlib import Path
from legacy import SCHEMA, PAPER_SECTIONS, recommendations

ROOT = Path(__file__).resolve().parent
DB = Path(os.environ.get('RESEARCH_DB', ROOT / 'data/research.db'))
FIELDS = {
    'reading_cards': ['title', 'source', 'question', 'method', 'evidence', 'limitation', 'transfer'],
    'problems': ['phenomenon', 'consequence', 'existing', 'gap', 'priority'],
    'hypotheses': ['statement', 'condition', 'prediction', 'falsifier', 'novelty_type', 'status'],
    'experiments': ['rq', 'variables', 'baseline', 'metrics', 'protocol', 'status', 'result'],
    'claims': ['claim', 'evidence', 'alternative', 'boundary', 'strength'],
    'reviews': ['reviewer', 'concern', 'action', 'status'],
    'members': ['name', 'role', 'note'],
}
STAGES = ['诊断项目', '找问题', '找创新', '做实验', '整理证据', '写论文初稿', '模拟审稿', '投稿准备']

def new_id():
    return str(uuid.uuid4())

def connect():
    c = sqlite3.connect(DB, timeout=20)
    c.row_factory = sqlite3.Row
    c.execute('PRAGMA foreign_keys=ON')
    c.execute('PRAGMA busy_timeout=20000')
    return c

def initialize():
    DB.parent.mkdir(parents=True, exist_ok=True)
    with closing(connect()) as c, c:
        c.executescript(SCHEMA)
        for table, name, definition in [
            ('projects', 'archived', 'INTEGER NOT NULL DEFAULT 0'),
            ('projects', 'ai_conversation', "TEXT NOT NULL DEFAULT ''"),
            ('projects', 'learning_conversation', "TEXT NOT NULL DEFAULT ''"),
            ('drafts', 'revision', 'INTEGER NOT NULL DEFAULT 0'),
            ('draft_versions', 'revision', 'INTEGER NOT NULL DEFAULT 0'),
            ('draft_versions', 'request_id', 'TEXT'),
            ('chapter_reviews', 'source', "TEXT NOT NULL DEFAULT 'rule'"),
        ]:
            cols = {x['name'] for x in c.execute('PRAGMA table_info(' + table + ')')}
            if name not in cols:
                c.execute('ALTER TABLE ' + table + ' ADD COLUMN ' + name + ' ' + definition)
        c.executescript('''
        CREATE UNIQUE INDEX IF NOT EXISTS draft_request ON draft_versions(project_id, request_id);
        CREATE TABLE IF NOT EXISTS review_resolutions (
          review_id TEXT NOT NULL, issue_index INTEGER NOT NULL, resolved INTEGER NOT NULL DEFAULT 0,
          PRIMARY KEY(review_id,issue_index));
        CREATE TABLE IF NOT EXISTS ai_reviews (
          id TEXT PRIMARY KEY, project_id TEXT NOT NULL, section TEXT NOT NULL,
          version_id TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'creating', job_id TEXT DEFAULT '',
          progress TEXT DEFAULT '', error TEXT DEFAULT '', link TEXT DEFAULT '', model TEXT DEFAULT 'gpt-6.1-sol',
          created_at TEXT DEFAULT CURRENT_TIMESTAMP, updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
          request_id TEXT NOT NULL, UNIQUE(project_id,request_id));
        ''')
        from tutor import setup
        setup(c)
        if not c.execute('SELECT 1 FROM projects LIMIT 1').fetchone():
            c.execute('INSERT INTO projects(id,title,field) VALUES(?,?,?)',
                      (new_id(), '我的第一个研究项目', '待填写研究方向'))

def get_project(c, pid):
    p = c.execute('SELECT * FROM projects WHERE id=?', (pid,)).fetchone()
    if not p:
        raise KeyError('项目不存在')
    data = {'project': dict(p)}
    for table in FIELDS:
        data[table] = [dict(x) for x in c.execute('SELECT * FROM ' + table + ' WHERE project_id=? ORDER BY rowid DESC', (pid,))]
    for table in ['drafts', 'draft_versions', 'submission_checks', 'ai_reviews']:
        data[table] = [dict(x) for x in c.execute('SELECT * FROM ' + table + ' WHERE project_id=? ORDER BY rowid DESC', (pid,))]
    data['chapter_reviews'] = []
    for row in c.execute('SELECT * FROM chapter_reviews WHERE project_id=? ORDER BY rowid DESC', (pid,)):
        item = dict(row)
        for k in ['issues', 'strengths', 'suggestions']:
            item[k] = json.loads(item.pop(k + '_json') or '[]')
        item['resolved'] = [x[0] for x in c.execute('SELECT issue_index FROM review_resolutions WHERE review_id=? AND resolved=1', (item['id'],))]
        data['chapter_reviews'].append(item)
    data['sections'] = [{'id': k, 'title': v} for k, v in PAPER_SECTIONS.items()]
    data['stages'] = STAGES
    data['draft_progress'] = {'written': sum(bool(x['content'].strip()) for x in data['drafts']), 'total': len(PAPER_SECTIONS)}
    data['recommendations'] = recommendations(data)
    return data

class Conflict(Exception):
    def __init__(self, current):
        self.current = current

def save_draft(c, pid, section, content, expected, request_id, source='manual'):
    if section not in PAPER_SECTIONS:
        raise ValueError('未知论文章节')
    if not isinstance(content, str) or len(content) > 2_000_000:
        raise ValueError('章节内容格式不正确或过长')
    previous = c.execute('SELECT * FROM draft_versions WHERE project_id=? AND request_id=?', (pid, request_id)).fetchone() if request_id else None
    if previous:
        if previous['section'] != section or previous['content'] != content:
            raise ValueError('同一次保存不能提交不同内容')
        return dict(previous)
    row = c.execute('SELECT * FROM drafts WHERE project_id=? AND section=?', (pid, section)).fetchone()
    revision = row['revision'] if row else 0
    if expected is not None and int(expected) != revision:
        raise Conflict(dict(row) if row else {'content': '', 'revision': 0})
    if row and row['content'] == content:
        previous = c.execute('SELECT * FROM draft_versions WHERE project_id=? AND section=? ORDER BY rowid DESC LIMIT 1', (pid, section)).fetchone()
        if previous:
            return dict(previous)
    version_id = new_id()
    c.execute('''INSERT INTO drafts(id,project_id,section,content,revision) VALUES(?,?,?,?,?)
      ON CONFLICT(project_id,section) DO UPDATE SET content=excluded.content,revision=excluded.revision,updated_at=CURRENT_TIMESTAMP''',
              (new_id(), pid, section, content, revision + 1))
    c.execute('INSERT INTO draft_versions(id,project_id,section,content,source,revision,request_id) VALUES(?,?,?,?,?,?,?)',
              (version_id, pid, section, content, source, revision + 1, request_id or None))
    c.execute('UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=?', (pid,))
    return dict(c.execute('SELECT * FROM draft_versions WHERE id=?', (version_id,)).fetchone())

def add_review(c, pid, section, version_id, report, source='rule'):
    rid = new_id()
    c.execute('''INSERT INTO chapter_reviews(id,project_id,section,draft_version_id,score,summary,issues_json,strengths_json,suggestions_json,source)
      VALUES(?,?,?,?,?,?,?,?,?,?)''', (rid, pid, section, version_id, report['score'], report['summary'],
        *[json.dumps(report.get(k, []), ensure_ascii=False) for k in ['issues', 'strengths', 'suggestions']], source))
    return rid
