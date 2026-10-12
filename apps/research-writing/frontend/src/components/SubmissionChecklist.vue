<script setup>
import { reactive,watch,computed } from 'vue'
import { submissionItems } from '../lib/schema'
const props=defineProps({records:Array,save:Function})
const edits=reactive({}), busy=reactive({}), errors=reactive({})
const items=computed(()=>[...new Set([...submissionItems,...props.records.map(r=>r.item)])])
watch(()=>props.records,()=>{for(const item of items.value){if(!edits[item]){const row=props.records.find(r=>r.item===item);edits[item]={item,status:row?.status||'todo',note:row?.note||''}}}},{immediate:true})
async function saveItem(item){busy[item]=true;errors[item]='';try{await props.save('submission',edits[item])}catch(e){errors[item]=e.message}finally{busy[item]=false}}
</script>
<template><section class="panel submission-panel"><h2>投稿前逐项确认</h2><p class="muted">状态和备注一起保存在服务器，手机与电脑共享。</p><form v-for="item in items" :key="item" class="submission-check" @submit.prevent="saveItem(item)"><label class="submission-item"><input type="checkbox" :checked="edits[item].status==='done'" @change="edits[item].status=$event.target.checked?'done':'todo'"><span>{{item}}</span></label><label class="field-label">备注或证据位置<textarea v-model="edits[item].note" rows="2"></textarea></label><p v-if="errors[item]" class="error-message" role="alert">{{errors[item]}}</p><button class="button" :disabled="busy[item]">{{busy[item]?'保存中…':'保存检查项'}}</button></form></section></template>
