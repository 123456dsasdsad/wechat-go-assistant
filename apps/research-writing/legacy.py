#!/usr/bin/env python3
import json, sqlite3, uuid, re
from pathlib import Path
from http.server import ThreadingHTTPServer, SimpleHTTPRequestHandler
from urllib.parse import urlparse

ROOT = Path(__file__).resolve().parent
DB = ROOT / 'data' / 'research.db'
STATIC = ROOT / 'static'

SCHEMA = '''
CREATE TABLE IF NOT EXISTS projects (
 id TEXT PRIMARY KEY, title TEXT NOT NULL, field TEXT DEFAULT '', stage INTEGER DEFAULT 1,
 created_at TEXT DEFAULT CURRENT_TIMESTAMP, updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS problems (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, phenomenon TEXT, consequence TEXT,
 existing TEXT, gap TEXT, priority INTEGER DEFAULT 0, created_at TEXT DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(project_id) REFERENCES projects(id)
);
CREATE TABLE IF NOT EXISTS hypotheses (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, statement TEXT, condition TEXT,
 prediction TEXT, falsifier TEXT, novelty_type TEXT, status TEXT DEFAULT 'draft',
 created_at TEXT DEFAULT CURRENT_TIMESTAMP, FOREIGN KEY(project_id) REFERENCES projects(id)
);
CREATE TABLE IF NOT EXISTS experiments (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, rq TEXT, variables TEXT, baseline TEXT,
 metrics TEXT, protocol TEXT, status TEXT DEFAULT 'planned', result TEXT, created_at TEXT DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(project_id) REFERENCES projects(id)
);
CREATE TABLE IF NOT EXISTS claims (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, claim TEXT, evidence TEXT,
 alternative TEXT, boundary TEXT, strength TEXT DEFAULT 'needs-evidence', created_at TEXT DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(project_id) REFERENCES projects(id)
);
CREATE TABLE IF NOT EXISTS reviews (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, reviewer TEXT, concern TEXT,
 action TEXT, status TEXT DEFAULT 'open', created_at TEXT DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(project_id) REFERENCES projects(id)
);
CREATE TABLE IF NOT EXISTS reading_cards (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, title TEXT, source TEXT,
 question TEXT, method TEXT, evidence TEXT, limitation TEXT, transfer TEXT,
 created_at TEXT DEFAULT CURRENT_TIMESTAMP, FOREIGN KEY(project_id) REFERENCES projects(id)
);
CREATE TABLE IF NOT EXISTS drafts (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, section TEXT NOT NULL,
 content TEXT DEFAULT '', updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(project_id, section), FOREIGN KEY(project_id) REFERENCES projects(id)
);
CREATE TABLE IF NOT EXISTS draft_versions (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, section TEXT NOT NULL,
 content TEXT DEFAULT '', source TEXT DEFAULT 'manual', created_at TEXT DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(project_id) REFERENCES projects(id)
);
CREATE TABLE IF NOT EXISTS chapter_reviews (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, section TEXT NOT NULL,
 draft_version_id TEXT, score INTEGER DEFAULT 0, summary TEXT DEFAULT '',
 issues_json TEXT DEFAULT '[]', strengths_json TEXT DEFAULT '[]', suggestions_json TEXT DEFAULT '[]',
 created_at TEXT DEFAULT CURRENT_TIMESTAMP, FOREIGN KEY(project_id) REFERENCES projects(id)
);
CREATE TABLE IF NOT EXISTS submission_checks (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, item TEXT NOT NULL,
 status TEXT DEFAULT 'todo', note TEXT DEFAULT '', updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(project_id,item), FOREIGN KEY(project_id) REFERENCES projects(id)
);
CREATE TABLE IF NOT EXISTS members (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, name TEXT NOT NULL,
 role TEXT DEFAULT 'author', note TEXT DEFAULT '', created_at TEXT DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(project_id) REFERENCES projects(id)
);
'''

