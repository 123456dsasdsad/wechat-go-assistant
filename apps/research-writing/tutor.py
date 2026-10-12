"""Beginner writing lessons and durable practice/feedback state."""
import json
import secrets
import repository as repo

LESSONS = [
    {'id':'paper-purpose','title':'论文到底在说什么','time':'5 分钟','goal':'用问题、方法、证据和边界理解一篇论文。','explain':['论文是在回答一个值得研究的问题。它需要说明：问题是什么、采用了什么方法、有哪些证据、结论在什么条件下成立。','“我用了一个复杂模型”只是方法线索。读者还需要知道为什么要用它，以及怎样检查它有没有作用。'],'example':'练习例子：想知道两个杯子哪个保温更好。问题是水温下降的差异；方法是相同条件下记录温度；证据是记录的数据；边界是只测试了这两个杯子和这组条件。这里没有实际实验结果。','type':'quiz','prompt':'准备写论文时，最先要说清什么？','options':[{'id':'question','text':'我想回答什么问题，为什么值得回答'},{'id':'tool','text':'我要使用多复杂的模型或软件'},{'id':'layout','text':'先把格式排漂亮，再考虑研究内容'}],'answer':'question','feedback':'先把问题说清，方法、实验和写作才能围绕同一件事展开。'},
    {'id':'one-question','title':'写出第一句研究问题','time':'8 分钟','goal':'把宽泛兴趣写成读者能理解的问题。','explain':['研究主题是一个领域，例如“保温材料”。研究问题是一句可以通过观察或比较回答的话。','先写对象，再写现象，最后说明你要比较或解释什么。现在不需要提出创新，也不需要使用专业术语。'],'example':'示例：“在相同起始水温与室温下，杯子 A 和杯子 B 在一小时后的温度下降有多大差异？”这是一个待验证的问题，不是结论。','type':'text','prompt':'写一句你想研究的问题。可以使用杯子例子，也可以写自己的主题。','rubric':'适合入门：有研究对象，并说明要回答或比较什么。暂不要求创新性、正式符号、完整数据集或统计设计。'},
    {'id':'paper-map','title':'认识论文的几个房间','time':'6 分钟','goal':'知道内容应该写在哪个章节。','explain':['引言说“为什么值得做”；方法说“具体怎么做”；结果说“观察到了什么”；讨论说“这些观察能说明什么，有什么限制”。','摘要是整篇论文的短地图。初学时先搭这条主线，再逐步学习相关工作、实验设置和参考文献。'],'example':'“两个杯子的初始水温相同，每十分钟测量一次”属于方法/实验设置。“一小时后记录到了某个温度”属于结果，必须有真实记录才可填写。','type':'quiz','prompt':'“每十分钟测量一次温度”主要应写在哪一部分？','options':[{'id':'method','text':'方法或实验设置：交代具体怎么做'},{'id':'result','text':'结果：直接证明某个杯子更好'},{'id':'conclusion','text':'结论：把测量步骤当作发现'}],'answer':'method','feedback':'测量频率是过程信息。它让别人理解和复核你怎样得到结果。'},
    {'id':'method-sentence','title':'把方法讲给一个新同学','time':'8 分钟','goal':'用输入、步骤和输出讲清过程。','explain':['方法段落回答“别人怎样照着做”。先说明处理的对象，再说明关键步骤，最后说明得到什么记录或输出。','先用平实的话写清过程，再学习公式、算法图和参数。不要为了显得专业而加入自己还不能解释的词。'],'example':'示例：“把两种杯子装入等量、相同起始温度的水；放在同一房间并每十分钟测量一次；记录一小时内的温度变化。”这里只是拟定过程。','type':'text','prompt':'写 2–3 句话说明你准备怎么做，包含对象、主要步骤和输出。','rubric':'入门方法说明：能辨认处理对象、至少一个可理解的步骤、预计记录或输出。只要求读者理解过程，不要求完整论文级复现实验。'},
    {'id':'evidence-boundary','title':'分清观察和结论','time':'5 分钟','goal':'让结论与实际证据相匹配。','explain':['观察是你实际记录到的事情。结论是对观察的解释；结论的范围不能超过证据的范围。','没有做完实验，可以写“拟检验”“尚待验证”，不能把预期效果写成已经发生的结果。'],'example':'虚构教学例子：只在一种室温下测了两个杯子。即使其中一个温度下降较慢，也只能报告这组条件下的观察，不能直接断言它在所有环境里都最好。','type':'quiz','prompt':'只有一次、一个室温下的测量，哪句话更合适？','options':[{'id':'bounded','text':'在本次测量条件下，观察到两种杯子的温度变化不同'},{'id':'universal','text':'已经证明这个杯子在任何环境下都最好'},{'id':'expected','text':'虽然没有数据，但预期的提升可以先写成实验结果'}],'answer':'bounded','feedback':'限定条件能让读者判断证据支持到哪里。预期与实际结果应分别说明。'},
    {'id':'first-introduction','title':'写自己的第一小段引言','time':'10 分钟','goal':'用三句话说明场景、问题和准备做的事。','explain':['第一句交代场景：谁会遇到这件事？第二句交代问题：现在哪一点还不清楚？第三句交代计划：你准备如何检查？','这一轮只练逻辑。可以用普通话表达，先不追求完整论文和漂亮措辞。'],'example':'教学示例：“日常饮用热水时，杯子的保温能力会影响使用体验。目前我们还不清楚两种杯子在相同条件下的温度变化差异。为比较这一点，我们拟记录它们一小时内的温度变化。”','type':'text','prompt':'写一个 3–5 句话的小段落：场景 → 问题 → 你的检查计划。','rubric':'场景、问题、检查计划之间有可理解的联系；计划仍作为计划，没有编造结果。不要按完整投稿论文要求评分。'},
    {'id':'small-abstract','title':'拼出第一份摘要草稿','time':'10 分钟','goal':'把问题、方法、结果状态和边界放进四句话。','explain':['摘要可以先用四句话搭骨架：研究什么问题；准备或实际采用什么方法；目前有哪些结果；结论适用于什么条件。','如果还没有结果，练习时明确写“结果尚待验证”。这是一份练习草稿，正式摘要应在结果确认后再改写。'],'example':'练习模板：“我们关注……。拟通过……检查这个问题。目前结果尚待验证，因此暂不做效果结论。后续结论将限定在……条件下。”','type':'text','prompt':'用四句话写一份摘要练习。没有真实结果时，请明确写出当前状态。','rubric':'有问题、方法、结果或尚未有结果的说明、适用范围；允许粗略表达。不要求真实实验已经完成，不允许虚构结果。'},
    {'id':'sources-next','title':'知道哪些话需要来源','time':'6 分钟','goal':'区分自己的观察、别人的结论和未证实的判断。','explain':['引用让读者知道外部信息从哪里来。引用的论文应该真正支持你使用的那句话，而不只是标题看起来相关。','入门阶段先养成记录来源的习惯。自己的实验也要保留数据、条件和分析过程，方便回查。'],'example':'“已有研究普遍认为材料 X 更好”需要合适来源；“在我们的这次测量中观察到……”需要自己的测量记录。两者的依据不同。','type':'quiz','prompt':'引用一篇论文时，最需要检查什么？','options':[{'id':'supports','text':'原文是否真的支持我要写的这句话'},{'id':'title','text':'论文标题相似就可以直接当作证据'},{'id':'quantity','text':'参考文献数量越多，结论就一定越可靠'}],'answer':'supports','feedback':'来源与具体主张相匹配，比只增加引用数量更有用。下一步可把自己的练习修改后放入正文，再逐渐补齐证据。'},
]

