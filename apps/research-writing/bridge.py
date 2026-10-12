"""Submit model reviews to the existing campus queue; retain results across page closes."""
import http.cookies
import json
import os
import secrets
import threading
import time
import urllib.request
import urllib.parse
from contextlib import closing
from pathlib import Path
import repository as repo

class Gateway:
    def __init__(self):
        cfg = json.loads(Path(os.environ.get('RESEARCH_GATEWAY_CONFIG', '/home/sanlou/campus-stack/maintenance/config.json')).read_text(encoding='utf-8'))
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        key = Path(cfg['key_file']).read_text().strip()
        grant = self.request(cfg['relay_url'].rstrip('/') + '/workspace/grant', {}, {'Authorization': 'Bearer ' + key})
        url = urllib.parse.urlsplit(grant['url'])
        self.base = urllib.parse.urlunsplit((url.scheme, url.netloc, '/wechat-files/manage/api', '', ''))
        self.origin = urllib.parse.urlunsplit((url.scheme, url.netloc, '', '', ''))
        self.cookie = 'wechat_access=' + grant['access_session']
        self.call('login', token=url.fragment)

    def request(self, url, data=None, headers=None):
        raw = json.dumps(data).encode() if data is not None else None
        req = urllib.request.Request(url, data=raw, headers={'Content-Type': 'application/json', **(headers or {})})
        with self.opener.open(req, timeout=40) as r:
            for header in r.headers.get_all('Set-Cookie', []):
                cookies = http.cookies.SimpleCookie(); cookies.load(header)
                if 'wechat_manage' in cookies:
                    self.cookie = self.cookie.split('; wechat_manage=')[0] + '; wechat_manage=' + cookies['wechat_manage'].value
            return json.load(r)

    def call(self, action, **fields):
        return self.request(self.base, {'action': action, 'source': secrets.token_hex(12), **fields}, {'Origin': self.origin, 'Cookie': self.cookie})

    def snapshot(self, query=''):
        return self.request(self.base + '?q=' + urllib.parse.quote(query), headers={'Cookie': self.cookie})

    def close(self):
        try:
            self.call('logout')
        except Exception:
            pass

def prepare_review(c, pid, version, request_id):
    if c.execute("SELECT 1 FROM ai_reviews WHERE project_id=? AND section=? AND state IN ('creating','queued','running')", (pid, version['section'])).fetchone():
        raise ValueError('本章节已有 AI 评审在执行，请等待完成')
    jid = secrets.token_hex(12)
    root = repo.DB.parent / 'ai-reviews'; root.mkdir(parents=True, exist_ok=True)
    data = repo.get_project(c, pid)
    snapshot = {'request_id': jid, 'version_id': version['id'], 'section': version['section'],
                'title': repo.PAPER_SECTIONS[version['section']], 'content': version['content'],
                'project': data['project'], 'reading_cards': data['reading_cards'], 'experiments': data['experiments'], 'claims': data['claims']}
    (root / (jid + '.input.json')).write_text(json.dumps(snapshot, ensure_ascii=False, indent=2), encoding='utf-8')
    c.execute('INSERT INTO ai_reviews(id,project_id,section,version_id,request_id) VALUES(?,?,?,?,?)', (jid, pid, version['section'], version['id'], request_id))
    return {'ok': True, 'id': jid, 'state': 'creating', 'version': version}

def validate_report(raw, job):
    if raw.get('request_id') != job['id'] or raw.get('version_id') != job['version_id']:
        raise ValueError('评审版本不匹配')
    if not isinstance(raw.get('score'), int) or not 0 <= raw['score'] <= 100:
        raise ValueError('评审评分格式不正确')
    if not isinstance(raw.get('summary'), str) or not raw['summary'].strip():
        raise ValueError('评审缺少摘要')
    result = {'score': raw['score'], 'summary': raw['summary'][:5000]}
    issues = raw.get('issues', [])
    if not isinstance(issues, list) or len(issues) > 30:
        raise ValueError('评审问题格式不正确')
    result['issues'] = []
    for x in issues:
        if not isinstance(x, dict) or not isinstance(x.get('title'), str) or not isinstance(x.get('detail'), str):
            raise ValueError('评审问题内容不完整')
        result['issues'].append({'severity': x.get('severity') if x.get('severity') in ['high', 'medium', 'low'] else 'medium', 'title': x['title'][:500], 'detail': x['detail'][:6000], 'quote': str(x.get('quote', ''))[:2000]})
    for name in ['strengths', 'suggestions']:
        rows = raw.get(name, [])
        if not isinstance(rows, list) or not all(isinstance(x, str) for x in rows):
            raise ValueError('评审建议格式不正确')
        result[name] = [x[:6000] for x in rows[:30]]
    return result

