type EngineModel={id:string;name:string;reasoning_levels?:string[]|null;default_reasoning?:string};
let createModels:EngineModel[]=[];
const effortLabels:Record<string,string>={none:'关闭',minimal:'极低',low:'低',medium:'中',high:'高',xhigh:'很高',max:'最高',ultra:'Ultra（工具可能自动委派）'};
function taskEngineName(engine?:string){return engine==='claude'?'Claude Code':'Codex'}
function installExecution(){
 element('create-model').previousElementSibling!.insertAdjacentHTML('beforebegin','<label for="create-engine">AI 工具</label><select id="create-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option></select>');
 element('models-hint').insertAdjacentHTML('afterend','<label for="create-effort">推理强度</label><select id="create-effort"><option value="">工具默认</option></select><p class="muted" id="effort-hint"></p>');
 const modelLabel=element('create-model').previousElementSibling!,options=document.createElement('div');options.className='form-grid';modelLabel.before(options);
 for(const id of ['create-engine','create-effort']){const control=element(id),label=control.previousElementSibling!,column=document.createElement('div');options.append(column);column.append(label,control)}
 input('create-engine').onchange=()=>void loadCreateEnvironment(true);
 input('create-model').addEventListener('change',updateReasoning);
 input('custom-model').addEventListener('input',updateReasoning);
 element('setting-model').previousElementSibling!.textContent='Codex 默认模型（可留空）';
 element('setting-model').insertAdjacentHTML('afterend',`<label for="setting-claude">此环境中的 Claude Code 可执行文件</label><input id="setting-claude" placeholder="claude"><label for="setting-claude-model">Claude 默认模型（可留空）</label><input id="setting-claude-model" placeholder="例如 sonnet，或你的服务提供的模型 ID"><label for="setting-engine">默认 AI 工具（飞书新建也使用它）</label><select id="setting-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option></select><p class="muted">Claude 自动接受工作目录内的文件编辑；其他操作沿用该环境中的 Claude 权限设置。</p>`);
 button('check-codex').textContent='检查 Codex';
 button('check-codex').insertAdjacentHTML('afterend',' <button type="button" id="check-claude">检查 Claude</button>');
 button('check-claude').onclick=async()=>{button('check-claude').disabled=true;try{const r=await api('check','POST',{environment_id:editingID,engine:'claude'});element('check-result').textContent=(r.ok?'Claude 已配置\n':'Claude 检查失败\n')+r.output}catch(e){element('check-result').textContent=(e as Error).message}finally{button('check-claude').disabled=false}};
}
function updateReasoning(){
 const engine=input('create-engine').value,id=input('create-model').value==='__custom__'?input('custom-model').value.trim():input('create-model').value;
 const model=createModels.find(m=>m.id===id),known=model?.reasoning_levels;
 const levels=known??(engine==='claude'?['low','medium','high','xhigh','max']:['low','medium','high','xhigh']);
 const previous=input('create-effort').value;
 input('create-effort').innerHTML='<option value="">工具默认'+(model?.default_reasoning?' · '+escapeHTML(effortLabels[model.default_reasoning]||model.default_reasoning):'')+'</option>'+levels.map(v=>`<option value="${escapeHTML(v)}">${escapeHTML(effortLabels[v]||v)} · ${escapeHTML(v)}</option>`).join('');
 input('create-effort').value=levels.includes(previous)?previous:'';
 input('create-effort').disabled=levels.length===0;
 element('effort-hint').textContent=known?.length===0?'此模型不提供推理强度选择。':known==null?'默认沿用工具设置；手动选择需模型支持。':'';
}
