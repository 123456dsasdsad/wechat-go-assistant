<script setup>
import { computed } from 'vue'
import Icon from './Icon.vue'
const props=defineProps({data:Object,reports:Array})
const emit=defineEmits(['go','write','stage'])
const chain=computed(()=>[
  {label:'研究问题',title:props.data.problems[0]?.phenomenon||'记录一个真实问题',hint:props.data.problems.length+' 条问题',view:'ideas'},
  {label:'创新假设',title:props.data.hypotheses[0]?.statement||'提出可验证的判断',hint:props.data.hypotheses.length+' 条假设',view:'ideas'},
  {label:'实验验证',title:props.data.experiments[0]?.rq||'设计最小验证实验',hint:props.data.experiments.length+' 条实验',view:'experiments'},
  {label:'论文论证',title:props.data.claims[0]?.claim||'把证据写进论文',hint:props.data.draft_progress.written+' / 14 节已起草',view:'writing'},
])
const draft=computed(()=>props.data.drafts.find(x=>x.content?.trim()))
const pending=computed(()=>props.reports.reduce((n,r)=>n+r.issues.length-r.resolved.length,0))
</script>
<template>
  <div class="stage-track" aria-label="研究阶段"><button v-for="(s,i) in data.stages" :key="s" class="stage" :class="{done:i+1<data.project.stage,active:i+1===data.project.stage}" @click="emit('stage',i+1)"><span class="stage-dot"><Icon v-if="i+1<data.project.stage" name="check" :size="10"/><template v-else>{{i+1}}</template></span>{{s}}<i v-if="i<7"></i></button></div>
  <div class="overview-grid"><div class="overview-main">
    <section class="panel argument-panel"><div class="panel-heading"><div><span class="section-label">研究主线</span><h2>让问题、方法与证据连起来</h2></div><span class="subtle-label">当前论证链</span></div><p class="research-question"><span>RQ</span>{{data.problems[0]?.phenomenon||'先记录你想解释的现象，再形成研究假设。'}}</p><div class="argument-chain"><button v-for="(step,i) in chain" :key="step.label" @click="step.view==='writing'?emit('write'):emit('go',step.view)"><span class="chain-step">{{step.label}}</span><b>{{step.title}}</b><small>{{step.hint}}</small><Icon v-if="i<3" name="chevron" :size="14"/></button></div><div class="chain-footer"><Icon name="link" :size="15"/>点击环节，查看本项目的真实记录</div></section>
    <section class="panel manuscript-card"><div class="panel-heading"><div><span class="section-label">继续上次的工作</span><h2>论文初稿</h2></div><button class="text-button" @click="emit('write',draft?.section)">进入写作<Icon name="arrow" :size="16"/></button></div><div class="actual-manuscript"><h3>{{data.sections.find(s=>s.id===draft?.section)?.title||'从一个章节开始'}}</h3><p>{{draft?.content?.slice(0,280)||'目前还没有正文。先写出问题、实验设置或方法，再逐步补齐论证。'}}</p></div><div class="manuscript-footer"><span>{{data.draft_progress.written}} / 14 个章节已起草</span><span class="muted">更新于 {{data.project.updated_at}}</span></div></section>
    <section class="panel recent-papers"><div class="panel-heading"><h2>项目文献</h2><button class="text-button" @click="emit('go','library')">全部文献<Icon name="arrow" :size="16"/></button></div><button v-for="p in data.reading_cards.slice(0,3)" :key="p.id" class="paper-row" @click="emit('go','library')"><span class="paper-icon"><Icon name="book"/></span><span class="paper-row-main"><b>{{p.title}}</b><small>{{p.source||'来源待补充'}}</small></span><Icon name="chevron" :size="16"/></button><p v-if="!data.reading_cards.length" class="muted empty-inline">从资料库导入论文，或新增阅读卡片。</p></section>
  </div><aside class="overview-aside"><section class="panel next-action"><span class="section-label">下一步</span><h2>{{data.recommendations[0]?.text||'继续核对证据，完善你的论文。'}}</h2><p>按研究问题组织文献、实验与结论，每次保存都留下可追溯版本。</p><button class="button full" @click="emit('go',!data.reading_cards.length?'library':'writing')">开始整理<Icon name="arrow" :size="16"/></button></section><section class="panel review-summary"><div class="row-between"><h2>待完善的章节意见</h2><span class="count-pill">{{pending}}</span></div><button v-for="r in reports.slice(0,3)" :key="r.id" class="review-summary-item" @click="emit('write',r.section)"><span><small>{{data.sections.find(s=>s.id===r.section)?.title}} · v{{r.revision}}</small><b>{{r.summary}}</b></span><Icon name="chevron" :size="15"/></button><p v-if="!reports.length" class="muted empty-inline">保存正文后，运行章节检查或 AI 评审。</p><button class="text-button" @click="emit('go','reviews')">查看全部评审<Icon name="arrow" :size="15"/></button></section></aside></div>
</template>
