#!/usr/bin/env python3
"""Research writing website: Vue static assets and versioned SQLite APIs."""
import json
import os
from http.server import ThreadingHTTPServer, SimpleHTTPRequestHandler
from urllib.parse import urlparse, parse_qs
from pathlib import Path
import repository as repo
from legacy import review_chapter

ROOT = Path(__file__).resolve().parent
STATIC = ROOT / 'static'

class Handler(SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=str(STATIC), **kwargs)

    def end_headers(self):
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        super().end_headers()

    def send_json(self, data, status=200):
        raw = json.dumps(data, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json; charset=utf-8')
        self.send_header('Content-Length', str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        url = urlparse(self.path)
        if not url.path.startswith('/api/'):
            return super().do_GET()
        c = repo.connect()
        try:
            if url.path == '/api/projects':
                archived = parse_qs(url.query).get('archived', ['0'])[0] == '1'
                return self.send_json([dict(x) for x in c.execute('SELECT * FROM projects WHERE archived=? ORDER BY updated_at DESC,rowid DESC', (int(archived),))])
            if url.path == '/api/overview':
                return self.send_json({'stages': repo.STAGES, 'sections': [{'id': k, 'title': v} for k, v in repo.PAPER_SECTIONS.items()]})
            if url.path == '/api/library':
                from bridge import Gateway
                q = parse_qs(url.query).get('q', [''])[0]
                gateway = Gateway()
                try:
                    rows = gateway.call('library', library={'action': 'search', 'query': q})
                finally:
                    gateway.close()
                return self.send_json(rows)
            if url.path.startswith('/api/library/'):
                from bridge import Gateway
                gateway = Gateway()
                try:
                    row = gateway.call('library', library={'action': 'get', 'material_id': int(url.path.split('/')[-1])})
                finally:
                    gateway.close()
                return self.send_json(row)
            parts = url.path.strip('/').split('/')
            if len(parts) in [3, 4] and parts[:2] == ['api', 'projects']:
                if len(parts) == 4 and parts[3] == 'learning':
                    repo.get_project(c, parts[2])
                    from tutor import snapshot
                    return self.send_json(snapshot(c, parts[2]))
                data = repo.get_project(c, parts[2])
                if len(parts) == 4 and parts[3] != 'export':
                    return self.send_json({'error': '接口不存在'}, 404)
                if len(parts)==4:
                    from tutor import snapshot
                    data['learning']=snapshot(c,parts[2],full=True)
                return self.send_json(data)
            return self.send_json({'error': '接口不存在'}, 404)
        except KeyError:
            self.send_json({'error': '项目不存在'}, 404)
        except Exception:
            self.send_json({'error': '读取失败，请刷新后重试'}, 503)
        finally:
            c.close()

    def do_POST(self):
        c = repo.connect()
        try:
            n = int(self.headers.get('Content-Length', '0'))
            if n < 0 or n > 5_000_000:
                return self.send_json({'error': '请求内容过大'}, 413)
            body = json.loads(self.rfile.read(n) or b'{}')
            if not isinstance(body, dict):
                raise ValueError('请求格式无效')
            p = urlparse(self.path).path
            c.execute('BEGIN IMMEDIATE')
            if p == '/api/projects':
                title = str(body.get('title', '')).strip()
                if not title or len(title) > 240:
                    raise ValueError('请填写 1–240 字的项目名称')
                pid = repo.new_id()
                c.execute('INSERT INTO projects(id,title,field) VALUES(?,?,?)', (pid, title, str(body.get('field', ''))[:1000]))
                c.commit()
                return self.send_json({'id': pid})
            parts = p.strip('/').split('/')
            if len(parts) != 4 or parts[:2] != ['api', 'projects']:
                return self.send_json({'error': '接口不存在'}, 404)
            pid, kind = parts[2:]
            project = c.execute('SELECT * FROM projects WHERE id=?', (pid,)).fetchone()
            if not project:
                return self.send_json({'error': '项目不存在'}, 404)
            result = {'ok': True}
            if kind == 'meta':
                title = str(body.get('title', project['title'])).strip()
                if not title or len(title) > 240:
                    raise ValueError('项目名称长度不正确')
                c.execute('UPDATE projects SET title=?,field=?,updated_at=CURRENT_TIMESTAMP WHERE id=?', (title, str(body.get('field', project['field']))[:1000], pid))
            elif kind == 'stage':
                c.execute('UPDATE projects SET stage=?,updated_at=CURRENT_TIMESTAMP WHERE id=?', (max(1, min(8, int(body.get('stage', 1)))), pid))
            elif kind == 'learn':
                from tutor import submit
                result = submit(c, pid, body)
            elif kind == 'archive':
                c.execute('UPDATE projects SET archived=?,updated_at=CURRENT_TIMESTAMP WHERE id=?', (int(bool(body.get('archived', True))), pid))
            elif kind in ['drafts', 'chapter-review', 'ai-review']:
                request_id = str(body.get('request_id', ''))[:100]
                if kind == 'ai-review' and not request_id:
                    raise ValueError('缺少操作编号，请刷新页面')
                if kind == 'ai-review':
                    prior = c.execute('SELECT * FROM ai_reviews WHERE project_id=? AND request_id=?', (pid, request_id)).fetchone()
                    if prior:
                        version = c.execute('SELECT * FROM draft_versions WHERE id=?', (prior['version_id'],)).fetchone()
                        return self.send_json({'ok': True, **dict(prior), 'version': dict(version)})
                version = repo.save_draft(c, pid, str(body.get('section', '')), body.get('content', ''), body.get('revision'), request_id, 'manual' if kind == 'drafts' else 'review')
                result = {'ok': True, 'version': version}
                if kind == 'chapter-review':
                    report = review_chapter(version['section'], version['content'], repo.get_project(c, pid))
                    rid = repo.add_review(c, pid, version['section'], version['id'], report)
                    result.update({'report': report, 'review_id': rid})
                if kind == 'ai-review':
                    from bridge import prepare_review
                    result = prepare_review(c, pid, version, request_id)
            elif kind == 'review-resolution':
                rid, index = str(body.get('id', '')), int(body.get('issue_index', -1))
                row = c.execute('SELECT issues_json FROM chapter_reviews WHERE id=? AND project_id=?', (rid, pid)).fetchone()
                if not row or index < 0 or index >= len(json.loads(row['issues_json'])):
                    raise ValueError('评审意见不存在')
                c.execute('INSERT INTO review_resolutions(review_id,issue_index,resolved) VALUES(?,?,?) ON CONFLICT(review_id,issue_index) DO UPDATE SET resolved=excluded.resolved', (rid, index, int(bool(body.get('resolved')))))
            elif kind == 'submission':
                item = str(body.get('item', '')).strip()
                if not item:
                    raise ValueError('缺少检查项')
                c.execute('''INSERT INTO submission_checks(id,project_id,item,status,note) VALUES(?,?,?,?,?)
                  ON CONFLICT(project_id,item) DO UPDATE SET status=excluded.status,note=excluded.note,updated_at=CURRENT_TIMESTAMP''', (repo.new_id(), pid, item[:1000], 'done' if body.get('status') == 'done' else 'todo', str(body.get('note', ''))[:10000]))
            elif kind in repo.FIELDS:
                fields = repo.FIELDS[kind]
                vals = [str(body.get(k, ''))[:100000] for k in fields]
                if not vals[0].strip():
                    raise ValueError('请填写' + {'reading_cards': '文献标题', 'problems': '研究现象', 'hypotheses': '假设', 'experiments': '研究问题', 'claims': '主张', 'reviews': '审稿人', 'members': '成员姓名'}[kind])
                rid = str(body.get('id', ''))
                if rid:
                    found = c.execute('SELECT 1 FROM ' + kind + ' WHERE id=? AND project_id=?', (rid, pid)).fetchone()
                    if not found:
                        raise ValueError('记录不存在')
                    c.execute('UPDATE ' + kind + ' SET ' + ','.join(k + '=?' for k in fields) + ' WHERE id=? AND project_id=?', (*vals, rid, pid))
                else:
                    rid = repo.new_id()
                    c.execute('INSERT INTO ' + kind + '(id,project_id,' + ','.join(fields) + ') VALUES(' + ','.join('?' for _ in range(len(fields) + 2)) + ')', (rid, pid, *vals))
                result['id'] = rid
            else:
                return self.send_json({'error': '接口不存在'}, 404)
            c.execute('UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=?', (pid,))
            c.commit()
            self.send_json(result)
        except repo.Conflict as e:
            c.rollback()
            self.send_json({'error': '其他设备已更新此章节。你的编辑仍保留，请比较服务器版本后再保存。', 'current': e.current}, 409)
        except (ValueError, TypeError, json.JSONDecodeError) as e:
            c.rollback()
            self.send_json({'error': str(e)}, 400)
        except Exception:
            c.rollback()
            self.send_json({'error': '保存失败，内容仍保留，请稍后重试'}, 503)
        finally:
            c.close()

    def log_message(self, fmt, *args):
        pass

if __name__ == '__main__':
    repo.initialize()
    from bridge import start_worker
    start_worker()
    port = int(os.environ.get('RESEARCH_PORT', '8765'))
    ThreadingHTTPServer(('127.0.0.1', port), Handler).serve_forever()