PAPER_SECTIONS = {
    'title':'标题','abstract':'摘要','introduction':'引言','related_work':'相关工作',
    'problem':'问题定义与假设','method':'方法/系统设计','setup':'实验设置',
    'main_results':'主结果','ablation':'消融与机制分析','generalization':'泛化、效率与失败案例',
    'discussion':'讨论、局限性与伦理影响','conclusion':'结论','references':'参考文献','appendix':'附录与复现材料'
}

def review_chapter(section, content, project_data):
    """Local deterministic chapter reviewer. It produces auditable feedback without external credentials."""
    text = (content or '').strip()
    title = PAPER_SECTIONS.get(section, section)
    issues, strengths, suggestions = [], [], []
    words = len(re.findall(r'[\u4e00-\u9fff]|[A-Za-z0-9]+', text))
    if not text:
        issues.append({'severity':'high','title':'章节为空','detail':'当前章节没有内容，暂时无法判断论证。'})
        suggestions.append('先写出本节要回答的问题，再补充证据和细节。')
    elif words < 45:
        issues.append({'severity':'medium','title':'内容过短','detail':f'当前约 {words} 个中文字或英文词，可能不足以支撑完整论证。'})
        suggestions.append('补充本节的对象、条件、证据和适用边界。')
    else:
        strengths.append('本节已经有可供审阅的文字内容。')
    claim_words = ['提出','说明','表明','优于','提升','有效','发现','证明','we propose','we show','improves']
    evidence_words = ['表','图','实验','数据','结果','基线','指标','显著','table','figure','experiment','dataset']
    if any(w in text for w in claim_words):
        if any(w in text for w in evidence_words): strengths.append('检测到主张与证据相关词，具备进一步核对的基础。')
        else:
            issues.append({'severity':'medium','title':'主张缺少证据指向','detail':'发现了结论性表述，但没有看到表、图、实验或数据指向。'})
            suggestions.append('把每个重要判断绑定到具体表格、图、实验编号或定理。')
    if section in ('abstract','introduction','conclusion') and not any(w in text for w in ['问题','方法','结果','限制','problem','method','result']):
        issues.append({'severity':'medium','title':'主线信息不完整','detail':'本节应同时交代问题、方法、结果或边界。'})
        suggestions.append('按“问题—缺口—方法—证据—边界”检查段落是否齐全。')
    if section in ('main_results','ablation','generalization','setup') and not any(w in text for w in evidence_words):
        issues.append({'severity':'high','title':'实验证据不足','detail':'实验章节没有检测到明确的数据、指标、基线或图表描述。'})
        suggestions.append('补充数据集、基线、指标、运行次数和具体数值，避免只写结论。')
    if section in ('related_work','references') and len(re.findall(r'\b(?:20\d{2}|19\d{2})\b', text)) == 0:
        issues.append({'severity':'low','title':'引用线索不足','detail':'尚未检测到年份或明确的文献引用线索。'})
        suggestions.append('补充最近工作，并说明它们与本文问题、假设和方法的差异。')
    if any(w in text for w in ['显著','大幅','普遍','首次','always','significant','general']):
        suggestions.append('检查强断言是否有统计依据和适用条件；必要时收窄措辞。')
    score = max(0, min(100, 100 - 22*sum(1 for x in issues if x['severity']=='high') - 12*sum(1 for x in issues if x['severity']=='medium') - 5*sum(1 for x in issues if x['severity']=='low') + min(20, len(strengths)*7)))
    summary = f'{title}评审：当前得分 {score}/100。' + (' 建议先处理高严重度问题。' if any(x['severity']=='high' for x in issues) else ' 可以继续补充证据并收窄结论。')
    return {'score':score,'summary':summary,'issues':issues,'strengths':strengths,'suggestions':suggestions}

