/* Read-only administrator viewer. Data is rendered as text, never HTML. */
(()=>{
  const $=id=>document.getElementById(id);
  const dialog=$('logs-dialog');
  let loading=false,more=false,cursor='',query='',shown=0,skipped=0,epoch=0,reloadPending=false;
  const types={tool:'工具调用',command_state:'命令状态',session_state:'连接状态',control:'管理操作',execution_policy_update:'策略更新'};
  const states={dispatched_unconfirmed:'已派发，未确认',running:'运行中',completed:'已结束',uncertain:'结果不确定',connecting:'正在连接',waiting_agent:'等待探针',ready:'终端就绪',reconnecting:'正在重连',closed:'已关闭',expired:'会话失效',context_lost:'上下文丢失',attached_context_unverified:'已连接，上下文待核实',authentication_failed:'认证失败',connection_failed:'连接失败',bootstrap_failed:'终端准备失败',bootstrap_uncertain:'终端准备不确定',remote_admission_rejected:'探针拒绝准入'};
  function text(tag,value,className){const e=document.createElement(tag);e.textContent=value;if(className)e.className=className;return e}
  function problem(r){return ['execution_uncertain','context_lost','output_gap','output_log_truncated','timed_out'].some(k=>r[k]===true)||r.success===false||r.tool_success===false||(r.exit_code!==null&&r.exit_code!==undefined&&r.exit_code!==0)||Boolean(r.error_code)||['expired','context_lost'].includes(r.connection_state)||String(r.connection_state||'').endsWith('_failed')||String(r.connection_state||'').endsWith('_uncertain')}
  function date(value){const d=new Date(value);return Number.isNaN(d.getTime())?'时间未知':d.toLocaleString('zh-CN',{hour12:false})}
  function nodeLabel(uuid){if(!uuid)return '面板／桥接';const n=nodes.find(n=>n.uuid===uuid);return n?.name||'已移除或未知设备'}
  function keyLabel(id){if(!id)return '';return keyItems.find(k=>k.id===id)?.name||'已移除或未知 Key'}
  function fillSelect(id,items,first){const select=$(id),previous=select.value;select.replaceChildren();const all=document.createElement('option');all.value='';all.textContent=first;select.append(all);for(const item of items){const o=document.createElement('option');o.value=item.id;o.textContent=item.name;select.append(o)}if(items.some(i=>i.id===previous))select.value=previous}
  function buttons(){ $('logs-refresh').disabled=loading||busy;$('logs-more').disabled=loading||busy||!more||shown>=500;$('logs-open').disabled=busy; }
  window.updateMCPLogButtons=buttons;
  function notice(){let s='已显示 '+shown+' 条，按记录写入顺序从新到旧。';if(skipped)s+=' 跳过 '+skipped+' 条损坏或不完整记录。';if(shown>=500&&more)s+=' 当前窗口上限500条，请缩小筛选或重新查询。';else if(more)s+=' 还有更早记录，可继续加载。';else s+=' 已检索完当前保留的日志。';$('logs-status').textContent=s}
  function renderRecord(r){
    const row=document.createElement('details');row.className='log-record';const heading=document.createElement('summary');
    heading.append(text('time',date(r.time)));
    const device=text('span',nodeLabel(r.node_uuid),'log-node');const n=nodes.find(n=>n.uuid===r.node_uuid);if(n){const image=document.createElement('img');image.className='node-flag';const code=flagCode(n.region);image.src='/assets/flags/'+code+'.svg';image.alt=code+' 国旗';image.onerror=()=>{image.onerror=null;image.src='/assets/flags/UN.svg'};device.prepend(image)}heading.append(device,text('span',types[r.event]||'其他记录'));
    const state=r.execution_state||r.connection_state;let result=states[state]||state||((r.tool_success??r.success)===false?'失败':(r.tool_success??r.success)===true?'成功':'已记录');if(r.exit_code!==null&&r.exit_code!==undefined)result+=' · 退出码 '+r.exit_code;if(r.timed_out)result+=' · 超时';if(r.execution_uncertain)result+=' · 不确定';heading.append(text('span',result,'log-badge'+(problem(r)?' problem':'')));
    row.append(heading);if(r.key_id)row.append(text('p','调用方：'+keyLabel(r.key_id),'muted'));if(r.tool)row.append(text('p','工具：'+r.tool,'muted'));
    const details=text('pre',JSON.stringify(r,null,2));row.append(details);
    if(r.session_id){const show=text('button','查看会话回显','secondary');show.type='button';const output=document.createElement('div');output.className='log-output';output.hidden=true;show.onclick=()=>{show.hidden=true;output.hidden=false;loadOutput(r.session_id,output,epoch)};row.append(show,output)}
    $('logs-list').append(row);
  }
  function cleanDisplay(s){return s.replace(/\x1b\][^\x07]*(?:\x07|\x1b\\)/g,'').replace(/\x1b\[[0-?]*[ -/]*[@-~]/g,'')}
  function loadOutput(id,container,viewEpoch){
    container.replaceChildren(text('p','合并终端输出：这是会话流，可能混有后台输出；命令原文未专门保存。仅显示桥接实际收到且保留的内容，可能包含敏感信息。','muted'));
    const info=text('p','正在读取…','muted'),pre=text('pre',''),next=text('button','继续读取回显','secondary');let offset=0;container.append(info,pre,next);next.disabled=true;
    async function read(){next.disabled=true;try{const p=await api('logs/output?'+new URLSearchParams({session_id:id,offset:String(offset),limit:String(Math.min(16384,131072-offset))}));if(viewEpoch!==epoch)return;pre.textContent+=cleanDisplay(p.output);offset=p.next_offset;let note='已读取 '+offset+' / '+p.saved_bytes+' 字节。';if(p.output_log_truncated)note+=' 已超过保存上限，存在截断。';if(p.output_gap)note+=' 会话存在输出缺口。';if(p.invalid_utf8_replaced)note+=' 非UTF-8或截断字符已替换显示。';if(!p.saved_bytes)note+=' 暂无保存的回显。';if(offset>131072-256&&p.has_more)note+=' 本窗口显示上限128KiB，请使用诊断流程提取所需时间窗口。';info.textContent=note;next.hidden=!p.has_more;next.disabled=!p.has_more||offset>131072-256}catch(e){if(viewEpoch!==epoch)return;info.textContent='回显不可用：'+e.message;next.disabled=false}}
    next.onclick=read;read();
  }
  async function load(reset){
    if(loading){if(reset)reloadPending=true;return}if(reset){epoch++;shown=0;skipped=0;cursor='';more=false;$('logs-list').replaceChildren();const p=new URLSearchParams({limit:'50',to:new Date().toISOString()});for(const [field,id] of [['node_uuid','logs-node'],['key_id','logs-key'],['event','logs-type'],['status','logs-problem']])if($(id).value)p.set(field,$(id).value);const hours=Number($('logs-time').value);if(hours)p.set('from',new Date(Date.now()-hours*3600000).toISOString());query=p.toString()}
    const version=epoch;loading=true;buttons();$('logs-status').textContent='正在查询已保存日志…';
    try{const p=await api('logs?'+query+(cursor?'&cursor='+encodeURIComponent(cursor):''));if(version!==epoch)return;if(!shown&&p.records.length)$('logs-list').replaceChildren();for(const r of p.records){if(shown>=500)break;renderRecord(r);shown++}skipped+=p.skipped_records||0;cursor=p.next_cursor||'';more=p.has_more;notice();if(!shown){$('logs-list').replaceChildren(text('p',more?'本次扫描没有匹配记录，仍有更早日志，可继续检索。':'当前保留范围内没有符合筛选的日志。','muted'))}}
    catch(e){if(version!==epoch)return;$('logs-status').textContent=/snapshot expired/i.test(e.message)?'日志已轮转或筛选已变化，请点击“查询／刷新”重新读取。':'日志读取失败：'+e.message+'。请确认面板与桥接均已更新。';more=false}
    finally{loading=false;buttons();if(reloadPending&&dialog.open){reloadPending=false;load(true)}}
  }
  $('logs-open').onclick=()=>{fillSelect('logs-node',nodes.map(n=>({id:n.uuid,name:n.name||'未命名设备'})),'全部设备');fillSelect('logs-key',keyItems.map(k=>({id:k.id,name:k.name||'未命名 Key'})),'全部 Key');positionDialog(dialog);dialog.showModal();load(true)};
  $('logs-refresh').onclick=()=>load(true);$('logs-more').onclick=()=>load(false);$('logs-close').onclick=()=>dialog.close();dialog.onclose=()=>{epoch++;reloadPending=false;$('logs-list').replaceChildren();$('logs-status').textContent='';cursor='';more=false;shown=0;buttons()};
  buttons();
})();
