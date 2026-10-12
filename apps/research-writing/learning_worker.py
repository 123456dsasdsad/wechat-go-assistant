"""AI tutor feedback uses its own conversations and files, independent of reviews."""
import json
import hashlib
import repository as repo
from tutor import LESSONS

def submit_task(gateway, c, attempt):
    project = c.execute('SELECT * FROM projects WHERE id=?', (attempt['project_id'],)).fetchone()
    cid = project['learning_conversation']
    if not cid:
        before = gateway.snapshot()
        gateway.call('conversation', input='新建会话 研序写作导师-' + attempt['project_id'][:8])
        cid = gateway.snapshot()['current']
        c.execute('UPDATE projects SET learning_conversation=? WHERE id=?', (cid, attempt['project_id'])); c.commit()
        if before.get('current') and before['current'] != cid:
            gateway.call('conversation', input='继续会话 ' + before['current'])
    fallback = attempt.get('fallback_count',0)>0
    gateway.call('profile', conversation=cid, model='gpt-6-luna' if fallback else 'gpt-6.1-sol', effort='low' if fallback else 'medium', profile={})
    directory = (repo.DB.parent / 'writing-tutor').resolve()
    src = directory / (attempt['id'] + '.input.json')
    output = directory / (attempt['id'] + '.report.json')
    prompt = f'''你是研序的零基础论文写作导师。本次学习任务 {attempt['id']}。只读取 {src}。
学生选择从“如何写论文”开始。你只处理当前输入文件中的本课目标、教学例子和学生回答，不读取或使用其他微信/Codex会话的实验、结果和上下文。
用简单中文，先肯定一个具体做对的点，再最多指出两个与本课目标相关的问题。每个问题给一个小例子和可以马上做的改法。
不要替学生写完整论文，不要求零基础学生达到正式投稿标准，不布置超出本课范围的统计、创新或实验要求。没有真实结果时教他明确写尚待验证，不要编造效果、文献或数据。
intent=practice 时按本课 rubric 判断是否掌握入门目标；清楚表达且符合本课目标即可 passed=true。需要修改时 passed=false，并只提示下一步如何改。intent=question 时回答学生困惑，passed 必须为 false，不改变课程进度。
不要自动开始下一课，不继续其他科研任务，不改项目正文或系统设置。不调用需要等待其他会话的询问工具，把问题与下一步练习写入反馈文件。学生下一次回答到达后才能继续辅导。
只允许写本次反馈文件 {output}，以 UTF-8 JSON 原子写入。字段 request_id="{attempt['id']}", lesson_id="{attempt['lesson_id']}", passed（布尔）, feedback（中文，约150–350字）, example（一小段示例，示例数据必须明确虚构）, next_tip（一个小练习或修改动作）。完成后简短说明反馈已保存。'''
    source=attempt['id'] if not fallback else hashlib.sha256((attempt['id']+':fallback').encode()).hexdigest()[:24]
    result = gateway.request(gateway.base, {'action':'task','source':source,'conversation':cid,'input':prompt}, {'Origin':gateway.origin,'Cookie':gateway.cookie})
    c.execute("UPDATE learning_attempts SET state='queued',job_id=?,updated_at=CURRENT_TIMESTAMP WHERE id=?", (result['id'], attempt['id'])); c.commit()

def validate_feedback(raw, attempt):
    if raw.get('request_id') != attempt['id'] or raw.get('lesson_id') != attempt['lesson_id']:
        raise ValueError('导师反馈与本次练习不匹配')
    if not isinstance(raw.get('passed'), bool) or not isinstance(raw.get('feedback'), str) or not raw['feedback'].strip():
        raise ValueError('导师反馈格式不完整，请重试')
    return {'passed':raw['passed'] if attempt['intent']=='practice' else False,
            'feedback':raw['feedback'][:6000], 'example':str(raw.get('example',''))[:5000], 'next_tip':str(raw.get('next_tip',''))[:2000]}

def save_feedback(c,a,feedback):
    c.execute('BEGIN IMMEDIATE')
    c.execute("UPDATE learning_attempts SET state='completed',feedback_json=?,error='',progress='导师反馈已保存',updated_at=CURRENT_TIMESTAMP WHERE id=?", (json.dumps(feedback,ensure_ascii=False),a['id']))
    if feedback['passed']:
        c.execute('INSERT OR IGNORE INTO learning_progress(project_id,lesson_id) VALUES(?,?)', (a['project_id'],a['lesson_id']))
    c.commit()

def recover_feedback():
    """An agent may correct a malformed intermediate file before finishing."""
    c=repo.connect()
    try:
        rows=[dict(x) for x in c.execute("SELECT * FROM learning_attempts WHERE state='failed'")]
        for a in rows:
            path=repo.DB.parent/'writing-tutor'/(a['id']+'.report.json')
            if not path.is_file() or path.stat().st_size>150000:
                continue
            try:
                feedback=validate_feedback(json.loads(path.read_text(encoding='utf-8')),a)
            except (ValueError,KeyError):
                continue
            save_feedback(c,a,feedback)
    finally:
        c.close()

def tick_learning(gateway):
    c = repo.connect()
    try:
        attempts = [dict(x) for x in c.execute("SELECT * FROM learning_attempts WHERE state IN ('creating','queued','running') ORDER BY rowid")]
        for a in attempts:
            try:
                if a['state']=='creating':
                    submit_task(gateway, c, a); continue
                path = repo.DB.parent / 'writing-tutor' / (a['id']+'.report.json')
                if path.is_file():
                    if path.stat().st_size>150000:
                        raise ValueError('导师反馈文件过大，请重试')
                    feedback = validate_feedback(json.loads(path.read_text(encoding='utf-8')), a)
                    save_feedback(c,a,feedback);continue
                task = next((x for x in gateway.snapshot(a['id']).get('tasks',[]) if x['id']==a['job_id']),None)
                if task:
                    state=task['state']
                    if state in ['failed','cancelled','completed','delivered']:
                        if task.get('error')=='codex_server_overloaded' and not a.get('fallback_count',0):
                            c.execute("UPDATE learning_attempts SET state='creating',fallback_count=1,job_id='',progress='当前导师繁忙，正在重试。你的回答已保留。',updated_at=CURRENT_TIMESTAMP WHERE id=?",(a['id'],))
                        else:
                            c.execute("UPDATE learning_attempts SET state='failed',error='导师暂未返回有效反馈。你的回答已保存，可以重新提交。',updated_at=CURRENT_TIMESTAMP WHERE id=?",(a['id'],))
                    else:
                        c.execute('UPDATE learning_attempts SET state=?,progress=?,updated_at=CURRENT_TIMESTAMP WHERE id=?',('running' if state=='running' else 'queued',task.get('progress','')[-8000:],a['id']))
                    c.commit()
            except (ValueError,KeyError) as e:
                c.rollback()
                task=next((x for x in gateway.snapshot(a['id']).get('tasks',[]) if x['id']==a['job_id']),None) if a['job_id'] else None
                if task and task['state'] not in ['failed','cancelled','completed','delivered']:
                    c.execute("UPDATE learning_attempts SET progress='导师正在整理反馈，请稍候。',updated_at=CURRENT_TIMESTAMP WHERE id=?",(a['id'],))
                else:
                    c.execute("UPDATE learning_attempts SET state='failed',error='导师反馈格式暂不完整。你的回答已保存，可以稍后重试。',updated_at=CURRENT_TIMESTAMP WHERE id=?",(a['id'],))
                c.commit()
    finally:
        c.close()