def recommendations(result):
    tips = []
    if not result.get('reading_cards'): tips.append({'priority':'high','text':'先建立 3–5 张文献阅读卡，找出最近工作和研究缺口。'})
    if not result.get('problems'): tips.append({'priority':'high','text':'记录至少一个带真实证据的现象，再开始写创新。'})
    if not result.get('hypotheses'): tips.append({'priority':'high','text':'写出一个包含预测和反驳条件的创新假设。'})
    if not result.get('experiments'): tips.append({'priority':'high','text':'为创新假设设计 RQ、基线、指标和最小验证实验。'})
    if not result.get('claims'): tips.append({'priority':'medium','text':'把实验结果整理成主张—证据—替代解释—边界。'})
    if not any(x.get('content','').strip() for x in result.get('drafts',[])): tips.append({'priority':'medium','text':'打开“整篇论文”，先写问题定义和实验设置。'})
    if result.get('chapter_reviews') and any(x.get('score',0)<70 for x in result['chapter_reviews']): tips.append({'priority':'high','text':'优先处理得分低于 70 的章节评审意见，再继续润色。'})
    return tips[:6]

def db():
    conn = sqlite3.connect(DB)
    conn.row_factory = sqlite3.Row
    conn.executescript(SCHEMA)
    if conn.execute('SELECT COUNT(*) FROM projects').fetchone()[0] == 0:
        pid = str(uuid.uuid4())
        conn.execute('INSERT INTO projects(id,title,field,stage) VALUES(?,?,?,?)',
                     (pid, '我的第一个研究项目', '待填写研究方向', 1))
        conn.commit()
    return conn

def rowdict(row): return dict(row) if row else None

def parse_body(handler):
    n = int(handler.headers.get('Content-Length', 0) or 0)
    raw = handler.rfile.read(n) if n else b'{}'
    return json.loads(raw.decode('utf-8') or '{}')

def new_id(): return str(uuid.uuid4())

