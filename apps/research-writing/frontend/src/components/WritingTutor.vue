<script setup>
import { ref,computed,onMounted,onUnmounted } from 'vue'
import { api,operationID } from '../lib/api'
import Icon from './Icon.vue'
const props=defineProps({project:String})
const emit=defineEmits(['draft'])
const course=ref(null),selected=ref(''),answer=ref(''),question=ref(''),error=ref(''),busy=ref(false),helpOpen=ref(false),showMap=ref(false)
let timer,request='',polling=false
const lesson=computed(()=>course.value?.lessons.find(l=>l.id===selected.value))
const attempts=computed(()=>(course.value?.attempts||[]).filter(a=>a.lesson_id===selected.value))
const running=computed(()=>(course.value?.attempts||[]).some(a=>['creating','queued','running'].includes(a.state)))
const passed=computed(()=>course.value?.completed.includes(selected.value))
const position=computed(()=>course.value?.lessons.findIndex(l=>l.id===selected.value)||0)
const canDraft=computed(()=>attempts.value.find(a=>a.intent==='practice'&&a.feedback?.passed&&lesson.value?.type==='text'))
const draftSection=computed(()=>({'one-question':'problem','method-sentence':'method','first-introduction':'introduction','small-abstract':'abstract'}[selected.value]))
function unlocked(index){return course.value.lessons.slice(0,index).every(l=>course.value.completed.includes(l.id))}
async function load(){course.value=await api('projects/'+props.project+'/learning');if(!selected.value)selected.value=course.value.current}
async function reload(){try{await load();error.value=''}catch(e){error.value=e.message}}
async function choose(id){try{await api('projects/'+props.project+'/learn',{action:'select',lesson_id:id});selected.value=id;answer.value='';question.value='';helpOpen.value=false;error.value='';request='';window.scrollTo({top:0,behavior:'instant'})}catch(e){error.value=e.message}}
async function submit(intent='practice',text){busy.value=true;error.value='';if(text!==undefined)request='';request||=operationID();try{await api('projects/'+props.project+'/learn',{lesson_id:selected.value,intent,answer:text??(intent==='question'?question.value:answer.value),request_id:request});request='';await load();if(intent==='question')question.value=''}catch(e){error.value=e.message}finally{busy.value=false}}
function next(){const l=course.value.lessons[position.value+1];if(l)choose(l.id)}
function changed(){request=''}
function displayAnswer(a){return lesson.value.type==='quiz'&&a.intent==='practice'?lesson.value.options.find(o=>o.id===a.answer)?.text||a.answer:a.answer}
onMounted(async()=>{try{await load()}catch(e){error.value=e.message}timer=setInterval(async()=>{if(!running.value||polling||busy.value)return;polling=true;try{await load()}catch{}finally{polling=false}},10000)})
onUnmounted(()=>clearInterval(timer))
</script>
<template>
  <div v-if="!course" class="panel empty-state"><Icon name="book" :size="30"/><p>{{error||'正在准备你的第一小步…'}}</p><button v-if="error" class="button" @click="reload()">重试</button></div>
  <div v-else class="tutor-layout">
    <aside class="panel lesson-map" :class="{expanded:showMap}"><span class="section-label">写论文入门</span><h2>一步一步来</h2><p>{{course.completed.length}} / {{course.lessons.length}} 步已完成</p><div class="lesson-progress"><i :style="{width:course.completed.length/course.lessons.length*100+'%'}"></i></div><button class="text-button mobile-route-toggle" :aria-expanded="showMap" @click="showMap=!showMap">{{showMap?'收起学习路线':'查看 8 步学习路线'}}<Icon name="down" :size="15"/></button><button v-for="(l,i) in course.lessons" :key="l.id" class="lesson-link" :class="{active:selected===l.id,completed:course.completed.includes(l.id)}" :disabled="!unlocked(i)" :title="unlocked(i)?l.title:'完成前一小步后解锁'" @click="choose(l.id);showMap=false"><span class="lesson-number"><Icon v-if="course.completed.includes(l.id)" name="check" :size="14"/><template v-else>{{i+1}}</template></span><span>{{l.title}}<small>{{l.time}}</small></span></button><p class="lesson-map-note">先理解，再练习。通过后由你点击下一步。</p></aside>
    <main class="lesson-main">
      <section class="panel lesson-content"><div class="row-between"><span class="section-label">第 {{position+1}} 小步 · {{lesson.time}}</span><span v-if="passed" class="tag green">已掌握入门目标</span></div><h2>{{lesson.title}}</h2><p class="lesson-goal">这一课，你只需要：{{lesson.goal}}</p><p v-for="(p,i) in lesson.explain" :key="i" class="lesson-paragraph">{{p}}</p><div class="lesson-example"><span class="section-label">看一个小例子</span><p>{{lesson.example}}</p></div><button class="text-button" :disabled="busy||running" @click="submit('question','我还是没懂。请用更简单的日常例子解释本课最重要的概念，再给我一个很小的问题。')"><Icon name="help" :size="17"/>我还是没懂，请换个例子</button></section>
      <form class="panel lesson-practice" @submit.prevent="submit()"><span class="section-label">现在轮到你</span><h3>{{lesson.prompt}}</h3><div v-if="lesson.type==='quiz'" class="lesson-options"><label v-for="o in lesson.options" :key="o.id"><input v-model="answer" type="radio" :value="o.id" name="lesson-answer" @change="changed"><span>{{o.text}}</span></label></div><label v-else class="field-label" for="practice-answer">你的练习<textarea id="practice-answer" v-model="answer" @input="changed" rows="5" placeholder="不用写得专业。先用自己的话表达…"></textarea></label><p class="muted">{{lesson.type==='quiz'?'选择题会立即解释答案。':'导师会按这一小步的目标反馈，不要求你一次写完整篇论文。'}}</p><button class="button primary" :disabled="busy||running||!answer.trim()">{{busy?'正在保存…':running?'导师正在反馈…':'提交这一步'}}</button><button type="button" class="text-button ask-link" @click="helpOpen=!helpOpen">{{helpOpen?'收起提问':'我想问一个问题'}}</button><p v-if="error" class="error-message" role="alert">{{error}}</p></form>
      <form v-if="helpOpen" class="panel lesson-question" @submit.prevent="submit('question')"><label class="field-label" for="tutor-question">哪里还不明白？<textarea id="tutor-question" v-model="question" @input="changed" rows="3" placeholder="例如：研究问题和主题有什么区别？"></textarea></label><button class="button" :disabled="busy||running||!question.trim()">向导师提问</button></form>
      <section v-if="attempts.length" class="panel lesson-feedback"><div class="panel-heading"><h3>你的回答与导师反馈</h3><span class="muted">保存在服务器</span></div><article v-for="a in attempts.slice(0,6).reverse()" :key="a.id" class="tutor-exchange"><div class="student-answer"><span>{{a.intent==='question'?'你的问题':'你的练习'}}</span><p>{{displayAnswer(a)}}</p></div><div v-if="a.state==='completed'" class="teacher-answer"><span class="tag" :class="a.feedback.passed?'green':'muted-tag'">{{a.intent==='question'?'导师解释':a.feedback.passed?'这一小步已完成':'再试着改一点'}}</span><p>{{a.feedback.feedback}}</p><blockquote v-if="a.feedback.example">{{a.feedback.example}}</blockquote><p v-if="a.feedback.next_tip" class="tutor-next-tip">下一小步：{{a.feedback.next_tip}}</p></div><p v-else-if="a.state==='failed'" class="error-message">{{a.error}}</p><div v-else class="teacher-answer waiting"><Icon name="clock" :size="18"/><p>{{a.progress.startsWith('导师连接')?a.progress:'导师正在看你的回答。你可以关闭页面，稍后回来查看。'}}</p></div></article></section>
      <div class="lesson-next"><p v-if="!passed" class="muted">先完成本课小练习，下一课会解锁。</p><template v-else><button v-if="position<course.lessons.length-1" class="button primary" @click="next()">下一小步<Icon name="arrow" :size="17"/></button><div v-else class="panel course-done"><h3>你已经走完第一条写作主线</h3><p>接下来把练习整理成草稿，再逐步补充真实文献、方法细节与证据。</p></div></template><button v-if="canDraft&&draftSection" class="button" @click="emit('draft',draftSection,canDraft.answer)">把我的练习放入章节编辑区</button></div>
    </main>
  </div>
</template>
