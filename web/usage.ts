type UsageTotals={runs:number;reported:number;missing:number;running:number;input:number;output:number;total:number};
type UsageModel=UsageTotals&{model:string;engines:string[]};
type UsageReport={timezone:string;generated:number;total:UsageTotals;period:UsageTotals;days:(UsageTotals&{date:string;models:UsageModel[]})[]};
let usageRequest=0,usageTask='';
function installUsageDashboard(){
 const entry=document.createElement('button');entry.id='usage-open';entry.type='button';entry.textContent='用量统计';
 // Keep the task toolbar compact. This dialog returns to the settings form
 // without discarding any edits the user has made there.
 element('settings-form').querySelector('.settings-nav')!.append(entry);
 element('root').insertAdjacentHTML('beforeend',`<dialog id="usage-dialog" class="usage-dialog"><div class="usage-heading"><h2>Token 用量</h2><button type="button" id="usage-close">关闭</button></div><div class="usage-controls"><label>统计范围<select id="usage-scope"><option value="all">全部任务</option><option value="task">当前任务</option></select></label><label>每日用量<select id="usage-days"><option value="7">最近 7 天</option><option value="30" selected>最近 30 天</option><option value="90">最近 90 天</option></select></label><button type="button" id="usage-refresh">刷新</button></div><p id="usage-status" role="status"></p><div id="usage-content"></div><p class="muted usage-explanation">只统计 Duo 已保存的引擎回报，包含归档和回收站任务；不代表账号账单或 CLI 在 Duo 外的用量。每日按本页时区的执行开始日期归属，跨日执行归入开始当天；旧记录以创建日期代替。执行中的数字可能继续增加。</p></dialog>`);
 entry.onclick=()=>void openUsageDashboard();button('usage-close').onclick=()=>element<HTMLDialogElement>('usage-dialog').close();
 element('usage-dialog').addEventListener('close',()=>{usageRequest++});
 for(const id of ['usage-scope','usage-days'])input(id).onchange=()=>void loadUsageDashboard();
 button('usage-refresh').onclick=()=>void loadUsageDashboard();
}
async function openUsageDashboard(){
 usageTask=chosen;const choice=element<HTMLOptionElement>('usage-scope').querySelector<HTMLOptionElement>('[value="task"]')!;choice.disabled=!usageTask;choice.textContent=usageTask?'当前任务：'+(detail?.task.title||'所选任务'):'当前任务（尚未选择）';
 input('usage-scope').value='all';element<HTMLDialogElement>('usage-dialog').showModal();await loadUsageDashboard();
}
async function loadUsageDashboard(){
 const request=++usageRequest,epoch=shellEpoch,zone=Intl.DateTimeFormat().resolvedOptions().timeZone||'UTC';
 const params=new URLSearchParams({days:input('usage-days').value,timezone:zone});
 if(input('usage-scope').value==='task')params.set('task_id',usageTask);
 button('usage-refresh').disabled=true;element('usage-status').textContent='正在读取用量…';element('usage-content').replaceChildren();
 try{const report=await api<UsageReport>('usage?'+params,'GET',undefined,shellController.signal);if(request!==usageRequest||!shellCurrent(epoch)||!element<HTMLDialogElement>('usage-dialog').open)return;renderUsageDashboard(report)}
 catch(e){if(request===usageRequest&&shellCurrent(epoch))element('usage-status').textContent='读取失败，可刷新重试：'+(e as Error).message}
 finally{if(request===usageRequest&&shellCurrent(epoch))button('usage-refresh').disabled=false}
}
function usageCount(value:number,u:UsageTotals){return u.reported||!u.runs?value.toLocaleString('zh-CN'):'—'}
function renderUsageDashboard(report:UsageReport){
 const total=report.total,period=report.period,today=report.days[0],tokens=(u:UsageTotals,key:'total'|'input'|'output')=>usageCount(u[key],u);
 element('usage-status').textContent=`时区 ${report.timezone} · 更新于 ${new Date(report.generated).toLocaleTimeString()} · ${total.running} 轮执行中`;
 const card=(label:string,value:string,note:string)=>`<div class="usage-card"><span>${label}</span><strong>${escapeHTML(value)}</strong><small>${escapeHTML(note)}</small></div>`;
 const cells=(u:UsageTotals)=>`<td>${u.runs.toLocaleString('zh-CN')}${u.missing?`<small>缺失 ${u.missing} 轮用量</small>`:''}</td><td>${tokens(u,'input')}</td><td>${tokens(u,'output')}</td><td>${tokens(u,'total')}</td>`;
 const rows=report.days.map(day=>day.models.length?day.models.map(m=>`<tr><th scope="row">${day.date}</th><td class="usage-model">${escapeHTML(m.model||'默认模型（未记录名称）')}<small>${escapeHTML(m.engines.map(e=>e?taskEngineName(e):'未记录引擎').join(' + '))}</small></td>${cells(m)}</tr>`).join(''):`<tr><th scope="row">${day.date}</th><td class="usage-model">全部模型</td>${cells(day)}</tr>`).join('');
 element('usage-content').innerHTML=`<div class="usage-cards">${card('累计 Token',tokens(total,'total'),`${total.reported} 轮有用量 · ${total.missing} 轮缺失`)}${card('本期 Token',tokens(period,'total'),`最近 ${report.days.length} 天`)}${card('今日 Token',today?tokens(today,'total'):'—',today?`${today.runs} 轮执行`:'')}${card('累计执行次数',total.runs.toLocaleString('zh-CN'),'按 Duo 执行轮次统计')}</div><p class="usage-detail">累计 Prompt tokens ${tokens(total,'input')} · Completion tokens ${tokens(total,'output')}</p><p class="muted">次数按 Duo 执行轮次统计，一轮可能发起多次模型 API 请求；当前无法还原 API 请求总次数。输入包含缓存，Total = Prompt + Completion。缺失用量显示“—”，部分缺失只合计已回报数据。</p><div class="usage-table-wrap" tabindex="0" role="region" aria-label="每日模型用量表，可横向滚动"><table class="usage-table"><caption>每日模型用量 · 最近 ${report.days.length} 天</caption><thead><tr><th>日期</th><th>模型合计</th><th title="Duo 执行轮次，不是底层 API 请求数">执行次数</th><th>Prompt tokens</th><th>Completion tokens</th><th>Total tokens</th></tr></thead><tbody>${rows}</tbody><tfoot><tr><th scope="row">本期合计</th><td>全部模型</td>${cells(period)}</tr></tfoot></table></div>`;
}
