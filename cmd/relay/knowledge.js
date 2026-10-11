let knowledge,knowledgeGeneration=0;
const mapKinds={topic:'研究类别',problem:'子问题',method:'方法',limitation:'局限 / 待核实缺口',direction:'候选方向',paper:'文献索引'};
const mapBasis={author_report:'原文描述',system_synthesis:'资料库推断',hypothesis:'待验证假设',index:'索引，尚不代表方法精读'};
const mapLinkKinds={combination:'方法组合',transfer:'应用迁移',disagreement:'条件与分歧',constraint:'约束变化',shared_paper:'同一论文关联'};
function mapNodeTitle(n){return (n.annotation?.pinned?'★ ':'')+(n.annotation?.title||n.title)}
async function loadKnowledge(){let generation=++knowledgeGeneration;let next=await lib('map');if(generation!==knowledgeGeneration)return;knowledge=next;$('#map-disclosure').textContent=next.disclosure;renderKnowledge()}
function renderKnowledge(){
 if(!knowledge)return;
 let box=$('#map-tree');box.replaceChildren();box.append(el('h3','研究资料库','map-root'));
 let query=$('#map-query').value.trim().toLowerCase(),pins=$('#map-pins').checked,domains=new Map(),shown=0;
 for(let status of knowledge.branches){
  let b=status.branch;
  if(pins&&!b.nodes.some(n=>n.annotation?.pinned))continue;
  let match=[b.topic,b.domain,b.group,...b.nodes.map(n=>mapNodeTitle(n)+' '+n.text+' '+(n.direction?.question||''))].join(' ').toLowerCase();
  if(query&&!match.includes(query))continue;shown++;
  let domain=domains.get(b.domain);
  if(!domain){let d=el('details');d.open=true;d.append(el('summary',b.domain));let ul=el('ul');d.append(ul);box.append(d);domain={ul,groups:new Map()};domains.set(b.domain,domain)}
  let group=domain.groups.get(b.group);
  if(!group){let li=el('li'),d=el('details');d.open=true;d.append(el('summary',b.group));group=el('ul');d.append(group);li.append(d);domain.ul.append(li);domain.groups.set(b.group,group)}
  let li=el('li'),d=el('details');d.open=!!query;let root=b.nodes.find(n=>n.key==='root');
  d.append(el('summary',(root?mapNodeTitle(root):b.topic)+' · '+status.topic.count+' 篇'+(status.dirty?' · 待整理':'')));
  let controls=el('div',undefined,'row');
  controls.append(button('查看类别',()=>showMapNode(b,root||{key:'root',kind:'topic',title:b.topic,text:'',material_ids:[],evidence:[]})),button('查看综述',()=>{switchView('library');return showReview(b.topic)}));
  if(status.last_error)controls.append(el('span','更新未完成：'+status.last_error,'error'),button('重试整理',async()=>{await api('library_update',{name:b.topic});notice('已安排知识树与综述更新。')}));
  d.append(controls);
  let byParent=new Map();for(let n of b.nodes){if(n.key==='root')continue;if(!byParent.has(n.parent))byParent.set(n.parent,[]);byParent.get(n.parent).push(n)}
  function children(parent){let ul=el('ul');for(let n of byParent.get(parent)||[]){let item=el('li'),kids=byParent.get(n.key)||[];if(kids.length){let child=el('details');child.open=!!query;let summary=el('summary',mapNodeTitle(n));summary.append(el('span',mapKinds[n.kind],'map-kind'));child.append(summary,button('查看依据',()=>showMapNode(b,n)),children(n.key));item.append(child)}else{let choose=button(mapNodeTitle(n),()=>showMapNode(b,n),n.kind+'-button');choose.append(el('span',mapKinds[n.kind],'map-kind'));item.append(choose)}ul.append(item)}return ul}
  d.append(children('root'));li.append(d);group.append(li);
 }
 if(!shown)box.append(el('p',knowledge.branches.length?'没有匹配的分支，请清空筛选或关闭置顶筛选。':'先在“资料与综述”导入论文，再更新知识树。','muted'));
}
function mapField(box,title,value){if(!value?.length)return;let field=el('div',undefined,'map-field');field.append(el('h3',title));if(Array.isArray(value)){let ul=el('ul');for(let x of value)ul.append(el('li',x));field.append(ul)}else field.append(el('p',value));box.append(field)}
async function showMapNode(b,n,historical=false){
 let box=$('#map-detail');box.replaceChildren();
 box.append(el('p',(mapKinds[n.kind]||'节点')+' · '+(mapBasis[n.basis]||'索引'),'muted'),el('h2',mapNodeTitle(n),n.direction?'hypothesis':''),el('p',b.topic+' · 知识树 v'+b.version+' · 综述 v'+b.review_version+(historical?' · 历史快照':''),'muted'));
 if(n.text)box.append(el('p',n.text));
 if(n.kind==='topic'){let currentKeys=new Set(b.nodes.map(x=>x.key));for(let [key,a] of Object.entries(b.annotations||{})){if(currentKeys.has(key))continue;mapField(box,'保留的个人笔记 / idea · '+(a.title||key),[a.notes,a.idea].filter(Boolean).join('\n'));}}
 if(n.direction){let d=n.direction;mapField(box,'核心问题',d.question);mapField(box,'库内近邻工作',d.nearest_work);mapField(box,'拟验证的改变',d.change);mapField(box,'最小基线',d.baseline);mapField(box,'数据与待核实条件',d.dataset);mapField(box,'评价指标',d.metrics);mapField(box,'最小实验步骤',d.steps);mapField(box,'风险',d.risks);mapField(box,'否证标准',d.falsification);mapField(box,'新颖性核实',d.verification);if(!historical)box.append(button('单独创建研究任务',async()=>{let v=await api('direction_task',{library:{collection:b.topic,key:n.key}});notice('已创建独立研究会话，使用 gpt-6.1-sol。当前会话保持不变。任务 '+v.id.slice(0,8));await load()},'primary'))}
 let evidence=el('div',undefined,'map-field');evidence.append(el('h3','原文依据与阅读范围'));
 for(let ref of n.evidence||[]){let m=b.references?.find(x=>x.id===ref.material_id),s=m?.sources?.[ref.source];evidence.append(el('blockquote',ref.excerpt),el('p','资料 '+ref.material_id+' · '+ref.locator+' · '+(readingLabels[s?.reading_scope]||s?.reading_scope||'阅读范围待核实'),'muted'))}
 for(let id of n.material_ids||[])evidence.append(button('查看资料 '+id,async()=>{switchView('library');await showMaterial(id)}));
 if(n.kind==='topic')for(let id of b.coverage||[])evidence.append(button('资料 '+id,async()=>{switchView('library');await showMaterial(id)}));box.append(evidence);
 let links=(b.links||[]).filter(x=>x.from===n.key);
 if(links.length){let cross=el('div',undefined,'map-field');cross.append(el('h3','跨分支联系'));for(let link of links){cross.append(el('p',(mapLinkKinds[link.kind]||'关联')+'：'+link.reason),button('探索 '+link.target_topic,()=>{$('#map-query').value=link.target_topic;renderKnowledge();$('#map-tree').scrollIntoView({block:'start'})}))}box.append(cross)}
 let history=el('div',undefined,'row');
 if(b.version>1)history.append(button('查看上一版',async()=>{let previous=await lib('map_branch',{collection:b.topic,version:b.version-1});let node=previous.nodes.find(x=>x.key===n.key)||previous.nodes.find(x=>x.key==='root');await showMapNode(previous,node,true)}));
 if(historical)history.append(button('回到当前版',async()=>{await loadKnowledge();let current=knowledge.branches.find(x=>x.branch.topic===b.topic)?.branch;if(current)await showMapNode(current,current.nodes.find(x=>x.key===n.key)||current.nodes[0])}));
 if(b.changes?.length){let change=el('details');change.append(el('summary','本次变化'),el('p',b.changes.join('\n')));box.append(change)}box.append(history);
 if(!historical){
  let form=el('div',undefined,'map-field');form.append(el('h3','我的名称、笔记与 idea'),el('p','独立保存，自动更新不会改写这些内容。','muted'));
  let title=el('input');title.value=n.annotation?.title||'';title.setAttribute('aria-label','自定义名称');title.placeholder='自定义名称（留空使用原名）';
  let notes=el('textarea');notes.value=n.annotation?.notes||'';notes.setAttribute('aria-label','个人笔记');notes.placeholder='个人笔记';
  let idea=el('textarea');idea.value=n.annotation?.idea||'';idea.setAttribute('aria-label','个人研究idea');idea.placeholder='你的研究 idea，与系统假设分开保存';
  let pinned=el('input');pinned.type='checkbox';pinned.style.width='auto';pinned.checked=!!n.annotation?.pinned;let label=el('label');label.append(pinned,document.createTextNode(' 置顶标记'));
  form.append(title,notes,idea,label,button('保存我的笔记',async()=>{await lib('map_annotation',{collection:b.topic,key:n.key,annotation:{title:title.value,notes:notes.value,idea:idea.value,pinned:pinned.checked}});notice('名称、笔记和 idea 已保存，没有调用AI。');await loadKnowledge();let current=knowledge.branches.find(x=>x.branch.topic===b.topic)?.branch;if(current)await showMapNode(current,current.nodes.find(x=>x.key===n.key)||current.nodes[0])}));box.append(form);
 }
 if(window.innerWidth<761)box.scrollIntoView({block:'start',behavior:'auto'});
}
$('#map-refresh').onclick=()=>action(loadKnowledge);$('#map-query').oninput=renderKnowledge;$('#map-pins').onchange=renderKnowledge;
$('#map-update').onclick=()=>action(async()=>{await api('library_update',{name:'*'});notice('已安排更新所有待整理分支，使用 gpt-6.1-sol。已有内容和个人笔记会保留。')});
$('#map-export').onclick=()=>{if(knowledge)downloadText('研究知识树.json',JSON.stringify(knowledge,null,2))};
