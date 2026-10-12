<script setup>
import { ref, watch } from 'vue'
import Icon from './Icon.vue'
import { definitions, statusLabels } from '../lib/schema'
const props=defineProps({ kind:String, records:Array, save:Function })
const editing=ref(false), values=ref({}), busy=ref(false), error=ref(''), search=ref('')
function edit(row={}) { values.value={...row}; editing.value=true; error.value='' }
watch(()=>props.kind,()=>{editing.value=false;search.value='';error.value=''})
async function submit(){busy.value=true;error.value='';try{await props.save(props.kind,values.value);editing.value=false}catch(e){error.value=e.message}finally{busy.value=false}}
defineExpose({ edit })
</script>
<template>
  <section class="record-module">
    <div class="panel-heading"><div><span class="section-label">{{records.length}} 条研究记录</span><h2>{{definitions[kind].title}}</h2></div><button class="button primary" @click="edit()"><Icon name="plus" :size="16"/>新增记录</button></div>
    <p class="muted record-lead">{{definitions[kind].lead}}</p>
    <label class="inline-search record-search"><Icon name="search" :size="16"/><input v-model="search" aria-label="搜索当前记录" placeholder="搜索记录内容"></label>
    <form v-if="editing" class="panel record-form" @submit.prevent="submit">
      <div class="panel-heading"><h3>{{values.id?'编辑记录':'新增记录'}}</h3><button type="button" class="icon-button" aria-label="关闭记录编辑" @click="editing=false"><Icon name="close"/></button></div>
      <label v-for="(field,i) in definitions[kind].fields" :key="field[0]" class="field-label" :for="'record-'+kind+'-'+field[0]">{{field[1]}}
        <select v-if="field[2]==='select'" :id="'record-'+kind+'-'+field[0]" v-model="values[field[0]]"><option value="">请选择</option><option v-for="s in field[3]" :key="s" :value="s">{{statusLabels[s]||s}}</option></select>
        <input v-else-if="i===0||['source','role','priority','novelty_type'].includes(field[0])" :id="'record-'+kind+'-'+field[0]" v-model="values[field[0]]" :required="i===0" :type="field[0]==='priority'?'number':'text'">
        <textarea v-else :id="'record-'+kind+'-'+field[0]" v-model="values[field[0]]" rows="3"></textarea>
      </label>
      <p v-if="error" class="error-message" role="alert">{{error}}</p><button class="button primary" :disabled="busy">{{busy?'保存中…':'保存记录'}}</button>
    </form>
    <div class="record-grid">
      <article v-for="row in records.filter(x=>Object.values(x).some(v=>String(v).includes(search)))" :key="row.id" class="panel actual-record">
        <div class="row-between"><span class="section-label">{{row.created_at}}</span><button class="text-button" @click="edit(row)">编辑<Icon name="pen" :size="14"/></button></div>
        <h3>{{row[definitions[kind].fields[0][0]]}}</h3><template v-for="field in definitions[kind].fields.slice(1)" :key="field[0]"><div v-if="row[field[0]]" class="idea-field"><small>{{field[1]}}</small><p>{{field[2]==='select'?(statusLabels[row[field[0]]]||row[field[0]]):row[field[0]]}}</p></div></template>
      </article>
    </div>
    <div v-if="!records.length&&!editing" class="panel empty-state"><Icon name="file" :size="30"/><b>还没有{{definitions[kind].title}}</b><p>添加第一条记录，让后续写作有依据可查。</p><button class="button" @click="edit()">新增记录</button></div>
  </section>
</template>