def setup(c):
    c.executescript('''
    CREATE TABLE IF NOT EXISTS learning_progress (
      project_id TEXT NOT NULL, lesson_id TEXT NOT NULL, completed_at TEXT DEFAULT CURRENT_TIMESTAMP,
      PRIMARY KEY(project_id,lesson_id));
    CREATE TABLE IF NOT EXISTS learning_positions (
      project_id TEXT PRIMARY KEY, lesson_id TEXT NOT NULL, updated_at TEXT DEFAULT CURRENT_TIMESTAMP);
    CREATE TABLE IF NOT EXISTS learning_attempts (
      id TEXT PRIMARY KEY, project_id TEXT NOT NULL, lesson_id TEXT NOT NULL, intent TEXT NOT NULL,
      answer TEXT NOT NULL, state TEXT NOT NULL, feedback_json TEXT DEFAULT '{}',
      job_id TEXT DEFAULT '', progress TEXT DEFAULT '', error TEXT DEFAULT '',
      created_at TEXT DEFAULT CURRENT_TIMESTAMP, updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
      request_id TEXT NOT NULL, fallback_count INTEGER NOT NULL DEFAULT 0, UNIQUE(project_id,request_id));
    ''')
    if 'fallback_count' not in {x['name'] for x in c.execute('PRAGMA table_info(learning_attempts)')}:
        c.execute('ALTER TABLE learning_attempts ADD COLUMN fallback_count INTEGER NOT NULL DEFAULT 0')

