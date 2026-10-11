const campusAppsSection=document.createElement('section');campusAppsSection.id='campus-apps';campusAppsSection.hidden=true;
campusAppsSection.innerHTML='<div class="panel"><h2>校园应用</h2><p class="muted">输入校园服务器上已启动的网站地址，检测后生成现有域名下的访问链接。复用校园通道与管理页登录，不调用AI。</p><label for="campus-app-title">名称</label><input id="campus-app-title" maxlength="80" placeholder="例如研究写作训练系统"><label for="campus-app-url">校园端地址</label><input id="campus-app-url" type="url" placeholder="http://127.0.0.1:8765"><p class="muted">使用校园服务器的本机 HTTP 地址（127.0.0.1 或 localhost，附端口）。应用需要保持运行；生成链接会转发页面，不负责安装应用。</p><div class="row"><button id="campus-app-create" class="primary">检测并生成链接</button><button id="campus-app-refresh">刷新列表</button></div></div><div id="campus-app-list" aria-live="polite"></div>';
$('#workflows').before(campusAppsSection);
async function loadCampusApps(){
 let apps=await lib('site_list'),box=$('#campus-app-list');box.replaceChildren();
 if(!apps.length)box.append(el('div','尚未添加校园应用。在上方填写校园端已部署的地址。','empty'));
 for(let app of apps){let row=el('article',undefined,'panel');let url=new URL('apps/'+app.id+'/',location.href).href;
  row.append(el('h2',app.title),el('p',app.url,'muted'));let a=el('a','打开应用 ↗');a.href=url;a.target='_blank';a.rel='noopener';row.append(a,el('p',url,'mono'));
  let status=el('p','尚未检测。','muted');row.append(status);let controls=el('div',undefined,'row');
  controls.append(button('复制链接',async()=>{await navigator.clipboard.writeText(url);notice('访问链接已复制；首次使用需要登录管理页。')}),button('检测状态',async()=>{let v=await lib('site_check',{id:app.id});status.textContent=v.ok?'校园服务可连接 · HTTP '+v.status:'校园服务不可用，请检查是否已启动。'+(v.status?' HTTP '+v.status:'');status.className=v.ok?'muted':'error'}),button('停止转发',async()=>{if(!confirm('停止此访问链接？校园应用和数据仍保留。'))return;await lib('site_remove',{id:app.id});notice('此链接已停止转发。');await loadCampusApps()},'danger'));row.append(controls);box.append(row);
 }
}
$('#campus-app-refresh').onclick=()=>action(loadCampusApps);
$('#campus-app-create').onclick=()=>action(async()=>{let url=$('#campus-app-url').value.trim(),title=$('#campus-app-title').value.trim();if(!url)throw Error('请填写校园端地址，例如 http://127.0.0.1:8765');if(!title)title='校园应用';await lib('site_add',{collection:title,text:url});notice('校园地址检测成功，访问链接已生成。');await loadCampusApps()});
const researchShortcut=$('#knowledge a');researchShortcut.href='#';researchShortcut.onclick=e=>{e.preventDefault();switchView('campus-apps')};researchShortcut.textContent='校园应用与研究写作系统 ↗';researchShortcut.removeAttribute('target');
