<script setup>
import { computed } from 'vue'
import Icon from './Icon.vue'
const props=defineProps({sections:Array,section:String,content:String,revision:Number,busy:Boolean,dirty:Boolean,reports:Array,jobs:Array})
const emit=defineEmits(['update:content','select','save','history','rule','ai','resolve'])
const current=computed(()=>props.sections.find(s=>s.id===props.section))
const report=computed(()=>props.reports.find(r=>r.section===props.section))
const activeJobs=computed(()=>props.jobs.filter(j=>j.section===props.section&&['creating','queued','running'].includes(j.state)))
const old=computed(()=>report.value&&report.value.revision!==props.revision)
</script>
<template>
  <div class="writing-layout production-writing">
    <aside class="chapter-list panel"><div class="chapter-heading"><h3>论文目录</h3><span class="count-pill">{{sections.length}} 节</span></div><button v-for="s in sections" :key="s.id" class="chapter-item" :class="{active:section===s.id}" @click="emit('select',s.id)"><span>{{s.title}}</span></button><div class="chapter-bottom">草稿与历史保存在服务器</div></aside>
    <article class="editor-panel panel">
      <div class="editor-toolbar"><div class="desktop-only">论文初稿 / {{current?.title}}</div><div class="mobile-chapter-select"><label for="chapter-select">章节</label><select id="chapter-select" :value="section" @change="emit('select',$event.target.value)"><option v-for="s in sections" :value="s.id" :key="s.id">{{s.title}}</option></select></div><span class="save-status">{{busy?'保存中…':dirty?'尚未同步':'已保存'}} · v{{revision}}</span></div>
      <div class="document-heading"><span class="section-label">MANUSCRIPT</span><div class="row-between"><h2>{{current?.title}}</h2><button class="text-button" @click="emit('history')"><Icon name="history" :size="16"/>历史</button></div><p>写清本节的判断、依据和适用条件。保存后可在手机与电脑继续编辑。</p></div>
      <label class="sr-only" for="draft-editor">章节正文</label><textarea id="draft-editor" class="draft-editor" :value="content" placeholder="从本节最重要的判断开始写…" @input="emit('update:content',$event.target.value)"></textarea>
      <div class="editor-footer"><span>{{content.replace(/\s/g,'').length}} 字 · Ctrl/Cmd+S 保存</span><button class="button primary" :disabled="busy" @click="emit('save')">保存草稿</button></div>
    </article>
    <aside class="review-rail expanded actual-review-rail">
      <div class="review-rail-heading"><h3>章节检查与评审</h3></div><p class="review-rail-description">规则检查即时返回；AI 评审使用 gpt-6.1-sol，在后台执行。</p>
      <div class="review-actions"><button class="button" :disabled="busy" @click="emit('rule')">规则检查</button><button class="button primary" :disabled="busy||!!activeJobs.length" @click="emit('ai')">{{activeJobs.length?'AI 评审中…':'AI 评审'}}</button></div>
      <article v-for="job in jobs.filter(j=>j.section===section).slice(0,1)" :key="job.id" class="panel review-job"><b>{{{creating:'准备任务',queued:'等待 AI',running:'AI 评审中',completed:'AI 评审已完成',failed:'AI 评审失败'}[job.state]}}</b><p v-if="job.error" class="error-message">{{job.error}}</p><details v-if="job.progress"><summary>查看执行过程</summary><pre>{{job.progress}}</pre></details><a v-if="job.link" class="text-button" :href="job.link" target="_blank" rel="noopener">打开任务详情 ↗</a></article>
      <template v-if="report"><div class="review-version"><span class="tag" :class="report.source==='ai'?'green':'muted-tag'">{{report.source==='ai'?'AI 评审':'本地规则检查'}}</span><span>v{{report.revision}} · {{report.score}}/100</span></div><p v-if="old||dirty" class="notice-box">评审针对已保存的 v{{report.revision}}。当前正文已变化，需重新评审。</p><p class="review-summary-text">{{report.summary}}</p>
      <article v-for="(issue,i) in report.issues" :key="i" class="review-card" :class="{resolved:report.resolved.includes(i)}"><span class="section-label">{{{high:'优先处理',medium:'建议完善',low:'细节检查'}[issue.severity]}}</span><h4>{{issue.title}}</h4><blockquote v-if="issue.quote">{{issue.quote}}</blockquote><p>{{issue.detail}}</p><button class="text-button" @click="emit('resolve',report,i)">{{report.resolved.includes(i)?'重新标记待处理':'标记已处理'}}</button></article>
      <div v-if="!report.issues.length" class="notice-box">本次未列出问题；这不代表结论或引用已经得到事实核验。</div><details v-if="report.suggestions.length" class="panel advice-panel"><summary>修改建议</summary><p v-for="(s,i) in report.suggestions" :key="i">{{s}}</p></details><details v-if="report.strengths.length" class="panel advice-panel"><summary>已具备的基础</summary><p v-for="(s,i) in report.strengths" :key="i">{{s}}</p></details></template>
      <div v-else class="empty-state small"><Icon name="review" :size="26"/><p>本章节还没有检查记录。</p></div>
    </aside>
  </div>
</template>
