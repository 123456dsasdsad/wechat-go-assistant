export const nav = [
  { id:'overview', label:'项目总览', icon:'grid' },
  { id:'learn', label:'写作入门', icon:'book' },
  { id:'library', label:'文献资料', icon:'book' },
  { id:'ideas', label:'问题与假设', icon:'bulb' },
  { id:'experiments', label:'实验与证据', icon:'flask' },
  { id:'claims', label:'主张与论证', icon:'link' },
  { id:'writing', label:'论文写作', icon:'pen' },
  { id:'reviews', label:'审稿与修订', icon:'review' },
  { id:'submission', label:'投稿检查', icon:'check' },
  { id:'members', label:'项目协作', icon:'file' },
]
export const definitions = {
  reading_cards:{title:'文献阅读卡', lead:'让每篇论文留下可迁移的方法、证据和局限。', fields:[['title','文献标题'],['source','来源 / DOI / 链接'],['question','研究问题'],['method','方法与机制'],['evidence','证据与结果'],['limitation','局限与边界'],['transfer','对本项目的启发']]},
  problems:{title:'研究问题',lead:'从真实现象开始，写清已有解释和仍未解决的缺口。',fields:[['phenomenon','观察到的现象'],['consequence','为什么值得解决'],['existing','已有解释与相关方法'],['gap','仍存在的缺口'],['priority','优先级（数字）']]},
  hypotheses:{title:'创新假设',lead:'把创新写成可以检查、也可以被推翻的判断。',fields:[['statement','假设'],['condition','适用条件'],['prediction','预期结果'],['falsifier','反驳条件'],['novelty_type','创新类型'],['status','状态','select',['draft','testing','supported','refuted']]]},
  experiments:{title:'实验与证据',lead:'每次实验回答一个问题，保留比较设置、结果和失败案例。',fields:[['rq','研究问题 / 实验名称'],['variables','变量与对照'],['baseline','基线'],['metrics','评价指标'],['protocol','数据、训练预算与复现协议'],['status','状态','select',['planned','running','completed','failed']],['result','结果 / 图表 / 文件位置']]},
  claims:{title:'主张与论证',lead:'每一条主张都应指向证据，并说明其他解释和适用边界。',fields:[['claim','主张'],['evidence','证据 / 图表 / 实验编号'],['alternative','替代解释'],['boundary','适用边界'],['strength','证据强度','select',['needs-evidence','supported','limited']]]},
  reviews:{title:'审稿与修订',lead:'记录导师、同伴和审稿人的意见，跟踪回应与修改。',fields:[['reviewer','审阅者'],['concern','意见与质疑'],['action','回应 / 修改计划'],['status','状态','select',['open','in-progress','resolved']]]},
  members:{title:'项目协作',lead:'记录成员职责和负责内容。此页用于分工记录。',fields:[['name','成员姓名'],['role','角色'],['note','负责内容']]},
}
export const statusLabels = {draft:'草案',testing:'验证中',supported:'有证据支持',refuted:'已反驳',planned:'待开展',running:'执行中',completed:'已完成',failed:'失败',open:'待处理','in-progress':'处理中',resolved:'已处理','needs-evidence':'待补证据',limited:'有限支持'}
export const submissionItems = ['研究问题、贡献和边界清晰','最近工作和引用已核验','基线、指标与训练预算公平','主结果、消融和失败分析齐全','匿名、页数和模板符合要求','代码、数据、许可证和伦理声明齐全']