class Handler(SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs): super().__init__(*args, directory=str(STATIC), **kwargs)
    def send_json(self, data, status=200):
        payload = json.dumps(data, ensure_ascii=False).encode('utf-8')
        self.send_response(status); self.send_header('Content-Type','application/json; charset=utf-8')
        self.send_header('Content-Length', str(len(payload))); self.end_headers(); self.wfile.write(payload)
    def do_GET(self):
        p = urlparse(self.path).path
        conn = db()
        try:
            if p == '/api/projects':
                rows = conn.execute('SELECT * FROM projects ORDER BY updated_at DESC').fetchall()
                return self.send_json([dict(x) for x in rows])
            if p.startswith('/api/projects/') and p.endswith('/export'):
                pid = p.split('/')[3]
                project = conn.execute('SELECT * FROM projects WHERE id=?',(pid,)).fetchone()
                if not project: return self.send_json({'error':'项目不存在'},404)
                export = {'project':dict(project)}
                for name in ('reading_cards','problems','hypotheses','experiments','claims','reviews','drafts','draft_versions','chapter_reviews','submission_checks','members'):
                    export[name] = [dict(x) for x in conn.execute(f'SELECT * FROM {name} WHERE project_id=? ORDER BY rowid',(pid,))]
                payload = json.dumps(export,ensure_ascii=False).encode('utf-8')
                self.send_response(200); self.send_header('Content-Type','application/json; charset=utf-8')
                self.send_header('Content-Disposition',f'attachment; filename="research-project-{pid}.json"')
                self.send_header('Content-Length',str(len(payload))); self.end_headers(); self.wfile.write(payload); return
            if p.startswith('/api/projects/'):
                pid = p.split('/')[3]
                project = conn.execute('SELECT * FROM projects WHERE id=?',(pid,)).fetchone()
                if not project: return self.send_json({'error':'项目不存在'},404)
                result = {'project':dict(project)}
                for name in ('reading_cards','problems','hypotheses','experiments','claims','reviews'):
                    result[name] = [dict(x) for x in conn.execute(f'SELECT * FROM {name} WHERE project_id=? ORDER BY created_at DESC',(pid,))]
                result['drafts'] = [dict(x) for x in conn.execute('SELECT * FROM drafts WHERE project_id=? ORDER BY section',(pid,))]
                result['draft_progress'] = {'written': sum(1 for x in result['drafts'] if x.get('content','').strip()), 'total': 14}
                result['draft_versions'] = [dict(x) for x in conn.execute('SELECT * FROM draft_versions WHERE project_id=? ORDER BY created_at DESC',(pid,))]
                result['chapter_reviews'] = []
                for x in conn.execute('SELECT * FROM chapter_reviews WHERE project_id=? ORDER BY created_at DESC',(pid,)):
                    item = dict(x)
                    for k in ('issues_json','strengths_json','suggestions_json'):
                        item[k[:-5]] = json.loads(item.pop(k) or '[]')
                    result['chapter_reviews'].append(item)
                result['submission_checks'] = [dict(x) for x in conn.execute('SELECT * FROM submission_checks WHERE project_id=? ORDER BY id',(pid,))]
                result['members'] = [dict(x) for x in conn.execute('SELECT * FROM members WHERE project_id=? ORDER BY created_at',(pid,))]
                result['recommendations'] = recommendations(result)
                return self.send_json(result)
            if p == '/api/overview':
                return self.send_json({'stages':[
                    {'id':1,'name':'找问题','hint':'从现象、后果和已有解释开始'},
                    {'id':2,'name':'找创新','hint':'提出可反驳的创新假设'},
                    {'id':3,'name':'做实验','hint':'用对照和指标验证假设'},
                    {'id':4,'name':'写论证','hint':'把结果转成主张和证据'},
                    {'id':5,'name':'模拟审稿','hint':'处理质疑并准备投稿'}
                ]})
            return super().do_GET()
        finally: conn.close()
    def do_POST(self):
        p = urlparse(self.path).path; data = parse_body(self); conn = db()
        try:
            if p == '/api/projects':
                pid = new_id(); title = data.get('title','未命名项目').strip() or '未命名项目'
                conn.execute('INSERT INTO projects(id,title,field,stage) VALUES(?,?,?,1)',(pid,title,data.get('field','')))
                conn.commit(); return self.send_json({'id':pid})
            parts = p.split('/')
            if len(parts) >= 5 and parts[1:3] == ['api','projects']:
                pid, kind = parts[3], parts[4]
                if kind == 'stage':
                    stage = max(1,min(8,int(data.get('stage',1))))
                    conn.execute("UPDATE projects SET stage=?,updated_at=CURRENT_TIMESTAMP WHERE id=?",(stage,pid)); conn.commit()
                    return self.send_json({'ok':True})
                if kind == 'meta':
                    conn.execute("UPDATE projects SET field=?,updated_at=CURRENT_TIMESTAMP WHERE id=?",(data.get('field',''),pid)); conn.commit()
                    return self.send_json({'ok':True})
                if kind == 'drafts':
                    section = str(data.get('section','')).strip()
                    content = str(data.get('content',''))
                    if not section: return self.send_json({'error':'缺少章节'},400)
                    conn.execute('''INSERT INTO drafts(id,project_id,section,content,updated_at)
                                    VALUES(?,?,?,?,CURRENT_TIMESTAMP)
                                    ON CONFLICT(project_id,section) DO UPDATE SET content=excluded.content, updated_at=CURRENT_TIMESTAMP''',
                                 (new_id(),pid,section,content))
                    conn.execute('INSERT INTO draft_versions(id,project_id,section,content,source) VALUES(?,?,?,?,?)',
                                 (new_id(),pid,section,content,'manual'))
                    conn.execute("UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=?",(pid,)); conn.commit()
                    return self.send_json({'ok':True})
                if kind == 'chapter-review':
                    section = str(data.get('section','')).strip(); content = str(data.get('content',''))
                    if section not in PAPER_SECTIONS: return self.send_json({'error':'未知论文章节'},400)
                    conn.execute('''INSERT INTO drafts(id,project_id,section,content,updated_at)
                                    VALUES(?,?,?,?,CURRENT_TIMESTAMP)
                                    ON CONFLICT(project_id,section) DO UPDATE SET content=excluded.content, updated_at=CURRENT_TIMESTAMP''',
                                 (new_id(),pid,section,content))
                    version_id = new_id()
                    conn.execute('INSERT INTO draft_versions(id,project_id,section,content,source) VALUES(?,?,?,?,?)',
                                 (version_id,pid,section,content,'review'))
                    report = review_chapter(section, content, {})
                    conn.execute('''INSERT INTO chapter_reviews(id,project_id,section,draft_version_id,score,summary,issues_json,strengths_json,suggestions_json)
                                    VALUES(?,?,?,?,?,?,?,?,?)''',
                                 (new_id(),pid,section,version_id,report['score'],report['summary'],json.dumps(report['issues'],ensure_ascii=False),json.dumps(report['strengths'],ensure_ascii=False),json.dumps(report['suggestions'],ensure_ascii=False)))
                    conn.execute("UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=?",(pid,)); conn.commit()
                    report['version_id'] = version_id
                    return self.send_json(report)
                if kind == 'submission':
                    item = str(data.get('item','')).strip(); status = str(data.get('status','todo'))
                    note = str(data.get('note',''))
                    if not item: return self.send_json({'error':'缺少检查项'},400)
                    conn.execute('''INSERT INTO submission_checks(id,project_id,item,status,note,updated_at)
                                    VALUES(?,?,?,?,?,CURRENT_TIMESTAMP)
                                    ON CONFLICT(project_id,item) DO UPDATE SET status=excluded.status,note=excluded.note,updated_at=CURRENT_TIMESTAMP''',
                                 (new_id(),pid,item,status,note))
                    conn.commit(); return self.send_json({'ok':True})
                if kind == 'members':
                    name = str(data.get('name','')).strip()
                    if not name: return self.send_json({'error':'请填写成员姓名'},400)
                    conn.execute('INSERT INTO members(id,project_id,name,role,note) VALUES(?,?,?,?,?)',
                                 (new_id(),pid,name,data.get('role','author'),data.get('note','')))
                    conn.commit(); return self.send_json({'ok':True})
                if kind in ('reading_cards','problems','hypotheses','experiments','claims','reviews'):
                    allowed = {
                      'reading_cards':['title','source','question','method','evidence','limitation','transfer'],
                      'problems':['phenomenon','consequence','existing','gap','priority'],
                      'hypotheses':['statement','condition','prediction','falsifier','novelty_type','status'],
                      'experiments':['rq','variables','baseline','metrics','protocol','status','result'],
                      'claims':['claim','evidence','alternative','boundary','strength'],
                      'reviews':['reviewer','concern','action','status']
                    }[kind]
                    vals = [data.get(k,'') for k in allowed]
                    if kind == 'problems' and not vals[0]: return self.send_json({'error':'请填写现象'},400)
                    cols = ','.join(['id','project_id']+allowed); qs=','.join(['?']*(len(allowed)+2))
                    conn.execute(f'INSERT INTO {kind}({cols}) VALUES({qs})',[new_id(),pid]+vals)
                    conn.execute("UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=?",(pid,)); conn.commit()
                    return self.send_json({'ok':True})
            return self.send_json({'error':'未知请求'},404)
        except Exception as e:
            conn.rollback(); return self.send_json({'error':str(e)},500)
        finally: conn.close()
    def log_message(self, fmt, *args):
        pass

if __name__ == '__main__':
    db().close()
    port = 8765
    print(f'研究写作训练系统运行于 http://127.0.0.1:{port}')
    ThreadingHTTPServer(('127.0.0.1', port), Handler).serve_forever()