def snapshot(c, pid, full=False):
    completed = [r[0] for r in c.execute('SELECT lesson_id FROM learning_progress WHERE project_id=?', (pid,))]
    first = next((x['id'] for x in LESSONS if x['id'] not in completed), LESSONS[-1]['id'])
    public = [{k:v for k,v in lesson.items() if k not in ['answer','feedback','rubric']} for lesson in LESSONS]
    attempts = []
    for row in c.execute('SELECT * FROM learning_attempts WHERE project_id=? ORDER BY rowid DESC'+('' if full else ' LIMIT 80'), (pid,)):
        a = dict(row); a['feedback'] = json.loads(a.pop('feedback_json') or '{}'); attempts.append(a)
    position=c.execute('SELECT lesson_id FROM learning_positions WHERE project_id=?',(pid,)).fetchone()
    current=position[0] if position else attempts[0]['lesson_id'] if attempts else first
    return {'title':'从零开始写论文','lessons':public,'completed':completed,'current':current,'attempts':attempts}

def submit(c, pid, body):
    lesson_id = str(body.get('lesson_id', ''))
    lesson = next((x for x in LESSONS if x['id'] == lesson_id), None)
    if not lesson:
        raise ValueError('学习步骤不存在')
    complete = {r[0] for r in c.execute('SELECT lesson_id FROM learning_progress WHERE project_id=?', (pid,))}
    before = LESSONS[:LESSONS.index(lesson)]
    if any(x['id'] not in complete for x in before):
        raise ValueError('先完成前一小步，再继续这一课')
    if body.get('action')=='select':
        c.execute('INSERT INTO learning_positions(project_id,lesson_id) VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET lesson_id=excluded.lesson_id,updated_at=CURRENT_TIMESTAMP',(pid,lesson_id))
        return {'ok':True,'selected':lesson_id}
    request_id = str(body.get('request_id', ''))[:100]
    if not request_id:
        raise ValueError('缺少操作编号，请刷新页面')
    answer = str(body.get('answer', '')).strip()
    intent = 'question' if body.get('intent') == 'question' else 'practice'
    if not answer or len(answer) > 15000:
        raise ValueError('请填写你的回答或问题（最多 15000 字）')
    previous = c.execute('SELECT * FROM learning_attempts WHERE project_id=? AND request_id=?', (pid, request_id)).fetchone()
    if previous:
        if previous['lesson_id'] != lesson_id or previous['answer'] != answer or previous['intent'] != intent:
            raise ValueError('同一次提交不能更换内容')
        return {'ok':True,'id':previous['id'],'state':previous['state']}
    if c.execute("SELECT 1 FROM learning_attempts WHERE project_id=? AND state IN ('creating','queued','running')", (pid,)).fetchone():
        raise ValueError('导师正在回复，请等反馈后再提交')
    aid = secrets.token_hex(12)
    state = 'creating'
    feedback = {}
    if intent == 'practice' and lesson['type'] == 'quiz':
        if answer not in {x['id'] for x in lesson['options']}:
            raise ValueError('请选择一个选项')
        passed = answer == lesson['answer']
        feedback = {'passed':passed,'feedback':lesson['feedback'] if passed else '先回看本课例子：'+lesson['feedback'], 'example':lesson['example'], 'next_tip':'理解后再试一次。' if not passed else '这一小步已完成，你可以手动进入下一课。'}
        state = 'completed'
        if passed:
            c.execute('INSERT OR IGNORE INTO learning_progress(project_id,lesson_id) VALUES(?,?)', (pid, lesson_id))
    c.execute('INSERT INTO learning_attempts(id,project_id,lesson_id,intent,answer,state,feedback_json,request_id) VALUES(?,?,?,?,?,?,?,?)', (aid, pid, lesson_id, intent, answer, state, json.dumps(feedback, ensure_ascii=False), request_id))
    c.execute('INSERT INTO learning_positions(project_id,lesson_id) VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET lesson_id=excluded.lesson_id,updated_at=CURRENT_TIMESTAMP',(pid,lesson_id))
    if state == 'creating':
        directory = repo.DB.parent / 'writing-tutor'; directory.mkdir(parents=True, exist_ok=True)
        value = {'request_id':aid,'lesson_id':lesson_id,'intent':intent,'lesson':lesson,'answer':answer}
        (directory / (aid+'.input.json')).write_text(json.dumps(value,ensure_ascii=False,indent=2),encoding='utf-8')
    return {'ok':True,'id':aid,'state':state}