def submit(gateway, c, job):
    project = c.execute('SELECT * FROM projects WHERE id=?', (job['project_id'],)).fetchone()
    cid = project['ai_conversation']
    if not cid:
        before = gateway.snapshot()
        title = '研序评审-' + job['project_id'][:8]
        gateway.call('conversation', input='新建会话 ' + title)
        snap = gateway.snapshot()
        cid = snap['current']
        c.execute('UPDATE projects SET ai_conversation=? WHERE id=?', (cid, job['project_id'])); c.commit()
        if before.get('current') and before['current'] != cid:
            gateway.call('conversation', input='继续会话 ' + before['current'])
    gateway.call('profile', conversation=cid, model='gpt-6.1-sol', effort='high', profile={})
    root = (repo.DB.parent / 'ai-reviews').resolve()
    src, output = root / (job['id'] + '.input.json'), root / (job['id'] + '.report.json')
    prompt = f'''研究写作章节评审，任务标识 {job['id']}。请读取 {src}。
这是独立的评审任务，每次只根据该 JSON 的章节版本和其中的研究记录判断。不得把上次评审的论文内容当作本次内容。
请评估论证、方法可复核性、证据、引用来源和结论边界。不要编造文献或实验结果。缺失材料明确说明。不要改论文，不执行实验。
允许读取输入文件，只允许写本次评审文件 {output}。不要修改其他项目或服务器设置。
将评审结果以 UTF-8 JSON 原子写入该文件，顶层字段：request_id="{job['id']}", version_id="{job['version_id']}", score（0–100整数）, summary（中文）, issues（数组，每项 severity=high/medium/low、title、detail、quote）, strengths（字符串数组）, suggestions（字符串数组）。
评审对象是已经提交的快照，后续章节编辑不改变本次评审版本。没有额外资料时给出有限的评审，不要阻塞询问。完成后简短回复评审已保存。'''
    result = gateway.request(gateway.base, {'action': 'task', 'source': job['id'], 'conversation': cid, 'input': prompt}, {'Origin': gateway.origin, 'Cookie': gateway.cookie})
    c.execute("UPDATE ai_reviews SET job_id=?,state='queued',updated_at=CURRENT_TIMESTAMP WHERE id=?", (result['id'], job['id'])); c.commit()

def tick(gateway):
    c = repo.connect()
    try:
        jobs = [dict(x) for x in c.execute("SELECT * FROM ai_reviews WHERE state IN ('creating','queued','running') ORDER BY rowid")]
        for job in jobs:
            try:
                if job['state'] == 'creating':
                    submit(gateway, c, job)
                    continue
                path = repo.DB.parent / 'ai-reviews' / (job['id'] + '.report.json')
                if path.is_file():
                    if path.stat().st_size > 250000:
                        raise ValueError('评审文件过大')
                    report = validate_report(json.loads(path.read_text(encoding='utf-8')), job)
                    c.execute('BEGIN IMMEDIATE')
                    repo.add_review(c, job['project_id'], job['section'], job['version_id'], report, 'ai')
                    c.execute("UPDATE ai_reviews SET state='completed',progress='AI 评审已保存',updated_at=CURRENT_TIMESTAMP WHERE id=?", (job['id'],)); c.commit()
                    continue
                snap = gateway.snapshot(job['id'])
                task = next((t for t in snap.get('tasks', []) if t['id'] == job['job_id']), None)
                if task:
                    state = task['state']
                    if state in ['failed', 'cancelled', 'completed', 'delivered']:
                        error = 'AI 任务未生成有效评审文件，请重试' if state == 'completed' else 'AI 评审任务失败或已停止，请查看任务详情'
                        c.execute("UPDATE ai_reviews SET state='failed',error=?,progress=?,link=?,updated_at=CURRENT_TIMESTAMP WHERE id=?", (error, task.get('progress', '')[-12000:], task.get('link', ''), job['id']))
                    else:
                        c.execute('UPDATE ai_reviews SET state=?,progress=?,link=?,updated_at=CURRENT_TIMESTAMP WHERE id=?', ('running' if state == 'running' else 'queued', task.get('progress', '')[-12000:], task.get('link', ''), job['id']))
                    c.commit()
            except (ValueError, KeyError) as e:
                c.rollback(); c.execute("UPDATE ai_reviews SET state='failed',error=?,updated_at=CURRENT_TIMESTAMP WHERE id=?", (str(e)[:1000], job['id'])); c.commit()
    finally:
        c.close()

def start_worker():
    def loop():
        gateway = None
        while True:
            try:
                from learning_worker import recover_feedback
                recover_feedback()
                with closing(repo.connect()) as c:
                    active = c.execute("SELECT 1 FROM ai_reviews WHERE state IN ('creating','queued','running') LIMIT 1").fetchone()
                    active = active or c.execute("SELECT 1 FROM learning_attempts WHERE state IN ('creating','queued','running') LIMIT 1").fetchone()
                if active:
                    if gateway is None:
                        gateway = Gateway()
                    tick(gateway)
                    from learning_worker import tick_learning
                    tick_learning(gateway)
                elif gateway is not None:
                    gateway.close(); gateway = None
            except Exception:
                with closing(repo.connect()) as c, c:
                    c.execute("UPDATE ai_reviews SET progress='校园任务通道暂时不可用，正在重试。',updated_at=CURRENT_TIMESTAMP WHERE state IN ('creating','queued','running')")
                    c.execute("UPDATE learning_attempts SET progress='导师连接暂时不可用，正在重试。你的回答已保存。',updated_at=CURRENT_TIMESTAMP WHERE state IN ('creating','queued','running')")
                if gateway is not None:
                    gateway.close(); gateway = None
                # Network errors are retried. Credentials and task contents never enter logs.
            time.sleep(8)
    threading.Thread(target=loop, name='research-review-sync', daemon=True).start()
