let updateLoading = false, updateInformation = null, updatePhase = 'idle', updateExpectedVersion = '';
function installUpdates() {
    const form = element('settings-form'), nav = form.querySelector('.settings-nav');
    nav.insertAdjacentHTML('beforeend', '<button type="button" data-settings="updates">版本更新</button>');
    element('settings-error').insertAdjacentHTML('beforebegin', '<section id="settings-updates" class="settings-section hidden"><div class="update-version"><div><small>当前版本</small><strong id="update-current">正在读取…</strong><span id="update-mode" class="update-mode">正在识别安装方式</span></div><div class="update-actions"><button type="button" id="update-install" class="primary hidden">下载并安装更新</button><button type="button" id="update-check">检查更新</button></div></div><ol class="update-steps" aria-label="更新流程"><li data-update-step="checking">检查版本</li><li data-update-step="preparing">下载与校验</li><li data-update-step="reconnecting">安装与重连</li><li data-update-step="complete">确认新版</li></ol><p id="update-result" role="status" aria-live="polite"></p><button type="button" id="update-reconnect" class="hidden">重新连接并确认版本</button><p id="update-support" class="update-support"></p><div id="update-links" class="update-links"></div><details id="update-notes" class="hidden"><summary>更新说明</summary><pre id="update-notes-content"></pre></details><details class="update-source"><summary>更新来源</summary><label for="update-repository">GitHub 公开仓库</label><input id="update-repository" placeholder="用户名/仓库" autocomplete="off"><p>留空使用官方仓库。只查询正式发布版本，不上传任务或账号信息。</p><button type="button" id="update-source-save">保存来源</button></details><p class="update-help">更新会短暂重启Duo，保留数据、任务记录、知识和配置。请先等待 AI 任务结束、关闭终端会话并完成硬件操作；有正在进行的工作时不会强停更新。安装版继续使用安装包升级，便携版保留便携更新方式。</p></section>');
    button('update-check').onclick = checkNewVersion;
    button('update-install').onclick = installNewVersion;
    button('update-source-save').onclick = saveUpdateSource;
    button('update-reconnect').onclick = ()=>void reconnectAfterUpdate();
    setUpdateBusy(updateLoading);
}
function safeUpdateLink(url, repo) {
    if (!url) return '';
    try {
        const u = new URL(url), prefix = ('/' + repo + '/releases').toLowerCase(), path = u.pathname.toLowerCase();
        return u.protocol === 'https:' && u.host === 'github.com' && !u.username && !u.password && !u.search && !u.hash && (path === prefix || path.startsWith(prefix + '/')) ? u.href : '';
    } catch  {
        return '';
    }
}
function updateModeLabel(v) {
    return v.installation_mode === 'installed' ? 'Windows 安装版' : v.installation_mode === 'portable' ? 'Windows 便携版' : '独立运行 · 手动安装';
}
function updateLinks(v) {
    const portable = v.package_kind === 'portable' || v.installation_mode === 'portable';
    const links = [];
    if (v.download_url) links.push([
        portable ? '手动下载便携版 ZIP' : '手动下载安装包 EXE',
        v.download_url
    ]);
    else if (v.installer_download_url) links.push([
        '下载安装包 EXE',
        v.installer_download_url
    ]);
    if (v.installer_download_url && v.installer_download_url !== v.download_url) links.push([
        '安装版 EXE（推荐）',
        v.installer_download_url
    ]);
    if (v.portable_download_url && v.portable_download_url !== v.download_url) links.push([
        '便携版 ZIP',
        v.portable_download_url
    ]);
    if (v.checksum_url) links.push([
        'SHA-256 校验文件',
        v.checksum_url
    ]);
    links.push([
        '版本页面',
        v.release_url || v.releases_url
    ]);
    return links;
}
function updateCanInstall() {
    const v = updateInformation;
    return !!(v?.state === 'available' && v.install_supported && safeUpdateLink(v.download_url, v.repository) && !updateExpectedVersion);
}
function setUpdateBusy(busy) {
    updateLoading = busy;
    for (const id of [
        'update-check',
        'update-source-save',
        'update-reconnect'
    ]){
        const b = document.getElementById(id);
        if (b) b.disabled = busy || id === 'update-source-save' && !!updateExpectedVersion;
    }
    const install = document.getElementById('update-install');
    if (install) install.disabled = busy || !updateCanInstall();
    const repository = document.getElementById('update-repository');
    if (repository) repository.disabled = busy || !!updateExpectedVersion;
}
function setUpdatePhase(phase, message, error = false) {
    updatePhase = phase;
    const result = document.getElementById('update-result');
    if (result) {
        result.textContent = message;
        result.classList.toggle('error', error);
    }
    document.querySelectorAll('[data-update-step]').forEach((step)=>{
        const active = step.dataset.updateStep === phase || phase === 'timeout' && step.dataset.updateStep === 'reconnecting';
        step.classList.toggle('active', active);
        if (active) step.setAttribute('aria-current', 'step');
        else step.removeAttribute('aria-current');
    });
    const retry = document.getElementById('update-reconnect');
    if (retry) retry.classList.toggle('hidden', phase !== 'timeout');
    const section = document.getElementById('settings-updates');
    if (section) section.setAttribute('aria-busy', String([
        'checking',
        'preparing',
        'reconnecting'
    ].includes(phase)));
}
function renderUpdateInformation(v) {
    updateInformation = v;
    if (!document.getElementById('settings-updates')) return;
    element('update-current').textContent = v.current;
    element('update-mode').textContent = updateModeLabel(v);
    input('update-repository').value = v.repository;
    if (!updateExpectedVersion) setUpdatePhase('idle', v.message + (v.latest ? ' · ' + v.latest : '') + (v.checked ? ' · ' + new Date(v.checked).toLocaleTimeString('zh-CN', {
        hour: '2-digit',
        minute: '2-digit'
    }) : ''));
    element('update-links').innerHTML = updateLinks(v).map(([label, url])=>{
        const href = safeUpdateLink(url, v.repository);
        return href ? '<a target="_blank" rel="noopener noreferrer" href="' + escapeHTML(href) + '">' + escapeHTML(label) + ' ↗</a>' : '';
    }).join('');
    element('update-notes').classList.toggle('hidden', !v.notes);
    element('update-notes-content').textContent = v.notes || '';
    element('update-support').textContent = v.install_message || (v.installation_mode === 'installed' ? '使用安装包原位升级，保留现有安装位置和数据目录。' : v.installation_mode === 'portable' ? '在当前目录更新便携程序，数据目录保持不变。' : '此运行方式暂不支持自动安装，请下载 Windows 安装包或查看版本页面。');
    const install = button('update-install');
    install.classList.toggle('hidden', v.state !== 'available');
    install.title = v.install_supported ? '' : v.install_message || '自动安装暂不可用，请手动下载。';
    setUpdateBusy(updateLoading);
}
function updateFailure(e) {
    const error = e, message = error?.message || '连接失败，请稍后重试。';
    const prefix = error.status === 401 ? '登录已过期，请重新登录后再试。' : error.status === 403 ? '请求未获授权，请刷新页面重新登录后再试。' : error.status === 409 ? '当前不能更新：请等待任务结束、关闭终端会话并完成硬件操作后重试。' : '';
    setUpdatePhase(updateExpectedVersion ? 'timeout' : 'failed', prefix + (prefix ? ' ' : '') + message, true);
}
async function loadUpdateInformation() {
    if (updateLoading) return;
    setUpdateBusy(true);
    try {
        renderUpdateInformation(await api('updates'));
    } catch (e) {
        updateFailure(e);
    } finally{
        setUpdateBusy(false);
    }
}
async function saveUpdateSource() {
    if (updateLoading || updateExpectedVersion) return;
    setUpdateBusy(true);
    try {
        renderUpdateInformation(await api('updates', 'PUT', {
            repository: input('update-repository').value
        }));
        notify('更新来源已保存');
    } catch (e) {
        updateFailure(e);
    } finally{
        setUpdateBusy(false);
    }
}
async function checkNewVersion() {
    if (updateLoading) return;
    setUpdateBusy(true);
    if (!updateExpectedVersion) setUpdatePhase('checking', '正在检查 GitHub 正式发布版本…');
    try {
        renderUpdateInformation(await api('updates/check', 'POST', {}));
    } catch (e) {
        updateFailure(e);
    } finally{
        setUpdateBusy(false);
    }
}
function normalizedUpdateVersion(value) {
    return /^v?\d+\.\d+\.\d+(?:-portable)?$/.test(value) ? value.replace(/^v/, '').replace(/-portable$/, '') : '';
}
async function waitForUpdatedService(expected, timeoutMs = 180000, intervalMs = 2000) {
    const target = normalizedUpdateVersion(expected);
    if (!target) return {
        matched: false,
        lastVersion: ''
    };
    const deadline = Date.now() + timeoutMs;
    let lastVersion = '';
    while(Date.now() < deadline){
        const controller = new AbortController(), timer = setTimeout(()=>controller.abort(), Math.min(4000, Math.max(1, deadline - Date.now())));
        try {
            const response = await fetch('/healthz', {
                credentials: 'same-origin',
                cache: 'no-store',
                signal: controller.signal
            });
            if (response.status === 401 || response.status === 403) return {
                matched: false,
                lastVersion,
                status: response.status
            };
            if (response.ok) {
                const health = await response.json();
                if (health.app === 'jianzuo' && typeof health.version === 'string') {
                    lastVersion = health.version;
                    if (normalizedUpdateVersion(lastVersion) === target) return {
                        matched: true,
                        lastVersion
                    };
                }
            }
        } catch  {} finally{
            clearTimeout(timer);
        }
        if (Date.now() < deadline) await new Promise((resolve)=>setTimeout(resolve, Math.min(intervalMs, deadline - Date.now())));
    }
    return {
        matched: false,
        lastVersion
    };
}
async function reconnectAfterUpdate() {
    if (updateLoading || !updateExpectedVersion) return;
    setUpdateBusy(true);
    setUpdatePhase('reconnecting', '更新已进入安装与重连等待，正在确认 ' + updateExpectedVersion + '。服务可能短暂离线；确认新版前不会显示成功。');
    try {
        const result = await waitForUpdatedService(updateExpectedVersion);
        if (result.matched) {
            updateExpectedVersion = '';
            setUpdatePhase('complete', '已连接新版 ' + result.lastVersion + '，正在刷新工作台…');
            location.reload();
            return;
        }
        const reason = result.status === 401 ? '访问服务需要重新登录。' : result.status === 403 ? '当前连接没有访问权限，请检查登录或代理设置。' : result.lastVersion ? '服务仍返回版本 ' + result.lastVersion + '。' : '服务暂未恢复连接。';
        setUpdatePhase('timeout', reason + ' 尚未确认更新成功；可再次连接，或在服务电脑上启动Duo并查看数据目录中的 update.log。请勿重复安装或删除数据。', true);
    } catch (e) {
        setUpdatePhase('timeout', '暂时无法确认更新结果。可重新连接，或查看数据目录中的 update.log；不要重复安装。', true);
    } finally{
        if (updatePhase !== 'complete') setUpdateBusy(false);
    }
}
async function installNewVersion() {
    if (updateLoading || !updateCanInstall()) return;
    const expected = normalizedUpdateVersion(updateInformation?.latest || '');
    if (!expected) {
        updateFailure(new Error('缺少可验证的目标版本，请重新检查更新。'));
        return;
    }
    if (!confirm('下载并安装 ' + expected + '？\n更新会短暂重启Duo并保留数据。请先等待 AI 任务结束、关闭终端会话并完成硬件操作；更新不会强停这些工作。')) return;
    setUpdateBusy(true);
    setUpdatePhase('preparing', '正在下载、校验并准备更新。完成前不会开始安装；请勿关闭服务电脑。');
    try {
        const result = await api('updates/install', 'POST', {});
        if (result.state !== 'scheduled' || !normalizedUpdateVersion(result.version)) throw Object.assign(new Error('服务未返回有效的安装安排，请重新检查更新。'), {
            status: 502
        });
        updateExpectedVersion = result.version;
    } catch (e) {
        if (!e?.status) {
            updateExpectedVersion = expected;
            setUpdatePhase('reconnecting', '安装请求的连接已中断，正在确认服务版本；尚未确认更新成功。');
        } else {
            updateFailure(e);
            setUpdateBusy(false);
            return;
        }
    }
    setUpdateBusy(false);
    await reconnectAfterUpdate();
}
const harnessAttachmentHint = 'Harness 当前不支持附件。请切换到 Codex / Claude Code，或手动移除附件后继续；已选附件不会自动删除。';
function validateEngineAttachments(engine, count) {
    if (engine === 'deepseek-harness' && count > 0) throw new Error(harnessAttachmentHint);
}
let workCatalog = {
    modes: [],
    commands: []
};
const attachmentDrafts = new Map(), uploadingTasks = new Set();
const pendingUploadFiles = new Map();
let renameTaskID = '', trashTaskID = '', stoppingTask = '', presetType = 'modes', presetID = '', directoryEnvironment = '', directoryPath = '', directoryParent = '', directoryRequest = 0, workspacePicked = null;
function modeOptions(select, snapshot) {
    const previous = select.value, engine = select.id === 'create-mode' ? input('create-engine')?.value : detail?.task.engine, items = workCatalog.modes.filter((m)=>m.id !== 'harness:read' || engine === 'deepseek-harness');
    if (snapshot?.id && !items.some((m)=>m.id === snapshot.id)) items.push(snapshot);
    select.innerHTML = items.map((m)=>`<option value="${escapeHTML(m.id)}"${modeSupportsEngine(m, engine) ? '' : ' disabled'}>${escapeHTML(modeLabel(m))}${modeSupportsEngine(m, engine) ? '' : '（当前引擎不支持）'}</option>`).join('');
    const preferred = previous || snapshot?.id || 'work';
    select.value = items.some((m)=>m.id === preferred && modeSupportsEngine(m, engine)) ? preferred : items.find((m)=>m.id === 'work' && modeSupportsEngine(m, engine))?.id || items.find((m)=>modeSupportsEngine(m, engine))?.id || '';
}
function modeApproval(mode) {
    return mode.approval || (mode.permission === 'workspace' ? 'request' : 'never');
}
function modeSupportsEngine(mode, engine) {
    return (mode.id !== 'harness:read' || engine === 'deepseek-harness') && (engine === 'codex' || modeApproval(mode) !== 'auto') && (engine !== 'deepseek-harness' || mode.allow_network !== false);
}
function modeLabel(mode) {
    const access = mode.permission === 'read' ? '只读' : mode.permission === 'full' ? '完全访问' : mode.allow_network === false ? '工作区 · 离线' : '工作区 · 联网';
    const approval = modeApproval(mode), review = approval === 'auto' ? ' · Codex 自动风险评审' : approval === 'request' ? ' · 请求批准' : mode.permission === 'workspace' ? ' · 不请求批准' : '';
    return `${mode.name} · ${access}${review}`;
}
function installWorkflow() {
    element('new-task').insertAdjacentHTML('afterend', '<button id="workspace-open" class="workspace-open">▣ 选择工作区 <span>⌄</span></button>');
    button('workspace-open').onclick = ()=>openWorkspacePicker(settings.config.default_environment, '', (path, env)=>void showCreateAt(path, env));
    const form = element('create-form'), heading = element('create-heading'), lead = heading.nextElementSibling;
    const head = document.createElement('div');
    head.className = 'create-page-head';
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.id = 'create-cancel';
    cancel.className = 'subtle';
    cancel.textContent = '返回';
    head.append(heading, lead, cancel);
    lead.textContent = '选择执行位置，直接描述目标；第一条要求会在任务创建后立即开始。';
    const context = document.createElement('div');
    context.className = 'create-context';
    const environment = input('create-environment'), environmentLabel = environment.previousElementSibling;
    environmentLabel.remove();
    environment.setAttribute('aria-label', '执行环境');
    const environmentField = document.createElement('div');
    environmentField.className = 'create-field create-context-field';
    environmentField.innerHTML = '<span>环境</span>';
    environmentField.append(environment);
    const workspace = input('create-workspace'), workspaceLabel = workspace.previousElementSibling, directoryHint = workspace.nextElementSibling;
    workspaceLabel.remove();
    directoryHint.remove();
    workspace.setAttribute('aria-label', '工作目录');
    const workspaceControl = document.createElement('div');
    workspaceControl.className = 'create-directory-control';
    const browse = document.createElement('button');
    browse.type = 'button';
    browse.id = 'create-browse';
    browse.className = 'create-browse';
    browse.title = '浏览文件夹';
    browse.setAttribute('aria-label', '浏览工作目录');
    browse.textContent = '浏览';
    workspaceControl.append(workspace, browse);
    const workspaceField = document.createElement('div');
    workspaceField.className = 'create-field create-context-field create-workspace-field';
    workspaceField.innerHTML = '<span>目录</span>';
    workspaceField.append(workspaceControl);
    const workspaceOptions = element('workspace-options') || Object.assign(document.createElement('datalist'), {
        id: 'workspace-options'
    });
    context.append(environmentField, workspaceField, workspaceOptions);
    const inputLabel = input('create-input').previousElementSibling;
    inputLabel.classList.add('sr-only');
    inputLabel.textContent = '任务要求';
    input('create-input').required = false;
    const createComposer = document.createElement('div');
    createComposer.className = 'create-composer';
    const attachmentDrafts = document.createElement('div');
    attachmentDrafts.id = 'create-attachment-drafts';
    attachmentDrafts.className = 'attachment-drafts';
    const toolbar = document.createElement('div');
    toolbar.className = 'create-composer-tools';
    const options = document.createElement('div');
    options.className = 'create-options';
    const attach = document.createElement('button');
    attach.type = 'button';
    attach.id = 'create-attach';
    attach.className = 'create-icon-button';
    attach.title = '添加附件';
    attach.setAttribute('aria-label', '添加附件');
    attach.textContent = '＋';
    const files = document.createElement('input');
    files.id = 'create-files';
    files.type = 'file';
    files.multiple = true;
    files.className = 'hidden';
    const permission = document.createElement('div');
    permission.className = 'create-permission';
    permission.innerHTML = '<span>权限</span><div class="create-segmented" id="create-permission-options" role="group" aria-label="任务权限"><button type="button" data-create-permission="read" title="只读规划，不修改工作区">只读规划</button><button type="button" data-create-permission="request" title="工作区内执行，需要提升权限时请求批准">请求批准</button><button type="button" data-create-permission="auto" title="Codex 原生自动风险评审，不是无条件放行；Claude Code 不支持">帮我批准</button><button type="button" data-create-permission="full" title="跳过工作区边界，仅在你明确选择时使用">完全访问</button></div><small id="create-permission-hint"></small>';
    const mode = document.createElement('select');
    mode.id = 'create-mode';
    mode.className = 'hidden';
    mode.setAttribute('aria-hidden', 'true');
    mode.tabIndex = -1;
    const optionField = (name, control, className = '')=>{
        const box = document.createElement('div');
        box.className = 'create-field' + (className ? ' ' + className : '');
        const label = document.createElement('span');
        label.textContent = name;
        box.append(label, control);
        return box;
    };
    const engine = input('create-engine'), engineLabel = engine.previousElementSibling;
    engineLabel.remove();
    const effort = input('create-effort'), effortLabel = effort.previousElementSibling;
    effortLabel.remove();
    const modelPicker = element('model-picker'), modelMenu = element('model-menu'), modelLabel = modelPicker.previousElementSibling;
    modelLabel?.remove();
    const modelField = optionField('模型', modelPicker, 'create-model-field');
    modelField.append(input('create-model'));
    options.append(attach, files, permission, mode, optionField('AI 工具', engine), modelField, optionField('推理强度', effort), input('custom-model'));
    const submitWrap = document.createElement('div');
    submitWrap.className = 'create-submit-wrap';
    const createStatus = document.createElement('span');
    createStatus.id = 'create-status';
    createStatus.className = 'create-status';
    createStatus.setAttribute('role', 'status');
    const submit = button('create-submit');
    submit.classList.add('create-submit');
    submit.closest('.dialog-footer').remove();
    submitWrap.append(createStatus, submit);
    toolbar.append(options, submitWrap);
    createComposer.append(inputLabel, input('create-input'), attachmentDrafts, toolbar);
    const meta = document.createElement('div');
    meta.className = 'create-meta';
    const finalHint = form.querySelectorAll(':scope > p');
    for (const node of finalHint)if (node !== element('effort-hint') && node !== element('models-hint') && node !== element('model-test-result') && node !== element('create-error')) node.remove();
    const modelFooter = document.createElement('div');
    modelFooter.className = 'model-catalog-footer';
    modelFooter.innerHTML = '<p id="models-context" class="model-context"></p><p id="models-status" class="model-catalog-status" role="status"></p><div class="model-catalog-actions" id="model-catalog-actions"></div><details class="model-catalog-details" id="model-catalog-details"><summary>来源与诊断</summary></details>';
    modelMenu.append(modelFooter);
    modelFooter.querySelector('#model-catalog-actions').append(element('reload-models'), element('test-models'));
    modelFooter.querySelector('#model-catalog-details').append(element('models-hint'));
    modelFooter.append(element('model-test-result'));
    meta.append(element('effort-hint'), element('create-error'));
    form.replaceChildren(head, context, createComposer, meta);
    button('create-cancel').onclick = cancelCreate;
    button('create-browse').onclick = ()=>openWorkspacePicker(input('create-environment').value, input('create-workspace').value, async (p, env)=>{
            input('create-environment').value = env;
            await loadCreateEnvironment();
            input('create-workspace').value = p;
            await loadCreateModels();
        });
    button('create-attach').onclick = ()=>input('create-files').click();
    input('create-files').onchange = ()=>{
        addCreateFiles(Array.from(input('create-files').files || []));
        input('create-files').value = '';
    };
    element('create-permission-options').querySelectorAll('[data-create-permission]').forEach((b)=>b.onclick = ()=>{
            const value = b.dataset.createPermission;
            if (value === 'full' && !confirm('完全访问会跳过工作区边界，允许 AI 读写工作区外文件并执行高权限命令。是否继续？')) return;
            setCreatePermission(value === 'request' ? 'request' : value === 'full' ? 'full' : value === 'read' ? 'read' : 'auto');
        });
    input('create-input').onkeydown = (e)=>{
        if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
            e.preventDefault();
            element('create-form').requestSubmit();
        }
    };
    const bar = document.createElement('div');
    bar.className = 'composer-tools';
    bar.innerHTML = '<button type="button" id="attach-open" title="添加文件或图片，也可以拖放、粘贴图片">＋ 附件</button><button type="button" id="command-open">/ 指令</button><select id="message-mode" aria-label="本轮工作模式"></select><button type="button" id="mode-manage" title="管理工作模式">⚙</button><input id="attachment-input" type="file" multiple hidden><small id="mode-engine-hint" class="mode-engine-hint hidden"></small>';
    element('composer').querySelector('.composer-bottom').before(bar);
    element('message').after(Object.assign(document.createElement('div'), {
        id: 'attachment-drafts',
        className: 'attachment-drafts'
    }));
    modeOptions(element('message-mode'));
    modeOptions(element('create-mode'));
    setCreatePermission(createPermission);
    button('attach-open').onclick = ()=>input('attachment-input').click();
    input('attachment-input').onchange = ()=>{
        const files = Array.from(input('attachment-input').files || []);
        input('attachment-input').value = '';
        void addAttachments(files);
    };
    const composer = element('composer');
    composer.ondragover = (e)=>{
        if (e.dataTransfer?.types.includes('Files')) {
            e.preventDefault();
            composer.classList.add('dragover');
        }
    };
    composer.ondragleave = ()=>composer.classList.remove('dragover');
    composer.ondrop = (e)=>{
        composer.classList.remove('dragover');
        if (e.dataTransfer?.files.length) {
            e.preventDefault();
            void addAttachments(Array.from(e.dataTransfer.files));
        }
    };
    input('message').addEventListener('paste', (e)=>{
        const files = Array.from(e.clipboardData?.files || []);
        if (files.length) {
            e.preventDefault();
            void addAttachments(files);
        }
    });
    input('message').addEventListener('input', ()=>{
        renderWorkflow();
        if (input('message').value === '/') openCommands();
    });
    button('mode-manage').onclick = ()=>openPresetEditor('modes');
    button('command-open').onclick = openCommands;
    button('stop').onclick = async ()=>{
        if (detail?.task.engine === 'deepseek-harness' && !confirm('停止会关闭 Harness 运行进程，原会话无法恢复；网页里的消息记录仍然保留。确定停止？')) return;
        const id = chosen;
        stoppingTask = id;
        renderWorkflow();
        try {
            await api('tasks/' + id + '/stop', 'POST', {});
            await poll();
        } catch (e) {
            stoppingTask = '';
            notify(e.message);
            renderWorkflow();
        }
    };
    element('tasks-archived').insertAdjacentHTML('afterend', '<button id="trash-open" title="查看可恢复的会话">回收站</button>');
    button('trash-open').onclick = showTrash;
    element('task-title').ondblclick = ()=>void openTaskRename();
    element('task-title').title = '双击重命名';
    element('root').insertAdjacentHTML('beforeend', `<dialog id="rename-dialog"><form id="rename-form"><h2>重命名会话</h2><label for="rename-title">新名称</label><input id="rename-title" required maxlength="180"><p id="rename-error" class="error"></p><div class="dialog-footer"><button type="button" id="rename-cancel">取消</button><button class="primary" id="rename-save">保存名称</button></div></form></dialog><dialog id="trash-confirm-dialog"><h2>删除会话</h2><p id="trash-confirm-title"></p><p>移入回收站，可随时恢复。此会话的飞书绑定会解除，工作目录文件保留。</p><p id="trash-confirm-error" class="error"></p><div class="dialog-footer"><button id="trash-cancel">取消</button><button id="trash-confirm" class="danger">移入回收站</button></div></dialog><dialog id="workspace-dialog" class="workspace-dialog"><h2>选择工作区</h2><select id="workspace-environment" aria-label="工作区执行环境"></select><div class="workspace-choices" id="workspace-recent"></div><div class="directory-address"><button id="directory-up" title="上一级">↑</button><input id="directory-path" aria-label="目录路径"><button id="directory-go">前往</button></div><div id="directory-list" class="directory-list"></div><p id="directory-status" role="status"></p><label class="check-row"><input type="checkbox" id="workspace-remember" checked>添加到常用工作区</label><div class="dialog-footer"><button id="workspace-cancel">取消</button><button class="primary" id="workspace-select">选择此目录</button></div></dialog>
 <dialog id="presets-dialog"><h2 id="presets-title">工作模式</h2><p id="presets-hint"></p><div class="preset-list" id="preset-list"></div><form id="preset-form"><label for="preset-name">名称</label><input id="preset-name" required maxlength="40"><div id="preset-permission-wrap"><label for="preset-permission">执行方式</label><select id="preset-permission"><option value="workspace">工作区内执行</option><option value="read">分析规划</option><option value="full">完全访问（高风险）</option></select><label class="check-row" id="preset-network-wrap"><input id="preset-network" type="checkbox">允许联网（下载、GitHub 等）</label></div><label for="preset-content" id="preset-content-label">要求（可留空）</label><textarea id="preset-content" rows="5" maxlength="16000"></textarea><p id="preset-error" class="error"></p><div class="dialog-footer"><button type="button" id="preset-delete" class="danger">删除</button><button type="button" id="presets-close">关闭</button><button class="primary" id="preset-save">保存</button></div></form></dialog>
 <dialog id="commands-dialog"><h2>快捷指令</h2><div id="commands-list" class="command-list"></div><div class="dialog-footer"><button id="commands-manage">管理指令</button><button id="commands-close">关闭</button></div></dialog>
 <dialog id="trash-dialog"><h2>回收站</h2><p>恢复后放回“已归档”。删除会话不会删除工作目录中的文件。</p><div id="trash-list"></div><div class="dialog-footer"><button id="trash-close">关闭</button></div></dialog>`);
    input('preset-permission').insertAdjacentHTML('afterend', '<label for="preset-approval">审批方式</label><select id="preset-approval"><option value="request">需要提升权限时请求批准</option><option value="auto">Codex 原生自动风险评审（非全放行）</option><option value="never">不请求批准（仍受所选执行边界约束）</option></select>');
    button('rename-cancel').onclick = ()=>element('rename-dialog').close();
    element('rename-form').onsubmit = saveTaskRename;
    button('trash-cancel').onclick = ()=>element('trash-confirm-dialog').close();
    button('trash-confirm').onclick = confirmTrashTask;
    input('workspace-environment').onchange = ()=>{
        directoryEnvironment = input('workspace-environment').value;
        renderWorkspaceRecent();
        void browseDirectory(settings.config.environments.find((e)=>e.id === directoryEnvironment)?.workspaces[0] || '');
    };
    button('directory-up').onclick = ()=>void browseDirectory(directoryParent);
    button('directory-go').onclick = ()=>void browseDirectory(input('directory-path').value.trim());
    input('directory-path').onkeydown = (e)=>{
        if (e.key === 'Enter') {
            e.preventDefault();
            void browseDirectory(input('directory-path').value.trim());
        }
    };
    button('workspace-cancel').onclick = ()=>{
        directoryRequest++;
        element('workspace-dialog').close();
    };
    button('workspace-select').onclick = selectWorkspace;
    button('presets-close').onclick = ()=>element('presets-dialog').close();
    element('preset-form').onsubmit = savePreset;
    input('preset-permission').onchange = ()=>{
        input('preset-approval').value = input('preset-permission').value === 'workspace' ? 'request' : 'never';
        syncPresetNetwork();
    };
    button('preset-delete').onclick = deletePreset;
    button('commands-close').onclick = ()=>element('commands-dialog').close();
    button('commands-manage').onclick = ()=>{
        element('commands-dialog').close();
        openPresetEditor('commands');
    };
    button('trash-close').onclick = ()=>element('trash-dialog').close();
}
function modeForPermission(permission, engine = 'codex') {
    if (engine === 'deepseek-harness' && permission === 'read') return workCatalog.modes.find((mode)=>mode.id === 'harness:read' && mode.permission === 'read' && modeApproval(mode) === 'never' && mode.allow_network === true);
    const wanted = permission === 'read' ? 'read' : permission === 'full' ? 'full' : 'workspace', preferred = permission === 'read' ? 'plan' : permission === 'request' ? 'work' : permission === 'auto' ? 'codex:auto' : permission, approval = permission === 'read' || permission === 'full' ? 'never' : permission;
    const matches = (mode)=>mode.permission === wanted && modeApproval(mode) === approval && modeSupportsEngine(mode, engine);
    return workCatalog.modes.find((m)=>m.id === preferred && matches(m)) || workCatalog.modes.find(matches);
}
function setCreatePermission(permission) {
    const engine = input('create-engine')?.value || 'codex', codex = engine === 'codex', harness = engine === 'deepseek-harness';
    if (permission === 'auto' && !codex) permission = 'request';
    const requested = modeForPermission(permission, engine);
    if (harness && (!requested || !modeSupportsEngine(requested, engine))) permission = 'request';
    createPermission = permission;
    const selected = modeForPermission(permission, engine), mode = input('create-mode');
    if (mode) mode.value = selected?.id || '';
    element('create-permission-options')?.querySelectorAll('[data-create-permission]').forEach((b)=>{
        const value = b.dataset.createPermission, candidate = modeForPermission(value, engine), on = value === permission;
        b.classList.toggle('selected', on);
        b.setAttribute('aria-pressed', String(on));
        b.disabled = value === 'auto' && !codex || harness && (!candidate || !modeSupportsEngine(candidate, engine));
        if (value === 'request') {
            b.textContent = harness ? '工作区边界' : codex ? '请求批准' : 'CLI 预授权';
            b.title = harness ? '仅允许边界内操作；超出权限直接拒绝，不弹出审批' : codex ? '工作区内执行，需要提升权限时请求批准' : '遵循 Claude CLI 预授权规则；不支持此页交互审批';
        }
        if (value === 'read') {
            b.textContent = harness ? '只读·可联网' : '只读规划';
            b.title = harness ? b.disabled ? '当前服务未提供 Harness 只读联网模式，请更新服务' : '不修改工作区，但不限制网络；这不是离线模式' : '只读规划，不修改工作区';
        }
    });
    const hint = permission === 'read' ? harness ? '只读分析，不允许修改工作区；允许联网，不提供离线保证' : '只读分析规划，不允许修改工作区' : permission === 'request' ? harness ? '仅在工作区权限边界内执行，越界请求直接拒绝' : codex ? '允许工作区内执行与联网；需要提升权限时请求批准' : '允许工作区内执行，遵循 Claude CLI 预授权规则' : permission === 'full' ? '跳过工作区边界且不请求批准，请确认任务来源可信' : 'Codex 原生自动风险评审，不是全部放行；仍可能需要人工确认';
    if (element('create-permission-hint')) element('create-permission-hint').textContent = hint + (harness ? '。Harness 无交互审批及独立网络开关，网络由原生策略和执行环境控制；当前离线模式不可选。' : codex ? '' : '。Claude Code 当前不支持 Codex 网页交互审批或自动风险评审。');
    renderCreateFiles();
}
function renderCreateFiles() {
    const target = element('create-attachment-drafts');
    if (!target) return;
    const harness = input('create-engine').value === 'deepseek-harness';
    button('create-attach').disabled = harness;
    button('create-attach').title = harness ? harnessAttachmentHint : '添加附件';
    input('create-files').disabled = harness;
    target.innerHTML = createFiles.map((file, index)=>`<span class="attachment-chip" title="${escapeHTML(file.name)}">${escapeHTML(file.name)} <button type="button" data-remove-create-file="${index}" aria-label="移除附件 ${escapeHTML(file.name)}">×</button></span>`).join('') + (harness && createFiles.length ? `<p class="error" role="alert">${escapeHTML(harnessAttachmentHint)}</p>` : '');
    target.querySelectorAll('[data-remove-create-file]').forEach((b)=>b.onclick = ()=>{
            createFiles.splice(Number(b.dataset.removeCreateFile), 1);
            renderCreateFiles();
        });
    if (element('create-status') && !button('create-submit').dataset.starting) element('create-status').textContent = createFiles.length ? '已选择 ' + createFiles.length + '/5 个附件' : '准备开始';
}
function addCreateFiles(files) {
    if (!files.length) return;
    try {
        validateEngineAttachments(input('create-engine').value, files.length);
        validateAttachmentFiles(files, createFiles.length);
        createFiles.push(...files);
        element('create-error').textContent = '';
        renderCreateFiles();
        notify('已添加 ' + files.length + ' 个附件，任务创建后会上传。');
    } catch (e) {
        element('create-error').textContent = e.message;
    }
}
function setCreateSubmitState(state) {
    const submit = button('create-submit');
    if (!submit) return;
    const starting = state === 'starting' || createSubmitting;
    submit.dataset.starting = starting ? '1' : '';
    element('create-form').querySelectorAll('input,select,textarea,button').forEach((control)=>{
        if (control === submit) return;
        if (starting) {
            if (control.dataset.submitDisabled === undefined) control.dataset.submitDisabled = String(control.disabled);
            control.disabled = true;
        } else if (control.dataset.submitDisabled !== undefined) {
            control.disabled = control.dataset.submitDisabled === 'true';
            delete control.dataset.submitDisabled;
        }
    });
    submit.disabled = starting;
    submit.textContent = starting ? '▶' : '↑';
    submit.title = starting ? '正在创建并开始任务' : '创建并开始任务';
    submit.setAttribute('aria-label', submit.title);
    element('create-form').setAttribute('aria-busy', String(starting));
    if (element('create-status')) element('create-status').textContent = starting ? '正在创建并开始…' : createFiles.length ? '已选择 ' + createFiles.length + '/5 个附件' : '准备开始';
}
async function showCreateAt(path, environment) {
    await showCreate();
    input('create-environment').value = environment;
    await loadCreateEnvironment();
    input('create-workspace').value = path;
    await loadCreateModels();
}
function openWorkspacePicker(environment, path, pick) {
    workspacePicked = pick;
    directoryEnvironment = environment;
    input('workspace-environment').innerHTML = settings.config.environments.map((e)=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');
    input('workspace-environment').value = environment;
    renderWorkspaceRecent();
    element('workspace-dialog').showModal();
    void browseDirectory(path || settings.config.environments.find((e)=>e.id === environment)?.workspaces[0] || '');
}
function renderWorkspaceRecent() {
    const env = settings.config.environments.find((e)=>e.id === directoryEnvironment);
    const paths = [
        ...new Set([
            ...env?.workspaces || [],
            ...tasks.filter((t)=>t.environment.id === directoryEnvironment).map((t)=>t.workspace)
        ])
    ];
    element('workspace-recent').innerHTML = paths.map((p)=>`<button data-directory="${escapeHTML(p)}" title="${escapeHTML(p)}">${escapeHTML(p)}</button>`).join('');
    element('workspace-recent').querySelectorAll('[data-directory]').forEach((b)=>b.onclick = ()=>void browseDirectory(b.dataset.directory));
}
async function browseDirectory(path) {
    const request = ++directoryRequest, env = directoryEnvironment;
    button('workspace-select').disabled = true;
    element('directory-status').textContent = '正在读取目录…';
    element('directory-list').innerHTML = '';
    try {
        const r = await api(`environments/${encodeURIComponent(env)}/directories?path=${encodeURIComponent(path)}`);
        if (request !== directoryRequest) return;
        directoryPath = r.path;
        directoryParent = r.parent;
        input('directory-path').value = r.path;
        button('directory-up').disabled = !r.path;
        element('directory-list').innerHTML = r.items.map((item)=>`<button data-directory="${escapeHTML(item.path)}"><span>▱</span>${escapeHTML(item.name)}<span>›</span></button>`).join('') || '<p class="muted">没有子文件夹，可以选择当前目录。</p>';
        element('directory-list').querySelectorAll('[data-directory]').forEach((b)=>b.onclick = ()=>void browseDirectory(b.dataset.directory));
        element('directory-status').textContent = r.truncated ? '显示前 500 个目录，可输入完整路径。' : '';
        button('workspace-select').disabled = !r.path;
    } catch (e) {
        if (request === directoryRequest) element('directory-status').textContent = e.message;
    }
}
async function selectWorkspace() {
    const path = directoryPath, env = directoryEnvironment;
    button('workspace-select').disabled = true;
    try {
        if (input('workspace-remember').checked) {
            await api(`environments/${env}/workspaces`, 'POST', {
                path
            });
            settings = await api('settings');
        }
        element('workspace-dialog').close();
        workspacePicked?.(path, env);
    } catch (e) {
        element('directory-status').textContent = e.message;
    } finally{
        button('workspace-select').disabled = false;
    }
}
function renderWorkflow() {
    if (!element('message-mode')) return;
    if (detail) {
        const control = element('message-mode'), key = chosen + '|' + detail.task.engine, harness = detail.task.engine === 'deepseek-harness';
        if (control.dataset.task !== key) {
            control.dataset.task = key;
            control.value = '';
            modeOptions(control, detail.task.mode);
        }
        control.disabled = detail.task.archived || harness;
        const hint = element('mode-engine-hint');
        hint.textContent = harness ? 'Harness 无交互审批 · 仅运行进程内可续聊 · 网络由原生策略和执行环境控制' : 'Claude CLI 预授权 · 当前不支持此页交互审批或 Codex 自动风险评审';
        hint.title = harness ? harnessSessionHint : hint.textContent;
        hint.classList.toggle('hidden', detail.task.engine === 'codex');
        control.title = harness || detail.task.engine === 'claude' ? hint.title : '本轮 Codex 工作模式与原生审批方式';
        button('task-model-button').title = harness ? harnessSessionHint : '本任务使用的 AI 工具、模型和推理强度';
        const footnote = element('composer-wrap').querySelector('.footnote');
        if (footnote) footnote.textContent = harness ? 'Harness SDK 会话 · 仅当前运行进程内可续聊' : '在服务所在电脑执行 · 保留所选工具的原生会话';
    }
    const active = detail?.runs.some((r)=>[
            'queued',
            'running'
        ].includes(r.status));
    if (!active && stoppingTask === chosen) stoppingTask = '';
    button('stop').disabled = stoppingTask === chosen;
    button('stop').textContent = stoppingTask === chosen ? '…' : '■';
    button('stop').title = stoppingTask === chosen ? '正在停止' : '停止当前执行';
    button('stop').setAttribute('aria-label', button('stop').title);
    const files = attachmentDrafts.get(chosen) || [], pending = pendingUploadFiles.get(chosen) || [];
    button('send').textContent = sending ? '▶' : '↑';
    button('send').title = sending ? '正在开始' : active ? '追加要求并排队' : '开始执行';
    button('send').setAttribute('aria-label', button('send').title);
    button('send').disabled = sending || uploadingTasks.has(chosen) || !!detail?.task.archived || harnessSessionClosed() || sessionResetTask === chosen || pending.length > 0 || !input('message').value.trim() && !files.length;
    const attachmentsBlocked = detail?.task.engine === 'deepseek-harness';
    button('attach-open').disabled = attachmentsBlocked || !!detail?.task.archived || uploadingTasks.has(chosen);
    button('attach-open').title = attachmentsBlocked ? harnessAttachmentHint : '添加文件或图片，也可以拖放、粘贴图片';
    input('attachment-input').disabled = attachmentsBlocked;
    button('command-open').disabled = !!detail?.task.archived;
    const html = files.map((f)=>`<span class="attachment-chip" title="${escapeHTML(f.name)}">${escapeHTML(f.name)} <button type="button" data-remove-attachment="${f.id}" aria-label="移除附件 ${escapeHTML(f.name)}">×</button></span>`).join('') + pending.map((f, i)=>`<span class="attachment-chip pending-upload">待上传：${escapeHTML(f.name)} <button type="button" data-remove-pending="${i}" aria-label="移除待上传附件 ${escapeHTML(f.name)}">×</button></span>`).join('') + (pending.length && !uploadingTasks.has(chosen) ? '<button type="button" id="retry-uploads">重试未完成的上传</button><small>文件仅保留在当前页面，刷新后需重新选择。</small>' : '') + (uploadingTasks.has(chosen) ? '<small>正在上传…</small>' : '');
    const target = element('attachment-drafts');
    if (target.innerHTML !== html) {
        target.innerHTML = html;
        target.querySelectorAll('[data-remove-attachment]').forEach((b)=>b.onclick = ()=>{
                attachmentDrafts.set(chosen, (attachmentDrafts.get(chosen) || []).filter((f)=>f.id !== b.dataset.removeAttachment));
                renderWorkflow();
            });
        target.querySelectorAll('[data-remove-pending]').forEach((b)=>b.onclick = ()=>{
                pendingUploadFiles.set(chosen, (pendingUploadFiles.get(chosen) || []).filter((_, i)=>i !== Number(b.dataset.removePending)));
                renderWorkflow();
            });
        if (button('retry-uploads')) button('retry-uploads').onclick = ()=>void retryPendingUploads(chosen);
    }
    renderCodexApprovals(detail);
}
function validateAttachmentFiles(files, existing = 0) {
    if (files.length + existing > 5) throw new Error('每条消息最多 5 个附件');
    if (files.some((f)=>f.size > 8 * 1024 * 1024)) throw new Error('单个附件最多 8 MiB');
}
async function uploadTaskFile(task, file) {
    const data = new FormData();
    data.append('file', file);
    const response = await fetch(`/api/tasks/${task}/attachments`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
            'X-CSRF-Token': csrf
        },
        body: data
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || '上传失败');
    return result;
}
async function addAttachments(files, task = chosen) {
    if (!task || !files.length) return;
    if (uploadingTasks.has(task)) {
        notify('请等待当前附件上传完成');
        return;
    }
    let uploadedCount = 0, accepted = false;
    try {
        const engine = detail?.task.id === task ? detail.task.engine : tasks.find((item)=>item.id === task)?.engine;
        validateEngineAttachments(engine, files.length);
        validateAttachmentFiles(files, (attachmentDrafts.get(task) || []).length + (pendingUploadFiles.get(task) || []).length);
        accepted = true;
        uploadingTasks.add(task);
        renderWorkflow();
        for (const file of files){
            const uploaded = await uploadTaskFile(task, file);
            attachmentDrafts.set(task, [
                ...attachmentDrafts.get(task) || [],
                uploaded
            ]);
            uploadedCount++;
            if (task === chosen) renderWorkflow();
        }
    } catch (e) {
        if (accepted) pendingUploadFiles.set(task, [
            ...pendingUploadFiles.get(task) || [],
            ...files.slice(uploadedCount)
        ]);
        notify(e.message);
    } finally{
        uploadingTasks.delete(task);
        if (task === chosen) renderWorkflow();
    }
}
async function retryPendingUploads(task) {
    if (uploadingTasks.has(task)) return;
    const files = pendingUploadFiles.get(task) || [];
    if (!files.length) return;
    try {
        const engine = detail?.task.id === task ? detail.task.engine : tasks.find((item)=>item.id === task)?.engine;
        validateEngineAttachments(engine, files.length);
        validateAttachmentFiles(files, (attachmentDrafts.get(task) || []).length);
    } catch (e) {
        notify(e.message);
        return;
    }
    pendingUploadFiles.delete(task);
    await addAttachments(files, task);
}
function selectedMessageMode() {
    const value = input('message-mode').value;
    return workCatalog.modes.some((m)=>m.id === value && modeSupportsEngine(m, detail?.task.engine)) ? value : '';
}
async function openPresetEditor(type) {
    try {
        workCatalog = await api('workbench');
        presetType = type;
        element('presets-title').textContent = type === 'modes' ? '工作模式' : '快捷指令';
        element('presets-hint').textContent = type === 'modes' ? '模式决定执行方式，可添加自己的工作要求。分析规划模式不接入任务硬件工具。' : '指令会填入输入框，检查后由你开始执行。';
        element('preset-permission-wrap').classList.toggle('hidden', type !== 'modes');
        element('preset-content-label').textContent = type === 'modes' ? '要求（可留空）' : '指令内容';
        input('preset-content').required = type === 'commands';
        renderPresetList();
        editPreset('');
        element('presets-dialog').showModal();
    } catch (e) {
        notify(e.message);
    }
}
function renderPresetList() {
    const items = presetType === 'modes' ? workCatalog.modes : workCatalog.commands;
    element('preset-list').innerHTML = items.map((v)=>`<button type="button" data-preset="${escapeHTML(v.id)}">${escapeHTML(v.name)}</button>`).join('') + '<button type="button" data-preset="">＋ 新增</button>';
    element('preset-list').querySelectorAll('[data-preset]').forEach((b)=>b.onclick = ()=>editPreset(b.dataset.preset));
}
function editPreset(id) {
    presetID = id;
    const item = (presetType === 'modes' ? workCatalog.modes : workCatalog.commands).find((m)=>m.id === id), builtin = !!item?.builtin;
    input('preset-name').value = item?.name || '';
    input('preset-content').value = item?.prompt || item?.content || '';
    input('preset-permission').value = item?.permission || 'workspace';
    input('preset-approval').value = item && presetType === 'modes' ? modeApproval(item) : 'request';
    input('preset-network').checked = item?.allow_network !== false;
    for (const id of [
        'preset-name',
        'preset-content',
        'preset-permission'
    ])input(id).disabled = builtin;
    input('preset-network').dataset.builtin = builtin ? '1' : '';
    syncPresetNetwork();
    button('preset-save').disabled = builtin;
    button('preset-delete').disabled = builtin || !presetID;
    element('preset-error').textContent = builtin ? '内置模式可直接选择；需要调整时新增一个模式。' : '';
}
function presetNetworkForPermission(permission, id, current) {
    return permission === 'read' ? id === 'harness:read' : permission === 'full' ? true : current;
}
function syncPresetNetwork() {
    const permission = input('preset-permission').value, network = input('preset-network'), approval = input('preset-approval');
    network.checked = presetNetworkForPermission(permission, presetID, network.checked);
    if (permission !== 'workspace') approval.value = 'never';
    network.disabled = network.dataset.builtin === '1' || permission !== 'workspace';
    approval.disabled = network.dataset.builtin === '1' || permission !== 'workspace';
    element('preset-network-wrap').title = presetID === 'harness:read' ? '只读但不限制联网；此模式仅适用于 Harness' : permission === 'workspace' ? '关闭后工作区模式不能下载依赖或访问 GitHub' : '该执行方式由系统确定网络权限';
}
async function savePreset(e) {
    e.preventDefault();
    await changePreset(false);
}
async function deletePreset() {
    if (presetID && confirm('删除此预设？已有执行记录保留原模式。')) await changePreset(true);
}
async function changePreset(remove) {
    button('preset-save').disabled = true;
    try {
        const fresh = await api('workbench'), id = presetID || 'custom_' + Date.now().toString(36);
        fresh.modes = fresh.modes.filter((m)=>!m.builtin);
        if (presetType === 'modes') {
            fresh.modes = fresh.modes.filter((m)=>m.id !== id);
            if (!remove) fresh.modes.push({
                id,
                name: input('preset-name').value,
                permission: input('preset-permission').value,
                approval: input('preset-approval').value,
                prompt: input('preset-content').value,
                allow_network: input('preset-network').checked
            });
        } else {
            fresh.commands = fresh.commands.filter((c)=>c.id !== id);
            if (!remove) fresh.commands.push({
                id,
                name: input('preset-name').value,
                content: input('preset-content').value
            });
        }
        workCatalog = await api('workbench', 'PUT', fresh);
        modeOptions(element('message-mode'), detail?.task.mode);
        modeOptions(element('create-mode'));
        setCreatePermission(createPermission);
        renderPresetList();
        editPreset(remove ? '' : id);
        notify(remove ? '预设已删除' : '预设已保存');
    } catch (e) {
        element('preset-error').textContent = e.message;
    } finally{
        button('preset-save').disabled = false;
    }
}
function openCommands() {
    element('commands-list').innerHTML = workCatalog.commands.map((c)=>`<button data-command="${escapeHTML(c.id)}"><strong>/${escapeHTML(c.name)}</strong><span>${escapeHTML(c.content.slice(0, 100))}</span></button>`).join('') || '<p class="muted">还没有指令，点“管理指令”添加常用要求。</p>';
    element('commands-list').querySelectorAll('[data-command]').forEach((b)=>b.onclick = ()=>{
            const command = workCatalog.commands.find((c)=>c.id === b.dataset.command);
            if (!command) return;
            if (input('message').value === '/') {
                input('message').value = '';
                drafts.set(chosen, '');
            }
            bringToChat(command.content);
            element('commands-dialog').close();
            renderWorkflow();
        });
    element('commands-dialog').showModal();
}
function trashCurrentTask(id = chosen) {
    const task = tasks.find((t)=>t.id === id) || (id === chosen ? detail?.task : undefined);
    if (!id || !task) return;
    if (id === chosen && !mayLeave()) return;
    trashTaskID = id;
    element('trash-confirm-title').textContent = task.title;
    element('trash-confirm-error').textContent = '';
    element('trash-confirm-dialog').showModal();
}
async function confirmTrashTask() {
    const id = trashTaskID;
    button('trash-confirm').disabled = true;
    try {
        await api('tasks/' + id, 'DELETE', {});
        element('trash-confirm-dialog').close();
        tasks = await api('tasks');
        attachmentDrafts.delete(id);
        drafts.delete(id);
        if (id !== chosen) {
            renderList();
            notify('已移入回收站');
            void loadStickyBoard();
            return;
        }
        dirty = false;
        if (tasks.length) {
            const next = tasks.find((t)=>t.archived === (taskView === 'archived')) || tasks[0];
            taskView = next.archived ? 'archived' : 'active';
            await choose(next.id);
        } else {
            chosen = '';
            detail = null;
            resetConversation();
            element('conversation').innerHTML = '<div class="empty"><h2>开始一个新任务</h2></div>';
            for (const name of [
                'tabs',
                'task-actions',
                'composer-wrap'
            ])element(name).classList.add('hidden');
            element('task-title').textContent = 'Duo';
            element('task-workspace').textContent = '';
            history.replaceState(null, '', '/');
            switchTab('chat');
        }
        renderList();
        notify('已移入回收站');
        void loadStickyBoard();
    } catch (e) {
        element('trash-confirm-error').textContent = e.message;
    } finally{
        button('trash-confirm').disabled = false;
    }
}
async function showTrash() {
    try {
        const items = await api('trash');
        element('trash-list').innerHTML = items.map((t)=>`<div class="trash-item"><span>${escapeHTML(t.title)}</span><button data-restore="${t.id}">恢复</button></div>`).join('') || '<p class="muted">回收站为空</p>';
        element('trash-list').querySelectorAll('[data-restore]').forEach((b)=>b.onclick = async ()=>{
                try {
                    await api(`tasks/${b.dataset.restore}/restore`, 'POST', {});
                    tasks = await api('tasks');
                    renderList();
                    await showTrash();
                    notify('已恢复到已归档会话');
                } catch (e) {
                    notify(e.message);
                }
            });
        if (!element('trash-dialog').open) element('trash-dialog').showModal();
    } catch (e) {
        notify(e.message);
    }
}
function openTaskRename(id = chosen) {
    const task = tasks.find((t)=>t.id === id) || (id === chosen ? detail?.task : undefined);
    if (!id || !task) return;
    renameTaskID = id;
    input('rename-title').value = task.title;
    element('rename-error').textContent = '';
    element('rename-dialog').showModal();
    input('rename-title').focus();
    input('rename-title').select();
}
async function saveTaskRename(e) {
    e.preventDefault();
    const id = renameTaskID;
    button('rename-save').disabled = true;
    try {
        await api('tasks/' + id, 'PATCH', {
            title: input('rename-title').value.trim()
        });
        element('rename-dialog').close();
        tasks = await api('tasks');
        if (id === chosen) await poll();
        renderList();
        void loadStickyBoard();
        notify('会话已重命名');
    } catch (e) {
        element('rename-error').textContent = e.message;
    } finally{
        button('rename-save').disabled = false;
    }
}
let stickyItems = [], stickyLoading = false, stickyScope = 'all', stickyFilter = 'open', stickyLast = '', stickyRequest = 0;
const defaultAppearance = {
    theme: 'light',
    accent: 'blue',
    font: 14,
    sidebar: 248,
    width: 820,
    tool: 42,
    notes: true,
    compact: false,
    path: true
};
let appearance = {
    ...defaultAppearance
};
function parseAppearance(raw) {
    try {
        const v = JSON.parse(raw || '{}');
        return {
            theme: [
                'light',
                'dark',
                'system'
            ].includes(v.theme) ? v.theme : 'light',
            accent: [
                'blue',
                'green',
                'purple'
            ].includes(v.accent) ? v.accent : 'blue',
            font: Math.max(12, Math.min(18, Number(v.font) || 14)),
            sidebar: Math.max(180, Math.min(420, Number(v.sidebar) || 248)),
            width: Math.max(640, Math.min(1100, Number(v.width) || 820)),
            tool: Math.max(22, Math.min(75, Number(v.tool) || 42)),
            notes: v.notes !== false,
            compact: v.compact === true,
            path: v.path !== false
        };
    } catch  {
        return {
            ...defaultAppearance
        };
    }
}
function installStickyBoard() {
    try {
        stickyScope = localStorage.getItem('jianzuo-sticky-scope') || 'all';
        stickyFilter = localStorage.getItem('jianzuo-sticky-filter') || 'open';
    } catch  {}
    element('task-list').insertAdjacentHTML('afterend', '<section id="sticky-board" class="sticky-board"><header><strong>待办</strong><span id="sticky-count" class="muted"></span><button id="sticky-expand" type="button" aria-expanded="false" aria-controls="sticky-list sticky-filter sticky-scope" aria-label="展开待办和筛选">展开</button><select id="sticky-filter" aria-label="待办筛选"><option value="open">未完成</option><option value="all">全部</option><option value="done">已完成</option></select><select id="sticky-scope" aria-label="待办范围"><option value="all">全部任务</option><option value="task">本任务</option></select><button id="sticky-new" title="新建待办">＋</button></header><div id="sticky-list" class="sticky-list"></div></section>');
    button('sticky-expand').onclick = ()=>{
        const board = element('sticky-board');
        board.dataset.mobileExpanded = board.dataset.mobileExpanded === 'true' ? 'false' : 'true';
        syncStickyCompact(board.dataset.empty === 'true');
    };
    input('sticky-scope').value = stickyScope;
    input('sticky-scope').onchange = ()=>{
        stickyScope = input('sticky-scope').value;
        try {
            localStorage.setItem('jianzuo-sticky-scope', stickyScope);
        } catch  {}
        renderStickyBoard();
    };
    input('sticky-filter').value = stickyFilter;
    input('sticky-filter').onchange = ()=>{
        stickyFilter = input('sticky-filter').value;
        try {
            localStorage.setItem('jianzuo-sticky-filter', stickyFilter);
        } catch  {}
        renderStickyBoard();
    };
    button('sticky-new').onclick = ()=>{
        if (!chosen) {
            notify('先选择一个任务，便签会保存在该任务中。');
            return;
        }
        editScratch(null);
    };
    button('scratch-tab').classList.add('hidden');
    stickyLast = '';
    void loadStickyBoard();
}
async function loadStickyBoard() {
    if (!authenticated || !element('sticky-list') || stickyLoading) return;
    stickyLoading = true;
    const request = ++stickyRequest;
    try {
        const items = await api('scratch');
        if (request !== stickyRequest) return;
        stickyItems = items;
        renderStickyBoard();
    } catch (e) {
        if (element('sticky-list') && !stickyItems.length) {
            syncStickyCompact(false);
            element('sticky-list').textContent = e.message;
        }
    } finally{
        stickyLoading = false;
    }
}
function syncStickyCompact(empty) {
    const board = element('sticky-board'), toggle = element('sticky-expand');
    if (!board || !toggle) return;
    board.dataset.empty = String(empty);
    const expanded = !empty || board.dataset.mobileExpanded === 'true';
    toggle.setAttribute('aria-expanded', String(expanded));
    toggle.setAttribute('aria-label', expanded ? '收起空待办和筛选' : '展开待办和筛选');
    toggle.textContent = expanded ? '收起' : '展开';
}
function renderStickyBoard() {
    if (!element('sticky-list')) return;
    const scoped = stickyItems.filter((n)=>stickyScope !== 'task' || n.task_id === chosen);
    const items = scoped.filter((n)=>{
        const s = scratchStatus(n);
        return stickyFilter === 'all' || (stickyFilter === 'open' ? s !== 'done' : s === 'done');
    });
    syncStickyCompact(items.length === 0);
    const html = items.map((n)=>scratchCardHTML(n, 'all')).join('') || `<p class="muted">${stickyFilter === 'done' ? '还没有完成的待办。' : '点 ＋ 记下一步要做的事，勾选即可完成。'}</p>`;
    if (element('sticky-count')) element('sticky-count').textContent = scoped.filter((n)=>scratchStatus(n) !== 'done').length + ' 项未完成';
    if (html === stickyLast) return;
    stickyLast = html;
    element('sticky-list').innerHTML = html;
    element('sticky-list').querySelectorAll('[data-toggle]').forEach((box)=>box.onclick = ()=>void toggleScratch(stickyItems.find((n)=>n.id === box.dataset.toggle), box.checked));
    element('sticky-list').querySelectorAll('[data-edit]').forEach((b)=>b.onclick = ()=>editScratch(stickyItems.find((n)=>n.id === b.dataset.edit) || null));
    element('sticky-list').querySelectorAll('[data-use]').forEach((b)=>b.onclick = ()=>useScratchItem(stickyItems.find((n)=>n.id === b.dataset.use)));
    element('sticky-list').querySelectorAll('[data-delete]').forEach((b)=>b.onclick = ()=>void deleteScratch(stickyItems.find((n)=>n.id === b.dataset.delete)));
}
setInterval(()=>{
    if (authenticated && !document.hidden && element('sticky-board')) void loadStickyBoard();
}, 5000);
function applyAppearance(value) {
    const root = document.documentElement;
    root.dataset.theme = value.theme === 'system' ? matchMedia('(prefers-color-scheme:dark)').matches ? 'dark' : 'light' : value.theme;
    root.dataset.accent = value.accent;
    root.dataset.density = value.compact ? 'compact' : 'normal';
    root.style.setProperty('--chat-font', value.font + 'px');
    root.style.setProperty('--sidebar-width', value.sidebar + 'px');
    root.style.setProperty('--chat-width', value.width + 'px');
    element('workspace')?.style.setProperty('--tool-width', value.tool + '%');
    element('sticky-board')?.classList.toggle('hidden', !value.notes);
    element('task-workspace')?.classList.toggle('hidden', !value.path);
}
function installAppearance() {
    try {
        appearance = parseAppearance(localStorage.getItem('jianzuo-appearance-v1'));
        if (!localStorage.getItem('jianzuo-appearance-v1') && localStorage.getItem('jianzuo-theme') === 'dark') appearance.theme = 'dark';
    } catch  {}
    applyAppearance(appearance);
    element('root').insertAdjacentHTML('beforeend', `<dialog id="appearance-dialog"><form id="appearance-form"><h2>调整界面</h2><div class="form-grid"><div><label for="appearance-theme">主题</label><select id="appearance-theme"><option value="light">浅色</option><option value="dark">深色</option><option value="system">跟随系统</option></select></div><div><label for="appearance-accent">强调色</label><select id="appearance-accent"><option value="blue">蓝色</option><option value="green">绿色</option><option value="purple">紫色</option></select></div></div><label for="appearance-font">对话字号</label><input id="appearance-font" type="range" min="12" max="18" step="1"><label for="appearance-sidebar">任务侧栏宽度</label><input id="appearance-sidebar" type="range" min="180" max="420" step="5"><label for="appearance-tool">左右工具栏宽度</label><input id="appearance-tool" type="range" min="22" max="75" step="1"><label for="appearance-width">对话内容宽度</label><input id="appearance-width" type="range" min="640" max="1100" step="20"><label class="check-row"><input id="appearance-notes" type="checkbox">常驻便签</label><label class="check-row"><input id="appearance-compact" type="checkbox">紧凑布局</label><label class="check-row"><input id="appearance-path" type="checkbox">显示任务工作路径</label><p>即时预览，保存到当前浏览器。</p><div class="dialog-footer"><button type="button" id="appearance-layout">恢复默认布局</button><button type="button" id="appearance-reset">恢复默认</button><button type="button" id="appearance-cancel">取消</button><button class="primary">保存</button></div></form></dialog>`);
    button('theme-toggle').onclick = ()=>{
        fillAppearance(appearance);
        element('appearance-dialog').showModal();
    };
    element('appearance-form').oninput = ()=>applyAppearance(readAppearance());
    element('appearance-form').onsubmit = (e)=>{
        e.preventDefault();
        appearance = readAppearance();
        try {
            localStorage.setItem('jianzuo-appearance-v1', JSON.stringify(appearance));
            localStorage.setItem('jianzuo-theme', appearance.theme);
        } catch  {}
        applyAppearance(appearance);
        element('appearance-dialog').close();
    };
    button('appearance-layout').onclick = ()=>{
        resetPanelLayout();
        fillAppearance(appearance);
    };
    button('appearance-reset').onclick = ()=>{
        fillAppearance(defaultAppearance);
        applyAppearance(defaultAppearance);
    };
    button('appearance-cancel').onclick = ()=>{
        applyAppearance(appearance);
        element('appearance-dialog').close();
    };
    element('appearance-dialog').addEventListener('cancel', ()=>applyAppearance(appearance));
}
function fillAppearance(v) {
    for (const key of [
        'theme',
        'accent',
        'font',
        'sidebar',
        'width',
        'tool'
    ])input('appearance-' + key).value = String(v[key]);
    for (const key of [
        'notes',
        'compact',
        'path'
    ])input('appearance-' + key).checked = v[key];
}
function readAppearance() {
    return parseAppearance(JSON.stringify({
        theme: input('appearance-theme').value,
        accent: input('appearance-accent').value,
        font: input('appearance-font').value,
        sidebar: input('appearance-sidebar').value,
        tool: input('appearance-tool').value,
        width: input('appearance-width').value,
        notes: input('appearance-notes').checked,
        compact: input('appearance-compact').checked,
        path: input('appearance-path').checked
    }));
}
const conversationFilterKey = 'jianzuo-conversation-filter-v1';
let conversationFilter = {
    tools: false,
    process: false
};
let conversationItems = [];
const conversationTurns = new Map();
function readConversationFilter(raw) {
    try {
        const value = JSON.parse(raw || 'null');
        if (typeof value?.tools === 'boolean' && typeof value?.process === 'boolean') return {
            tools: value.tools,
            process: value.process
        };
    } catch  {}
    return {
        tools: false,
        process: false
    };
}
function finalConversationEvents(events, runs) {
    const results = new Map(runs.filter((r)=>r.status === 'done' && r.result).map((r)=>[
            r.id,
            r.result
        ]));
    const final = new Set();
    for(let i = events.length - 1; i >= 0; i--){
        const event = events[i];
        if (event.kind === 'assistant' && results.get(event.run_id) === event.text) {
            final.add(event.seq);
            results.delete(event.run_id);
        }
    }
    return final;
}
function conversationCategory(event, final) {
    if (event.kind === 'error' || event.kind === 'status' && /^(failed|interrupted)(\s|$)/.test(event.text.trim())) return 'error';
    if (event.kind === 'user' || event.kind === 'assistant' && (!event.run_id || final.has(event.seq))) return 'message';
    if (event.kind === 'tool' || event.kind === 'log') return 'tools';
    return 'process';
}
function conversationEventVisible(category, filter) {
    return category === 'message' || category === 'error' || (category === 'tools' ? filter.tools : filter.process);
}
function resetConversation() {
    conversationItems = [];
    conversationTurns.clear();
}
function conversationTurn(id) {
    const existing = conversationTurns.get(id);
    if (existing) return existing;
    const root = document.createElement('section');
    root.className = 'conversation-turn';
    root.dataset.run = id;
    root.innerHTML = '<div class="turn-input"></div><div class="turn-attachments"></div><details class="turn-process"><summary></summary><div class="turn-records"></div></details><div class="turn-output"></div><footer class="turn-footer"></footer>';
    const turn = {
        root,
        input: root.children[0],
        process: root.querySelector('details'),
        summary: root.querySelector('summary'),
        body: root.querySelector('.turn-records'),
        output: root.querySelector('.turn-output'),
        files: root.querySelector('.turn-attachments'),
        footer: root.querySelector('.turn-footer')
    };
    turn.process.open = conversationFilter.tools || conversationFilter.process;
    conversationTurns.set(id, turn);
    element('conversation').append(root);
    return turn;
}
function installConversationFilter() {
    try {
        conversationFilter = readConversationFilter(localStorage.getItem(conversationFilterKey));
    } catch  {}
    element('conversation').insertAdjacentHTML('beforebegin', `<div id="conversation-filter" class="conversation-filter hidden" role="group" aria-label="对话显示"><div class="filter-presets"><button id="conversation-results" title="折叠每轮执行过程，保留回复和错误">对话</button><button id="conversation-all" title="展开全部执行记录">轨迹</button></div><details class="display-menu"><summary>筛选</summary><div><label><input type="checkbox" id="conversation-tools">命令与工具</label><label><input type="checkbox" id="conversation-process">中间过程</label><small>仅改变显示，记录完整保留</small></div></details><small id="conversation-hidden"></small></div>`);
    button('conversation-results').onclick = ()=>setConversationFilter({
            tools: false,
            process: false
        });
    button('conversation-all').onclick = ()=>setConversationFilter({
            tools: true,
            process: true
        });
    input('conversation-tools').onchange = ()=>setConversationFilter({
            ...conversationFilter,
            tools: input('conversation-tools').checked
        });
    input('conversation-process').onchange = ()=>setConversationFilter({
            ...conversationFilter,
            process: input('conversation-process').checked
        });
    updateConversationFilterControls();
}
function setConversationFilter(filter) {
    conversationFilter = filter;
    for (const turn of conversationTurns.values())turn.process.open = filter.tools || filter.process;
    try {
        localStorage.setItem(conversationFilterKey, JSON.stringify(filter));
    } catch  {}
    updateConversationFilterControls();
    applyConversationFilter();
}
function updateConversationFilterControls() {
    input('conversation-tools').checked = conversationFilter.tools;
    input('conversation-process').checked = conversationFilter.process;
    for (const [id, selected] of [
        [
            'conversation-results',
            !conversationFilter.tools && !conversationFilter.process
        ],
        [
            'conversation-all',
            conversationFilter.tools && conversationFilter.process
        ]
    ]){
        button(id).classList.toggle('selected', selected);
        button(id).setAttribute('aria-pressed', String(selected));
    }
}
function applyConversationFilter() {
    const container = element('conversation');
    if (!container) return;
    const nearBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 100, top = container.scrollTop;
    const final = finalConversationEvents(conversationItems.map((item)=>item.event), detail?.runs || []), counts = new Map();
    let hidden = 0;
    const compact = !conversationFilter.tools && !conversationFilter.process;
    for (const { event, node } of conversationItems){
        const category = conversationCategory(event, final), record = category === 'tools' || category === 'process';
        const turn = event.run_id ? conversationTurn(event.run_id) : null;
        const visible = node.dataset.searchMatch === 'true' || conversationEventVisible(category, conversationFilter) || !!turn && compact;
        node.dataset.category = category;
        node.classList.toggle('hidden', !visible);
        node.classList.toggle('error', category === 'error');
        if (!visible) hidden++;
        if (turn) {
            const parent = record ? turn.body : event.kind === 'user' ? turn.input : turn.output;
            if (node.parentElement !== parent) parent.append(node);
            if (record) counts.set(event.run_id, (counts.get(event.run_id) || 0) + 1);
            if (node.dataset.searchMatch === 'true') turn.process.open = true;
        }
    }
    for (const [id, turn] of conversationTurns){
        const count = counts.get(id) || 0, run = detail?.runs.find((r)=>r.id === id);
        turn.process.classList.toggle('hidden', count === 0);
        const label = detail?.approvals?.some((request)=>request.run_id === id) ? '等待你处理' : run?.status === 'running' ? '正在执行' : run?.status === 'queued' ? '排队中' : '执行记录';
        const text = label + ' · ' + count + ' 条';
        if (turn.summary.textContent !== text) turn.summary.textContent = text;
        const footer = run ? runFooter(run) : '';
        if (turn.footer.innerHTML !== footer) turn.footer.innerHTML = footer;
        const files = (run?.attachments || []).map((f)=>`<a href="/api/tasks/${encodeURIComponent(chosen)}/attachments/${encodeURIComponent(f.id)}" class="attachment-chip" download="${escapeHTML(f.name)}">↧ ${escapeHTML(f.name)}</a>`).join('');
        if (turn.files.innerHTML !== files) turn.files.innerHTML = files;
    }
    const counter = element('conversation-hidden');
    if (counter) counter.textContent = hidden ? '已筛除 ' + hidden + ' 条' : '';
    if (nearBottom) container.scrollTop = container.scrollHeight;
    else container.scrollTop = top;
}
function formatTokens(n) {
    if (n >= 1000000) return (n / 1000000).toFixed(1).replace(/\.0$/, '') + 'M';
    if (n >= 1000) return (n / 1000).toFixed(1).replace(/\.0$/, '') + 'K';
    return String(n);
}
function formatDuration(ms) {
    const seconds = Math.max(0, Math.round(ms / 1000));
    return seconds >= 60 ? Math.floor(seconds / 60) + '分' + seconds % 60 + '秒' : seconds + '秒';
}
function runFooter(run) {
    if (!run.finished || [
        'queued',
        'running'
    ].includes(run.status)) return '';
    const usage = run.usage, usageTip = usage ? `输入 ${usage.input} · 输出 ${usage.output} · 缓存读取 ${usage.cached || 0} · 缓存写入 ${usage.cache_write || 0}` : '此轮引擎未返回用量，历史记录不作估算';
    const durationTip = run.started ? '从本轮实际开始执行计算' : '旧记录未保存开始时间，包含排队时间';
    const runId = typeof run.id === 'string' ? run.id : '';
    const settled = runId && typeof knowledgeForRun === 'function' ? knowledgeForRun(runId) : null;
    const knowledge = runId ? `<button type="button" class="run-knowledge" data-knowledge-run="${escapeHTML(runId)}" ${settled ? 'disabled' : ''} title="${settled ? '这轮结果已经沉淀到任务知识' : '把这轮结果存成一条任务知识'}">${settled ? '已沉淀' : '沉淀为知识'}</button>` : '';
    return `<span title="${escapeHTML(usageTip)}">用量 ${usage ? formatTokens(usage.total) + ' tok' : '未提供'}</span><span title="${durationTip}">用时 ${formatDuration(run.finished - (run.started || run.created))}</span><time title="${escapeHTML(new Date(run.finished).toLocaleString())}">时间 ${new Date(run.finished).toLocaleTimeString('zh-CN', {
        hour: '2-digit',
        minute: '2-digit',
        hour12: false
    })}</time>${knowledge}`;
}
const codexApprovalCards = new Map();
let codexApprovalRevision = 0;
function codexRecord(value) {
    return value !== null && typeof value === 'object' && !Array.isArray(value) ? value : {};
}
function codexText(value) {
    return typeof value === 'string' ? value : '';
}
function codexPretty(value) {
    return value === undefined || value === null ? '' : typeof value === 'string' ? value : JSON.stringify(value, null, 2);
}
function codexApprovalKey(request) {
    return JSON.stringify([
        request.task_id,
        request.run_id,
        request.id
    ]);
}
function codexApprovalKind(request) {
    return ({
        'item/commandExecution/requestApproval': '命令执行',
        'item/fileChange/requestApproval': '文件修改',
        'item/permissions/requestApproval': '临时权限',
        'item/tool/requestUserInput': '补充信息'
    })[request.method] || '暂不支持的请求';
}
function codexApprovalDecisions(request) {
    if (request.method === 'item/permissions/requestApproval') return [
        'accept',
        'decline'
    ];
    if (![
        'item/commandExecution/requestApproval',
        'item/fileChange/requestApproval'
    ].includes(request.method)) return [];
    const offered = codexRecord(request.params).availableDecisions;
    return [
        'accept',
        'decline',
        'cancel'
    ].filter((value)=>!Array.isArray(offered) || offered.includes(value));
}
function codexDecisionBody(request, decision) {
    if (!codexApprovalDecisions(request).includes(decision)) throw new Error('此请求不支持该操作');
    return {
        decision
    };
}
function codexQuestions(request) {
    const values = codexRecord(request.params).questions;
    if (!Array.isArray(values)) return [];
    const ids = new Set();
    return values.map((value)=>{
        const q = codexRecord(value), id = codexText(q.id);
        if (!id || ids.has(id)) throw new Error('原生问题缺少唯一标识，无法安全提交');
        ids.add(id);
        return {
            id,
            header: codexText(q.header),
            question: codexText(q.question),
            isOther: q.isOther === true,
            isSecret: q.isSecret === true,
            options: Array.isArray(q.options) ? q.options.map((option)=>{
                const o = codexRecord(option);
                return {
                    label: codexText(o.label),
                    description: codexText(o.description)
                };
            }).filter((o)=>o.label) : []
        };
    });
}
function codexAnswersBody(request, read) {
    if (request.method !== 'item/tool/requestUserInput') throw new Error('此请求不是问题表单');
    const questions = codexQuestions(request);
    if (!questions.length) throw new Error('请求未提供可回答的问题，请停止本轮后重试');
    const answers = Object.create(null);
    questions.forEach((question, index)=>{
        const values = read(question, index).map((s)=>question.isSecret ? s : s.trim()).filter((s)=>s.trim());
        if (!values.length) throw new Error('请回答：' + (question.header || question.question || question.id));
        if (values.some((v)=>v.length > 16000)) throw new Error('答案过长，请缩短后重试');
        if (question.options.length && !question.isOther && values.some((v)=>!question.options.some((o)=>o.label === v))) throw new Error('请选择请求提供的答案');
        answers[question.id] = {
            answers: values
        };
    });
    return {
        answers
    };
}
function codexApprovalFields(request, task) {
    const params = codexRecord(request.params), env = task.environment;
    const fields = [
        {
            label: '执行环境',
            value: [
                env?.name,
                env?.type?.toUpperCase(),
                env?.distro,
                env?.host,
                env?.user
            ].filter(Boolean).join(' · ') || '未提供'
        },
        {
            label: '工作目录',
            value: codexText(params.cwd) || task.workspace || '未提供'
        },
        {
            label: '原因',
            value: codexText(params.reason) || '原生请求未提供原因'
        }
    ];
    const add = (label, value)=>{
        const text = codexPretty(value);
        if (text) fields.push({
            label,
            value: text
        });
    };
    if (request.method === 'item/commandExecution/requestApproval') add('命令', params.command ?? '原生请求未提供命令');
    const network = codexRecord(params.networkApprovalContext);
    if (network.host) add('网络目标', [
        codexText(network.protocol),
        codexText(network.host)
    ].filter(Boolean).join(' · '));
    if (params.grantRoot) add('申请授权路径', params.grantRoot);
    if (params.changes) add('申请修改路径 / 内容', params.changes);
    if (params.commandActions) add('命令解析', params.commandActions);
    if (params.additionalPermissions) add('附加权限', params.additionalPermissions);
    if (params.permissions) add('本轮申请的权限', params.permissions);
    if (request.method === 'item/fileChange/requestApproval' && !params.changes) add('文件范围', '此原生请求未提供逐文件差异；请结合执行记录及申请授权路径核对。');
    return fields;
}
function codexApprovalFieldsHTML(request, task) {
    return codexApprovalFields(request, task).map((field)=>`<div class="codex-approval-field"><dt>${escapeHTML(field.label)}</dt><dd><pre>${escapeHTML(field.value)}</pre></dd></div>`).join('');
}
function installCodexApprovals() {
    const panel = document.createElement('section');
    panel.id = 'codex-approvals';
    panel.className = 'codex-approvals hidden';
    panel.setAttribute('aria-label', 'Codex 待处理请求');
    panel.innerHTML = '<div class="codex-approvals-heading"><strong id="codex-approvals-title" role="status"></strong><span>仅处理此原生请求，不创建永久授权规则</span></div><div id="codex-approval-list"></div>';
    element('workspace').before(panel);
}
function resetCodexApprovals() {
    codexApprovalRevision++;
    codexApprovalCards.clear();
    element('codex-approval-list')?.replaceChildren();
    element('codex-approvals')?.classList.add('hidden');
}
function createCodexApprovalCard(request, task) {
    const node = document.createElement('article');
    node.className = 'codex-approval-card';
    node.dataset.request = request.id;
    node.innerHTML = `<h3>${escapeHTML(codexApprovalKind(request))}</h3><dl class="codex-approval-fields">${codexApprovalFieldsHTML(request, task)}</dl><div class="codex-approval-questions"></div><p class="codex-approval-scope">${request.method === 'item/permissions/requestApproval' ? '仅授予本次请求列出的权限，有效范围为当前轮次。' : '批准仅针对当前请求；不会改写会话的审批模式。'}</p><div class="codex-approval-actions"></div><p class="codex-approval-status" role="status"></p>`;
    const card = {
        request,
        node,
        busy: false,
        settled: false,
        error: ''
    }, actions = node.querySelector('.codex-approval-actions');
    if (request.method === 'item/tool/requestUserInput') {
        try {
            const questions = codexQuestions(request), target = node.querySelector('.codex-approval-questions');
            questions.forEach((question, index)=>{
                const field = document.createElement('fieldset');
                field.dataset.question = String(index);
                const legend = document.createElement('legend');
                legend.textContent = question.header || question.id;
                field.append(legend);
                const prompt = document.createElement('p');
                prompt.textContent = question.question;
                field.append(prompt);
                question.options.forEach((option)=>{
                    const label = document.createElement('label');
                    label.className = 'codex-answer-option';
                    const control = document.createElement('input');
                    control.type = 'radio';
                    control.name = codexApprovalKey(request) + '-' + index;
                    control.value = option.label;
                    control.dataset.answer = 'option';
                    const text = document.createElement('span');
                    text.textContent = option.label + (option.description ? ' — ' + option.description : '');
                    label.append(control, text);
                    field.append(label);
                });
                if (question.isOther || !question.options.length) {
                    const label = document.createElement('label');
                    label.textContent = question.options.length ? '其他答案（填写后优先提交）' : '你的答案';
                    const control = document.createElement('input');
                    control.type = question.isSecret ? 'password' : 'text';
                    control.autocomplete = 'off';
                    control.maxLength = 16000;
                    control.dataset.answer = 'free';
                    control.setAttribute('aria-label', question.header || question.question || question.id);
                    label.append(control);
                    field.append(label);
                }
                target.append(field);
            });
            if (questions.length) appendCodexAction(actions, '提交答案', 'primary', ()=>{
                try {
                    const body = codexAnswersBody(request, (_question, index)=>{
                        const field = target.querySelector(`[data-question="${index}"]`), free = field.querySelector('[data-answer="free"]')?.value, option = field.querySelector('[data-answer="option"]:checked')?.value;
                        return free?.trim() ? [
                            free
                        ] : option ? [
                            option
                        ] : [];
                    });
                    void submitCodexApproval(card, body);
                } catch (error) {
                    card.error = error.message;
                    updateCodexApprovalCard(card);
                }
            });
            else card.error = '原生请求未提供可回答的问题，请停止本轮后重试。';
        } catch (error) {
            card.error = error.message;
        }
        appendCodexAction(actions, '停止本轮', '', ()=>void stopCodexApprovalRun(card));
    } else {
        for (const decision of codexApprovalDecisions(request))appendCodexAction(actions, {
            accept: '批准本次',
            decline: '拒绝本次',
            cancel: '取消本轮'
        }[decision], decision === 'accept' ? 'primary' : '', ()=>void submitCodexApproval(card, codexDecisionBody(request, decision)));
        if (!actions.childElementCount) {
            card.error = '暂不支持此请求，不能在此页面授权；请停止本轮。';
            appendCodexAction(actions, '停止本轮', '', ()=>void stopCodexApprovalRun(card));
        }
    }
    const refresh = document.createElement('button');
    refresh.type = 'button';
    refresh.className = 'codex-approval-refresh subtle';
    refresh.textContent = '刷新请求';
    refresh.onclick = ()=>void refreshCodexApprovals(request.task_id).catch((error)=>{
            card.error = error.message;
            updateCodexApprovalCard(card);
        });
    node.append(refresh);
    return card;
}
function appendCodexAction(target, label, className, action) {
    const control = document.createElement('button');
    control.type = 'button';
    control.className = className;
    control.textContent = label;
    control.onclick = action;
    target.append(control);
}
function updateCodexApprovalCard(card) {
    const disabled = card.busy || card.settled || stoppingTask === card.request.task_id;
    card.node.setAttribute('aria-busy', String(card.busy));
    card.node.querySelectorAll('.codex-approval-actions button,.codex-approval-questions input').forEach((control)=>control.disabled = disabled);
    const status = card.node.querySelector('.codex-approval-status'), message = card.error || (card.busy ? '正在提交…' : card.settled ? '已提交或请求已过期，正在同步最新状态…' : stoppingTask === card.request.task_id ? '正在停止本轮…' : '等待你处理');
    if (status.textContent !== message) status.textContent = message;
    status.classList.toggle('error', !!card.error);
}
function renderCodexApprovals(current) {
    const panel = element('codex-approvals'), list = element('codex-approval-list');
    if (!panel || !list) return;
    const pending = current && !creatingTask && current.task.id === chosen ? (current.approvals || []).filter((request)=>request.task_id === current.task.id) : [], keys = new Set(pending.map(codexApprovalKey));
    for (const [key, card] of codexApprovalCards)if (!keys.has(key)) {
        card.node.remove();
        codexApprovalCards.delete(key);
    }
    for (const request of pending){
        const key = codexApprovalKey(request);
        let card = codexApprovalCards.get(key);
        if (!card) {
            card = createCodexApprovalCard(request, current.task);
            codexApprovalCards.set(key, card);
            list.append(card.node);
        }
        updateCodexApprovalCard(card);
    }
    panel.classList.toggle('hidden', !pending.length);
    const heading = element('codex-approvals-title'), title = 'Codex 等待处理 · ' + pending.length;
    if (heading.textContent !== title) heading.textContent = title;
}
async function refreshCodexApprovals(taskID) {
    if (taskID !== chosen || !authenticated) return;
    const token = selection, revision = ++codexApprovalRevision;
    const fresh = await api('tasks/' + encodeURIComponent(taskID) + '?after=' + sequence);
    if (taskID !== chosen || token !== selection || revision !== codexApprovalRevision) return;
    detail = fresh;
    appendEvents(fresh.events);
    renderTask();
}
async function submitCodexApproval(card, body) {
    if (card.busy || card.settled || stoppingTask === card.request.task_id || !detail?.approvals?.some((request)=>codexApprovalKey(request) === codexApprovalKey(card.request))) return;
    card.busy = true;
    card.error = '';
    codexApprovalRevision++;
    updateCodexApprovalCard(card);
    try {
        await api('tasks/' + encodeURIComponent(card.request.task_id) + '/approvals/' + encodeURIComponent(card.request.id), 'POST', body);
        card.settled = true;
    } catch (error) {
        if (error.status === 409) {
            card.settled = true;
            card.error = '请求已过期或已在其他窗口处理，正在刷新。';
        } else card.error = error.message;
    } finally{
        card.busy = false;
        updateCodexApprovalCard(card);
    }
    if (card.settled) try {
        await refreshCodexApprovals(card.request.task_id);
    } catch (error) {
        card.error = '同步失败，请刷新请求：' + error.message;
        updateCodexApprovalCard(card);
    }
}
async function stopCodexApprovalRun(card) {
    if (card.busy || card.settled || stoppingTask === card.request.task_id || !detail?.approvals?.some((request)=>codexApprovalKey(request) === codexApprovalKey(card.request))) return;
    card.busy = true;
    stoppingTask = card.request.task_id;
    renderWorkflow();
    updateCodexApprovalCard(card);
    try {
        await api('tasks/' + encodeURIComponent(card.request.task_id) + '/stop', 'POST', {});
        card.settled = true;
        await refreshCodexApprovals(card.request.task_id);
    } catch (error) {
        card.error = error.message;
        stoppingTask = '';
    } finally{
        card.busy = false;
        updateCodexApprovalCard(card);
        if (detail?.task.id === card.request.task_id) renderWorkflow();
    }
}
const collapsedWorkspaces = new Set();
try {
    const saved = JSON.parse(localStorage.getItem('jianzuo-folders-v1') || '[]');
    if (Array.isArray(saved)) saved.filter((x)=>typeof x === 'string').forEach((x)=>collapsedWorkspaces.add(x));
} catch  {}
function groupWorkspaceTasks(items) {
    const groups = new Map();
    for (const task of [
        ...items
    ].sort((a, b)=>Number(b.pinned) - Number(a.pinned) || b.updated - a.updated)){
        const env = task.environment, path = task.workspace;
        const key = JSON.stringify([
            env?.id || '',
            env?.type || '',
            env?.host || '',
            env?.distro || '',
            env?.user || '',
            path
        ]);
        let group = groups.get(key);
        if (!group) {
            group = {
                key,
                name: path.replace(/[\\/]+$/, '').split(/[\\/]/).at(-1) || path,
                environment: env?.name || '本机',
                path,
                tasks: []
            };
            groups.set(key, group);
        }
        group.tasks.push(task);
    }
    return [
        ...groups.values()
    ];
}
function taskItemMenu(t) {
    const busy = !t.archived && (t.status === 'running' || t.status === 'queued');
    return `<details class="task-item-menu"><summary aria-label="任务操作" title="任务操作">⋯</summary><div><button data-task-action="rename" data-task-id="${escapeHTML(t.id)}">改名…</button><button data-task-action="pin" data-task-id="${escapeHTML(t.id)}">${t.pinned ? '取消置顶' : '置顶'}</button><button data-task-action="archive" data-task-id="${escapeHTML(t.id)}"${busy ? ' disabled' : ''}>${t.archived ? '恢复任务' : '归档'}</button><button class="danger" data-task-action="trash" data-task-id="${escapeHTML(t.id)}"${busy ? ' disabled' : ''}>删除会话…</button></div></details>`;
}
function workspaceTaskList(items) {
    return groupWorkspaceTasks(items).map((g)=>`<section class="workspace-group"><button class="workspace-heading" data-workspace="${escapeHTML(g.key)}" aria-expanded="${!collapsedWorkspaces.has(g.key)}" title="${escapeHTML(g.environment + ' · ' + g.path)}"><span class="folder-arrow">${collapsedWorkspaces.has(g.key) ? '›' : '⌄'}</span><span class="folder-name">${escapeHTML(g.name)}</span><small>${escapeHTML(g.environment)}</small></button><div class="workspace-tasks ${collapsedWorkspaces.has(g.key) ? 'hidden' : ''}">${g.tasks.map((t)=>`<div class="task-row"><button class="task ${t.id === chosen ? 'selected' : ''}" data-task="${escapeHTML(t.id)}" title="${escapeHTML(t.title)}"><strong>${t.pinned ? '↑ ' : ''}${escapeHTML(t.title)}</strong><small class="task-state ${t.status === 'running' || t.status === 'queued' ? 'active' : ''}">${t.archived ? '已归档' : names[t.status] || escapeHTML(t.status)}</small></button>${taskItemMenu(t)}</div>`).join('')}</div></section>`).join('');
}
function installLayout() {
    let theme = 'light';
    try {
        theme = localStorage.getItem('jianzuo-theme') === 'dark' ? 'dark' : 'light';
    } catch  {}
    document.documentElement.dataset.theme = theme;
    element('settings-open').insertAdjacentHTML('afterend', '<button id="theme-toggle" class="subtle" title="切换浅色 / 深色外观">外观</button>');
    const footer = element('settings-open').parentElement;
    footer.id = 'sidebar-footer';
    const utilities = document.createElement('nav');
    utilities.className = 'sidebar-utilities';
    utilities.setAttribute('aria-label', '工作台设置');
    for (const id of [
        'settings-open',
        'theme-toggle',
        'sidebar-close',
        'logout'
    ])utilities.append(element(id));
    footer.prepend(utilities);
    element('connection').setAttribute('role', 'status');
    button('theme-toggle').onclick = ()=>{
        const next = document.documentElement.dataset.theme === 'light' ? 'dark' : 'light';
        document.documentElement.dataset.theme = next;
        try {
            localStorage.setItem('jianzuo-theme', next);
        } catch  {}
    };
    const tabs = element('tabs');
    tabs.prepend(element('conversation-filter'));
    button('chat-tab').classList.add('hidden');
    for (const id of [
        'conversation-results',
        'conversation-all'
    ])button(id).addEventListener('click', ()=>{
        if (matchMedia('(max-width:760px)').matches) switchTab('chat');
    });
    element('task-model').insertAdjacentHTML('beforebegin', '<details class="tools-menu"><summary>更多</summary><div id="more-tools"></div></details>');
    for (const id of [
        'note-tab',
        'scratch-tab'
    ])element('more-tools').append(element(id));
    element('more-tools').addEventListener('click', ()=>element('more-tools').parentElement.removeAttribute('open'));
    button('files-tab').textContent = '文件';
    button('hardware-tab').textContent = '硬件';
    const model = element('task-model');
    element('composer').querySelector('.composer-bottom').prepend(model);
    element('task-model-button').title = '本任务使用的 AI 工具、模型和推理强度';
    element('composer').querySelector('.composer-bottom small').remove();
    input('message').title = 'Enter 发送，Shift + Enter 换行';
    element('composer-wrap').querySelector('.footnote').remove();
    element('task-list').addEventListener('click', (e)=>{
        const node = e.target;
        const menu = node.closest('.task-item-menu');
        if (menu) {
            const action = node.closest('[data-task-action]');
            if (!action) return;
            menu.removeAttribute('open');
            if (action.disabled) return;
            const id = action.dataset.taskId || '', kind = action.dataset.taskAction;
            if (kind === 'rename') void openTaskRename(id);
            else if (kind === 'pin') void changeTaskPreference('pinned', id);
            else if (kind === 'archive') void changeTaskPreference('archived', id);
            else if (kind === 'trash') void trashCurrentTask(id);
            return;
        }
        const target = node.closest('[data-workspace]');
        if (!target) return;
        const key = target.dataset.workspace;
        if (collapsedWorkspaces.has(key)) collapsedWorkspaces.delete(key);
        else collapsedWorkspaces.add(key);
        try {
            localStorage.setItem('jianzuo-folders-v1', JSON.stringify([
                ...collapsedWorkspaces
            ]));
        } catch  {}
        renderList();
    });
    const closeTaskMenus = ()=>element('task-list').querySelectorAll('.task-item-menu[open]').forEach((d)=>d.removeAttribute('open'));
    element('task-list').addEventListener('toggle', (e)=>{
        const details = e.target;
        if (!details.classList.contains('task-item-menu')) return;
        const panel = details.querySelector('div');
        if (!panel) return;
        if (!details.open) {
            panel.classList.remove('shown');
            return;
        }
        const box = details.getBoundingClientRect(), height = panel.offsetHeight, width = panel.offsetWidth;
        const top = box.bottom + height + 12 > window.innerHeight && box.top > height + 12 ? box.top - height - 6 : box.bottom + 6;
        panel.style.left = Math.round(Math.max(8, Math.min(window.innerWidth - width - 8, box.right - width))) + 'px';
        panel.style.top = Math.round(top) + 'px';
        panel.classList.add('shown');
    }, true);
    element('task-list').addEventListener('scroll', closeTaskMenus, {
        passive: true
    });
    listenWithShell(document, 'click', (e)=>{
        if (!e.target.closest('.task-item-menu')) closeTaskMenus();
    });
    listenWithShell(document, 'keydown', (e)=>{
        if (e.key === 'Escape') closeTaskMenus();
    });
    element('task-title').textContent = '今天，从哪件事开始？';
    element('task-workspace').textContent = 'Windows · WSL · SSH';
    element('conversation').querySelector('.empty').innerHTML = '<div class="empty-mark">D</div><h2>一件事，一个任务。</h2><p>把目标交给 Codex、Claude Code 或 DeepSeek Harness，<br>在网页和飞书继续，留下可复用的经验。</p><button class="primary" id="empty-new">＋ 新建任务</button>';
}
try {
    document.documentElement.dataset.theme = localStorage.getItem('jianzuo-theme') === 'dark' ? 'dark' : 'light';
} catch  {
    document.documentElement.dataset.theme = 'light';
}
let detectedEnvironments = [];
function sameDetectedEnvironment(a, b) {
    return a.type === b.type && (a.type === 'windows' || a.distro === b.distro && a.user === b.user);
}
function installEnvironmentDiscovery() {
    const section = element('settings-environment');
    section.insertAdjacentHTML('afterbegin', '<div class="environment-detection"><div><strong>找到这台电脑上的 AI 工具</strong><button type="button" id="detect-local-environments">检测环境</button></div><p>检测 Windows 和 WSL，可能启动已安装的 WSL。SSH 使用下方“发现局域网 SSH”。</p><p id="environment-detection-status" role="status"></p><div id="detected-environments"></div></div>');
    button('detect-local-environments').onclick = async ()=>{
        const control = button('detect-local-environments');
        control.disabled = true;
        element('environment-detection-status').textContent = '正在检测安装位置和本地登录配置，约需 10–25 秒…';
        try {
            const result = await api('environments/discover', 'POST', {});
            if (!element('detected-environments')) return;
            detectedEnvironments = result.items;
            renderDetectedEnvironments();
            element('environment-detection-status').textContent = result.message;
        } catch (e) {
            if (element('environment-detection-status')) element('environment-detection-status').textContent = e.message;
        } finally{
            control.disabled = false;
        }
    };
    const advanced = document.createElement('details');
    advanced.className = 'environment-advanced';
    advanced.innerHTML = '<summary>高级设置 · AI 程序位置和默认模型</summary>';
    const first = element('setting-codex').previousElementSibling, last = element('setting-workspaces').previousElementSibling;
    first.before(advanced);
    let node = first;
    while(node && node !== last){
        const next = node.nextSibling;
        advanced.append(node);
        node = next;
    }
    input('search').placeholder = '搜索任务和记录';
    const modelManager = document.createElement('section');
    modelManager.className = 'model-manager';
    modelManager.innerHTML = '<div class="model-manager-head"><div><h3>模型目录</h3><p>为当前环境的指定引擎补充模型 ID。新建任务会自动读取目标 CLI；这里添加的名称不代表已验证可用，实际调用仍由引擎和 provider 决定。</p></div><span class="model-manager-badge">本地配置</span></div><div id="configured-models" class="configured-models"></div><label for="custom-model-engine">模型所属引擎</label><select id="custom-model-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option><option value="deepseek-harness">DeepSeek Harness</option><option value="">所有引擎（兼容共享配置）</option></select><div class="model-add-row"><input id="custom-model-id" aria-label="模型 ID" maxlength="120" placeholder="模型 ID，例如 deepseek-flash"><input id="custom-model-name" aria-label="模型显示名称" maxlength="120" placeholder="显示名称（可选）"><button type="button" id="custom-model-add" class="primary">添加模型</button></div><p id="custom-model-result" class="settings-result" role="status"></p>';
    section.append(modelManager);
    button('custom-model-add').onclick = ()=>{
        const id = input('custom-model-id').value.trim(), name = input('custom-model-name').value.trim() || id, engine = input('custom-model-engine').value;
        if (!id) {
            element('custom-model-result').textContent = '请输入模型 ID';
            input('custom-model-id').focus();
            return;
        }
        if (!/^[^\s\r\n]{1,120}$/.test(id)) {
            element('custom-model-result').textContent = '模型 ID 不能包含空格或换行';
            return;
        }
        const env = editingEnvironments.find((e)=>e.id === editingID);
        if (!env) return;
        env.models = env.models || [];
        if (env.models.some((m)=>m.id === id && (m.engine || '') === engine)) {
            element('custom-model-result').textContent = '这个引擎的模型已经添加';
            return;
        }
        env.models.push({
            id,
            name,
            engine
        });
        input('custom-model-id').value = '';
        input('custom-model-name').value = '';
        element('custom-model-result').textContent = '已添加，保存设置后生效';
        renderConfiguredModels();
    };
    renderConfiguredModels();
}
function renderConfiguredModels() {
    const target = element('configured-models');
    if (!target) return;
    const env = editingEnvironments?.find((e)=>e.id === editingID);
    const models = env?.models || [];
    target.innerHTML = models.map((m, i)=>`<div class="configured-model"><div><strong>${escapeHTML(m.id)}</strong><small>${escapeHTML(m.name || m.id)} · ${m.engine ? escapeHTML(taskEngineName(m.engine)) : '所有引擎'}</small></div><button type="button" data-remove-custom-model="${i}" title="删除此模型">删除</button></div>`).join('') || '<p class="muted">还没有自定义模型。发现的 CLI 模型仍会自动显示。</p>';
    target.querySelectorAll('[data-remove-custom-model]').forEach((b)=>b.onclick = ()=>{
            const e = editingEnvironments.find((x)=>x.id === editingID);
            if (!e?.models) return;
            e.models.splice(Number(b.dataset.removeCustomModel), 1);
            renderConfiguredModels();
            element('custom-model-result').textContent = '已移除，保存设置后生效';
        });
}
function renderDetectedEnvironments() {
    element('detected-environments').innerHTML = detectedEnvironments.map((item, index)=>{
        const env = item.environment, existing = editingEnvironments.find((e)=>sameDetectedEnvironment(e, env));
        return `<div class="detected-environment"><div><strong>${escapeHTML(env.name)}</strong><small>${escapeHTML(env.user ? env.user + ' · ' + env.workspaces[0] : env.workspaces[0])}</small><small>Codex：${escapeHTML(item.codex.label)}<br>Claude：${escapeHTML(item.claude.label)}</small>${item.message ? '<small>' + escapeHTML(item.message) + '</small>' : ''}</div><button type="button" data-detected="${index}">${existing ? '查看配置' : '添加'}</button></div>`;
    }).join('');
    element('detected-environments').querySelectorAll('[data-detected]').forEach((b)=>b.onclick = ()=>{
            const item = detectedEnvironments[Number(b.dataset.detected)];
            if (!item) return;
            storeEnvironmentEditor();
            const existing = editingEnvironments.find((e)=>sameDetectedEnvironment(e, item.environment));
            if (existing) editingID = existing.id;
            else {
                if (editingEnvironments.length >= 30) {
                    notify('最多配置 30 个环境');
                    return;
                }
                const env = {
                    ...item.environment,
                    id: 'env_' + Date.now().toString(36) + '_' + Math.random().toString(36).slice(2, 6),
                    workspaces: [
                        ...item.environment.workspaces
                    ]
                };
                editingEnvironments.push(env);
                editingID = env.id;
            }
            environmentPickers();
            loadEnvironmentEditor();
            renderDetectedEnvironments();
            input('setting-workspaces').scrollIntoView({
                block: 'center'
            });
            notify(existing ? '已打开已有配置，检测结果没有覆盖它。' : '已填入环境。选好工作目录后，点击保存设置。');
        });
}
let hardwareView = null, hardwarePortsRequest = 0, hardwareEditingTask = '', hardwareLoadRequest = 0;
let hardwarePollTimer, hardwareSaving = false;
const hardwareDrafts = new Map();
let hardwareAIGrants = {}, hardwareAIEditing = null;
let hardwareOverview = [], hardwareOverviewTask = '', hardwareOverviewLoading = false, hardwareAISaving = false, hardwareLibraryAttaching = false;
let hardwareOverviewTimer;
function hexBytes(hex) {
    return Uint8Array.from(hex.match(/.{1,2}/g) || [], (pair)=>parseInt(pair, 16));
}
function bytesHex(bytes) {
    return Array.from(bytes, (b)=>b.toString(16).padStart(2, '0')).join('');
}
function hardwarePlainText(events) {
    const decode = new TextDecoder();
    let text = '';
    for (const e of events){
        if (e.direction === 'rx') text += decode.decode(hexBytes(e.hex), {
            stream: true
        });
        else if (e.direction === 'status') text += decode.decode() + '\n[' + e.text + ']\n';
    }
    return (text + decode.decode()).replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '').replace(/\x1b\][\s\S]*?(?:\x07|\x1b\\)/g, '');
}
function installHardware() {
    element('hardware-panel').innerHTML = `<div class="hardware-connect"><select id="device-picker" aria-label="硬件连接"></select><button id="device-connect" class="primary">连接</button><button id="device-disconnect" class="hidden">断开</button><button id="device-edit">配置</button><button id="device-library">设备库</button><button id="device-new" title="添加串口或网络连接">＋</button></div><div class="hardware-ownership"><span id="device-owner"></span><button id="device-claim" class="hidden">取得控制</button><button id="device-release" class="hidden">释放控制</button></div><section id="relay-panel" class="hidden"><strong id="relay-status">尚未查询</strong><div class="actions"><button data-relay="query">查询</button><button data-relay="on">上电</button><button data-relay="off">断电</button><button data-relay="cycle">断电重启</button></div><small>显示继电器触点反馈，不代表板子已启动。</small></section><div class="hardware-meta"><span id="device-status" role="status">未连接</span><span id="device-description"></span></div><div class="hardware-viewbar"><select id="device-view" aria-label="显示方式"><option value="terminal">交互终端</option><option value="text">收发日志</option><option value="hex">HEX 日志</option></select><button id="device-clear">清屏</button><button id="device-pause">暂停显示</button><button id="device-export">导出</button><button id="device-analyze">选中分析</button><label id="hardware-task-filter" class="check-row"><input type="checkbox" id="hardware-task-log">本任务日志</label></div><div id="hardware-terminal" role="group" aria-label="硬件交互终端"></div><pre id="device-console" class="hidden" tabindex="0" aria-label="硬件收发日志"></pre><div class="hardware-keys"><button data-hardware-key="enter">Enter</button><button data-hardware-key="tab">Tab</button><button data-hardware-key="up" aria-label="串口历史上一条">↑</button><button data-hardware-key="down" aria-label="串口历史下一条">↓</button><button data-hardware-key="ctrl-c">Ctrl+C</button><button data-hardware-key="esc">Esc</button><button id="device-bottom">到底部</button></div><p id="hardware-hint" class="muted">连接后点击终端，直接输入命令。</p><details id="hardware-send-details"><summary>文本 / HEX 发送与终端选项</summary><form id="device-send"><textarea id="device-data" rows="2" aria-label="发送内容" placeholder="输入文本或十六进制字节"></textarea><div class="device-toolbar"><select id="device-encoding" aria-label="发送编码"><option value="text">文本</option><option value="hex">HEX</option></select><select id="device-newline" aria-label="行尾"><option value="cr">CR</option><option value="lf">LF</option><option value="crlf">CRLF</option><option value="none">无</option></select><button type="button" id="device-interrupt">Ctrl+C</button><button id="device-send-button" class="primary">发送</button></div></form><div class="hardware-options"><label>终端回车<select id="hardware-enter"><option value="cr">CR</option><option value="lf">LF</option><option value="crlf">CRLF</option></select></label><label>退格<select id="hardware-backspace"><option value="del">DEL</option><option value="bs">BS</option></select></label><label class="check-row"><input type="checkbox" id="hardware-echo">本地回显</label></div><p class="muted">关闭面板不会断开设备。清屏只清当前显示，后台保留最近 2000 条收发记录。</p></details>`;
    element('root').insertAdjacentHTML('beforeend', `<dialog id="hardware-library-dialog"><h2>设备库</h2><p class="muted">查看每台设备关联的任务、AI 权限和当前控制权。</p><div id="hardware-library-list"></div><div class="dialog-footer"><button id="hardware-library-close">关闭</button></div></dialog>`);
    element('device-form').querySelector('label[for="device-name"]').insertAdjacentHTML('beforebegin', '<label for="device-kind">设备用途</label><select id="device-kind"><option value="console">串口 / 网络控制台</option><option value="relay">串口继电器 · 板子电源</option></select>');
    element('device-readonly').parentElement.insertAdjacentHTML('beforebegin', '<div id="relay-fields" class="hidden"><label for="relay-contact">实际接线</label><select id="relay-contact"><option value="">请选择接线方式…</option><option value="no">COM + NO（常开）</option><option value="nc">COM + NC（常闭）</option></select><div class="form-grid"><label>继电器地址<input id="relay-channel" type="number" min="1" max="254" value="1"></label><label>断电间隔（秒）<input id="relay-delay" type="number" min="1" max="60" value="3"></label></div><p class="muted">CH340 / A0 协议，9600 · 8N1。COM 口变更后在此修改，设备名称和任务关联会保留。</p></div>');
    input('device-kind').onchange = ()=>{
        if (input('device-kind').value === 'relay') {
            input('device-baud').value = '9600';
            input('hardware-bits').value = '8';
            input('hardware-parity').value = 'none';
            input('hardware-stop').value = '1';
            if (!input('device-name').value) input('device-name').value = '板子电源';
        }
        deviceFields();
    };
    button('device-library').onclick = ()=>void openHardwareLibrary();
    button('hardware-library-close').onclick = ()=>element('hardware-library-dialog').close();
    element('device-library').insertAdjacentHTML('afterend', '<button id="device-ai">AI 使用</button>');
    element('hardware-panel').insertAdjacentHTML('afterbegin', '<section class="hardware-ai-overview"><div class="hardware-overview-head"><strong>本任务 AI 硬件</strong><span id="hardware-ai-count"></span><button id="hardware-overview-refresh" title="刷新设备连接和任务授权">刷新</button></div><div id="hardware-ai-list"></div><small id="hardware-ai-tip">无需提前连接。点设备设置 AI 权限。</small></section>');
    button('hardware-overview-refresh').onclick = ()=>void loadDevices();
    element('hardware-ai-list').onclick = (e)=>{
        const b = e.target.closest('[data-ai-device]');
        if (b) {
            selectDevice(b.dataset.aiDevice);
            void editHardwareAI();
        }
    };
    element('hardware-library-list').onclick = (e)=>{
        const b = e.target.closest('button');
        if (!b) return;
        if (b.dataset.attach) void attachHardwareFromLibrary(b.dataset.attach);
        else if (b.dataset.hardwareTask) {
            element('hardware-library-dialog').close();
            deviceID = b.dataset.device;
            void choose(b.dataset.hardwareTask, 'hardware');
        }
    };
    element('root').insertAdjacentHTML('beforeend', `<dialog id="hardware-ai-dialog"><form id="hardware-ai-form"><h2>允许本任务 AI 使用</h2><p id="hardware-ai-name"></p><label class="check-row"><input type="checkbox" id="hardware-ai-read">连接、读取日志和查询状态</label><label class="check-row" id="hardware-ai-write-row"><input type="checkbox" id="hardware-ai-write">发送串口 / 控制台命令</label><label class="check-row" id="hardware-ai-power-row"><input type="checkbox" id="hardware-ai-power">上电、断电和断电重启</label><p>适用于本任务的 Codex 和 Claude。新增权限从下一轮使用；收回权限会立即撤销本轮硬件凭据。操作记录留在任务和硬件日志中。</p><p class="error" id="hardware-ai-error"></p><div class="dialog-footer"><button type="button" id="hardware-ai-cancel">取消</button><button class="primary" id="hardware-ai-save">保存</button></div></form></dialog>`);
    button('device-ai').onclick = ()=>void editHardwareAI();
    button('hardware-ai-cancel').onclick = ()=>element('hardware-ai-dialog').close();
    input('hardware-ai-read').onchange = renderHardwareAIForm;
    element('hardware-ai-form').onsubmit = (e)=>{
        e.preventDefault();
        void saveHardwareAI();
    };
    button('device-claim').onclick = ()=>{
        const h = hardwareView;
        if (h?.controller && h.controller !== chosen && !confirm('接管“' + (h.controllerTitle || h.controller) + '”正在控制的设备？原任务会转为查看模式。')) return;
        cancelHardwareInput();
        void deviceAction('claim', {});
    };
    button('device-release').onclick = ()=>{
        cancelHardwareInput();
        void deviceAction('release', {});
    };
    element('relay-panel').querySelectorAll('[data-relay]').forEach((b)=>b.onclick = ()=>void relayOperation(b.dataset.relay));
    input('hardware-task-log').onchange = renderDeviceLog;
    element('device-portname').insertAdjacentHTML('beforebegin', '<select id="device-serial-choice" aria-label="检测到的串口"></select>');
    input('device-baud').setAttribute('list', 'hardware-bauds');
    input('device-baud').insertAdjacentHTML('afterend', '<datalist id="hardware-bauds">' + [
        9600,
        19200,
        38400,
        57600,
        115200,
        230400,
        460800,
        921600,
        1500000
    ].map((b)=>`<option value="${b}"></option>`).join('') + '</datalist><details class="hardware-advanced"><summary>串口参数 · 默认 8N1</summary><div class="form-grid"><label>数据位<select id="hardware-bits"><option>8</option><option>7</option><option>6</option><option>5</option></select></label><label>校验<select id="hardware-parity"><option value="none">无</option><option value="even">偶</option><option value="odd">奇</option><option value="mark">Mark</option><option value="space">Space</option></select></label><label>停止位<select id="hardware-stop"><option>1</option><option>1.5</option><option>2</option></select></label><div><label class="check-row"><input id="hardware-dtr" type="checkbox">DTR</label><label class="check-row"><input id="hardware-rts" type="checkbox">RTS</label></div></div></details>');
    element('device-baud').previousElementSibling.textContent = '波特率';
    element('device-form').querySelector('.dialog-footer').insertAdjacentHTML('beforeend', '<button type="button" class="primary" id="device-save-connect">保存并连接</button>');
    button('device-new').onclick = ()=>editDevice(null);
    button('device-edit').onclick = ()=>editDevice(devices.find((d)=>d.id === deviceID) || null);
    input('device-picker').onchange = ()=>selectDevice(input('device-picker').value);
    button('device-refresh-ports').onclick = ()=>void refreshHardwarePorts();
    input('device-protocol').onchange = ()=>{
        deviceFields();
        input('device-port').value = input('device-protocol').value === 'ssh' ? '22' : '23';
        if (input('device-protocol').value === 'serial') void refreshHardwarePorts();
    };
    input('device-serial-choice').onchange = ()=>{
        const name = input('device-serial-choice').value;
        input('device-portname').classList.toggle('hidden', name !== '__manual__');
        if (name !== '__manual__') {
            input('device-portname').value = name;
            if (!input('device-name').value || input('device-name').value.startsWith('串口 · ')) input('device-name').value = name ? '串口 · ' + name : '';
        }
    };
    button('device-cancel').onclick = ()=>element('device-dialog').close();
    element('device-form').onsubmit = (e)=>{
        e.preventDefault();
        void saveDevice(false);
    };
    button('device-save-connect').onclick = ()=>{
        if (element('device-form').reportValidity()) void saveDevice(true);
    };
    button('device-delete').textContent = '移出当前任务';
    button('device-delete').onclick = async ()=>{
        if (!deviceEditing || !confirm('从当前任务移除此设备？设备库及历史记录会保留。')) return;
        try {
            await api(`tasks/${hardwareEditingTask}/hardware/${deviceEditing.id}`, 'DELETE', {});
            element('device-dialog').close();
            await loadDevices();
        } catch (e) {
            element('device-error').textContent = e.message;
        }
    };
    button('device-connect').onclick = ()=>connectSelectedDevice();
    button('device-password-cancel').onclick = ()=>{
        input('device-password').value = '';
        element('device-password-dialog').close();
    };
    element('device-password-form').onsubmit = (e)=>{
        e.preventDefault();
        const password = input('device-password').value;
        input('device-password').value = '';
        element('device-password-dialog').close();
        void deviceAction('connect', {
            password
        });
    };
    button('device-disconnect').onclick = ()=>{
        cancelHardwareInput();
        void deviceAction('disconnect', {});
    };
    element('device-send').onsubmit = (e)=>{
        e.preventDefault();
        void deviceAction('send', {
            data: input('device-data').value,
            encoding: input('device-encoding').value,
            newline: input('device-newline').value
        });
    };
    button('device-interrupt').onclick = ()=>queueHardwareInput(new Uint8Array([
            3
        ]));
    input('device-data').oninput = ()=>hardwareDrafts.set(chosen + '/' + deviceID, input('device-data').value);
    input('device-view').onchange = ()=>{
        renderHardwareState();
        renderDeviceLog();
        fitHardwareTerminal();
    };
    button('device-clear').onclick = ()=>{
        deviceEvents = [];
        hardwareView?.term.reset();
        renderDeviceLog();
    };
    button('device-pause').onclick = ()=>{
        const h = hardwareView;
        if (!h) return;
        h.paused = !h.paused;
        if (h.paused) cancelHardwareInput();
        else renderDeviceLog();
        renderHardwareState();
    };
    button('device-bottom').onclick = ()=>{
        hardwareView?.term.scrollToBottom();
        const e = element('device-console');
        e.scrollTop = e.scrollHeight;
    };
    button('device-export').onclick = ()=>{
        const text = input('device-view').value === 'terminal' ? hardwarePlainText(hardwareLogEvents()) : deviceLog();
        const a = document.createElement('a');
        a.href = URL.createObjectURL(new Blob([
            text
        ], {
            type: 'text/plain;charset=utf-8'
        }));
        a.download = 'hardware-' + deviceID + '.log';
        a.click();
        setTimeout(()=>URL.revokeObjectURL(a.href), 1000);
    };
    button('device-analyze').onclick = ()=>{
        const selected = input('device-view').value === 'terminal' ? hardwareView?.term.getSelection() : window.getSelection()?.toString();
        const text = (selected || hardwarePlainText(hardwareLogEvents())).slice(-24000);
        if (!text) {
            notify('先接收日志，或选中要分析的文字');
            return;
        }
        bringToChat('请分析以下硬件调试输出：\n\n' + text);
    };
    element('hardware-panel').querySelectorAll('[data-hardware-key]').forEach((b)=>b.onclick = ()=>{
            const key = b.dataset.hardwareKey;
            const data = key === 'enter' ? hardwareEnter() : ({
                tab: '\t',
                up: '\x1b[A',
                down: '\x1b[B',
                'ctrl-c': '\x03',
                esc: '\x1b'
            })[key];
            queueHardwareInput(new TextEncoder().encode(data));
            hardwareView?.term.focus();
        });
    clearInterval(hardwarePollTimer);
    hardwarePollTimer = setInterval(()=>void pollDevice(), 100);
    clearInterval(hardwareOverviewTimer);
    hardwareOverviewTimer = setInterval(()=>{
        if (authenticated && chosen && !document.hidden && !hardwareOverviewLoading && !hardwareAISaving && (toolsTab === 'hardware' || element('hardware-library-dialog')?.open)) void loadDevices(true);
    }, 4000);
    disposeWithShell(()=>{
        clearInterval(hardwarePollTimer);
        clearInterval(hardwareOverviewTimer);
    });
}
function hardwareEnter() {
    return ({
        cr: '\r',
        lf: '\n',
        crlf: '\r\n'
    })[input('hardware-enter').value] || '\r';
}
function hardwareKeyEvent(e, selected, enter, backspace, send) {
    if (e.isComposing || e.keyCode === 229) return true;
    const interrupt = e.ctrlKey && !e.altKey && !e.metaKey && e.key.toLowerCase() === 'c';
    if (interrupt && selected) return false;
    const carriage = e.key === 'Enter' && !e.altKey && !e.ctrlKey, bs = e.key === 'Backspace' && !e.altKey && !e.ctrlKey && backspace === 'bs';
    if (!interrupt && !carriage && !bs) return true;
    e.preventDefault();
    if (e.type === 'keydown') {
        send(interrupt ? new Uint8Array([
            3
        ]) : carriage ? new TextEncoder().encode(enter) : new Uint8Array([
            8
        ]));
        if ((carriage || interrupt) && e.target instanceof HTMLTextAreaElement) e.target.value = '';
    }
    return false;
}
function deviceFields() {
    const relay = input('device-kind').value === 'relay';
    element('relay-fields').classList.toggle('hidden', !relay);
    input('relay-contact').required = relay;
    const p = input('device-protocol').value;
    element('device-serial-fields').classList.toggle('hidden', p !== 'serial');
    element('device-network-fields').classList.toggle('hidden', p === 'serial');
    element('device-ssh-fields').classList.toggle('hidden', p !== 'ssh');
}
async function refreshHardwarePorts() {
    const request = ++hardwarePortsRequest;
    button('device-refresh-ports').disabled = true;
    try {
        const ports = await api('hardware/serial-ports');
        if (request !== hardwarePortsRequest) return;
        const current = input('device-portname').value;
        input('device-serial-choice').innerHTML = '<option value="">选择串口…</option>' + ports.map((p)=>`<option value="${escapeHTML(p.name)}">${escapeHTML(p.name + ' · ' + (p.product || [
                p.vid,
                p.pid
            ].filter(Boolean).join(':') || '串口') + (p.busy ? '（Duo内已占用）' : ''))}</option>`).join('') + '<option value="__manual__">手动填写 / 未检测到的端口…</option>';
        input('device-serial-choice').value = ports.some((p)=>p.name === current) ? current : current || !ports.length ? '__manual__' : '';
        input('device-portname').classList.toggle('hidden', input('device-serial-choice').value !== '__manual__');
        element('device-ports').innerHTML = ports.map((p)=>`<option value="${escapeHTML(p.name)}">${escapeHTML(p.product)}</option>`).join('');
    } catch (e) {
        if (request === hardwarePortsRequest) {
            input('device-serial-choice').innerHTML = '<option value="__manual__">手动填写串口</option>';
            input('device-portname').classList.remove('hidden');
            element('device-error').textContent = e.message;
        }
    } finally{
        if (request === hardwarePortsRequest) button('device-refresh-ports').disabled = false;
    }
}
function editDevice(d) {
    hardwareEditingTask = chosen;
    deviceEditing = d;
    input('device-kind').value = d?.kind || 'console';
    input('relay-contact').value = d?.relay?.contact || '';
    input('relay-channel').value = String(d?.relay?.channel || 1);
    input('relay-delay').value = String(d?.relay?.cycle_seconds || 3);
    for (const [key, value] of Object.entries({
        name: d?.name || '',
        protocol: d?.protocol || 'serial',
        portname: d?.device || '',
        baud: d?.baud || 115200,
        host: d?.host || '',
        port: d?.port || 22,
        user: d?.user || 'root',
        fingerprint: d?.fingerprint || ''
    }))input('device-' + key).value = String(value);
    input('device-readonly').checked = d?.read_only || false;
    input('hardware-bits').value = String(d?.data_bits || 8);
    input('hardware-parity').value = d?.parity || 'none';
    input('hardware-stop').value = d?.stop_bits || '1';
    input('hardware-dtr').checked = d ? d.dtr ?? true : false;
    input('hardware-rts').checked = d ? d.rts ?? true : false;
    element('device-error').textContent = '';
    button('device-delete').classList.toggle('hidden', !d);
    deviceFields();
    element('device-dialog').showModal();
    if (!d || d.protocol === 'serial') void refreshHardwarePorts();
}
async function saveDevice(connect) {
    if (hardwareSaving) return;
    hardwareSaving = true;
    const task = hardwareEditingTask, c = {
        kind: input('device-kind').value,
        relay: input('device-kind').value === 'relay' ? {
            channel: Number(input('relay-channel').value),
            contact: input('relay-contact').value,
            cycle_seconds: Number(input('relay-delay').value)
        } : undefined,
        name: input('device-name').value || '串口 · ' + input('device-portname').value,
        protocol: input('device-protocol').value,
        device: input('device-portname').value.trim(),
        baud: Number(input('device-baud').value),
        host: input('device-host').value.trim(),
        port: Number(input('device-port').value),
        user: input('device-user').value,
        fingerprint: input('device-fingerprint').value.trim(),
        read_only: input('device-readonly').checked,
        data_bits: Number(input('hardware-bits').value),
        parity: input('hardware-parity').value,
        stop_bits: input('hardware-stop').value,
        dtr: input('hardware-dtr').checked,
        rts: input('hardware-rts').checked
    };
    const saveButtons = element('device-form').querySelectorAll('button');
    saveButtons.forEach((b)=>b.disabled = true);
    try {
        const d = await api(`tasks/${task}/hardware` + (deviceEditing ? '/' + deviceEditing.id : ''), deviceEditing ? 'PUT' : 'POST', c);
        element('device-dialog').close();
        if (chosen !== task) return;
        deviceID = d.id;
        await loadDevices();
        if (connect) connectSelectedDevice();
    } catch (e) {
        element('device-error').textContent = e.message;
    } finally{
        hardwareSaving = false;
        saveButtons.forEach((b)=>b.disabled = false);
    }
}
function connectSelectedDevice() {
    if (devices.find((d)=>d.id === deviceID)?.protocol === 'ssh') {
        input('device-password').value = '';
        element('device-password-dialog').showModal();
    } else void deviceAction('connect', {});
}
async function loadDevices(silent = false) {
    const task = chosen, request = ++hardwareLoadRequest;
    if (!task) return;
    hardwareOverviewLoading = true;
    if (hardwareOverviewTask !== task) {
        devices = [];
        hardwareAIGrants = {};
        closeHardwareView();
        input('device-picker').disabled = true;
        renderHardwareState();
    }
    button('hardware-overview-refresh').disabled = true;
    try {
        const list = await api('hardware/overview');
        if (chosen !== task || request !== hardwareLoadRequest) return;
        hardwareOverview = list;
        hardwareOverviewTask = task;
        devices = list.filter((d)=>d.tasks.some((t)=>t.id === task)).map((d)=>d.config);
        hardwareAIGrants = {};
        for (const item of list){
            const use = item.tasks.find((t)=>t.id === task);
            if (use) hardwareAIGrants[item.config.id] = use.ai;
        }
        const picker = input('device-picker'), html = devices.map((d)=>`<option value="${d.id}">${escapeHTML(d.name)}</option>`).join('') || '<option value="">添加串口或网络设备</option>';
        if (picker.dataset.snapshot !== html) {
            picker.innerHTML = html;
            picker.dataset.snapshot = html;
        }
        picker.disabled = false;
        if (!devices.some((d)=>d.id === deviceID)) deviceID = devices[0]?.id || '';
        selectDevice(deviceID);
        renderHardwareOverview();
        if (element('hardware-library-dialog').open) renderHardwareLibrary();
        element('hardware-ai-tip').textContent = '无需提前连接。点设备设置 AI 权限。';
    } catch (e) {
        if (chosen === task && request === hardwareLoadRequest) {
            element('hardware-ai-tip').textContent = '刷新失败，当前显示可能已过期。';
            if (!silent) notify(e.message);
        }
    } finally{
        if (request === hardwareLoadRequest) {
            hardwareOverviewLoading = false;
            if (element('hardware-overview-refresh')) button('hardware-overview-refresh').disabled = false;
        }
    }
}
function closeHardwareView() {
    const h = hardwareView;
    if (h) {
        h.alive = false;
        h.resize.disconnect();
        clearTimeout(h.flushTimer);
        h.pending = [];
        h.term.dispose();
        hardwareView = null;
    }
}
function selectDevice(id) {
    if (hardwareView?.task === chosen && hardwareView.id === id) {
        input('device-picker').value = id;
        renderHardwareState();
        fitHardwareTerminal();
        void pollDevice();
        return;
    }
    closeHardwareView();
    if (devices.find((d)=>d.id === id)?.kind === 'relay') input('device-view').value = 'hex';
    deviceID = id;
    deviceTask = chosen;
    deviceSeq = 0;
    deviceEvents = [];
    input('device-picker').value = id;
    input('device-data').value = hardwareDrafts.get(chosen + '/' + id) || '';
    element('hardware-terminal').replaceChildren();
    if (id) {
        const term = new Terminal({
            cols: 100,
            rows: 24,
            fontFamily: 'Consolas, "Microsoft YaHei", monospace',
            fontSize: 13,
            lineHeight: 1.2,
            scrollback: 5000,
            cursorBlink: true,
            screenReaderMode: true,
            disableStdin: true,
            convertEol: true,
            allowProposedApi: true,
            theme: {
                background: '#101318',
                foreground: '#e0e7f1',
                cursor: '#d8f383',
                selectionBackground: '#46533a'
            }
        });
        const h = {
            task: chosen,
            id,
            term,
            resize: new ResizeObserver(()=>fitHardwareTerminal()),
            alive: true,
            connected: false,
            generation: '',
            ready: false,
            controller: '',
            controllerTitle: '',
            requesting: false,
            busy: false,
            pending: [],
            pendingBytes: 0,
            flushing: false,
            written: 0,
            screenBytes: 0,
            paused: false
        };
        hardwareView = h;
        for (const prefix of [
            '',
            '?',
            '>',
            '='
        ])for (const final of [
            'n',
            'c',
            't'
        ])term.parser.registerCsiHandler({
            prefix,
            final
        }, ()=>true);
        for (const id of [
            10,
            11,
            12,
            52
        ])term.parser.registerOscHandler(id, ()=>true);
        for (const intermediates of [
            '$',
            '+'
        ])term.parser.registerDcsHandler({
            intermediates,
            final: 'q'
        }, ()=>true);
        term.open(element('hardware-terminal'));
        h.resize.observe(element('hardware-terminal'));
        term.onData((data)=>{
            if (hardwareView === h) queueHardwareInput(new TextEncoder().encode(data));
        });
        term.onBinary((data)=>{
            if (hardwareView === h) queueHardwareInput(Uint8Array.from(data, (c)=>c.charCodeAt(0)));
        });
        term.attachCustomKeyEventHandler((e)=>hardwareKeyEvent(e, term.getSelection(), hardwareEnter(), input('hardware-backspace').value, queueHardwareInput));
    }
    renderDeviceLog();
    renderHardwareState();
    void pollDevice();
}
function fitHardwareTerminal() {
    const h = hardwareView, host = element('hardware-terminal');
    if (!h || toolsTab !== 'hardware' || input('device-view').value !== 'terminal' || !host.clientHeight) return;
    const screen = h.term.element?.querySelector('.xterm-screen'), rect = screen?.getBoundingClientRect();
    if (!rect?.width || !rect.height) return;
    const viewport = h.term.element?.querySelector('.xterm-viewport');
    h.term.resize(Math.max(2, Math.min(400, Math.floor((viewport?.clientWidth || host.clientWidth - 22) / (rect.width / h.term.cols)))), Math.max(2, Math.min(150, Math.floor((host.clientHeight - 14) / (rect.height / h.term.rows)))));
}
function hardwareCanWrite(h) {
    return !!h && toolsTab === 'hardware' && h.alive && h.task === chosen && h.id === deviceID && h.connected && h.controller === chosen && devices.find((d)=>d.id === h.id)?.kind !== 'relay' && h.ready && !h.busy && !h.paused && !!h.generation && !devices.find((d)=>d.id === h.id)?.read_only;
}
function renderHardwareState() {
    if (!element('hardware-panel')) return;
    const h = hardwareView, d = devices.find((d)=>d.id === deviceID), relayDevice = d?.kind === 'relay', terminal = input('device-view').value === 'terminal' && !relayDevice;
    element('hardware-task-filter').classList.toggle('hidden', terminal);
    element('device-view').querySelector('option[value=terminal]').disabled = !!relayDevice;
    button('device-ai').disabled = !d || !!detail?.task.archived;
    button('device-ai').textContent = hardwareAIGrants[deviceID]?.read ? 'AI 权限' : 'AI 使用';
    renderHardwareOverview();
    element('relay-panel').classList.toggle('hidden', !relayDevice);
    element('hardware-send-details').classList.toggle('hidden', !!relayDevice);
    element('hardware-panel').querySelector('.hardware-keys').classList.toggle('hidden', !!relayDevice);
    element('hardware-terminal').classList.toggle('hidden', !terminal);
    element('device-console').classList.toggle('hidden', terminal);
    element('hardware-panel').classList.toggle('hardware-log-view', !terminal);
    element('device-description').textContent = d ? d.protocol === 'serial' ? `${d.device} · ${d.baud} · ${d.data_bits || 8}${({
        none: 'N',
        even: 'E',
        odd: 'O',
        mark: 'M',
        space: 'S'
    })[d.parity || 'none']}${d.stop_bits || '1'}` : `${d.protocol.toUpperCase()} · ${d.host}:${d.port}` : '';
    element('device-status').textContent = h?.busy ? '处理中…' : h?.connected ? '已连接' + (d?.read_only ? ' · 只读' : '') : h && !h.ready ? '读取状态…' : '未连接';
    button('device-connect').classList.toggle('hidden', !!h?.connected);
    button('device-disconnect').classList.toggle('hidden', !h?.connected);
    button('device-connect').disabled = !d || !!h?.busy || !h?.ready;
    button('device-disconnect').disabled = !!h?.busy || h?.controller !== chosen || !!h?.relay?.busy;
    button('device-edit').disabled = !d || !!h?.connected || !!h?.busy;
    for (const id of [
        'device-send-button',
        'device-interrupt'
    ])button(id).disabled = !hardwareCanWrite(h);
    element('hardware-panel').querySelectorAll('[data-hardware-key]').forEach((b)=>b.disabled = !hardwareCanWrite(h));
    for (const id of [
        'device-clear',
        'device-export',
        'device-analyze',
        'device-pause',
        'device-bottom'
    ])button(id).disabled = !h;
    button('device-pause').textContent = h?.paused ? '继续显示' : '暂停显示';
    if (h) h.term.options.disableStdin = !hardwareCanWrite(h) || toolsTab !== 'hardware';
    element('device-owner').textContent = h?.connected ? h.controller === chosen ? '本任务控制' : h.controller ? '由「' + (h.controllerTitle || h.controller) + '」控制 · 当前只查看' : '设备已连接 · 控制权空闲' : '';
    button('device-claim').classList.toggle('hidden', !h?.connected || h.controller === chosen);
    button('device-release').classList.toggle('hidden', !h?.connected || h.controller !== chosen);
    button('device-claim').disabled = !!h?.busy || !!h?.relay?.busy;
    button('device-release').disabled = !!h?.busy || !!h?.relay?.busy;
    const power = h?.relay;
    element('relay-status').textContent = !h?.connected ? '未连接' : power?.busy ? '电源操作中…' : power?.known ? '继电器反馈：供电' + (power.power ? '接通' : '断开') + ' · ' + new Date(power.updated).toLocaleTimeString() : '供电状态未知 · 点击查询';
    element('relay-panel').querySelectorAll('[data-relay]').forEach((b)=>b.disabled = !h?.connected || !h.ready || h.controller !== chosen || h.busy || !!power?.busy || !!d?.read_only);
    element('hardware-hint').classList.toggle('hidden', !!relayDevice);
    element('hardware-hint').textContent = h?.paused ? '已暂停显示与输入，后台仍在接收。' : h?.connected ? '点击终端直接输入；Tab 补全，↑↓ 历史，Ctrl+C 中断。' : '连接后点击终端，直接输入命令。';
}
function cancelHardwareInput() {
    const h = hardwareView;
    if (h) {
        clearTimeout(h.flushTimer);
        h.pending = [];
        h.pendingBytes = 0;
    }
}
function queueHardwareInput(bytes) {
    const h = hardwareView;
    if (!hardwareCanWrite(h) || !h || !bytes.length) return;
    if (h.pendingBytes + bytes.length > 32768) {
        notify('输入过长，请分段粘贴');
        return;
    }
    h.pending.push(bytes);
    h.pendingBytes += bytes.length;
    if (!h.flushing) {
        clearTimeout(h.flushTimer);
        h.flushTimer = setTimeout(()=>void flushHardwareInput(h), 12);
    }
}
async function flushHardwareInput(h) {
    if (h.flushing) return;
    h.flushing = true;
    const generation = h.generation;
    try {
        while(h.pendingBytes){
            if (hardwareView !== h || !hardwareCanWrite(h) || h.generation !== generation) {
                h.pending = [];
                h.pendingBytes = 0;
                break;
            }
            const bytes = new Uint8Array(h.pendingBytes);
            let offset = 0;
            for (const p of h.pending){
                bytes.set(p, offset);
                offset += p.length;
            }
            h.pending = [];
            h.pendingBytes = 0;
            await api(`tasks/${h.task}/hardware/${h.id}/send`, 'POST', {
                encoding: 'hex',
                data: bytesHex(bytes),
                connection_id: generation
            });
        }
    } catch (e) {
        h.pending = [];
        h.pendingBytes = 0;
        if (hardwareView === h) {
            h.connected = false;
            h.ready = false;
            renderHardwareState();
            notify('发送未完成，未自动重发：' + e.message);
        }
    } finally{
        h.flushing = false;
    }
}
function deviceLog() {
    const hexView = input('device-view').value === 'hex', decoders = new Map();
    return hardwareLogEvents().map((e)=>{
        let text = e.text;
        if (e.direction !== 'status') {
            const decoder = decoders.get(e.direction) || new TextDecoder();
            decoders.set(e.direction, decoder);
            text = hexView ? (e.hex.match(/.{2}/g) || []).join(' ').toUpperCase() : decoder.decode(hexBytes(e.hex), {
                stream: true
            }).replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '');
        }
        return `${new Date(e.created).toLocaleTimeString()} ${e.direction.toUpperCase()}  ${text}`;
    }).join('\n');
}
function renderDeviceLog() {
    const h = hardwareView;
    if (h && !h.paused) {
        for (const e of deviceEvents){
            if (e.seq <= h.written) continue;
            if (e.direction === 'rx' || e.direction === 'tx' && input('hardware-echo').checked) {
                const bytes = hexBytes(e.hex);
                if (h.screenBytes + bytes.length > 2 * 1024 * 1024) {
                    h.paused = true;
                    renderHardwareState();
                    notify('输出过快，已暂停显示；后台仍在接收。');
                    break;
                }
                h.screenBytes += bytes.length;
                h.term.write(bytes, ()=>{
                    h.screenBytes -= bytes.length;
                });
            }
            h.written = e.seq;
        }
    }
    if (!h?.paused && (input('device-view').value !== 'terminal' || devices.find((d)=>d.id === deviceID)?.kind === 'relay')) {
        const el = element('device-console'), bottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
        el.textContent = deviceLog();
        if (bottom) el.scrollTop = el.scrollHeight;
    }
}
async function pollDevice() {
    const h = hardwareView;
    if (!authenticated || toolsTab !== 'hardware' || !h || h.task !== chosen || h.requesting || document.hidden) return;
    h.requesting = true;
    try {
        const r = await api(`tasks/${h.task}/hardware/${h.id}/events?after=${deviceSeq}` + (h.ready ? '&wait=1' : '&tail=1'));
        if (hardwareView !== h || chosen !== h.task) return;
        if (h.generation && h.generation !== r.connection_id) cancelHardwareInput();
        h.generation = r.connection_id;
        h.connected = r.connected;
        h.controller = r.controller_task;
        h.controllerTitle = r.controller_title;
        h.relay = r.relay;
        h.ready = true;
        deviceEvents.push(...r.events);
        deviceEvents = deviceEvents.slice(-2000);
        if (r.events.length) deviceSeq = r.events.at(-1).seq;
        renderDeviceLog();
        renderHardwareState();
    } catch (e) {
        if (hardwareView === h) {
            h.connected = false;
            h.ready = false;
            cancelHardwareInput();
            renderHardwareState();
            element('device-status').textContent = e.message;
        }
    } finally{
        h.requesting = false;
    }
}
async function deviceAction(action, data) {
    const h = hardwareView;
    if (!h || h.busy) return;
    if (action === 'send' && !hardwareCanWrite(h)) return;
    h.busy = true;
    renderHardwareState();
    try {
        await api(`tasks/${h.task}/hardware/${h.id}/${action}`, 'POST', {
            ...data,
            connection_id: h.generation
        });
        if (hardwareView === h) {
            if (action === 'disconnect') {
                h.connected = false;
                h.generation = '';
            }
            if (action === 'connect') h.term.focus();
        }
    } catch (e) {
        notify(e.message);
    } finally{
        h.busy = false;
        if (hardwareView === h) {
            renderHardwareState();
            void pollDevice();
        }
    }
}
function hardwareLogEvents() {
    return input('hardware-task-log').checked && (input('device-view').value !== 'terminal' || devices.find((d)=>d.id === deviceID)?.kind === 'relay') ? deviceEvents.filter((e)=>e.task_id === chosen) : deviceEvents;
}
async function editHardwareAI() {
    const d = devices.find((d)=>d.id === deviceID);
    if (!d || detail?.task.archived) return;
    const task = chosen;
    try {
        const grants = await api(`tasks/${task}/hardware-ai`);
        if (task !== chosen || d.id !== deviceID) return;
        hardwareAIGrants = grants;
        hardwareAIEditing = {
            task,
            device: d
        };
        const g = grants[d.id];
        element('hardware-ai-name').textContent = '任务：' + (detail?.task.title || task) + '\n设备：' + d.name + ' · ' + (d.device || d.host);
        for (const key of [
            'read',
            'write',
            'power'
        ])input('hardware-ai-' + key).checked = !!g?.[key];
        element('hardware-ai-write-row').classList.toggle('hidden', d.kind === 'relay');
        element('hardware-ai-power-row').classList.toggle('hidden', d.kind !== 'relay');
        element('hardware-ai-error').textContent = '';
        renderHardwareAIForm();
        element('hardware-ai-dialog').showModal();
    } catch (e) {
        notify(e.message);
    }
}
function renderHardwareAIForm() {
    const d = hardwareAIEditing?.device, read = input('hardware-ai-read').checked;
    for (const key of [
        'write',
        'power'
    ]){
        const el = input('hardware-ai-' + key);
        el.disabled = !read || !!d?.read_only;
        if (el.disabled) el.checked = false;
    }
    if (d?.kind === 'relay') input('hardware-ai-write').checked = false;
    else input('hardware-ai-power').checked = false;
}
async function saveHardwareAI() {
    const editing = hardwareAIEditing;
    if (!editing || hardwareAISaving) return;
    hardwareAISaving = true;
    ++hardwareLoadRequest;
    hardwareOverviewLoading = false;
    button('hardware-ai-save').disabled = true;
    try {
        await api(`tasks/${editing.task}/hardware/${editing.device.id}/ai`, 'PUT', {
            read: input('hardware-ai-read').checked,
            write: input('hardware-ai-write').checked,
            power: input('hardware-ai-power').checked
        });
        element('hardware-ai-dialog').close();
        if (chosen === editing.task) await loadDevices();
        notify('AI 硬件权限已保存');
    } catch (e) {
        element('hardware-ai-error').textContent = e.message;
    } finally{
        hardwareAISaving = false;
        button('hardware-ai-save').disabled = false;
    }
}
function hardwareGrantText(g, d, archived = false) {
    if (archived) return '已归档 · AI 停用';
    if (!g?.read) return 'AI 未授权';
    return [
        d.kind === 'relay' ? '读取 / 查询' : '读取',
        g.write && !d.read_only && d.kind !== 'relay' ? '发送命令' : '',
        g.power && !d.read_only && d.kind === 'relay' ? '电源控制' : ''
    ].filter(Boolean).join(' · ');
}
function hardwareConnectionText(item, task) {
    const h = hardwareView?.task === chosen && hardwareView?.id === item.config.id && hardwareView.ready ? hardwareView : null;
    const connected = h ? h.connected : item.connected, owner = h ? h.controller : item.controller_task, title = h ? h.controllerTitle : item.controller_title;
    if (!connected) {
        const use = item.tasks.find((t)=>t.id === task);
        return '未连接' + (use?.ai.read && !use.archived ? ' · AI 可按需连接' : '');
    }
    return '已连接 · ' + (owner === task ? '本任务控制' : owner ? '由「' + (title || owner) + '」控制' : '控制权空闲');
}
function renderHardwareOverview() {
    const host = element('hardware-ai-list');
    if (!host) return;
    if (hardwareOverviewTask !== chosen) {
        element('hardware-ai-count').textContent = '';
        host.textContent = '正在读取设备和权限…';
        delete host.dataset.snapshot;
        return;
    }
    const list = hardwareOverview.filter((d)=>d.tasks.some((t)=>t.id === chosen)), enabled = list.filter((d)=>d.tasks.some((t)=>t.id === chosen && !t.archived && t.ai.read)).length;
    element('hardware-ai-count').textContent = `已授权 ${enabled} / ${list.length}`;
    const html = list.map((item)=>{
        const d = item.config, use = item.tasks.find((t)=>t.id === chosen);
        return `<button class="hardware-ai-device ${d.id === deviceID ? 'selected' : ''}" data-ai-device="${d.id}" ${use.archived ? 'disabled' : ''}><span><strong>${escapeHTML(d.name)}</strong><small>${escapeHTML((d.device || d.host) + ' · ' + hardwareConnectionText(item, chosen))}</small>${use.active_ai ? `<small class="hardware-active-grant">本轮：${escapeHTML(hardwareGrantText(use.active_ai, d))}</small>` : ''}</span><span class="hardware-grant ${use.ai.read && !use.archived ? 'enabled' : ''}">${escapeHTML(hardwareGrantText(use.ai, d, use.archived))}</span></button>`;
    }).join('') || '<p class="muted">当前任务尚未添加设备，可从设备库选择。</p>';
    if (host.dataset.snapshot !== html) {
        host.innerHTML = html;
        host.dataset.snapshot = html;
    }
}
function renderHardwareLibrary() {
    if (hardwareLibraryAttaching) return;
    const host = element('hardware-library-list');
    const html = hardwareOverview.map((item)=>{
        const d = item.config, attached = item.tasks.some((t)=>t.id === chosen);
        return `<article class="library-device"><div class="library-device-head"><div><strong>${escapeHTML(d.name)}</strong><small>${escapeHTML(d.kind === 'relay' ? '板子电源' : d.protocol.toUpperCase())} · ${escapeHTML(d.device || d.host)}</small><small>${escapeHTML(hardwareConnectionText(item, chosen))}</small></div><button data-attach="${d.id}" ${attached || detail?.task.archived ? 'disabled' : ''}>${attached ? '本任务已添加' : '添加到本任务'}</button></div><div class="library-task-list">${item.tasks.map((t)=>`<div class="library-task"><button data-hardware-task="${t.id}" data-device="${d.id}" title="打开此任务的硬件面板">${escapeHTML(t.title)}${t.id === chosen ? '（本任务）' : ''}</button><span class="hardware-grant ${t.ai.read && !t.archived ? 'enabled' : ''}">${escapeHTML(hardwareGrantText(t.ai, d, t.archived))}</span>${t.active_ai ? `<small>本轮：${escapeHTML(hardwareGrantText(t.active_ai, d))}</small>` : ''}</div>`).join('') || '<small>尚未关联任务</small>'}</div></article>`;
    }).join('') || '<p>还没有设备。在硬件面板点击 ＋ 添加。</p>';
    if (host.dataset.snapshot !== html) {
        host.innerHTML = html;
        host.dataset.snapshot = html;
    }
}
async function openHardwareLibrary() {
    const task = chosen;
    await loadDevices();
    if (task !== chosen || hardwareOverviewTask !== task) return;
    renderHardwareLibrary();
    element('hardware-library-dialog').showModal();
}
async function attachHardwareFromLibrary(id) {
    if (hardwareLibraryAttaching) return;
    const task = chosen;
    hardwareLibraryAttaching = true;
    element('hardware-library-list').querySelectorAll('[data-attach]').forEach((b)=>b.disabled = true);
    try {
        await api(`tasks/${task}/hardware/${id}/attach`, 'POST', {});
        element('hardware-library-dialog').close();
        if (chosen === task) {
            deviceID = id;
            await loadDevices();
        }
    } catch (e) {
        notify(e.message);
    } finally{
        hardwareLibraryAttaching = false;
        delete element('hardware-library-list').dataset.snapshot;
        renderHardwareLibrary();
    }
}
async function relayOperation(action) {
    const h = hardwareView, d = devices.find((d)=>d.id === deviceID);
    if (!h || !d || h.busy || h.controller !== chosen) return;
    const label = {
        on: '上电',
        off: '断电',
        cycle: '断电 ' + d.relay?.cycle_seconds + ' 秒后重新上电',
        query: '查询'
    }[action];
    if (action !== 'query' && !confirm('对「' + d.name + '」执行' + label + '？' + (action === 'off' || action === 'cycle' ? '这会中断板子当前运行。' : ''))) return;
    h.busy = true;
    renderHardwareState();
    try {
        await api(`tasks/${h.task}/hardware/${h.id}/relay`, 'POST', {
            action,
            connection_id: h.generation,
            confirmed: action !== 'query'
        });
        notify('已收到继电器反馈');
    } catch (e) {
        notify(e.message);
    } finally{
        h.busy = false;
        if (hardwareView === h) {
            renderHardwareState();
            void pollDevice();
        }
    }
}
let createModels = [];
let createResolvedDefault = '';
let taskPickerModels = [];
let taskModelRequest = 0, modelTestRequest = 0, modelProfileRevision = 0;
let modelTestBusy = false;
const modelCatalogState = {
    create: {
        loading: false,
        key: '',
        summary: '',
        details: '',
        failed: false
    },
    task: {
        loading: false,
        key: '',
        summary: '',
        details: '',
        failed: false
    }
};
const modelCatalogControllers = {};
const effortLabels = {
    off: '关闭',
    none: '关闭',
    minimal: '极低',
    low: '低',
    medium: '中',
    high: '高',
    xhigh: '很高',
    max: '最高',
    ultra: 'Ultra（工具可能自动委派）'
};
const defaultModelLabel = '使用此工具的默认模型';
const modelPickers = {
    create: {
        root: 'model-picker',
        button: 'model-picker-button',
        label: 'model-picker-label',
        menu: 'model-menu',
        search: 'model-search',
        list: 'model-list'
    },
    task: {
        root: 'task-model',
        button: 'task-model-button',
        label: 'task-model-label',
        menu: 'task-model-menu',
        search: 'task-model-search',
        list: 'task-model-list'
    }
};
function taskEngineName(engine) {
    return engine === 'claude' ? 'Claude Code' : engine === 'deepseek-harness' ? 'DeepSeek Harness' : 'Codex';
}
function engineDefaultModel(env, engine) {
    return engine === 'claude' ? env.claude_model || '' : engine === 'deepseek-harness' ? env.harness_model || 'deepseek-flash' : env.model;
}
const harnessSessionHint = 'Harness 仅在同一个运行进程中连续对话；闲置 30 分钟、停止任务或重启服务后不能恢复原生上下文。可新建空白会话，旧记录仍保留但不会自动带入 AI 上下文。更换模型、provider 或权限请新建任务。';
const harnessKnowledgeHint = 'Harness 暂不支持自动整理任务知识，请使用 Codex 任务整理；仍可手动新增、编辑和导出笔记。';
function effortLevels(engine, model) {
    if (engine === 'deepseek-harness') return [
        'off',
        'low',
        'high',
        'max'
    ];
    if (model && Array.isArray(model.reasoning_levels)) return model.reasoning_levels;
    return engine === 'claude' ? [
        'low',
        'medium',
        'high',
        'xhigh',
        'max'
    ] : [
        'low',
        'medium',
        'high',
        'xhigh'
    ];
}
function installExecution() {
    disposeWithShell(()=>{
        modelCatalogControllers.create?.abort();
        modelCatalogControllers.task?.abort();
    });
    input('create-engine').onchange = ()=>void loadCreateEnvironment(true);
    button('test-models').textContent = '测试当前模型';
    button('test-models').onclick = ()=>void testCreateModels();
    input('create-workspace').addEventListener('input', ()=>{
        invalidateModelTest();
        modelCatalogControllers.create?.abort();
        modelRequest++;
        modelCatalogState.create.key = '';
        createModels = [];
        createResolvedDefault = '';
        if (!element('model-menu').classList.contains('hidden')) {
            modelCatalogState.create.loading = false;
            modelCatalogState.create.summary = '目录已改变，离开目录输入框后自动读取。';
            renderModelMenu('create');
        }
    });
    input('create-workspace').addEventListener('change', ()=>void loadCreateModels());
    installModelPicker();
    element('setting-model').previousElementSibling.textContent = 'Codex 默认模型（可留空）';
    element('setting-model').insertAdjacentHTML('afterend', `<label for="setting-claude">此环境中的 Claude Code 可执行文件</label><input id="setting-claude" placeholder="claude"><label for="setting-claude-model">Claude 默认模型（可留空）</label><input id="setting-claude-model" placeholder="例如 sonnet，或你的服务提供的模型 ID"><label for="setting-harness">此环境中的 DeepSeek Harness 可执行文件</label><input id="setting-harness" placeholder="Windows: dsh.cmd；WSL / SSH: dsh"><label for="setting-harness-model">Harness 默认模型 ID</label><input id="setting-harness-model" placeholder="deepseek-flash"><label for="setting-harness-provider">Harness provider ID</label><input id="setting-harness-provider" placeholder="deepseek-official"><p class="muted">通过 Harness SDK JSON-RPC 执行；模型 ID 和 provider 必须存在于目标环境的 Harness 配置中，登录和密钥在该环境配置。${harnessSessionHint}</p><label for="setting-engine">默认 AI 工具（飞书新建也使用它）</label><select id="setting-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option><option value="deepseek-harness">DeepSeek Harness</option></select><p class="muted">Claude 自动接受工作目录内的文件编辑；其他操作沿用该环境中的 Claude 权限设置。</p>`);
    button('check-codex').textContent = '检查 Codex';
    button('check-codex').insertAdjacentHTML('afterend', ' <button type="button" id="check-claude">检查 Claude</button>');
    button('check-claude').onclick = async ()=>{
        button('check-claude').disabled = true;
        try {
            const r = await api('check', 'POST', {
                environment_id: editingID,
                engine: 'claude'
            });
            element('check-result').textContent = (r.ok ? 'Claude 已配置\n' : 'Claude 检查失败\n') + r.output;
        } catch (e) {
            element('check-result').textContent = e.message;
        } finally{
            button('check-claude').disabled = false;
        }
    };
    button('check-claude').insertAdjacentHTML('afterend', ' <button type="button" id="check-harness">检查 Harness</button>');
    button('check-harness').onclick = async ()=>{
        button('check-harness').disabled = true;
        try {
            const r = await api('check', 'POST', {
                environment_id: editingID,
                engine: 'deepseek-harness'
            });
            element('check-result').textContent = (r.ok ? 'Harness 基础检查通过（不代表模型调用成功）\n' : 'Harness 检查失败\n') + r.output + '\n检查使用已保存的环境配置；可在新建任务中测试模型调用。';
        } catch (e) {
            element('check-result').textContent = e.message;
        } finally{
            button('check-harness').disabled = false;
        }
    };
}
function resolveModelProbeTarget(env, engine, selected, custom, workspace) {
    if (!env) throw new Error('请先选择执行环境。');
    const model = (selected === '__custom__' ? custom : selected || engineDefaultModel(env, engine) || '').trim();
    if (!model) throw new Error('请先选择或输入一个明确的模型 ID；不会批量测试模型列表。');
    if (model.length > 120 || /[\0\r\n]/.test(model)) throw new Error('模型名称无效。');
    const provider = engine === 'deepseek-harness' ? env.harness_provider || 'deepseek-official' : '该 CLI 的原生配置';
    const target = {
        environmentID: env.id,
        environmentName: env.name,
        engine,
        provider,
        model,
        workspace: workspace.trim()
    };
    return {
        ...target,
        key: JSON.stringify([
            target.environmentID,
            engine,
            provider,
            model,
            target.workspace
        ])
    };
}
function currentModelProbeTarget() {
    const value = resolveModelProbeTarget(settings.config.environments.find((env)=>env.id === input('create-environment').value), input('create-engine').value, input('create-model').value || createResolvedDefault, input('custom-model').value, input('create-workspace').value);
    return {
        ...value,
        key: value.key + ':' + modelProfileRevision
    };
}
function syncModelTestButton() {
    const control = button('test-models'), picker = button('model-picker-button');
    if (control) control.disabled = createSubmitting || modelTestBusy || !picker || picker.disabled;
}
function invalidateModelTest() {
    modelTestRequest++;
    element('model-test-result').textContent = '';
    syncModelTestButton();
}
function modelProbeStillCurrent(request, key) {
    if (!creatingTask || request !== modelTestRequest) return false;
    try {
        return currentModelProbeTarget().key === key;
    } catch  {
        return false;
    }
}
async function testCreateModels() {
    if (createSubmitting || modelTestBusy || !creatingTask || button('model-picker-button').disabled) return;
    let target;
    try {
        target = currentModelProbeTarget();
    } catch (e) {
        element('model-test-result').textContent = e.message;
        return;
    }
    if (!confirm(`将使用以下配置发送一条最小测试消息，可能消耗少量模型额度：\n环境：${target.environmentName}\nAI 工具：${taskEngineName(target.engine)}\nProvider：${target.provider}\n模型：${target.model}\n目录：${target.workspace}\n\n仅测试当前模型，不遍历列表。关闭页面不会取消已提交的测试。是否继续？`)) return;
    const request = ++modelTestRequest;
    modelTestBusy = true;
    syncModelTestButton();
    element('model-test-result').textContent = '正在测试当前模型 ' + target.model + '，请稍候…';
    try {
        const result = await api('environments/' + encodeURIComponent(target.environmentID) + '/models/test', 'POST', {
            engine: target.engine,
            workspace: target.workspace,
            models: [
                target.model
            ]
        });
        if (!modelProbeStillCurrent(request, target.key)) return;
        renderModelTestResult(result);
    } catch (e) {
        if (modelProbeStillCurrent(request, target.key)) element('model-test-result').textContent = e.message;
    } finally{
        modelTestBusy = false;
        syncModelTestButton();
    }
}
function renderModelTestResult(result) {
    const available = result.results.filter((item)=>item.status === 'available').length;
    const lines = [
        `可用 ${available}/${result.results.length}`
    ];
    for (const item of result.results){
        const label = item.model || '默认模型', duration = item.duration_ms ? ` · ${(item.duration_ms / 1000).toFixed(1)} 秒` : '';
        lines.push(`${item.status === 'available' ? '✓' : item.status === 'timeout' ? '…' : '×'} ${label}：${item.message}${duration}`);
    }
    element('model-test-result').textContent = lines.join('\n');
}
function catalogContextKey(environment, engine, workspace) {
    return JSON.stringify([
        shellEpoch,
        modelProfileRevision,
        environment,
        engine,
        workspace.trim()
    ]);
}
function currentCreateCatalogContext() {
    const environment = settings.config.environments.find((env)=>env.id === input('create-environment').value);
    if (!environment) return null;
    const engine = input('create-engine').value, workspace = input('create-workspace').value.trim();
    return {
        environment,
        engine,
        workspace,
        key: catalogContextKey(environment, engine, workspace)
    };
}
function modelsURL(environmentID, engine, workspace, refresh = false) {
    const query = new URLSearchParams({
        engine,
        workspace
    });
    if (refresh) query.set('refresh', '1');
    return 'environments/' + encodeURIComponent(environmentID) + '/models?' + query;
}
function catalogSummary(result) {
    return result.models?.length ? (result.status === 'fallback' ? '已读取配置/缓存中的 ' : '已读取 ') + result.models.length + ' 个模型' : '该环境的此引擎尚未发现模型';
}
function catalogDetails(result) {
    return [
        result.message,
        result.source ? '来源：' + result.source : '',
        result.modified ? '更新：' + new Date(result.modified).toLocaleString() : '',
        '列表表示已发现或已配置，不代表调用已验证。'
    ].filter(Boolean).join('\n');
}
function renderModelCatalogStatus(target) {
    const state = modelCatalogState[target], prefix = target === 'create' ? '' : 'task-';
    const status = element(prefix + 'models-status'), details = element(prefix + 'models-hint'), context = element(prefix + 'models-context');
    if (status) {
        status.textContent = state.loading ? '正在读取目标环境的模型…' : state.summary;
        status.classList.toggle('error', state.failed);
        status.setAttribute('aria-busy', String(state.loading));
    }
    if (details) details.textContent = state.details;
    if (context) {
        const env = target === 'create' ? settings.config.environments.find((item)=>item.id === input('create-environment').value) : detail?.task.environment;
        context.textContent = (env?.name || '未选择环境') + ' · ' + taskEngineName(target === 'create' ? input('create-engine').value : detail?.task.engine);
    }
    const reload = button(prefix + 'reload-models');
    if (reload) {
        reload.disabled = state.loading || createSubmitting;
        reload.textContent = state.loading ? '读取中…' : '刷新列表';
    }
    element(modelPickers[target].list)?.setAttribute('aria-busy', String(state.loading));
}
async function loadCreateModels(reset = false, refresh = false) {
    if (!creatingTask || createSubmitting) return;
    const target = currentCreateCatalogContext();
    if (!target) return;
    const state = modelCatalogState.create;
    if (!reset && !refresh && state.loading && state.key === target.key) return;
    if (state.key !== target.key) {
        createModels = [];
        createResolvedDefault = '';
    }
    const request = ++modelRequest, defaultModel = engineDefaultModel(target.environment, target.engine);
    modelCatalogControllers.create?.abort();
    const controller = new AbortController();
    modelCatalogControllers.create = controller;
    state.key = target.key;
    state.loading = true;
    state.failed = false;
    state.summary = '';
    state.details = '';
    if (reset) {
        setCreateModelsLoading();
        setCreateModels(defaultModel ? [
            {
                id: defaultModel,
                name: defaultModel + '（环境默认）',
                origin: 'configured'
            }
        ] : [], defaultModel);
    }
    renderModelMenu('create');
    try {
        const result = await api(modelsURL(target.environment.id, target.engine, target.workspace, refresh), 'GET', undefined, controller.signal);
        if (request !== modelRequest || !creatingTask || createSubmitting || currentCreateCatalogContext()?.key !== target.key) return;
        const models = result.models || [], fallback = defaultModel || result.default_model || '';
        if (fallback && !models.some((model)=>model.id === fallback)) models.unshift({
            id: fallback,
            name: fallback + '（环境默认）',
            origin: 'configured'
        });
        createModels = models;
        createResolvedDefault = fallback;
        state.summary = catalogSummary(result);
        state.details = catalogDetails(result);
    } catch (e) {
        if (request !== modelRequest || !creatingTask || createSubmitting || currentCreateCatalogContext()?.key !== target.key) return;
        state.failed = true;
        state.summary = '读取失败；可重试、沿用默认或输入模型 ID';
        state.details = e.message;
    } finally{
        if (request === modelRequest && creatingTask && !createSubmitting && currentCreateCatalogContext()?.key === target.key) {
            state.loading = false;
            updateCreateModelLabel();
            updateReasoning();
            renderModelMenu('create');
            syncModelTestButton();
        }
    }
}
function invalidateModelCatalogs() {
    modelCatalogControllers.create?.abort();
    modelCatalogControllers.task?.abort();
    modelProfileRevision++;
    modelRequest++;
    taskModelRequest++;
    invalidateModelTest();
    for (const target of [
        'create',
        'task'
    ]){
        Object.assign(modelCatalogState[target], {
            key: '',
            loading: false,
            summary: '配置已改变，请重新读取模型',
            details: '',
            failed: false
        });
    }
    createModels = [];
    taskPickerModels = [];
    createResolvedDefault = '';
    if (creatingTask) void loadCreateModels(true);
    else if (detail && !element('task-model-menu').classList.contains('hidden')) void refreshTaskModels();
}
function installModelPicker() {
    element('task-model-menu').insertAdjacentHTML('beforeend', '<div class="model-catalog-footer"><p id="task-models-context" class="model-context"></p><p id="task-models-status" class="model-catalog-status" role="status"></p><div class="model-catalog-actions"><button id="task-reload-models" type="button">刷新列表</button></div><details class="model-catalog-details"><summary>来源与诊断</summary><p id="task-models-hint"></p></details></div>');
    button('task-reload-models').onclick = ()=>void refreshTaskModels(true);
    for (const target of Object.keys(modelPickers)){
        const ids = modelPickers[target];
        if (!element(ids.root)) continue;
        button(ids.button).onclick = ()=>void toggleModelMenu(target);
        button(ids.button).setAttribute('aria-haspopup', 'dialog');
        button(ids.button).setAttribute('aria-controls', ids.menu);
        element(ids.menu).setAttribute('role', 'dialog');
        element(ids.menu).setAttribute('aria-label', '选择模型');
        element(ids.list).setAttribute('role', 'listbox');
        element(ids.list).setAttribute('aria-label', '已配置的模型');
        input(ids.search).setAttribute('aria-label', '搜索模型或输入自定义模型 ID');
        element(ids.menu).onkeydown = (e)=>{
            if (e.key === 'Escape') {
                e.preventDefault();
                closeModelMenu(target);
                button(ids.button).focus();
            } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
                const options = Array.from(element(ids.list).querySelectorAll('.model-item')), current = options.indexOf(document.activeElement);
                if (options.length) {
                    e.preventDefault();
                    options[current < 0 ? e.key === 'ArrowDown' ? 0 : options.length - 1 : (current + (e.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length].focus();
                }
            }
        };
        input(ids.search).oninput = ()=>renderModelMenu(target);
        input(ids.search).onkeydown = (e)=>{
            if (e.key === 'Escape') {
                e.preventDefault();
                closeModelMenu(target);
                button(ids.button).focus();
            } else if (e.key === 'Enter') {
                e.preventDefault();
                element(ids.list).querySelector('.model-item')?.click();
            }
        };
        element(ids.list).onclick = (e)=>{
            const node = e.target;
            const effort = node.closest('[data-effort]');
            if (target === 'task' && effort) {
                void chooseTaskEffort(effort.dataset.effort || '');
                return;
            }
            const item = node.closest('[data-model]');
            if (!item) return;
            if (target === 'create') void chooseCreateModel(item.dataset.model || '');
            else void chooseTaskModel(item.dataset.model === '__custom__' ? input(ids.search).value.trim() : item.dataset.model || '');
        };
    }
    input('custom-model').oninput = ()=>{
        invalidateModelTest();
        updateCreateModelLabel();
        updateReasoning();
    };
    listenWithShell(document, 'click', (e)=>{
        const node = e.target;
        for (const target of Object.keys(modelPickers))if (!node.closest('#' + modelPickers[target].root)) closeModelMenu(target);
    });
}
async function toggleModelMenu(target) {
    const ids = modelPickers[target];
    if (!element(ids.root)) return;
    if (!element(ids.menu).classList.contains('hidden')) {
        closeModelMenu(target);
        return;
    }
    closeModelMenu(target === 'create' ? 'task' : 'create');
    input(ids.search).value = '';
    if (target === 'task') {
        if (!detail) return;
        if (detail.task.engine === 'deepseek-harness') {
            notify(harnessSessionHint);
            return;
        }
        element(ids.menu).classList.remove('hidden');
        button(ids.button).setAttribute('aria-expanded', 'true');
        input(ids.search).focus();
        void refreshTaskModels();
        return;
    }
    renderModelMenu(target);
    element(ids.menu).classList.remove('hidden');
    button(ids.button).setAttribute('aria-expanded', 'true');
    input(ids.search).focus();
    void loadCreateModels();
}
function closeModelMenu(target) {
    const ids = modelPickers[target];
    if (!element(ids.root)) return;
    element(ids.menu).classList.add('hidden');
    button(ids.button).setAttribute('aria-expanded', 'false');
}
function modelRow(value, name, hint, active, effort) {
    return `<button type="button" class="model-item${active ? ' selected' : ''}" role="option" aria-selected="${active}" data-model="${escapeHTML(value)}"${effort === undefined ? '' : ` data-effort="${escapeHTML(effort)}"`}><span class="model-item-name">${escapeHTML(name)}</span>${hint ? `<small>${escapeHTML(hint)}</small>` : ''}${active ? '<span class="model-item-check">✓</span>' : ''}</button>`;
}
function renderModelMenu(target) {
    renderModelCatalogStatus(target);
    const ids = modelPickers[target], search = input(ids.search).value.trim(), filter = search.toLowerCase();
    const models = target === 'task' ? taskPickerModels : createModels;
    const selected = target === 'task' ? detail?.task.model || '' : input('create-model').value;
    const matches = models.filter((m)=>!filter || m.id.toLowerCase().includes(filter) || (m.name || '').toLowerCase().includes(filter));
    const custom = !!filter && matches.length === 0;
    const rows = [];
    for (const m of matches)rows.push(modelRow(m.id, m.id, [
        m.name === m.id ? '' : m.name,
        m.origin === 'configured' ? '已配置' : m.origin === 'cache' ? '缓存' : ''
    ].filter(Boolean).join(' · '), selected === m.id));
    const head = custom ? modelRow('__custom__', `使用「${search}」`, '列表里没有这个模型，按此名称启动', selected === '__custom__' || selected === search) : filter ? '' : modelRow('', defaultModelLabel, target === 'task' ? '沿用该环境的默认模型' : createResolvedDefault ? '当前默认：' + createResolvedDefault : '', !selected);
    const tail = target === 'create' && !filter ? modelRow('__custom__', '自定义模型…', '输入列表里没有的名称', selected === '__custom__') : '';
    const empty = modelCatalogState[target].loading ? '正在读取模型列表…' : filter ? '没有匹配的模型' : '暂无模型；可使用工具默认或输入自定义 ID';
    const body = head + (rows.join('') || (custom ? '' : `<p class="model-empty">${empty}</p>`)) + tail;
    if (target === 'create') {
        element(ids.list).innerHTML = body;
        return;
    }
    const levels = effortLevels(detail?.task.engine || 'codex', models.find((m)=>m.id === selected));
    const efforts = levels.length ? `<div class="model-efforts"><small>推理强度</small><div>${[
        '',
        ...levels
    ].map((v)=>`<button type="button" class="model-effort${(detail?.task.reasoning_effort || '') === v ? ' selected' : ''}" data-effort="${escapeHTML(v)}">${escapeHTML(v === '' ? '工具默认' : effortLabels[v] || v)}</button>`).join('')}</div></div>` : '<p class="model-empty">此模型不提供推理强度</p>';
    element(ids.list).innerHTML = body + efforts;
}
function taskCatalogContextKey(task) {
    return task.id + ':' + catalogContextKey(settings.config.environments.find((env)=>env.id === task.environment.id) || task.environment, task.engine || 'codex', task.workspace);
}
function mergeTaskModels(task, list) {
    const models = [
        ...list
    ], known = new Set(models.map((m)=>m.id));
    const configured = engineDefaultModel(task.environment, task.engine || 'codex');
    if (configured && !known.has(configured)) {
        models.unshift({
            id: configured,
            name: configured + '（环境默认）'
        });
        known.add(configured);
    }
    if (task.model && !known.has(task.model)) models.unshift({
        id: task.model,
        name: task.model + '（当前）'
    });
    return models;
}
async function refreshTaskModels(refresh = false) {
    if (!detail || detail.task.engine === 'deepseek-harness') return;
    const task = detail.task, key = taskCatalogContextKey(task), state = modelCatalogState.task;
    if (state.loading && state.key === key && !refresh) return;
    const request = ++taskModelRequest;
    modelCatalogControllers.task?.abort();
    const controller = new AbortController();
    modelCatalogControllers.task = controller;
    taskPickerModels = mergeTaskModels(task, []);
    Object.assign(state, {
        key,
        loading: true,
        summary: '',
        details: '',
        failed: false
    });
    renderModelMenu('task');
    try {
        const result = await api(modelsURL(task.environment.id, task.engine || 'codex', task.workspace, refresh), 'GET', undefined, controller.signal);
        if (request !== taskModelRequest || !detail || taskCatalogContextKey(detail.task) !== key) return;
        taskPickerModels = mergeTaskModels(task, result.models || []);
        state.summary = catalogSummary(result);
        state.details = catalogDetails(result);
    } catch (e) {
        if (request !== taskModelRequest || !detail || taskCatalogContextKey(detail.task) !== key) return;
        state.failed = true;
        state.summary = '读取失败；可刷新或输入模型 ID';
        state.details = e.message;
    }
    if (request !== taskModelRequest || !detail || taskCatalogContextKey(detail.task) !== key) return;
    state.loading = false;
    if (!element('task-model-menu').classList.contains('hidden')) renderModelMenu('task');
}
async function chooseTaskModel(id) {
    closeModelMenu('task');
    if (!chosen || !detail) return;
    await applyTaskModel(id, undefined);
}
async function chooseTaskEffort(effort) {
    closeModelMenu('task');
    if (!chosen || !detail) return;
    await applyTaskModel(detail.task.model, effort);
}
async function applyTaskModel(model, effort) {
    if (detail?.task.engine === 'deepseek-harness') {
        notify(harnessSessionHint);
        return;
    }
    const id = chosen, body = {
        model: model === '__custom__' ? '' : model
    };
    if (effort !== undefined) body.reasoning_effort = effort;
    try {
        await api('tasks/' + id, 'PATCH', body);
        if (id === chosen) await poll();
        notify('已更新此任务' + (effort === undefined ? '的模型' : '的推理强度'));
    } catch (e) {
        notify(e.message);
    }
}
async function chooseCreateModel(value) {
    invalidateModelTest();
    closeModelMenu('create');
    const custom = input('custom-model'), typed = input('model-search').value.trim();
    if (value === '__custom__') {
        input('create-model').value = '__custom__';
        if (typed && !createModels.some((m)=>m.id === typed)) custom.value = typed;
        custom.classList.remove('hidden');
        custom.focus();
    } else {
        input('create-model').value = value;
        custom.classList.add('hidden');
    }
    updateCreateModelLabel();
    updateReasoning();
}
function updateCreateModelLabel() {
    const value = input('create-model').value, label = element('model-picker-label');
    if (!label) return;
    label.textContent = value === '__custom__' ? input('custom-model').value.trim() || '自定义模型…' : value || (createResolvedDefault ? '默认 · ' + createResolvedDefault : defaultModelLabel);
}
function setCreateModelsLoading() {
    modelTestRequest++;
    createModels = [];
    createResolvedDefault = '';
    input('create-model').value = '';
    input('custom-model').value = '';
    input('custom-model').classList.add('hidden');
    element('model-picker-label').textContent = defaultModelLabel;
    button('model-picker-button').disabled = false;
    element('model-test-result').textContent = '';
    updateReasoning();
}
function setCreateModels(models, defaultModel) {
    createModels = models;
    input('create-model').value = defaultModel || '';
    element('model-picker-label').textContent = defaultModel || defaultModelLabel;
    button('model-picker-button').disabled = false;
    syncModelTestButton();
    element('model-test-result').textContent = '';
    updateReasoning();
}
function updateReasoning() {
    const engine = input('create-engine').value, id = input('create-model').value === '__custom__' ? input('custom-model').value.trim() : input('create-model').value;
    const model = createModels.find((m)=>m.id === id), known = model?.reasoning_levels;
    const levels = engine === 'deepseek-harness' ? effortLevels(engine) : known ?? effortLevels(engine);
    const previous = input('create-effort').value;
    input('create-effort').innerHTML = '<option value="">工具默认' + (model?.default_reasoning ? ' · ' + escapeHTML(effortLabels[model.default_reasoning] || model.default_reasoning) : '') + '</option>' + levels.map((v)=>`<option value="${escapeHTML(v)}">${escapeHTML(effortLabels[v] || v)} · ${escapeHTML(v)}</option>`).join('');
    input('create-effort').value = levels.includes(previous) ? previous : '';
    input('create-effort').disabled = levels.length === 0;
    element('effort-hint').textContent = engine === 'deepseek-harness' ? 'Harness 支持 off / low / high / max，模型及 provider 需支持所选值。' + harnessSessionHint : known?.length === 0 ? '此模型不提供推理强度选择。' : known == null ? '默认沿用工具设置；手动选择需模型支持。' : '';
}
let discoveryEpoch = 0, discoveryID = '', discoveryTimer, discoveryPick = null;
function installDiscovery() {
    button('add-ssh').insertAdjacentHTML('afterend', '<button type="button" id="discover-environment">发现局域网 SSH</button>');
    element('device-protocol').insertAdjacentHTML('afterend', '<button type="button" id="discover-device" class="subtle">发现局域网 SSH</button>');
    element('root').insertAdjacentHTML('beforeend', `<dialog id="discovery-dialog"><h2>发现局域网 SSH</h2><p class="muted">从Duo所在电脑扫描，选中后填入连接地址。</p><label for="discovery-network">本机网络</label><select id="discovery-network"></select><div class="form-grid"><div><label for="discovery-cidr">扫描网段</label><input id="discovery-cidr" placeholder="192.168.50.0/24"></div><div><label for="discovery-ports">端口</label><input id="discovery-ports" value="22,2222" placeholder="22,2222"></div></div><div class="actions"><button class="primary" id="discovery-start">开始扫描</button><button id="discovery-stop" disabled>停止</button></div><p id="discovery-status" role="status"></p><div id="discovery-results" class="discovery-results"></div><p class="muted">发现服务不代表已登录；用户名、密钥或密码仍需配置。</p><p class="error" id="discovery-error"></p><div class="dialog-footer"><button id="discovery-close">关闭</button></div></dialog>`);
    button('discover-environment').onclick = ()=>void openDiscovery((endpoint)=>{
            if (input('environment-type').value !== 'ssh') addEnvironment('ssh');
            input('setting-host').value = endpoint.host;
            input('setting-port').value = String(endpoint.port);
            if (input('environment-name').value === '新 SSH 环境') input('environment-name').value = 'SSH · ' + endpoint.host;
            showEnvironmentFields();
            input('setting-user').focus();
            notify('已填入地址，补全用户名和工作目录后保存。');
        });
    button('discover-device').onclick = ()=>void openDiscovery((endpoint)=>{
            if (input('device-host').value !== endpoint.host || Number(input('device-port').value) !== endpoint.port) input('device-fingerprint').value = '';
            input('device-protocol').value = 'ssh';
            input('device-host').value = endpoint.host;
            input('device-port').value = String(endpoint.port);
            if (!input('device-name').value) input('device-name').value = 'SSH · ' + endpoint.host;
            deviceFields();
            input('device-user').focus();
        });
    button('discovery-start').onclick = ()=>void startDiscovery();
    button('discovery-stop').onclick = async ()=>{
        const id = discoveryID;
        button('discovery-stop').disabled = true;
        try {
            if (id) await api('ssh-discovery/scans/' + id, 'DELETE', {});
        } catch (e) {
            element('discovery-error').textContent = e.message;
        }
    };
    button('discovery-close').onclick = ()=>element('discovery-dialog').close();
    element('discovery-dialog').addEventListener('close', closeDiscovery);
}
function closeDiscovery() {
    ++discoveryEpoch;
    clearTimeout(discoveryTimer);
    const id = discoveryID;
    discoveryID = '';
    discoveryPick = null;
    if (id && authenticated) void api('ssh-discovery/scans/' + id, 'DELETE', {}).catch(()=>{});
}
async function openDiscovery(pick) {
    closeDiscovery();
    discoveryPick = pick;
    const token = discoveryEpoch;
    element('discovery-error').textContent = '';
    element('discovery-status').textContent = '读取本机网络…';
    element('discovery-results').replaceChildren();
    button('discovery-start').disabled = true;
    button('discovery-stop').disabled = true;
    for (const id of [
        'discovery-network',
        'discovery-cidr',
        'discovery-ports'
    ])input(id).disabled = true;
    input('discovery-network').innerHTML = '';
    input('discovery-cidr').value = '';
    input('discovery-ports').value = '22,2222';
    element('discovery-dialog').showModal();
    try {
        const networks = await api('ssh-discovery/networks');
        if (token !== discoveryEpoch) return;
        input('discovery-network').innerHTML = networks.map((n, i)=>`<option value="${i}">${escapeHTML(n.name)} · ${escapeHTML(n.address)}</option>`).join('');
        const chooseNetwork = ()=>{
            input('discovery-cidr').value = networks[Number(input('discovery-network').value)]?.suggested || '';
        };
        input('discovery-network').onchange = chooseNetwork;
        chooseNetwork();
        for (const id of [
            'discovery-network',
            'discovery-cidr',
            'discovery-ports'
        ])input(id).disabled = !networks.length;
        button('discovery-start').disabled = !networks.length;
        element('discovery-status').textContent = networks.length ? '选择网段后开始扫描，最多 256 个地址、4 个端口。' : '未发现已连接的 IPv4 局域网。';
    } catch (e) {
        if (token === discoveryEpoch) element('discovery-error').textContent = e.message;
    }
}
async function startDiscovery() {
    const token = discoveryEpoch, ports = input('discovery-ports').value.split(/[,，\s]+/).filter(Boolean).map(Number);
    if (!ports.length || ports.length > 4 || ports.some((p)=>!Number.isInteger(p) || p < 1 || p > 65535)) {
        element('discovery-error').textContent = '请输入 1–4 个端口，范围 1–65535。';
        return;
    }
    button('discovery-start').disabled = true;
    element('discovery-error').textContent = '';
    element('discovery-results').replaceChildren();
    try {
        const scan = await api('ssh-discovery/scans', 'POST', {
            cidr: input('discovery-cidr').value.trim(),
            ports
        });
        if (token !== discoveryEpoch) {
            void api('ssh-discovery/scans/' + scan.id, 'DELETE', {}).catch(()=>{});
            return;
        }
        discoveryID = scan.id;
        renderDiscovery(scan);
        if (scan.status === 'running') scheduleDiscovery(token, scan.id);
    } catch (e) {
        if (token === discoveryEpoch) {
            element('discovery-error').textContent = e.message;
            button('discovery-start').disabled = false;
        }
    }
}
function scheduleDiscovery(token, id) {
    clearTimeout(discoveryTimer);
    discoveryTimer = setTimeout(async ()=>{
        try {
            const scan = await api('ssh-discovery/scans/' + id);
            if (token !== discoveryEpoch || id !== discoveryID) return;
            renderDiscovery(scan);
            if (scan.status === 'running') scheduleDiscovery(token, id);
        } catch (e) {
            if (token === discoveryEpoch) {
                element('discovery-error').textContent = e.message;
                button('discovery-start').disabled = false;
                button('discovery-stop').disabled = true;
            }
        }
    }, 650);
}
function renderDiscovery(scan) {
    const running = scan.status === 'running';
    button('discovery-start').disabled = running;
    button('discovery-stop').disabled = !running;
    for (const id of [
        'discovery-network',
        'discovery-cidr',
        'discovery-ports'
    ])input(id).disabled = running;
    const status = {
        running: '扫描中',
        done: '扫描完成',
        cancelled: '已停止',
        timeout: '扫描超时'
    };
    element('discovery-status').textContent = `${status[scan.status] || scan.status} · ${scan.completed}/${scan.total} · 发现 ${scan.results.filter((r)=>r.ssh).length} 个 SSH 服务`;
    element('discovery-results').innerHTML = scan.results.map((r, i)=>`<div class="discovery-row"><div><strong>${escapeHTML(r.host)}:${r.port}</strong><small>${escapeHTML(r.ssh ? r.banner : '端口开放，未确认是 SSH')}</small></div><button data-ssh-result="${i}" ${r.ssh ? '' : 'disabled'}>选择</button></div>`).join('') || (!running ? '<p class="muted">此范围未发现开放端口，可以检查网段或添加设备的 SSH 端口。</p>' : '');
    element('discovery-results').querySelectorAll('[data-ssh-result]').forEach((b)=>b.onclick = ()=>{
            const endpoint = scan.results[Number(b.dataset.sshResult)], pick = discoveryPick;
            if (!endpoint?.ssh || !pick) return;
            element('discovery-dialog').close();
            pick(endpoint);
        });
}
const taskTerminals = new Map();
const selectedTerminals = new Map();
let terminalDialogTask = '', terminalSequence = 0;
function currentTaskTerminal() {
    return taskTerminals.get(selectedTerminals.get(chosen) || '');
}
function terminalLabel(t) {
    return t.environment + ' · ' + t.workspace;
}
function chooseTerminalEnvironment() {
    const id = input('terminal-environment').value, e = settings.config.environments.find((e)=>e.id === id);
    input('terminal-workspace').value = id ? e?.workspaces[0] || '' : detail?.task.workspace || '';
    element('terminal-workspaces').innerHTML = (id ? e?.workspaces || [] : [
        detail?.task.workspace || ''
    ]).map((p)=>`<option value="${escapeHTML(p)}"></option>`).join('');
}
function newTerminalDialog() {
    if (!detail || detail.task.archived) return;
    terminalDialogTask = chosen;
    input('terminal-environment').innerHTML = '<option value="">当前任务环境</option>' + settings.config.environments.map((e)=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');
    chooseTerminalEnvironment();
    element('terminal-dialog').showModal();
}
function installTerminal() {
    element('task-model').insertAdjacentHTML('beforebegin', '<button id="terminal-tab">终端</button>');
    element('tool-body').insertAdjacentHTML('beforeend', `<section id="terminal-panel" class="hidden"><div class="terminal-toolbar"><select id="terminal-picker" aria-label="任务终端"></select><button id="terminal-new" title="新建终端，可另选环境">＋</button><span id="terminal-state" role="status">未打开</span><button id="terminal-open" class="primary">打开终端</button><button id="terminal-end" class="hidden">结束</button><button id="terminal-use" title="将选中的终端文本放入任务输入框">选中内容带入对话</button></div><p id="terminal-context" class="muted"></p><div id="terminal-stage"><div id="terminal-empty">在当前任务的工作目录打开终端。</div></div><div class="terminal-keys"><button data-terminal-key="ctrl-c">Ctrl+C</button><button data-terminal-key="tab">Tab</button><button data-terminal-key="esc">Esc</button><button data-terminal-key="up" aria-label="历史上一条">↑</button><button data-terminal-key="down" aria-label="历史下一条">↓</button><button data-terminal-key="enter">Enter</button><small>刷新或关闭页面会结束终端</small></div></section>`);
    element('root').insertAdjacentHTML('beforeend', `<dialog id="terminal-dialog"><form id="terminal-form"><h2>新建终端</h2><label for="terminal-environment">执行环境</label><select id="terminal-environment"></select><label for="terminal-workspace">工作目录</label><input id="terminal-workspace" list="terminal-workspaces" required><datalist id="terminal-workspaces"></datalist><p class="muted">只设置这个终端，任务的 AI 环境保持不变。</p><div class="dialog-footer"><button type="button" id="terminal-cancel">取消</button><button class="primary">打开终端</button></div></form></dialog>`);
    button('terminal-new').onclick = newTerminalDialog;
    input('terminal-picker').onchange = ()=>{
        selectedTerminals.set(chosen, input('terminal-picker').value);
        renderTerminal();
    };
    input('terminal-environment').onchange = chooseTerminalEnvironment;
    button('terminal-cancel').onclick = ()=>element('terminal-dialog').close();
    element('terminal-form').onsubmit = (e)=>{
        e.preventDefault();
        if (terminalDialogTask !== chosen) {
            notify('任务已切换，请重新打开终端设置');
            return;
        }
        element('terminal-dialog').close();
        void openTaskTerminal(true, input('terminal-environment').value, input('terminal-workspace').value);
    };
    button('terminal-tab').onclick = ()=>switchTab('terminal');
    button('terminal-open').onclick = ()=>void openTaskTerminal();
    button('terminal-end').onclick = ()=>{
        const t = currentTaskTerminal();
        if (t) {
            t.live = false;
            t.starting = false;
            t.state = '已结束';
            t.term.options.disableStdin = true;
            t.socket?.close();
            renderTerminal();
        }
    };
    button('terminal-use').onclick = ()=>{
        const text = currentTaskTerminal()?.term.getSelection();
        if (!text) {
            notify('先在终端里选中要分析的文字');
            return;
        }
        if (text.length > 24000) {
            notify('选中内容太长，请缩小到 24000 字以内');
            return;
        }
        bringToChat('请分析以下终端输出：\n\n' + text);
    };
    element('terminal-panel').querySelectorAll('[data-terminal-key]').forEach((b)=>b.onclick = ()=>{
            const t = currentTaskTerminal();
            if (!t) return;
            terminalInput(t, {
                'ctrl-c': '\x03',
                tab: '\t',
                esc: '\x1b',
                up: '\x1b[A',
                down: '\x1b[B',
                enter: '\r'
            }[b.dataset.terminalKey]);
            t.term.focus();
        });
}
function renderTerminal() {
    if (!element('terminal-panel')) return;
    const t = currentTaskTerminal();
    for (const entry of taskTerminals.values())entry.host.classList.toggle('hidden', entry !== t);
    const list = [
        ...taskTerminals.values()
    ].filter((v)=>v.task === chosen);
    input('terminal-picker').innerHTML = list.map((v, i)=>`<option value="${v.id}">${i + 1} · ${escapeHTML(v.environment)}${v.live ? '' : ' · ' + escapeHTML(v.state)}</option>`).join('') || '<option>暂无终端</option>';
    input('terminal-picker').value = t?.id || '';
    button('terminal-new').disabled = !detail || !!detail.task.archived;
    element('terminal-empty').classList.toggle('hidden', !!t);
    element('terminal-state').textContent = t?.state || '未打开';
    element('terminal-context').textContent = t ? terminalLabel(t) : detail ? (detail.task.environment?.name || '') + ' · ' + detail.task.workspace : '';
    button('terminal-open').classList.toggle('hidden', !!t && (t.live || t.starting));
    button('terminal-open').textContent = t ? '重新打开' : '打开终端';
    button('terminal-open').disabled = !detail || detail.task.archived;
    button('terminal-end').classList.toggle('hidden', !t || !t.live && !t.starting);
    button('terminal-use').disabled = !t;
    element('terminal-panel').querySelectorAll('[data-terminal-key]').forEach((b)=>b.disabled = !t?.live);
    const count = [
        ...taskTerminals.values()
    ].filter((x)=>x.live || x.starting).length;
    button('terminal-tab').textContent = count ? '终端 · ' + count : '终端';
    if (t && toolsTab === 'terminal') requestAnimationFrame(()=>fitTaskTerminal(t));
}
function fitTaskTerminal(t) {
    if (t.host.classList.contains('hidden') || !t.host.clientWidth || !t.host.clientHeight) return;
    const screen = t.term.element?.querySelector('.xterm-screen'), rect = screen?.getBoundingClientRect();
    if (!rect?.width || !rect.height) return;
    const viewport = t.term.element?.querySelector('.xterm-viewport');
    const cols = Math.max(2, Math.min(500, Math.floor((viewport?.clientWidth || t.host.clientWidth - 30) / (rect.width / t.term.cols)))), rows = Math.max(2, Math.min(200, Math.floor((t.host.clientHeight - 12) / (rect.height / t.term.rows))));
    if (cols !== t.term.cols || rows !== t.term.rows) t.term.resize(cols, rows);
    if (t.live && t.socket?.readyState === WebSocket.OPEN && (t.sentCols !== cols || t.sentRows !== rows)) {
        t.socket.send(JSON.stringify({
            type: 'resize',
            cols,
            rows
        }));
        t.sentCols = cols;
        t.sentRows = rows;
    }
}
function terminalInput(t, data) {
    if (!t.live || t.socket?.readyState !== WebSocket.OPEN) return;
    const bytes = typeof data === 'string' ? new TextEncoder().encode(data) : data;
    if (bytes.length > 65536 || t.socket.bufferedAmount > 131072) {
        notify('终端输入过长或网络繁忙，请分段发送');
        return;
    }
    t.socket.send(new Uint8Array(bytes));
}
function disposeTaskTerminal(t) {
    t.live = false;
    t.starting = false;
    t.resize.disconnect();
    if (t.timer) clearTimeout(t.timer);
    t.socket?.close();
    t.term.dispose();
    t.host.remove();
    taskTerminals.delete(t.id);
}
function closeTaskTerminals() {
    for (const t of taskTerminals.values())disposeTaskTerminal(t);
}
async function openTaskTerminal(fresh = false, environmentID = '', workspace = '') {
    const task = chosen, id = 'terminal-' + ++terminalSequence;
    if (!detail || detail.task.id !== task || detail.task.archived) return;
    const previous = currentTaskTerminal();
    if (!fresh) {
        if (previous?.live || previous?.starting) return;
        if (previous) {
            environmentID = previous.environmentID;
            workspace = previous.workspace;
            disposeTaskTerminal(previous);
        }
    }
    const environment = environmentID ? settings.config.environments.find((e)=>e.id === environmentID) : detail.task.environment;
    if (!environment) {
        notify('执行环境不存在');
        return;
    }
    workspace = workspace || (environmentID ? environment.workspaces[0] : detail.task.workspace);
    if (taskTerminals.size >= 8) {
        const old = [
            ...taskTerminals.values()
        ].find((t)=>!t.live && !t.starting);
        if (old) disposeTaskTerminal(old);
    }
    const host = document.createElement('div');
    host.className = 'task-terminal';
    host.setAttribute('aria-label', '任务交互终端');
    element('terminal-stage').append(host);
    const term = new Terminal({
        cols: 100,
        rows: 30,
        fontFamily: 'Consolas, "Microsoft YaHei", monospace',
        fontSize: 13,
        lineHeight: 1.2,
        scrollback: 3000,
        cursorBlink: true,
        screenReaderMode: true,
        disableStdin: true,
        theme: {
            background: '#101318',
            foreground: '#e0e7f1',
            cursor: '#d8f383',
            selectionBackground: '#46533a'
        }
    });
    const t = {
        id,
        task,
        environmentID,
        environment: environment.name,
        workspace,
        term,
        host,
        socket: null,
        state: '正在连接…',
        live: false,
        starting: true,
        resize: new ResizeObserver(()=>{
            if (t.timer) clearTimeout(t.timer);
            t.timer = setTimeout(()=>fitTaskTerminal(t), 100);
        }),
        timer: null,
        pendingBytes: 0,
        sentCols: 0,
        sentRows: 0
    };
    taskTerminals.set(id, t);
    selectedTerminals.set(task, id);
    term.open(host);
    t.resize.observe(host);
    term.onData((data)=>terminalInput(t, data));
    term.onBinary((data)=>terminalInput(t, Uint8Array.from(data, (c)=>c.charCodeAt(0))));
    term.attachCustomKeyEventHandler((e)=>!(e.type === 'keydown' && e.ctrlKey && e.key.toLowerCase() === 'c' && term.getSelection()));
    renderTerminal();
    try {
        const ticket = await api(`tasks/${task}/terminal`, 'POST', {
            environment_id: environmentID,
            workspace
        });
        if (!t.starting || taskTerminals.get(id) !== t) return;
        const socket = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/api/terminal/' + encodeURIComponent(ticket.ticket));
        t.socket = socket;
        socket.binaryType = 'arraybuffer';
        socket.onmessage = (e)=>{
            if (taskTerminals.get(id) !== t) return;
            if (e.data instanceof ArrayBuffer) {
                const data = new Uint8Array(e.data);
                t.pendingBytes += data.length;
                if (t.pendingBytes > 2 * 1024 * 1024) {
                    t.state = '输出过快，连接已结束';
                    t.live = false;
                    t.starting = false;
                    socket.close();
                    renderTerminal();
                    return;
                }
                term.write(data, ()=>t.pendingBytes -= data.length);
                return;
            }
            try {
                const msg = JSON.parse(e.data);
                if (msg.type === 'ready' && t.starting) {
                    t.starting = false;
                    t.live = true;
                    t.state = '已连接';
                    term.options.disableStdin = false;
                    fitTaskTerminal(t);
                    if (chosen === task && currentTaskTerminal() === t && toolsTab === 'terminal') term.focus();
                } else if (msg.type === 'error' || msg.type === 'exit') {
                    t.starting = false;
                    t.live = false;
                    t.state = msg.type === 'error' ? msg.message : `已退出 · ${msg.code}`;
                    term.options.disableStdin = true;
                }
                renderTerminal();
            } catch  {
                t.state = '终端响应异常';
                socket.close();
            }
        };
        socket.onclose = ()=>{
            if (taskTerminals.get(id) !== t) return;
            if (t.live || t.starting) t.state = '连接已断开，可重新打开';
            t.live = false;
            t.starting = false;
            term.options.disableStdin = true;
            renderTerminal();
        };
        socket.onerror = ()=>{
            if (taskTerminals.get(id) === t) {
                t.state = '连接失败，请重新打开';
                renderTerminal();
            }
        };
    } catch (e) {
        if (taskTerminals.get(id) === t) {
            t.starting = false;
            t.state = e.message;
            renderTerminal();
        }
    }
}
window.addEventListener('beforeunload', (e)=>{
    if ([
        ...taskTerminals.values()
    ].some((t)=>t.live || t.starting)) {
        e.preventDefault();
        e.returnValue = '';
    }
});
window.addEventListener('pagehide', closeTaskTerminals);
let taskView = 'active', searchHits = null, searchRequest = 0, searchTimer, searchPending = false, searchLimited = false, searchError = '';
let fileTask = '', fileDirectory = '', filePath = '', fileMode = 'list', fileView = 'read', fileContent = '', fileEntries = [], fileRequest = 0, filePreviewRequest = 0;
function installProductivity() {
    input('search').placeholder = '搜索任务、对话、便签、知识';
    input('search').maxLength = 160;
    input('search').setAttribute('aria-label', '统一搜索');
    input('search').insertAdjacentHTML('afterend', '<nav class="task-views" aria-label="任务视图"><button id="tasks-active" class="selected">任务</button><button id="tasks-archived">已归档</button></nav><div id="search-summary" class="muted hidden"></div><button id="search-clear" class="subtle hidden">清空搜索</button>');
    input('search').oninput = ()=>{
        searchRequest++;
        clearTimeout(searchTimer);
        searchHits = null;
        searchError = '';
        searchPending = !!input('search').value.trim();
        renderList();
        searchTimer = setTimeout(()=>void searchEverywhere(), 250);
    };
    button('search-clear').onclick = ()=>{
        input('search').value = '';
        searchRequest++;
        searchHits = null;
        searchPending = false;
        searchError = '';
        renderList();
    };
    for (const view of [
        'active',
        'archived'
    ])button('tasks-' + view).onclick = ()=>{
        taskView = view;
        button('search-clear').click();
        renderList();
    };
    element('task-model').insertAdjacentHTML('beforebegin', '<button id="files-tab">文件与改动</button>');
    button('files-tab').onclick = ()=>switchTab('files');
    element('tool-body').insertAdjacentHTML('beforeend', `<section id="files-panel" class="tool-panel hidden"><p class="muted">读取当前任务执行环境中的工作目录。Git 状态包含已有修改，不能归因于本轮执行。</p><div class="file-toolbar"><button id="files-root">根目录</button><button id="files-up">上一级</button><button id="files-refresh">刷新</button><button id="files-changes">查看 Git 改动</button></div><p id="file-location" class="file-location"></p><input id="file-filter" aria-label="筛选当前文件列表" placeholder="筛选当前列表中的文件"><p id="file-list-status" class="muted" role="status"></p><div id="file-list" class="file-list"></div><div id="file-preview" class="hidden"><h3 id="file-name"></h3><div class="file-toolbar"><button id="file-read">内容</button><button id="file-diff">Git 差异</button><a id="file-download">下载文件</a><button id="file-reference">路径带入对话</button></div><p id="file-status" class="muted" role="status"></p><pre id="file-code" tabindex="0" aria-label="文件内容或差异"></pre></div></section>`);
    button('files-root').onclick = ()=>{
        fileMode = 'list';
        fileDirectory = '';
        void loadFiles();
    };
    button('files-up').onclick = ()=>{
        fileMode = 'list';
        fileDirectory = fileDirectory.split('/').slice(0, -1).join('/');
        void loadFiles();
    };
    button('files-refresh').onclick = ()=>void loadFiles();
    button('files-changes').onclick = ()=>{
        fileMode = fileMode === 'changes' ? 'list' : 'changes';
        void loadFiles();
    };
    input('file-filter').oninput = renderFiles;
    button('file-read').onclick = ()=>void previewFile(filePath, 'read');
    button('file-diff').onclick = ()=>void previewFile(filePath, 'diff');
    button('file-reference').onclick = ()=>{
        if (fileTask !== chosen || !filePath) return;
        bringToChat('请查看当前工作目录中的文件：' + filePath);
    };
}
async function searchEverywhere() {
    const q = input('search').value.trim(), token = ++searchRequest;
    if (!q) {
        searchHits = null;
        searchPending = false;
        renderList();
        return;
    }
    searchPending = true;
    renderList();
    try {
        const r = await api('search?q=' + encodeURIComponent(q));
        if (token !== searchRequest) return;
        searchHits = r.hits;
        searchLimited = r.truncated;
        searchError = '';
    } catch (e) {
        if (token !== searchRequest) return;
        searchHits = [];
        searchError = e.message;
    } finally{
        if (token === searchRequest) {
            searchPending = false;
            renderList();
        }
    }
}
async function openSearchHit(hit) {
    try {
        await choose(hit.task_id);
        if (chosen !== hit.task_id || !detail) return;
        if (hit.kind === 'knowledge') {
            switchTab('note');
            element('knowledge-list').querySelector('[data-knowledge="' + hit.reference + '"]')?.scrollIntoView({
                block: 'center'
            });
            element('knowledge-list').querySelector('[data-knowledge-edit="' + hit.reference + '"]')?.focus();
        } else if (hit.kind === 'scratch') {
            switchTab('scratch');
            await loadScratch();
            const target = element('scratch-list').querySelector(`[data-edit="${hit.reference}"]`);
            target?.scrollIntoView({
                block: 'center'
            });
            target?.focus();
        } else if (hit.kind === 'message') {
            const seq = Number(hit.reference);
            if (seq > 0) {
                const d = await api('tasks/' + hit.task_id + '?after=' + Math.max(0, seq - 5));
                if (chosen !== hit.task_id) return;
                sequence = 0;
                resetConversation();
                detail = d;
                element('conversation').innerHTML = '<div class="search-context">正在显示搜索位置附近的对话。<button id="search-full-chat">从开头查看</button></div>';
                appendEvents(d.events);
                button('search-full-chat').onclick = ()=>void choose(hit.task_id);
                const target = element('conversation').querySelector(`[data-event="${seq}"]`);
                if (target) {
                    target.dataset.searchMatch = 'true';
                    applyConversationFilter();
                    notify('已临时显示搜索命中的消息，筛选偏好保持不变。');
                }
                target?.scrollIntoView({
                    block: 'center'
                });
            }
        }
    } catch (e) {
        notify(e.message);
    }
}
async function changeTaskPreference(field, id = chosen) {
    const target = tasks.find((t)=>t.id === id) || (id === chosen ? detail?.task : undefined);
    if (!id || !target) return;
    const value = !target[field];
    try {
        const t = await api('tasks/' + id + '/preferences', 'PATCH', {
            [field]: value
        });
        const i = tasks.findIndex((x)=>x.id === id);
        if (i >= 0) tasks[i] = t;
        if (id === chosen && detail) {
            detail.task = t;
            if (field === 'archived') taskView = t.archived ? 'archived' : 'active';
            renderTask();
        }
        renderList();
        notify(field === 'pinned' ? value ? '任务已置顶' : '已取消置顶' : value ? '任务已归档，记录保留，可随时恢复' : '任务已恢复');
        if (input('search').value.trim()) void searchEverywhere();
    } catch (e) {
        notify(e.message);
    }
}
function filesURL(action, p) {
    return `tasks/${fileTask}/files?action=${action}&path=${encodeURIComponent(p)}`;
}
async function loadFiles() {
    if (!chosen) return;
    if (fileTask !== chosen) {
        fileTask = chosen;
        filePreviewRequest++;
        fileDirectory = '';
        filePath = '';
        fileMode = 'list';
        input('file-filter').value = '';
        element('file-preview').classList.add('hidden');
    }
    const task = fileTask, token = ++fileRequest;
    fileEntries = [];
    renderFiles();
    element('file-list-status').textContent = '正在读取 ' + (detail?.task.environment?.name || '执行环境') + '…';
    element('file-location').textContent = fileMode === 'changes' ? '工作目录当前 Git 改动' : fileDirectory || '工作目录 /';
    button('files-up').disabled = !fileDirectory;
    button('files-changes').textContent = fileMode === 'changes' ? '返回文件列表' : '查看 Git 改动';
    try {
        const result = await api(filesURL(fileMode, fileMode === 'changes' ? '' : fileDirectory));
        if (chosen !== task || token !== fileRequest) return;
        fileEntries = result.items || [];
        renderFiles();
        element('file-list-status').textContent = (result.message || `${fileEntries.length} 项`) + (result.truncated ? ' · 仅显示前 500 项，请进入子目录查看' : '');
    } catch (e) {
        if (token === fileRequest) element('file-list-status').textContent = e.message;
    }
}
function renderFiles() {
    if (!element('file-list')) return;
    const q = input('file-filter').value.trim().toLowerCase();
    element('file-list').innerHTML = fileEntries.filter((f)=>f.path.toLowerCase().includes(q)).map((f)=>`<button class="file-row" data-file="${escapeHTML(f.path)}" ${f.blocked ? 'disabled' : ''}><span class="file-icon">${f.directory ? '▸' : '·'}</span><span>${escapeHTML(fileMode === 'changes' ? f.path : f.name)}${f.blocked ? '（链接或特殊文件）' : ''}</span><small>${escapeHTML(f.status || (!f.directory ? formatFileSize(f.size) : ''))}</small></button>`).join('') || '<p class="muted">列表为空。</p>';
    element('file-list').querySelectorAll('[data-file]').forEach((b)=>b.onclick = ()=>{
            const f = fileEntries.find((f)=>f.path === b.dataset.file);
            if (f.directory) {
                fileDirectory = f.path;
                fileMode = 'list';
                input('file-filter').value = '';
                void loadFiles();
            } else {
                void previewFile(f.path, fileMode === 'changes' && f.status !== '??' ? 'diff' : 'read');
            }
        });
}
function formatFileSize(size) {
    return size >= 1048576 ? (size / 1048576).toFixed(1) + ' MiB' : size >= 1024 ? (size / 1024).toFixed(1) + ' KiB' : size + ' B';
}
async function previewFile(p, view) {
    if (!p || fileTask !== chosen) return;
    filePath = p;
    fileView = view;
    fileContent = '';
    const token = ++filePreviewRequest, task = chosen;
    element('file-preview').classList.remove('hidden');
    element('file-name').textContent = p;
    element('file-status').textContent = '正在读取…';
    element('file-code').textContent = '';
    button('file-read').classList.toggle('selected', view === 'read');
    button('file-diff').classList.toggle('selected', view === 'diff');
    const download = element('file-download');
    download.href = '/api/' + filesURL('download', p);
    download.download = p.split('/').at(-1) || 'download';
    try {
        const r = await api(filesURL(view, p));
        if (chosen !== task || token !== filePreviewRequest) return;
        fileContent = r.content || '';
        element('file-status').textContent = (r.message || (view === 'read' ? formatFileSize(r.size) + ' · UTF-8 文本' : '工作目录当前差异')) + (r.truncated ? ' · 内容已截断' : '');
        if (view === 'diff') element('file-code').innerHTML = fileContent.split('\n').map((line)=>`<span class="${line.startsWith('+') ? 'diff-add' : line.startsWith('-') ? 'diff-remove' : line.startsWith('@@') ? 'diff-context' : ''}">${escapeHTML(line)}\n</span>`).join('');
        else element('file-code').textContent = fileContent;
    } catch (e) {
        if (token === filePreviewRequest) element('file-status').textContent = e.message;
    }
}
let toolsTab = '', scratchItems = [], scratchEditing = null, scratchTask = '', scratchOriginal = '', scratchFilter = 'open', scratchPending = {};
let devices = [], deviceID = '', deviceTask = '', deviceSeq = 0, deviceEvents = [], devicePolling = false, deviceEditing = null;
function installTools() {
    element('task-model').insertAdjacentHTML('beforebegin', '<button id="scratch-tab">待办</button><button id="hardware-tab">硬件调试</button>');
    element('notebook').insertAdjacentHTML('afterend', `<section id="scratch-panel" class="tool-panel hidden"><div class="note-head"><div><h2>待办</h2><p id="scratch-status" class="muted">每个任务一份待办清单，需要时带入对话。</p></div><button id="scratch-new" class="primary">＋ 新建待办</button></div><nav class="task-views" id="scratch-filter" aria-label="待办筛选"><button data-scratch-filter="open" class="selected">未完成</button><button data-scratch-filter="todo">待办</button><button data-scratch-filter="doing">进行中</button><button data-scratch-filter="done">已完成</button><button data-scratch-filter="all">全部</button></nav><div id="scratch-list" class="scratch-grid"></div></section><section id="hardware-panel" class="tool-panel hidden"></section>`);
    element('root').insertAdjacentHTML('beforeend', `<dialog id="scratch-dialog"><form id="scratch-form"><h2>待办</h2><label for="scratch-title">标题</label><input id="scratch-title" maxlength="120" placeholder="例如：验证继电器上电顺序"><div class="form-grid"><div><label for="scratch-status-field">状态</label><select id="scratch-status-field"><option value="todo">待办</option><option value="doing">进行中</option><option value="done">已完成</option></select></div><div><label for="scratch-due">截止日（可选）</label><input id="scratch-due" type="date"></div></div><label for="scratch-content">备注（可选）</label><textarea id="scratch-content" rows="8" maxlength="60000" aria-label="待办备注" placeholder="命令、线索或下一步想做的事"></textarea><label for="scratch-color">颜色</label><select id="scratch-color"><option value="yellow">黄色</option><option value="green">绿色</option><option value="blue">蓝色</option><option value="pink">粉色</option><option value="purple">紫色</option></select><p id="scratch-error" class="error"></p><div class="dialog-footer"><button type="button" id="scratch-cancel">取消</button><button class="primary">保存待办</button></div></form></dialog><dialog id="device-dialog"><form id="device-form"><h2>硬件连接</h2><label for="device-name">名称</label><input id="device-name" required maxlength="80"><label for="device-protocol">协议</label><select id="device-protocol"><option value="serial">串口 · 服务电脑</option><option value="tcp">TCP 原始字节流</option><option value="telnet">Telnet</option><option value="ssh">SSH</option></select><div id="device-serial-fields"><label for="device-portname">串口</label><input id="device-portname" list="device-ports" placeholder="COM3"><datalist id="device-ports"></datalist><button type="button" id="device-refresh-ports">刷新串口</button><label for="device-baud">波特率 · 8N1</label><input id="device-baud" type="number" min="300" max="4000000" value="115200"></div><div id="device-network-fields"><label for="device-host">主机</label><input id="device-host" placeholder="192.168.50.20"><label for="device-port">端口</label><input id="device-port" type="number" min="1" max="65535"></div><div id="device-ssh-fields"><label for="device-user">SSH 用户名</label><input id="device-user" autocomplete="off"><label for="device-fingerprint">主机 SHA256 指纹</label><input id="device-fingerprint" placeholder="SHA256:…"><p>首次连接会显示指纹。核对设备后填入并保存，再连接。也支持服务用户 .ssh 目录中默认命名的未加密私钥。</p></div><label class="check-row"><input id="device-readonly" type="checkbox">只接收，不允许发送</label><p id="device-error" class="error"></p><div class="dialog-footer"><button id="device-delete" type="button" class="danger">删除连接</button><button id="device-cancel" type="button">取消</button><button class="primary">保存配置</button></div></form></dialog><dialog id="device-password-dialog"><form id="device-password-form"><h2>连接 SSH</h2><label for="device-password">密码（使用密钥时留空）</label><input id="device-password" type="password" autocomplete="off"><p>密码仅用于本次连接，不保存到配置或日志。</p><div class="dialog-footer"><button type="button" id="device-password-cancel">取消</button><button class="primary">连接</button></div></form></dialog>`);
    button('scratch-tab').onclick = ()=>switchTab('scratch');
    button('hardware-tab').onclick = ()=>switchTab('hardware');
    button('scratch-new').onclick = ()=>editScratch(null);
    button('scratch-cancel').onclick = cancelScratch;
    element('scratch-dialog').addEventListener('cancel', (e)=>{
        e.preventDefault();
        cancelScratch();
    });
    element('scratch-filter').querySelectorAll('button').forEach((b)=>b.onclick = ()=>{
            scratchFilter = b.dataset.scratchFilter;
            renderScratchList();
        });
    element('scratch-form').onsubmit = async (e)=>{
        e.preventDefault();
        try {
            await api(`tasks/${scratchTask}/scratch` + (scratchEditing && scratchEditing.id ? '/' + scratchEditing.id : ''), scratchEditing ? 'PUT' : 'POST', {
                title: input('scratch-title').value.trim(),
                content: input('scratch-content').value,
                status: input('scratch-status-field').value,
                due: input('scratch-due').value,
                revision: scratchEditing?.revision,
                color: input('scratch-color').value
            });
            element('scratch-dialog').close();
            await loadScratch();
            await loadStickyBoard();
        } catch (e) {
            element('scratch-error').textContent = e.message;
        }
    };
    installHardware();
}
function bringToChat(text) {
    const old = drafts.get(chosen) || '';
    drafts.set(chosen, old ? old + '\n\n' + text : text);
    input('message').value = drafts.get(chosen);
    switchTab('chat');
    input('message').focus();
    notify('已放入输入框，确认后发送。');
}
function cancelScratch() {
    const changed = input('scratch-title').value.trim() !== (scratchEditing?.title || '') || input('scratch-content').value !== (scratchEditing?.content || '') || input('scratch-due').value !== (scratchEditing?.due || '') || input('scratch-status-field').value !== (scratchEditing?.status || 'todo') || input('scratch-color').value !== (scratchEditing?.color || 'yellow');
    if (changed && !confirm('放弃未保存的待办修改？')) return;
    element('scratch-dialog').close();
}
function editScratch(n) {
    scratchEditing = n;
    scratchTask = n?.task_id || chosen;
    scratchOriginal = n?.content || '';
    input('scratch-title').value = n?.title || '';
    input('scratch-content').value = scratchOriginal;
    input('scratch-status-field').value = n?.status || 'todo';
    input('scratch-due').value = n?.due || '';
    input('scratch-color').value = n?.color || 'yellow';
    element('scratch-error').textContent = '';
    element('scratch-dialog').showModal();
    input('scratch-title').focus();
}
function scratchStatus(n) {
    return n.status === 'done' ? 'done' : n.status === 'doing' ? 'doing' : 'todo';
}
function localToday() {
    const d = new Date();
    return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0');
}
function scratchCardHTML(n, scope) {
    const status = scratchStatus(n), title = n.title || scratchHeadingPlain(n.content), overdue = status !== 'done' && !!n.due && n.due < localToday();
    return `<article class="scratch-card" data-color="${escapeHTML(n.color || 'yellow')}" data-status="${status}"><header class="todo-head"><input type="checkbox" data-toggle="${n.id}" title="勾选完成" ${status === 'done' ? 'checked' : ''} ${scratchPending[n.id] ? 'disabled' : ''}><button class="todo-title" data-edit="${n.id}" title="编辑待办">${escapeHTML(title)}</button></header>${n.content ? `<div class="todo-note">${escapeHTML(n.content.slice(0, 300))}${n.content.length > 300 ? '…' : ''}</div>` : ''}<footer><span>${status === 'done' ? '已完成' : status === 'doing' ? '进行中' : '待办'}${n.due ? ` · ${overdue ? '已逾期 ' : '截止 '}${escapeHTML(n.due)}` : ''}${scope === 'all' ? ` · ${escapeHTML(n.task_title || '当前任务')}` : ''}</span><span class="todo-actions"><button data-use="${n.id}" title="把内容带入当前对话">↗</button><button data-delete="${n.id}" title="删除待办">×</button></span></footer></article>`;
}
function scratchHeadingPlain(content) {
    const line = content.split('\n').map((l)=>l.trim()).find(Boolean) || '未命名待办';
    return line.length > 60 ? line.slice(0, 60) + '…' : line;
}
function scratchVisible(list) {
    return list.filter((n)=>{
        const s = scratchStatus(n);
        return scratchFilter === 'all' || (scratchFilter === 'open' ? s !== 'done' : s === scratchFilter);
    });
}
function renderScratchList() {
    const items = scratchVisible(scratchItems);
    element('scratch-filter').querySelectorAll('button').forEach((b)=>{
        const on = b.dataset.scratchFilter === scratchFilter;
        b.classList.toggle('selected', on);
        b.setAttribute('aria-pressed', String(on));
    });
    if (element('scratch-status')) element('scratch-status').textContent = `共 ${scratchItems.length} 条 · 未完成 ${scratchItems.filter((n)=>scratchStatus(n) !== 'done').length} 条`;
    element('scratch-list').innerHTML = items.map((n)=>scratchCardHTML(n, 'task')).join('') || '<p class="muted">还没有待办。点“新建待办”记下下一步要做的事。</p>';
    element('scratch-list').querySelectorAll('[data-toggle]').forEach((box)=>box.onclick = ()=>void toggleScratch(scratchItems.find((n)=>n.id === box.dataset.toggle), box.checked));
    element('scratch-list').querySelectorAll('[data-edit]').forEach((b)=>b.onclick = ()=>editScratch(scratchItems.find((n)=>n.id === b.dataset.edit) || null));
    element('scratch-list').querySelectorAll('[data-use]').forEach((b)=>b.onclick = ()=>useScratchItem(scratchItems.find((n)=>n.id === b.dataset.use)));
    element('scratch-list').querySelectorAll('[data-delete]').forEach((b)=>b.onclick = ()=>void deleteScratch(scratchItems.find((n)=>n.id === b.dataset.delete)));
}
async function loadScratch() {
    const id = chosen;
    try {
        const items = await api(`tasks/${id}/scratch`);
        if (id !== chosen) return;
        scratchItems = items;
        renderScratchList();
    } catch (e) {
        notify(e.message);
    }
}
function useScratchItem(n) {
    if (!n) return;
    const text = [
        n.title,
        n.content
    ].filter(Boolean).join('\n\n');
    bringToChat(text);
    renderWorkflow();
    element('sidebar').classList.remove('open');
}
async function toggleScratch(n, checked) {
    if (!n || scratchPending[n.id]) return;
    scratchPending[n.id] = true;
    if (toolsTab === 'scratch') renderScratchList();
    try {
        await api(`tasks/${n.task_id}/scratch/${n.id}`, 'PUT', {
            title: n.title || '',
            content: n.content,
            status: checked ? 'done' : 'todo',
            due: n.due || '',
            revision: n.revision,
            color: n.color
        });
        await loadScratch();
        await loadStickyBoard();
    } catch (e) {
        notify(e.message);
        if (toolsTab === 'scratch') renderScratchList();
    } finally{
        delete scratchPending[n.id];
    }
}
async function deleteScratch(n) {
    if (!n || !confirm('删除这条待办？')) return;
    try {
        await api(`tasks/${n.task_id}/scratch/${n.id}`, 'DELETE', {
            revision: n.revision
        });
        await loadScratch();
        await loadStickyBoard();
    } catch (e) {
        notify(e.message);
    }
}
let toolFullscreen = false;
function installWorkspace() {
    const main = element('conversation').parentElement;
    const workspace = document.createElement('div');
    workspace.id = 'workspace';
    const left = document.createElement('aside');
    left.id = 'dock-left';
    left.className = 'dock hidden';
    left.setAttribute('aria-label', '左侧工具区');
    const right = document.createElement('aside');
    right.id = 'dock-right';
    right.className = 'dock hidden';
    right.setAttribute('aria-label', '右侧工具区');
    const chat = document.createElement('div');
    chat.id = 'chat-column';
    const bottom = document.createElement('div');
    bottom.id = 'dock-bottom';
    bottom.className = 'dock-bottom dock hidden';
    bottom.setAttribute('aria-label', '对话下方工具区');
    const dock = document.createElement('aside');
    dock.id = 'tool-dock';
    dock.className = 'hidden';
    dock.setAttribute('aria-label', '任务工具');
    dock.innerHTML = '<div id="tool-resizer" role="separator" tabindex="0" aria-label="调整工具宽度" aria-orientation="vertical" aria-valuemin="22" aria-valuemax="75"></div><header class="tool-dock-head"><strong id="tool-title">任务工具</strong><div class="actions"><button id="tool-expand" title="展开工具面板">全屏</button><button id="tool-close" aria-label="关闭工具面板">✕</button></div></header><div id="tool-body"></div>';
    main.append(workspace);
    workspace.append(left, chat, right);
    right.append(dock);
    chat.append(element('conversation'), bottom, element('composer-wrap'), element('create-page'));
    for (const id of [
        'notebook',
        'scratch-panel',
        'hardware-panel'
    ])element('tool-body').append(element(id));
    button('chat-tab').textContent = '对话';
    button('tool-close').onclick = ()=>switchTab('chat');
    button('tool-expand').onclick = ()=>{
        toolFullscreen = !toolFullscreen;
        workspace.classList.toggle('tool-full', toolFullscreen);
        button('tool-expand').textContent = toolFullscreen ? '分屏' : '全屏';
    };
}
function installSettingsSections() {
    const form = element('settings-form'), heading = form.querySelector('h2'), feishuHeading = form.querySelector('#setup-feishu').previousElementSibling, error = element('settings-error'), footer = form.querySelector('.dialog-footer');
    const environment = document.createElement('section');
    environment.id = 'settings-environment';
    environment.className = 'settings-section';
    while(heading.nextSibling && heading.nextSibling !== feishuHeading)environment.append(heading.nextSibling);
    const feishu = document.createElement('section');
    feishu.id = 'settings-feishu';
    feishu.className = 'settings-section hidden';
    let node = feishuHeading;
    while(node && node !== error){
        const next = node.nextSibling;
        feishu.append(node);
        node = next;
    }
    const manual = document.createElement('details');
    manual.className = 'manual-feishu';
    manual.innerHTML = '<summary>手工配置已有机器人</summary>';
    const firstLabel = feishu.querySelector('label[for="feishu-id"]');
    if (firstLabel) {
        while(firstLabel.previousSibling && firstLabel.previousSibling !== feishu.querySelector('#setup-feishu'))feishu.removeChild(firstLabel.previousSibling);
        let n = firstLabel;
        while(n){
            const next = n.nextSibling;
            manual.append(n);
            n = next;
        }
        ;
        feishu.append(manual);
    }
    feishu.insertBefore(feishu.querySelector('#feishu-state'), manual);
    feishu.insertBefore(feishu.querySelector('#feishu-owner'), manual);
    feishu.insertBefore(feishu.querySelector('#feishu-enabled').parentElement, manual);
    const tabs = document.createElement('nav');
    tabs.className = 'settings-nav';
    tabs.setAttribute('aria-label', '设置分类');
    tabs.innerHTML = '<button type="button" data-settings="environment" class="selected">执行环境</button><button type="button" data-settings="feishu">飞书连接</button><button type="button" data-settings="access">访问地址</button>';
    const access = document.createElement('section');
    access.id = 'settings-access';
    access.className = 'settings-section hidden';
    access.innerHTML = '<h3>从其他设备打开Duo</h3><p>Duo运行在这台电脑上，其他电脑或手机通过浏览器访问。远程访问前，请让两端连接同一 Tailscale 网络，并保持服务电脑开机。</p><label for="access-lan">局域网地址</label><input id="access-lan" type="url" placeholder="http://192.168.1.10:8789"><label for="access-tailscale">Tailscale 地址或域名</label><input id="access-tailscale" type="url" placeholder="http://100.100.100.10:8789"><p>保存后，新生成的飞书任务清单和详情卡片会显示这两个入口；详情入口会直接定位到对应任务。地址中不包含密码或登录令牌。</p><div id="access-preview" class="access-preview"></div><p>打开后仍需输入工作台密码。更换局域网、端口或设备后请更新地址。飞书内置浏览器打不开时，可复制地址到系统浏览器。</p>';
    form.insertBefore(tabs, error);
    form.insertBefore(environment, error);
    form.insertBefore(feishu, error);
    form.insertBefore(access, error);
    const saved = document.createElement('p');
    saved.id = 'settings-saved';
    saved.setAttribute('role', 'status');
    saved.style.color = 'var(--accent)';
    form.insertBefore(saved, footer);
    tabs.onclick = (e)=>{
        const target = e.target.closest('button[data-settings]');
        if (target && tabs.contains(target)) showSettingsSection(target.dataset.settings);
    };
    input('access-lan').oninput = renderAccessPreview;
    input('access-tailscale').oninput = renderAccessPreview;
    footer.classList.add('settings-footer');
}
function showSettingsSection(page) {
    const nav = element('settings-form').querySelector('.settings-nav');
    nav.querySelectorAll('button[data-settings]').forEach((b)=>b.classList.toggle('selected', b.dataset.settings === page));
    element('settings-form').querySelectorAll('.settings-section').forEach((section)=>section.classList.toggle('hidden', section.id !== 'settings-' + page));
    button('settings-save').classList.toggle('hidden', page === 'updates' || page === 'engines');
    if (page === 'engines') void loadEngineSettings();
    else if (page === 'updates') void loadUpdateInformation();
}
function loadAccessSettings() {
    input('access-lan').value = settings.config.access?.lan || '';
    input('access-tailscale').value = settings.config.access?.tailscale || '';
    renderAccessPreview();
}
function renderAccessPreview() {
    element('access-preview').innerHTML = [
        [
            '局域网',
            'access-lan'
        ],
        [
            'Tailscale',
            'access-tailscale'
        ]
    ].map(([name, id])=>{
        const value = input(id).value.trim();
        try {
            const u = new URL(value);
            if (![
                'http:',
                'https:'
            ].includes(u.protocol) || u.username || u.password || u.search || u.hash || ![
                '',
                '/'
            ].includes(u.pathname)) return '';
            return `<a href="${escapeHTML(u.href)}" target="_blank" rel="noopener noreferrer">${name}打开 ↗<small>${escapeHTML(u.origin)}</small></a>`;
        } catch  {
            return '';
        }
    }).join('');
}
let engineCatalog = null;
let engineSettingsRequest = 0;
function installEngineSettings() {
    const form = element('settings-form'), nav = form.querySelector('.settings-nav');
    nav.insertAdjacentHTML('beforeend', '<button type="button" data-settings="engines">AI 引擎</button>');
    element('settings-error').insertAdjacentHTML('beforebegin', `<section id="settings-engines" class="settings-section hidden"><h3>AI 引擎与账号</h3><p>引擎负责实际干活，执行环境负责在哪里干活；账号/API 只保存目标环境里的 profile 引用，不把密钥写进Duo数据库。</p><div id="engine-catalog" class="engine-catalog"><p class="muted">正在读取引擎目录…</p></div><details class="engine-profile-editor"><summary>添加或更新账号/API 引用</summary><label for="engine-profile-id">配置 ID</label><input id="engine-profile-id" placeholder="例如 codex-main"><label for="engine-profile-name">显示名称</label><input id="engine-profile-name" placeholder="工作账号"><label for="engine-profile-engine">引擎</label><select id="engine-profile-engine"></select><label for="engine-profile-environment">执行环境</label><select id="engine-profile-environment"></select><label for="engine-profile-kind">类型</label><select id="engine-profile-kind"></select><label for="engine-profile-reference">外部引用</label><input id="engine-profile-reference" placeholder="目录路径或 native profile 名称"><p class="muted">这里不填写 API key；只填写目标环境可访问的配置目录、profile 名称或后续适配器约定的引用。</p><button type="button" id="engine-profile-save" class="primary">保存引用</button><p id="engine-profile-result" role="status"></p></details></section>`);
    button('engine-profile-save').onclick = ()=>void saveEngineProfile();
    input('engine-profile-reference').nextElementSibling.textContent = '这里不填写 API key。native 继承目标环境默认配置，不指定命名 profile；配置目录引用必须是目标环境可访问的路径。';
}
function engineTargetName(id) {
    return settings.config.environments.find((e)=>e.id === id)?.name || id;
}
function engineCredentialLabel(kind) {
    return ({
        native: '继承目标环境默认配置',
        dsh_home: 'Harness 配置目录（DSH_HOME）',
        codex_home: 'Codex 配置目录（CODEX_HOME）',
        claude_home: 'Claude 配置目录（CLAUDE_CONFIG_DIR）',
        env_file: '环境文件引用（仅记录，尚未应用）'
    })[kind] || kind;
}
function engineProfileActivationMessage(profile) {
    if (profile?.kind === 'env_file') return '引用已选中，但环境文件尚未应用到运行进程；请在目标环境中配置。';
    if (profile?.engine === 'deepseek-harness') return profile.kind === 'native' ? '新建 Harness 任务将继承目标环境的默认配置；当前运行会话保持不变。' : '新建 Harness 任务将使用该 DSH_HOME 配置目录；当前运行会话保持不变。';
    return '账号/API 配置已切换，下一次运行生效。';
}
function renderEngineCatalog() {
    if (!engineCatalog) return;
    const profiles = engineCatalog.profiles;
    element('engine-catalog').innerHTML = engineCatalog.engines.map((e)=>{
        const rows = profiles.filter((p)=>p.engine === e.id).map((p)=>`<div class="engine-profile"><span>${escapeHTML(p.name)} · ${escapeHTML(engineTargetName(p.environment_id))}</span><code>${escapeHTML(engineCredentialLabel(p.kind))}${p.kind === 'native' ? '' : ': ' + escapeHTML(p.reference)}</code><button type="button" data-engine-activate="${escapeHTML(p.id)}">切换</button></div>`).join('');
        const active = Object.entries(engineCatalog.active_profile).filter(([key])=>key.endsWith(':' + e.id)).map(([, value])=>value);
        return `<article class="engine-card"><header><div><strong>${escapeHTML(e.name)}</strong><small>${escapeHTML(e.transport)} · ${e.runnable ? '可执行' : '待接入适配器'}</small></div><span>${active.length ? '已配置' : '未配置'}</span></header><p>${escapeHTML(e.description)}</p><p class="muted">${escapeHTML(e.install_description || '')}</p><div class="engine-actions"><button type="button" data-engine-plan="${escapeHTML(e.id)}">查看安装/检查计划</button>${e.documentation_url ? `<a href="${escapeHTML(e.documentation_url)}" target="_blank" rel="noopener noreferrer">官方文档 ↗</a>` : ''}</div>${rows || '<p class="muted">还没有账号/API 引用。</p>'}<pre class="engine-plan hidden" data-engine-plan-result="${escapeHTML(e.id)}"></pre></article>`;
    }).join('');
    element('engine-catalog').querySelectorAll('[data-engine-plan]').forEach((b)=>b.onclick = ()=>void showEnginePlan(b.dataset.enginePlan));
    element('engine-catalog').querySelectorAll('[data-engine-activate]').forEach((b)=>b.onclick = ()=>void activateEngineProfile(b.dataset.engineActivate));
}
async function loadEngineSettings() {
    const epoch = shellEpoch, request = ++engineSettingsRequest;
    try {
        const catalog = await api('engines', 'GET', undefined, shellController.signal);
        if (!shellCurrent(epoch) || request !== engineSettingsRequest) return;
        engineCatalog = catalog;
        renderEngineCatalog();
        populateEngineProfileForm();
    } catch (e) {
        if (shellCurrent(epoch) && request === engineSettingsRequest) element('engine-catalog').textContent = e.message;
    }
}
function populateEngineProfileForm() {
    if (!engineCatalog) return;
    const engines = element('engine-profile-engine'), envs = element('engine-profile-environment');
    engines.innerHTML = engineCatalog.engines.map((e)=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');
    envs.innerHTML = settings.config.environments.map((e)=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');
    const kinds = element('engine-profile-kind'), reference = input('engine-profile-reference');
    const refreshReference = ()=>{
        const native = kinds.value === 'native';
        reference.disabled = native;
        reference.placeholder = kinds.value === 'dsh_home' ? '目标环境中的 Harness 配置目录绝对路径' : native ? '自动继承目标环境默认配置' : '目标环境可访问的配置目录或外部引用';
        if (native) reference.value = 'default';
        else if (reference.value === 'default') reference.value = '';
        reference.title = native ? '不是命名 profile；使用目标环境默认配置。' : '不填写 API key。';
    };
    const refresh = ()=>{
        const e = engineCatalog.engines.find((x)=>x.id === engines.value);
        kinds.innerHTML = (e?.credential_kinds || []).filter((k)=>e?.id !== 'deepseek-harness' || k !== 'env_file').map((k)=>`<option value="${escapeHTML(k)}">${escapeHTML(engineCredentialLabel(k))}</option>`).join('');
        refreshReference();
    };
    engines.onchange = refresh;
    kinds.onchange = refreshReference;
    refresh();
}
async function showEnginePlan(engine) {
    const env = settings.config.default_environment, pre = document.querySelector(`[data-engine-plan-result="${CSS.escape(engine)}"]`);
    if (!pre) return;
    pre.classList.remove('hidden');
    pre.textContent = '正在读取计划…';
    try {
        const plan = await api(`environments/${encodeURIComponent(env)}/engines/${encodeURIComponent(engine)}/install-plan`);
        pre.textContent = [
            plan.message,
            ...(plan.steps || []).map((s)=>'• ' + s.description)
        ].join('\n');
    } catch (e) {
        pre.textContent = e.message;
    }
}
async function saveEngineProfile() {
    const profile = {
        id: input('engine-profile-id').value.trim(),
        name: input('engine-profile-name').value.trim(),
        engine: element('engine-profile-engine').value,
        environment_id: element('engine-profile-environment').value,
        kind: element('engine-profile-kind').value,
        reference: input('engine-profile-reference').value.trim(),
        created: 0,
        updated: 0
    };
    const epoch = shellEpoch;
    try {
        await api('engine-profiles', 'PUT', profile);
        if (!shellCurrent(epoch)) return;
        invalidateModelCatalogs();
        element('engine-profile-result').textContent = profile.kind === 'env_file' ? '已保存引用；环境文件目前仅记录，不会应用到运行进程。' : profile.engine === 'deepseek-harness' ? '已保存；点击对应配置的“切换”后，新建 Harness 任务使用该配置。' : '已保存；点击对应配置的“切换”后对下一次运行生效。';
        await loadEngineSettings();
    } catch (e) {
        if (shellCurrent(epoch)) element('engine-profile-result').textContent = e.message;
    }
}
async function activateEngineProfile(id) {
    const epoch = shellEpoch;
    try {
        await api(`engine-profiles/${encodeURIComponent(id)}/activate`, 'POST', {});
        if (!shellCurrent(epoch)) return;
        invalidateModelCatalogs();
        notify(engineProfileActivationMessage(engineCatalog?.profiles.find((p)=>p.id === id)));
        await loadEngineSettings();
    } catch (e) {
        if (shellCurrent(epoch)) notify(e.message);
    }
}
const element = (id)=>document.getElementById(id);
const input = (id)=>element(id);
const button = (id)=>element(id);
const escapeHTML = (v)=>v.replace(/[&<>"']/g, (c)=>({
            '&': '&amp;',
            '<': '&lt;',
            '>': '&gt;',
            '"': '&quot;',
            "'": '&#39;'
        })[c] || c);
const names = {
    idle: '待开始',
    queued: '排队中',
    running: '执行中',
    done: '等待下一步',
    failed: '执行失败',
    interrupted: '已停止'
};
let csrf = '', tasks = [], settings, chosen = '', detail = null, knowledgeItems = [], knowledgeEditing = null;
let sequence = 0, selection = 0, dirty = false, sending = false, polling = false, authenticated = false, refreshList = 0, lastList = '', lastKnowledge = '', noticeTimer;
let taskContext = [];
const drafts = new Map();
let editingEnvironments = [], editingID = "", modelRequest = 0;
let createFiles = [], creatingTask = false, createReturnTask = '', createPermission = 'auto';
let createSubmitting = false, sessionResetTask = '', settingsPolling = false;
let shellEpoch = 0, shellController = new AbortController();
let shellCleanups = [];
function shellCurrent(epoch) {
    return epoch === shellEpoch;
}
function disposeWithShell(cleanup) {
    shellCleanups.push(cleanup);
}
function listenWithShell(target, type, listener, options = {}) {
    target.addEventListener(type, listener, {
        ...options,
        signal: shellController.signal
    });
}
function renewShellScope() {
    shellController.abort();
    for (const cleanup of shellCleanups.splice(0))try {
        cleanup();
    } catch  {}
    shellController = new AbortController();
    shellEpoch++;
    selection++;
    modelRequest++;
    taskModelRequest++;
    modelTestRequest++;
    polling = false;
    settingsPolling = false;
    createSubmitting = false;
    sending = false;
    sessionResetTask = '';
}
function notify(text) {
    element('notice').textContent = text;
    element('notice').classList.add('show');
    clearTimeout(noticeTimer);
    noticeTimer = setTimeout(()=>element('notice').classList.remove('show'), 6500);
}
async function api(path, method = 'GET', data, signal) {
    const epoch = shellEpoch;
    const response = await fetch('/api/' + path, {
        method,
        credentials: 'same-origin',
        cache: 'no-store',
        signal,
        headers: {
            'Content-Type': 'application/json',
            'X-CSRF-Token': csrf
        },
        body: data === undefined ? undefined : JSON.stringify(data)
    });
    const result = await response.json().catch(()=>({
            error: '服务返回内容异常'
        }));
    if (!response.ok) {
        if (response.status === 401 && path !== 'login' && shellCurrent(epoch)) {
            authenticated = false;
            showLogin();
        }
        throw Object.assign(new Error(result.error || '请求失败'), {
            status: response.status
        });
    }
    return result;
}
function markdown(text) {
    const chunks = text.split(/```[^\n]*\n([\s\S]*?)```/g);
    return chunks.map((part, i)=>i % 2 ? '<pre><code>' + escapeHTML(part) + '</code></pre>' : escapeHTML(part).split(/\n\s*\n/).map((block)=>{
            const inline = (s)=>s.replace(/`([^`]+)`/g, '<code>$1</code>').replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
            if (/^#{1,3} /.test(block)) {
                const level = Math.min(3, block.match(/^#+/)[0].length);
                return `<h${level}>${inline(block.replace(/^#+ /, ''))}</h${level}>`;
            }
            if (block.split('\n').every((l)=>/^[-*] /.test(l))) return '<ul>' + block.split('\n').map((l)=>'<li>' + inline(l.slice(2)) + '</li>').join('') + '</ul>';
            return '<p>' + inline(block).replace(/\n/g, '<br>') + '</p>';
        }).join('')).join('');
}
function showLogin() {
    renewShellScope();
    closeHardwareView();
    closeDiscovery();
    closeTaskTerminals();
    resetConversation();
    resetCodexApprovals();
    chosen = '';
    detail = null;
    authenticated = false;
    element('root').innerHTML = `<div class="login-shell"><form class="login" id="login"><div class="brand"><div class="logo">D</div><div><strong>Duo</strong><small>LOCAL TASK WORKSPACE</small></div></div><h1>回来，接着做。</h1><p>你的任务、执行过程和解决办法，<br>都留在这里。</p><label for="password">访问密码</label><input id="password" type="password" autocomplete="current-password" required placeholder="输入工作台密码"><p id="login-error" class="error"></p><button class="primary" id="login-submit">进入工作台 →</button></form></div>`;
    element('login').onsubmit = async (e)=>{
        e.preventDefault();
        const epoch = shellEpoch;
        button('login-submit').disabled = true;
        try {
            const r = await api('login', 'POST', {
                password: input('password').value
            });
            if (!shellCurrent(epoch)) return;
            csrf = r.csrf;
            await boot();
        } catch (error) {
            if (shellCurrent(epoch) && element('login-error')) element('login-error').textContent = error.message;
        } finally{
            if (shellCurrent(epoch) && element('login-submit')) button('login-submit').disabled = false;
        }
    };
}
async function boot() {
    const epoch = shellEpoch, signal = shellController.signal;
    const status = await api('auth', 'GET', undefined, signal);
    if (!shellCurrent(epoch)) return;
    if (!status.authenticated) {
        showLogin();
        return;
    }
    csrf = status.csrf;
    const loaded = await Promise.all([
        api('tasks', 'GET', undefined, signal),
        api('settings', 'GET', undefined, signal),
        api('workbench', 'GET', undefined, signal)
    ]);
    if (!shellCurrent(epoch)) return;
    [tasks, settings, workCatalog] = loaded;
    authenticated = true;
    try {
        renderShell();
        renderList();
    } catch (error) {
        showShellFailure(error);
        return;
    }
    const query = new URLSearchParams(location.search), id = query.get('task');
    if (id && tasks.some((t)=>t.id === id)) await choose(id, query.get('view') === 'note' ? 'note' : 'chat');
    if (query.get('settings') === 'access') {
        await openSettings();
        document.querySelector('[data-settings="access"]')?.click();
    }
}
function showShellFailure(error) {
    authenticated = false;
    renewShellScope();
    const message = error instanceof Error ? error.message.slice(0, 500) : '界面初始化异常，请重新加载。';
    element('root').innerHTML = '<div class="login-shell"><section class="login" role="alert"><h1>工作台界面未能加载</h1><p>本机数据不会因此被清除。请重新加载；若仍失败，请把以下提示发给维护者。</p><p class="error">' + escapeHTML(message) + '</p><button type="button" id="shell-retry">重新加载</button></section></div>';
    button('shell-retry').onclick = ()=>location.reload();
}
function renderShell() {
    renewShellScope();
    lastList = '';
    element('root').innerHTML = `<div class="app"><aside class="sidebar" id="sidebar"><div class="brand"><div class="logo">D</div><div><strong>Duo</strong><small>LOCAL TASK WORKSPACE</small></div></div><button class="primary" id="new-task">＋ 新建任务</button><input id="search" placeholder="查找任务" aria-label="查找任务"><div class="task-list" id="task-list"></div><div class="sidebar-footer"><button class="subtle" id="settings-open">设置</button><button class="mobile-menu subtle" id="sidebar-close">收起</button><span id="connection">本机服务已连接</span><button class="subtle" id="logout">退出</button></div></aside><main><header class="header"><div class="actions"><button class="mobile-menu" id="menu" aria-label="展开任务列表">☰</button><div><h1 id="task-title">把事情做完，把经验留下。</h1><p id="task-workspace">独立工作台 · 本地 AI 工具</p></div></div><div class="actions hidden" id="task-actions"><button id="bind-open">飞书连接</button></div></header><nav class="tabs hidden" id="tabs"><button id="chat-tab" class="selected">对话与执行</button><button id="note-tab">任务知识</button><span class="model-picker" id="task-model"><button type="button" id="task-model-button" aria-expanded="false" aria-haspopup="listbox" title="本任务使用的 AI 工具、模型和推理强度"><span id="task-model-label"></span><span class="model-picker-caret">▾</span></button><div class="model-menu hidden" id="task-model-menu" role="listbox"><input id="task-model-search" class="model-search-input" placeholder="搜索或输入模型名称" autocomplete="off"><div id="task-model-list" class="model-list"></div></div></span></nav><div id="session-banner" class="session-banner hidden"></div><section id="conversation" class="conversation"><div class="empty"><div class="eyebrow">ONE TASK. KEEP GOING.</div><h2>从一个具体目标开始。</h2><p>选好本地目录，把要求交给 AI 工具。<br>在网页或飞书继续同一个任务，<br>再把有用的解决办法留在任务里。</p><button class="primary" id="empty-new">创建一个任务 →</button></div></section><section id="notebook" class="notebook hidden"><div class="note-head"><div><h2>任务知识</h2><p id="note-status">一份任务，一份可复用的记录。</p></div><div class="actions"><button class="primary" id="knowledge-new">＋ 新建知识</button><button id="summarize">整理任务知识</button><button id="export-note">导出</button></div></div><nav class="task-views" id="knowledge-filter" aria-label="知识筛选"><button data-knowledge-filter="all" class="selected">全部</button><button data-knowledge-filter="observed">待验证</button><button data-knowledge-filter="verified">已验证</button><button data-knowledge-filter="stale">已过时</button></nav><div id="draft-banner" class="draft-banner hidden"><span id="draft-label">执行总结 · 未保存</span><div class="actions"><button id="adopt-draft">编辑后保存</button><button id="save-draft" class="primary">保存总结</button></div></div><details id="draft-preview" class="knowledge-preview hidden" open><summary>草稿预览</summary><div id="draft-content" class="content"></div></details><div id="knowledge-list" class="knowledge-list"></div></section><section id="composer-wrap" class="composer-wrap hidden"><div id="run-status" class="run-status"></div><form id="composer" class="composer"><textarea id="message" rows="2" aria-label="任务要求" placeholder="下一步，要做什么？"></textarea><div class="composer-bottom"><small>Enter 发送<br>Shift + Enter 换行</small><div class="actions"><button type="button" id="stop" class="hidden">停止</button><button class="primary" id="send">发送 ↑</button></div></div></form><div class="footnote">在服务所在电脑执行 · 保留所选工具的原生会话</div></section></main></div>
 <section id="create-page" class="create-page hidden" aria-labelledby="create-heading"><form id="create-form" class="create-form"><h2 id="create-heading">新建任务</h2><p>给一个具体的目标，其余在对话中继续。</p><label for="create-input">任务要求</label><textarea id="create-input" rows="4" required placeholder="描述希望完成的事情"></textarea><label for="create-environment">执行环境</label><select id="create-environment"></select><label for="create-workspace">工作目录</label><input id="create-workspace" list="workspace-options" required autocomplete="off" placeholder="输入该环境中已有目录的绝对路径"><datalist id="workspace-options"></datalist><p>可直接修改路径，也可选择常用或最近使用的目录。</p><label for="create-engine">AI 工具</label><select id="create-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option><option value="deepseek-harness">DeepSeek Harness</option></select><label for="create-effort">推理强度</label><select id="create-effort"><option value="">工具默认</option></select><p class="muted" id="effort-hint"></p><label for="model-picker-button">模型</label><div class="model-picker" id="model-picker"><button type="button" id="model-picker-button" aria-expanded="false" aria-haspopup="listbox"><span id="model-picker-label">使用此工具的默认模型</span><span class="model-picker-caret">▾</span></button><div class="model-menu hidden" id="model-menu" role="listbox"><input id="model-search" class="model-search-input" placeholder="搜索或输入模型名称" autocomplete="off"><div id="model-list" class="model-list"></div></div></div><input id="create-model" type="hidden"><input id="custom-model" class="hidden" placeholder="输入自定义模型名称" aria-label="自定义模型"><p id="models-hint"></p><button type="button" id="reload-models">重读模型列表</button><button type="button" id="test-models">测试模型</button><p id="model-test-result" class="model-test-result" role="status"></p><p>AI 工具可在选定工作目录内读写文件。请填写所选环境中的已有目录，常用目录可在设置中管理。</p><p class="error" id="create-error"></p><div class="dialog-footer"><button type="button">取消</button><button class="primary" id="create-submit">创建并执行</button></div></form></section>
 <dialog id="settings-dialog"><form id="settings-form"><h2>工作台设置</h2><p>独立程序、独立数据。使用各环境中 Codex / Claude Code 的登录状态。</p><h3 class="section-title">执行环境</h3><p>任务保存自己的环境。这里的修改只影响之后新建的任务。</p><label for="default-environment">默认环境（飞书新建任务也使用它）</label><select id="default-environment"></select><label for="environment-picker">编辑环境</label><select id="environment-picker"></select><div class="actions environment-actions"><button type="button" id="add-wsl">新增 WSL</button><button type="button" id="add-windows">新增 Windows</button><button type="button" id="add-ssh">新增 SSH</button><button type="button" id="remove-environment" class="danger">删除环境</button></div><div class="form-grid"><div><label for="environment-name">环境名称</label><input id="environment-name"></div><div><label for="environment-type">执行方式</label><select id="environment-type"><option value="wsl">WSL</option><option value="windows">本机 Windows</option><option value="ssh">SSH · Linux 主机</option></select></div></div><div id="wsl-fields"><label for="setting-distro">WSL 发行版</label><input id="setting-distro"></div><div id="linux-user"><label for="setting-user">执行用户名（可留空使用默认用户）</label><input id="setting-user"></div><div id="ssh-fields"><div class="form-grid"><div><label for="setting-host">SSH 主机 / SSH 配置别名</label><input id="setting-host" placeholder="例如：192.168.50.20"></div><div><label for="setting-port">SSH 端口</label><input id="setting-port" type="number" min="1" max="65535"></div></div><label for="setting-identity">私钥文件（服务电脑上的路径，可留空）</label><input id="setting-identity"><p>支持密钥或 ssh-agent。请先在运行Duo的 Windows 用户下用 ssh 登录该主机，确认指纹并配置免密登录。远端需要所选 AI 工具和 Python 3。</p></div><label for="setting-codex">此环境中的 Codex 可执行文件</label><input id="setting-codex"><label for="setting-model">此环境的默认模型（可留空）</label><input id="setting-model"><label for="setting-workspaces">工作目录（每行一个绝对路径）</label><textarea id="setting-workspaces" rows="3"></textarea><p><button type="button" id="check-codex">检查已保存的当前环境</button></p><div id="check-result" class="settings-result"></div><h3 class="section-title">飞书私聊</h3><button type="button" id="setup-feishu">扫码创建并绑定机器人</button><p>首次使用可扫码自动创建，无需填写凭据。已有机器人也可使用下面的手工配置。</p><p>使用飞书自建应用的长连接。开启机器人，订阅 im.message.receive_v1，授予接收私聊消息和以机器人发送消息的权限。</p><p>若沿用原机器人，启用前先关闭它在其他程序中的连接。</p><label for="feishu-id">App ID</label><input id="feishu-id" autocomplete="off"><label for="feishu-secret">App Secret</label><input id="feishu-secret" type="password" autocomplete="new-password" placeholder="留空保留已保存的密钥"><label class="check-row"><input id="feishu-enabled" type="checkbox">启用飞书长连接</label><p id="feishu-state"></p><p>选中任务后直接发要求，每轮在同一张卡片中更新进展和结果。长结果可通过卡片的局域网或 Tailscale 入口查看。</p><p id="feishu-owner"></p><button type="button" id="pair-code">生成配对码</button><p id="pair-result" class="pair"></p><p>首次连接后，使用你的飞书向机器人发送配对命令。配对码十分钟有效，只有配对的账号可以操作任务。</p><p class="error" id="settings-error"></p><div class="dialog-footer"><button type="button" data-close="settings-dialog">关闭</button><button class="primary" id="settings-save">保存设置</button></div></form></dialog>
 <dialog id="bind-dialog"><h2>在飞书继续这个任务</h2><p id="bind-status"></p><p>连接后，从网页或飞书发来的要求进入同一个任务；该任务的完成结果会发送到此私聊。</p><div class="dialog-footer"><button data-close="bind-dialog">关闭</button><button id="bind-setup">扫码创建并绑定当前任务</button><button id="unbind">断开当前任务</button><button class="primary" id="bind">连接此任务</button></div></dialog>
 <dialog id="setup-dialog"><h2>扫码连接飞书</h2><p>确认后将创建“Duo助手”，绑定扫码账号，初始化“我的任务、整理知识”菜单并提交发布，最后发送一条绑定通知。已有机器人不会被修改。</p><p>请在飞书官方页面审阅并确认权限；企业审批可能影响发布。</p><div id="setup-display"><p>点击下方按钮生成二维码。</p></div><div class="dialog-footer"><button id="setup-close">关闭</button><button id="setup-cancel" class="hidden">取消等待</button><button class="primary" id="setup-start">生成飞书二维码</button></div></dialog><dialog id="knowledge-dialog"><form id="knowledge-form"><h2>任务知识</h2><label for="knowledge-title">标题</label><input id="knowledge-title" maxlength="120" placeholder="例如：继电器上电顺序与验证方法"><label for="knowledge-state">状态</label><select id="knowledge-state"><option value="observed">待验证</option><option value="verified">已验证</option><option value="stale">已过时</option></select><label for="knowledge-body">内容</label><textarea id="knowledge-body" rows="12" maxlength="200000" aria-label="知识内容" placeholder="结论、命令、解决办法和验证结果…"></textarea><p id="knowledge-error" class="error"></p><div class="dialog-footer"><button type="button" id="knowledge-cancel">取消</button><button class="primary" id="knowledge-save">保存知识</button></div></form></dialog>`;
    installTools();
    installWorkspace();
    installConversationFilter();
    installSettingsSections();
    installEngineSettings();
    installProductivity();
    installTerminal();
    installExecution();
    installDiscovery();
    installLayout();
    installEnvironmentDiscovery();
    installWorkflow();
    installCodexApprovals();
    installStickyBoard();
    installAppearance();
    installPanelLayout();
    installUpdates();
    button('new-task').onclick = showCreate;
    button('empty-new').onclick = showCreate;
    button('menu').onclick = ()=>element('sidebar').classList.toggle('open');
    element('root').querySelectorAll('[data-close]').forEach((b)=>b.onclick = ()=>element(b.dataset.close).close());
    button('logout').onclick = async ()=>{
        if (!mayLeave()) return;
        const epoch = shellEpoch;
        try {
            await api('logout', 'POST', {});
            if (shellCurrent(epoch)) showLogin();
        } catch (e) {
            if (shellCurrent(epoch)) notify(e.message);
        }
    };
    button('sidebar-close').onclick = ()=>element('sidebar').classList.remove('open');
    button('settings-open').onclick = openSettings;
    button('chat-tab').onclick = ()=>switchTab('chat');
    button('note-tab').onclick = ()=>switchTab('note');
    input('create-environment').onchange = ()=>void loadCreateEnvironment();
    button('reload-models').onclick = ()=>void loadCreateModels(false, true);
    element('create-form').onsubmit = createTask;
    element('composer').onsubmit = (e)=>{
        e.preventDefault();
        void send(input('message').value, true);
    };
    input('message').oninput = ()=>{
        drafts.set(chosen, input('message').value);
        renderWorkflow();
    };
    input('message').onkeydown = (e)=>{
        if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
            e.preventDefault();
            element('composer').requestSubmit();
        }
    };
    button('knowledge-new').onclick = ()=>editKnowledge(null);
    button('export-note').onclick = ()=>{
        location.href = '/api/tasks/' + chosen + '/knowledge?download=1';
    };
    button('save-draft').onclick = ()=>void saveRunKnowledge();
    button('summarize').onclick = async ()=>{
        if (detail?.task.engine === 'deepseek-harness') {
            notify(harnessKnowledgeHint);
            return;
        }
        switchTab('note');
        button('summarize').disabled = true;
        try {
            await api('tasks/' + chosen + '/summarize', 'POST', {});
            notify('正在整理，完成后会显示草稿');
            await poll();
        } catch (e) {
            notify(e.message);
        } finally{
            if (button('summarize')) button('summarize').disabled = detail?.task.engine === 'deepseek-harness';
        }
    };
    button('adopt-draft').onclick = ()=>editKnowledgeFromRun();
    element('knowledge-filter').querySelectorAll('button').forEach((b)=>b.onclick = ()=>{
            knowledgeFilter = b.dataset.knowledgeFilter;
            renderKnowledgeList();
        });
    element('knowledge-form').onsubmit = saveKnowledge;
    button('knowledge-cancel').onclick = ()=>element('knowledge-dialog').close();
    element('knowledge-dialog').addEventListener('cancel', (e)=>{
        e.preventDefault();
        element('knowledge-dialog').close();
    });
    element('conversation').addEventListener('click', (e)=>{
        const b = e.target.closest('[data-knowledge-run]');
        if (b) void saveRunKnowledge(b.dataset.knowledgeRun);
    });
    input('environment-picker').onchange = ()=>{
        storeEnvironmentEditor();
        editingID = input('environment-picker').value;
        loadEnvironmentEditor();
    };
    input('environment-type').onchange = showEnvironmentFields;
    button('add-wsl').onclick = ()=>addEnvironment('wsl');
    button('add-windows').onclick = ()=>addEnvironment('windows');
    button('add-ssh').onclick = ()=>addEnvironment('ssh');
    button('remove-environment').onclick = removeEnvironment;
    element('settings-form').onsubmit = saveSettings;
    button('check-codex').onclick = async ()=>{
        button('check-codex').disabled = true;
        try {
            const r = await api('check', 'POST', {
                environment_id: editingID
            });
            element('check-result').textContent = (r.ok ? '检查通过\n' : '检查失败\n') + r.output;
        } catch (e) {
            element('check-result').textContent = e.message;
        } finally{
            button('check-codex').disabled = false;
        }
    };
    button('pair-code').onclick = async ()=>{
        try {
            const r = await api('feishu/pair', 'POST', {});
            element('pair-result').textContent = '向机器人发送：\n/配对 ' + r.code;
        } catch (e) {
            notify(e.message);
        }
    };
    button('setup-feishu').onclick = ()=>openSetup('');
    button('bind-setup').onclick = ()=>openSetup(chosen);
    button('setup-start').onclick = startSetup;
    button('setup-close').onclick = ()=>element('setup-dialog').close();
    button('setup-cancel').onclick = cancelSetup;
    button('bind-open').onclick = openBinding;
    button('bind').onclick = ()=>setBinding(true);
    button('unbind').onclick = ()=>setBinding(false);
}
function mayLeave() {
    if (createSubmitting) {
        notify('正在创建任务，请等待提交完成。你的要求和附件会保留。');
        return false;
    }
    return true;
}
function setCreatePageVisible(visible) {
    element('create-page')?.classList.toggle('hidden', !visible);
    element('workspace')?.classList.toggle('create-mode', visible);
    if (visible) element('session-banner')?.classList.add('hidden');
    renderCodexApprovals(visible ? null : detail);
}
function renderList() {
    if (!element('task-list')) return;
    const query = input('search').value.trim(), archived = tasks.filter((t)=>t.archived).length;
    button('tasks-active').textContent = '任务 · ' + (tasks.length - archived);
    button('tasks-archived').textContent = '已归档 · ' + archived;
    for (const view of [
        'active',
        'archived'
    ])button('tasks-' + view).classList.toggle('selected', taskView === view && !query);
    element('search-summary').classList.toggle('hidden', !query);
    button('search-clear').classList.toggle('hidden', !query);
    let html = '';
    if (query) {
        element('search-summary').textContent = searchError || (searchPending ? '正在搜索全部任务…' : `共 ${searchHits?.length || 0} 条匹配 · 含归档` + (searchLimited ? ' · 最多 60 条，请缩小关键词' : ''));
        html = (searchHits || []).map((h, i)=>`<button class="task search-hit" data-hit="${i}"><small>${({
                task: '任务',
                message: '对话',
                knowledge: '知识',
                scratch: '待办'
            })[h.kind]}${h.archived ? ' · 已归档' : ''}</small><strong>${escapeHTML(h.title)}</strong><span>${escapeHTML(h.snippet)}</span></button>`).join('') || '<p class="muted">' + (searchPending ? '读取中…' : '没有匹配的内容。') + '</p>';
    } else html = workspaceTaskList(tasks.filter((t)=>t.archived === (taskView === 'archived'))) || '<p class="muted">' + (taskView === 'archived' ? '没有已归档的任务。' : '还没有任务。') + '</p>';
    if (html === lastList) return;
    lastList = html;
    element('task-list').innerHTML = html;
    element('task-list').querySelectorAll('[data-task]').forEach((b)=>b.onclick = ()=>void choose(b.dataset.task));
    element('task-list').querySelectorAll('[data-hit]').forEach((b)=>b.onclick = ()=>{
            const hit = searchHits?.[Number(b.dataset.hit)];
            if (hit) void openSearchHit(hit);
        });
}
async function choose(id, view = 'chat') {
    if (!mayLeave()) return;
    creatingTask = false;
    createReturnTask = '';
    setCreatePageVisible(false);
    chosen = id;
    const token = ++selection;
    detail = null;
    dirty = false;
    sequence = 0;
    resetConversation();
    resetCodexApprovals();
    void loadStickyBoard();
    taskContext = [];
    void loadTaskContext(id);
    element('conversation').innerHTML = '<p id="loading" class="muted">正在读取任务记录…</p>';
    input('message').value = drafts.get(id) || '';
    knowledgeItems = [];
    knowledgeEditing = null;
    lastKnowledge = '';
    renderKnowledgeList();
    for (const key of [
        'tabs',
        'task-actions',
        'composer-wrap',
        'conversation-filter'
    ])element(key).classList.remove('hidden');
    element('sidebar').classList.remove('open');
    switchTab('chat');
    renderList();
    history.replaceState(null, '', '/?task=' + id);
    try {
        const [d, k] = await Promise.all([
            api('tasks/' + id),
            api('tasks/' + id + '/knowledge')
        ]);
        if (token !== selection) return;
        detail = d;
        storeKnowledge(k || []);
        element('conversation').innerHTML = '';
        appendEvents(d.events);
        renderTask();
        switchTab(view);
    } catch (e) {
        if (token === selection) notify(e.message);
    }
}
function switchTab(tab) {
    if (creatingTask) {
        creatingTask = false;
        createReturnTask = '';
    }
    setCreatePageVisible(false);
    toolsTab = tab;
    const open = tab !== 'chat';
    if (chosen) {
        const query = new URLSearchParams({
            task: chosen
        });
        if (tab === 'note') query.set('view', 'note');
        history.replaceState(null, '', '/?' + query);
    }
    element('workspace').classList.toggle('tool-open', open);
    element('workspace').classList.toggle('tool-full', open && toolFullscreen);
    element('tool-dock').classList.toggle('hidden', !open);
    element('conversation').classList.remove('hidden');
    element('composer-wrap').classList.toggle('hidden', !chosen);
    for (const [key, panel] of [
        [
            'note',
            'notebook'
        ],
        [
            'scratch',
            'scratch-panel'
        ],
        [
            'hardware',
            'hardware-panel'
        ],
        [
            'files',
            'files-panel'
        ],
        [
            'terminal',
            'terminal-panel'
        ]
    ]){
        element(panel).classList.toggle('hidden', tab !== key);
        button(key + '-tab').classList.toggle('selected', tab === key);
        button(key + '-tab').setAttribute('aria-pressed', String(tab === key));
    }
    button('chat-tab').classList.toggle('selected', !open);
    element('tool-title').textContent = ({
        note: '任务知识',
        scratch: '待办',
        hardware: '硬件调试',
        files: '文件与改动',
        terminal: '终端'
    })[tab] || '任务工具';
    renderTerminal();
    renderHardwareState();
    if (tab !== 'hardware') cancelHardwareInput();
    if (tab === 'files') void loadFiles();
    if (tab === 'scratch') void loadScratch();
    if (tab === 'hardware') void loadDevices();
}
let knowledgeFilter = 'all';
function knowledgeStateLabel(v) {
    return v === 'verified' ? '已验证' : v === 'stale' ? '已过时' : '待验证';
}
function knowledgeSourceLabel(v) {
    return ({
        run: '来自执行记录',
        feishu: '来自飞书',
        organize: '整理生成',
        migrated: '历史任务知识'
    })[v] || '手工记录';
}
function knowledgeStampOf(list) {
    return list.map((k)=>k.id + ':' + k.revision).join(',');
}
function storeKnowledge(list) {
    const stamp = knowledgeStampOf(list);
    if (stamp === lastKnowledge) return;
    lastKnowledge = stamp;
    knowledgeItems = list;
    renderKnowledgeList();
    applyConversationFilter();
}
function knowledgeForRun(runId) {
    return knowledgeItems.find((k)=>k.run_id === runId) || null;
}
function latestKnowledgeRun() {
    return detail?.runs.filter((r)=>r.kind === 'knowledge' && r.status === 'done' && r.result).at(-1);
}
function renderKnowledgeList() {
    if (!element('knowledge-list')) return;
    element('knowledge-filter').querySelectorAll('button').forEach((b)=>{
        const on = b.dataset.knowledgeFilter === knowledgeFilter;
        b.classList.toggle('selected', on);
        b.setAttribute('aria-pressed', String(on));
    });
    const verified = knowledgeItems.filter((k)=>k.status === 'verified').length;
    element('note-status').textContent = knowledgeItems.length ? `共 ${knowledgeItems.length} 条知识 · 已验证 ${verified} 条` : '一份任务，一份可复用的记录。';
    const items = knowledgeFilter === 'all' ? knowledgeItems : knowledgeItems.filter((k)=>k.status === knowledgeFilter);
    element('knowledge-list').innerHTML = items.map((k)=>`<article class="knowledge-card" data-knowledge="${escapeHTML(k.id)}" data-state="${escapeHTML(k.status)}"><header><button class="knowledge-title" data-knowledge-edit="${escapeHTML(k.id)}" title="编辑这条知识">${escapeHTML(k.title)}</button><span class="knowledge-state">${knowledgeStateLabel(k.status)}</span></header><div class="knowledge-body">${markdown(k.content)}</div><footer><span>${knowledgeSourceLabel(k.source)} · ${new Date(k.updated).toLocaleString('zh-CN', {
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            hour12: false
        })}</span><span class="knowledge-actions"><button type="button" data-knowledge-state="${escapeHTML(k.id)}" title="切换验证状态">${k.status === 'verified' ? '标为待验证' : '标为已验证'}</button><button type="button" data-knowledge-use="${escapeHTML(k.id)}" title="把内容和标题带入输入框">↗</button><button type="button" data-knowledge-delete="${escapeHTML(k.id)}" title="删除这条知识">×</button></span></footer></article>`).join('') || '<p class="muted">还没有沉淀知识。跑完一轮后点对话里的“沉淀为知识”，或点“整理任务知识”让 AI 总结这几轮。</p>';
    element('knowledge-list').querySelectorAll('[data-knowledge-edit]').forEach((b)=>b.onclick = ()=>editKnowledge(b.dataset.knowledgeEdit));
    element('knowledge-list').querySelectorAll('[data-knowledge-state]').forEach((b)=>b.onclick = ()=>void toggleKnowledgeState(knowledgeItems.find((k)=>k.id === b.dataset.knowledgeState)));
    element('knowledge-list').querySelectorAll('[data-knowledge-use]').forEach((b)=>b.onclick = ()=>useKnowledge(knowledgeItems.find((k)=>k.id === b.dataset.knowledgeUse)));
    element('knowledge-list').querySelectorAll('[data-knowledge-delete]').forEach((b)=>b.onclick = ()=>void deleteKnowledge(knowledgeItems.find((k)=>k.id === b.dataset.knowledgeDelete)));
}
function harnessSessionClosed(value = detail) {
    return value?.task.engine === 'deepseek-harness' && value.runtime?.can_continue === false;
}
function renderSessionBanner() {
    const banner = element('session-banner');
    if (!detail || detail.task.archived || !detail.task.session && !detail.runs.length) {
        banner.classList.add('hidden');
        banner.innerHTML = '';
        return;
    }
    const started = detail.session_started ? new Date(detail.session_started).toLocaleString('zh-CN', {
        month: 'numeric',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false
    }) : '';
    const foreign = taskContext.length ? `<span class="session-warning" title="${escapeHTML(taskContext.map((f)=>f.label + ' · ' + f.name).join('\n'))}">工作目录有外部 AI 指令：${escapeHTML(taskContext.map((f)=>f.name).join('、'))}</span>` : '';
    const harness = detail.task.engine === 'deepseek-harness', closed = harnessSessionClosed(), busy = detail.runs.some((r)=>r.status === 'running' || r.status === 'queued') || detail.runtime?.state === 'busy';
    const title = harness ? closed ? '运行会话已结束' : detail.runtime?.state === 'new' ? '空白会话已就绪' : detail.runtime?.state === 'busy' ? 'Harness 正在执行' : 'Harness 连续对话' : detail.task.session ? `正在续用${started ? ' ' + started + ' 开始的' : ''}历史会话` : '空白会话已就绪';
    const explanation = harness ? detail.runtime?.reason || harnessSessionHint : '聊天记录保留在当前任务中。新建空白会话后，AI 不会自动记得之前的对话。';
    banner.dataset.state = closed ? 'closed' : busy ? 'busy' : 'live';
    const html = `<span class="session-text"><strong>${escapeHTML(title)}</strong><span>${escapeHTML(explanation)}</span></span>${foreign}${detail.task.session ? `<button type="button" id="session-reset" class="subtle"${busy || sessionResetTask === detail.task.id ? ' disabled' : ''}>${sessionResetTask === detail.task.id ? '正在新建…' : '新建空白会话'}</button>` : ''}`;
    if (banner.innerHTML !== html) banner.innerHTML = html;
    banner.classList.remove('hidden');
    if (button('session-reset')) button('session-reset').onclick = ()=>void resetSession();
}
async function resetSession() {
    if (!detail || !detail.task.session || sessionResetTask) return;
    if (detail.runs.some((r)=>r.status === 'running' || r.status === 'queued')) {
        notify('请先停止执行并取消排队，再新建空白会话。');
        return;
    }
    if (!confirm('新建空白会话？任务记录和任务知识都会保留，未发送的草稿也保留。下一轮 AI 不会自动记得之前的对话，这不是恢复旧会话。')) return;
    const id = detail.task.id, token = ++selection;
    sessionResetTask = id;
    renderTask();
    try {
        const task = await api('tasks/' + id + '/session/reset', 'POST', {});
        if (chosen !== id || selection !== token || !detail) return;
        detail.task = task;
        detail.session_started = 0;
        if (task.engine === 'deepseek-harness') detail.runtime = {
            state: 'new',
            can_continue: true,
            reason: '下一条要求将开启新的原生会话；旧记录不自动带入 AI 上下文。'
        };
        notify('空白会话已就绪；记录、知识和未发送的草稿均保留。');
    } catch (e) {
        if (chosen === id) notify(e.message);
    } finally{
        sessionResetTask = '';
        if (chosen === id && selection === token) renderTask();
    }
}
async function loadTaskContext(id) {
    const token = selection;
    taskContext = [];
    try {
        const r = await api('tasks/' + id + '/context');
        if (token !== selection) return;
        taskContext = r.files || [];
    } catch  {}
    if (token === selection && detail) renderSessionBanner();
}
function renderTask() {
    if (!detail) return;
    renderSessionBanner();
    const t = detail.task;
    element('task-title').textContent = t.title;
    element('task-workspace').textContent = (t.environment?.name ? t.environment.name + ' · ' : '') + t.workspace;
    element('task-workspace').title = element('task-workspace').textContent;
    const modelLabel = [
        taskEngineName(t.engine),
        t.model || '默认模型',
        effortLabels[t.reasoning_effort] || ''
    ].filter(Boolean).join(' · ');
    element('task-model-label').textContent = modelLabel;
    element('task-model-button').title = modelLabel;
    const active = detail.runs.some((r)=>[
            'running',
            'queued'
        ].includes(r.status));
    const queued = detail.runs.filter((r)=>r.status === 'queued').length;
    const latest = detail.runs.at(-1);
    element('run-status').textContent = detail.approvals?.length ? '等待你处理 ' + detail.approvals.length + ' 个 Codex 请求' : active ? '正在执行' + (queued ? ' · ' + queued + ' 条要求排队中' : '') : latest?.error || names[t.status] || t.status;
    element('run-status').classList.toggle('error', !active && !!latest?.error);
    button('stop').classList.toggle('hidden', !active);
    button('send').disabled = sending;
    input('message').placeholder = active ? '追加要求将排队，也可以停止当前执行…' : '下一步，要做什么？';
    const knowledge = latestKnowledgeRun(), saved = knowledge ? knowledgeForRun(knowledge.id) : null, pending = !!knowledge && (!saved || saved.content !== knowledge.result);
    element('draft-banner').classList.toggle('hidden', !pending);
    element('draft-preview').classList.toggle('hidden', !pending);
    button('note-tab').textContent = '任务知识' + (knowledgeItems.length ? ' · ' + knowledgeItems.length : '');
    button('summarize').disabled = active;
    if (knowledge && pending) {
        const preview = element('draft-content');
        if (preview.dataset.run !== knowledge.id) {
            preview.innerHTML = markdown(knowledge.result);
            preview.dataset.run = knowledge.id;
        }
        element('draft-label').textContent = saved ? '这轮执行的总结已保存，草稿可编辑合并' : '执行总结 · 未保存';
        button('save-draft').disabled = !!saved;
    }
    input('message').disabled = t.archived;
    button('send').disabled = sending || t.archived;
    button('summarize').disabled = active || t.archived;
    if (t.archived) element('run-status').textContent = '任务已归档，记录保留；恢复后可以继续执行。';
    if (harnessSessionClosed()) {
        element('run-status').textContent = '当前运行会话不可继续，请先新建空白会话。已输入的要求会保留。';
        element('run-status').classList.add('error');
        input('message').placeholder = '可先写下要求，新建空白会话后再发送…';
    }
    const harness = t.engine === 'deepseek-harness';
    button('summarize').disabled = active || t.archived || harness;
    button('summarize').title = harness ? harnessKnowledgeHint : '根据任务记录生成知识草稿';
    let knowledgeHint = element('harness-knowledge-hint');
    if (!knowledgeHint) {
        knowledgeHint = document.createElement('p');
        knowledgeHint.id = 'harness-knowledge-hint';
        knowledgeHint.className = 'muted';
        element('notebook').querySelector('.note-head').after(knowledgeHint);
    }
    knowledgeHint.textContent = harnessKnowledgeHint;
    knowledgeHint.classList.toggle('hidden', !harness);
    renderWorkflow();
    renderCodexApprovals(detail);
    renderTerminal();
    const i = tasks.findIndex((x)=>x.id === t.id);
    if (i >= 0) tasks[i] = t;
    renderList();
}
function appendEvents(events) {
    const container = element('conversation'), nearBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 100;
    for (const ev of events){
        if (ev.seq <= sequence) continue;
        sequence = ev.seq;
        const node = document.createElement('div');
        node.dataset.event = String(ev.seq);
        if (ev.kind === 'user' || ev.kind === 'assistant') {
            node.className = 'message ' + ev.kind;
            node.innerHTML = '<div class="label">' + (ev.kind === 'user' ? '你' : taskEngineName(detail?.task.engine)) + '</div><div class="content">' + (ev.kind === 'user' ? escapeHTML(ev.text) : markdown(ev.text)) + '</div>';
        } else if (ev.kind === 'tool' || ev.kind === 'log') {
            node.className = 'log';
            node.innerHTML = '<details><summary>' + escapeHTML(ev.text.split('\n')[0].slice(0, 200)) + '</summary><pre>' + escapeHTML(ev.text) + '</pre></details>';
        } else {
            node.className = 'progress' + (ev.kind === 'error' ? ' error' : '');
            node.textContent = ev.kind === 'status' ? '本轮执行 · ' + (names[ev.text.trim()] || ev.text) : ev.text;
        }
        container.append(node);
        conversationItems.push({
            event: ev,
            node
        });
    }
    applyConversationFilter();
    if (nearBottom) container.scrollTop = container.scrollHeight;
}
async function poll() {
    if (!authenticated || polling || document.hidden || sessionResetTask === chosen && !!chosen) return;
    polling = true;
    const epoch = shellEpoch, signal = shellController.signal, id = chosen, token = selection, approvalRevision = codexApprovalRevision;
    try {
        if (Date.now() - refreshList > 4000) {
            const loaded = await api('tasks', 'GET', undefined, signal);
            if (!shellCurrent(epoch)) return;
            tasks = loaded;
            refreshList = Date.now();
            renderList();
        }
        if (id) {
            const [d, k] = await Promise.all([
                api('tasks/' + id + '?after=' + sequence, 'GET', undefined, signal),
                api('tasks/' + id + '/knowledge', 'GET', undefined, signal)
            ]);
            if (!shellCurrent(epoch) || token !== selection || approvalRevision !== codexApprovalRevision) return;
            detail = d;
            if (k) storeKnowledge(k);
            appendEvents(d.events);
            renderTask();
        }
        if (element('connection')) element('connection').textContent = '本机服务已连接';
    } catch (e) {
        if (shellCurrent(epoch) && element('connection')) element('connection').textContent = '连接中断，正在重试';
    } finally{
        if (shellCurrent(epoch)) polling = false;
    }
}
async function showCreate() {
    if (!mayLeave()) return;
    const epoch = shellEpoch;
    try {
        const loaded = await api('settings', 'GET', undefined, shellController.signal);
        if (!shellCurrent(epoch)) return;
        settings = loaded;
        if (!creatingTask) createReturnTask = chosen;
        creatingTask = true;
        chosen = '';
        detail = null;
        selection++;
        createFiles = [];
        input('create-input').value = '';
        input('create-files').value = '';
        input('create-error').textContent = '';
        element('create-environment').innerHTML = settings.config.environments.map((e)=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)} · ${escapeHTML(e.type.toUpperCase())}</option>`).join('');
        input('create-environment').value = settings.config.default_environment;
        setCreatePermission('auto');
        renderCreateFiles();
        setCreateSubmitState('idle');
        setCreatePageVisible(true);
        element('sidebar').classList.remove('open');
        for (const id of [
            'tabs',
            'task-actions',
            'conversation',
            'composer-wrap'
        ])element(id).classList.add('hidden');
        element('workspace').classList.remove('tool-open', 'tool-full');
        element('tool-dock').classList.add('hidden');
        element('task-title').textContent = '新建任务';
        element('task-workspace').textContent = '选择环境和工作目录';
        element('task-workspace').title = '';
        history.replaceState(null, '', '/');
        renderList();
        input('create-input').focus();
        await loadCreateEnvironment();
    } catch (e) {
        if (shellCurrent(epoch)) notify(e.message);
    }
}
function createTaskTitle(text) {
    const line = text.split(/\r?\n/).map((v)=>v.trim()).find(Boolean) || '新的任务';
    return line.length > 180 ? line.slice(0, 180) : line;
}
function cancelCreate() {
    if (!mayLeave()) return;
    const id = createReturnTask;
    creatingTask = false;
    createReturnTask = '';
    createFiles = [];
    input('create-files').value = '';
    renderCreateFiles();
    setCreatePageVisible(false);
    if (id && tasks.some((t)=>t.id === id)) {
        void choose(id);
        return;
    }
    chosen = '';
    detail = null;
    selection++;
    resetConversation();
    for (const name of [
        'tabs',
        'task-actions',
        'composer-wrap'
    ])element(name).classList.add('hidden');
    element('conversation').classList.remove('hidden');
    element('task-title').textContent = '今天，从哪件事开始？';
    element('task-workspace').textContent = 'Windows · WSL · SSH';
    history.replaceState(null, '', '/');
    switchTab('chat');
    renderList();
}
async function loadCreateEnvironment(keepWorkspace = false) {
    if (createSubmitting) return;
    const env = settings.config.environments.find((e)=>e.id === input('create-environment').value);
    if (!env) return;
    if (!keepWorkspace) input('create-engine').value = env.default_engine || 'codex';
    setCreatePermission(createPermission);
    input('create-effort').value = '';
    const directories = [
        ...new Set([
            ...env.workspaces,
            ...tasks.filter((t)=>t.environment?.id === env.id).map((t)=>t.workspace)
        ])
    ];
    const workspaceOptions = element('workspace-options');
    if (workspaceOptions) workspaceOptions.innerHTML = directories.map((p)=>`<option value="${escapeHTML(p)}"></option>`).join('');
    if (!keepWorkspace) input('create-workspace').value = env.workspaces[0] || '';
    setCreateSubmitState('idle');
    await loadCreateModels(true);
}
async function createTask(e) {
    e.preventDefault();
    if (createSubmitting || button('create-submit').disabled || !creatingTask) return;
    const text = input('create-input').value, files = [
        ...createFiles
    ];
    if (!text.trim() && !files.length) {
        element('create-error').textContent = '请先写下任务要求，或添加附件。';
        input('create-input').focus();
        return;
    }
    const epoch = shellEpoch;
    createSubmitting = true;
    modelRequest++;
    setCreateSubmitState('starting');
    let created = '', uploadedCount = 0;
    try {
        validateEngineAttachments(input('create-engine').value, files.length);
        validateAttachmentFiles(files);
        const mode = modeForPermission(createPermission, input('create-engine').value);
        if (!mode || !modeSupportsEngine(mode, input('create-engine').value)) throw new Error('所选审批模式暂不可用，请刷新工作台或重新选择。');
        const r = await api('tasks', 'POST', {
            engine: input('create-engine').value,
            reasoning_effort: input('create-effort').value,
            environment_id: input('create-environment').value,
            title: createTaskTitle(text),
            workspace: input('create-workspace').value,
            model: input('create-model').value === '__custom__' ? input('custom-model').value.trim() : input('create-model').value,
            mode_id: mode.id
        });
        created = r.task.id;
        drafts.set(created, text);
        attachmentDrafts.set(created, []);
        if (!shellCurrent(epoch)) {
            if (files.length) pendingUploadFiles.set(created, files);
            return;
        }
        tasks.unshift(r.task);
        for (const file of files){
            const attachment = await uploadTaskFile(created, file);
            attachmentDrafts.get(created).push(attachment);
            uploadedCount++;
            if (!shellCurrent(epoch)) {
                if (uploadedCount < files.length) pendingUploadFiles.set(created, files.slice(uploadedCount));
                return;
            }
        }
        if (text.trim() || files.length) {
            await api('tasks/' + created + '/messages', 'POST', {
                content: text.trim() || '请查看这些附件。',
                mode_id: mode.id,
                attachment_ids: attachmentDrafts.get(created).map((f)=>f.id)
            });
            drafts.delete(created);
            attachmentDrafts.delete(created);
        }
        if (!shellCurrent(epoch)) return;
        dirty = false;
        creatingTask = false;
        createReturnTask = '';
        createFiles = [];
        input('create-files').value = '';
        renderCreateFiles();
        setCreatePageVisible(false);
        createSubmitting = false;
        setCreateSubmitState('idle');
        await choose(created);
    } catch (error) {
        if (created && uploadedCount < files.length) pendingUploadFiles.set(created, files.slice(uploadedCount));
        if (!shellCurrent(epoch)) return;
        if (created) {
            dirty = false;
            creatingTask = false;
            createReturnTask = '';
            createFiles = [];
            input('create-files').value = '';
            renderCreateFiles();
            setCreatePageVisible(false);
            createSubmitting = false;
            setCreateSubmitState('idle');
            await choose(created);
            notify('任务已创建，提交未完成；要求和未上传附件已保留，请检查记录后再发送：' + error.message);
        } else {
            element('create-error').textContent = error.message;
        }
    } finally{
        if (shellCurrent(epoch)) {
            createSubmitting = false;
            setCreateSubmitState('idle');
        }
    }
}
async function send(text, clear) {
    if (harnessSessionClosed() || sessionResetTask === chosen && !!chosen) {
        notify('请先新建空白会话，再发送要求；输入内容会保留。');
        return;
    }
    if (pendingUploadFiles.get(chosen)?.length) {
        notify('请先重试上传或移除待上传附件，避免遗漏文件。');
        return;
    }
    const files = [
        ...attachmentDrafts.get(chosen) || []
    ];
    if (!chosen || !detail || sending || uploadingTasks.has(chosen) || !text.trim() && !files.length) return;
    try {
        validateEngineAttachments(detail.task.engine, files.length);
    } catch (e) {
        notify(e.message);
        return;
    }
    const epoch = shellEpoch, id = chosen, original = input('message').value, mode = selectedMessageMode();
    sending = true;
    renderTask();
    try {
        await api('tasks/' + id + '/messages', 'POST', {
            content: text.trim() || '请查看这些附件。',
            mode_id: mode,
            attachment_ids: files.map((f)=>f.id)
        });
        attachmentDrafts.set(id, (attachmentDrafts.get(id) || []).filter((f)=>!files.some((sent)=>sent.id === f.id)));
        if (clear) {
            if (drafts.get(id) === original) drafts.delete(id);
            if (shellCurrent(epoch) && chosen === id && input('message').value === original) input('message').value = '';
        }
        if (shellCurrent(epoch)) await poll();
    } catch (e) {
        if (shellCurrent(epoch)) notify(e.message);
    } finally{
        if (shellCurrent(epoch)) {
            sending = false;
            if (chosen === id) renderTask();
        }
    }
}
async function loadKnowledge() {
    const id = chosen;
    try {
        const items = await api('tasks/' + id + '/knowledge');
        if (id !== chosen) return;
        storeKnowledge(items || []);
    } catch (e) {
        notify(e.message);
    }
}
function editKnowledge(id) {
    const item = id ? knowledgeItems.find((k)=>k.id === id) || null : null;
    knowledgeEditing = item?.id || null;
    input('knowledge-title').value = item?.title || '';
    input('knowledge-body').value = item?.content || '';
    input('knowledge-state').value = item?.status || 'observed';
    element('knowledge-error').textContent = '';
    element('knowledge-dialog').showModal();
    input('knowledge-title').focus();
}
function editKnowledgeFromRun() {
    const r = latestKnowledgeRun();
    if (!r) return;
    const saved = knowledgeForRun(r.id), stamp = new Date(r.created).toLocaleString('zh-CN', {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false
    });
    knowledgeEditing = saved?.id || null;
    input('knowledge-title').value = saved?.title || '执行总结 · ' + stamp;
    input('knowledge-body').value = r.result;
    input('knowledge-state').value = saved?.status || 'observed';
    element('knowledge-error').textContent = '';
    element('knowledge-dialog').showModal();
    input('knowledge-title').focus();
}
async function saveKnowledge(e) {
    e.preventDefault();
    const item = knowledgeEditing ? knowledgeItems.find((k)=>k.id === knowledgeEditing) || null : null, id = chosen;
    button('knowledge-save').disabled = true;
    try {
        const payload = {
            title: input('knowledge-title').value.trim(),
            content: input('knowledge-body').value,
            status: input('knowledge-state').value,
            source: item?.source || 'manual',
            revision: item?.revision || 0
        };
        if (item) await api('tasks/' + id + '/knowledge/' + item.id, 'PUT', payload);
        else await api('tasks/' + id + '/knowledge', 'POST', payload);
        if (id !== chosen) return;
        element('knowledge-dialog').close();
        knowledgeEditing = null;
        await loadKnowledge();
        notify(item ? '知识已更新' : '知识已保存');
    } catch (error) {
        element('knowledge-error').textContent = error.message;
    } finally{
        if (element('knowledge-save')) button('knowledge-save').disabled = false;
    }
}
async function saveRunKnowledge(runId) {
    const r = runId ? detail?.runs.find((x)=>x.id === runId) : latestKnowledgeRun();
    if (!r) return;
    try {
        const k = await api('tasks/' + chosen + '/knowledge/from-run', 'POST', {
            run_id: r.id
        });
        await loadKnowledge();
        notify('已沉淀到任务知识：' + k.title);
    } catch (e) {
        notify(e.message);
    }
}
async function toggleKnowledgeState(k) {
    if (!k) return;
    const next = k.status === 'verified' ? 'observed' : 'verified';
    try {
        await api('tasks/' + k.task_id + '/knowledge/' + k.id, 'PUT', {
            title: k.title,
            content: k.content,
            status: next,
            source: k.source,
            revision: k.revision
        });
        await loadKnowledge();
    } catch (e) {
        notify(e.message);
    }
}
async function deleteKnowledge(k) {
    if (!k || !confirm('删除这条知识？')) return;
    try {
        await api('tasks/' + k.task_id + '/knowledge/' + k.id, 'DELETE', {
            revision: k.revision
        });
        await loadKnowledge();
        notify('知识已删除');
    } catch (e) {
        notify(e.message);
    }
}
function useKnowledge(k) {
    if (!k) return;
    bringToChat(k.title + '\n\n' + k.content);
    renderWorkflow();
    element('sidebar').classList.remove('open');
}
async function openSettings() {
    const epoch = shellEpoch;
    element('settings-saved').textContent = '';
    try {
        const loaded = await api('settings', 'GET', undefined, shellController.signal);
        if (!shellCurrent(epoch)) return;
        settings = loaded;
        const c = settings.config;
        loadAccessSettings();
        editingEnvironments = JSON.parse(JSON.stringify(c.environments));
        editingID = c.default_environment;
        environmentPickers(c.default_environment);
        loadEnvironmentEditor();
        input('feishu-id').value = c.feishu.app_id;
        input('feishu-secret').value = '';
        input('feishu-secret').placeholder = settings.secret_configured ? '已保存，留空保持不变' : '填写自建应用 App Secret';
        input('feishu-enabled').checked = c.feishu.enabled;
        updateFeishuStatus();
        element('check-result').textContent = '';
        element('settings-error').textContent = '';
        element('settings-dialog').showModal();
    } catch (e) {
        if (shellCurrent(epoch)) notify(e.message);
    }
}
function environmentPickers(defaultID) {
    const preferred = defaultID || input('default-environment').value || settings.config.default_environment;
    const options = editingEnvironments.map((e)=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)} · ${e.type.toUpperCase()}</option>`).join('');
    input('environment-picker').innerHTML = options;
    input('environment-picker').value = editingID;
    input('default-environment').innerHTML = options;
    input('default-environment').value = editingEnvironments.some((e)=>e.id === preferred) ? preferred : editingEnvironments[0].id;
    button('remove-environment').disabled = editingEnvironments.length < 2;
}
function loadEnvironmentEditor() {
    const e = editingEnvironments.find((e)=>e.id === editingID);
    if (!e) return;
    input('environment-name').value = e.name;
    input('environment-type').value = e.type;
    input('setting-distro').value = e.distro || '';
    input('setting-user').value = e.user || '';
    input('setting-host').value = e.host || '';
    input('setting-port').value = String(e.port || 22);
    input('setting-identity').value = e.identity || '';
    input('setting-claude').value = e.claude || '';
    input('setting-claude-model').value = e.claude_model || '';
    input('setting-harness').value = e.harness || (e.type === 'windows' ? 'dsh.cmd' : 'dsh');
    input('setting-harness-model').value = e.harness_model || 'deepseek-flash';
    input('setting-harness-provider').value = e.harness_provider || 'deepseek-official';
    input('setting-engine').value = e.default_engine || 'codex';
    input('setting-codex').value = e.codex;
    input('setting-model').value = e.model;
    input('setting-workspaces').value = e.workspaces.join('\n');
    element('check-result').textContent = '';
    showEnvironmentFields();
    renderConfiguredModels?.();
}
function showEnvironmentFields() {
    const type = input('environment-type').value;
    element('wsl-fields').classList.toggle('hidden', type !== 'wsl');
    element('ssh-fields').classList.toggle('hidden', type !== 'ssh');
    element('linux-user').classList.toggle('hidden', type === 'windows');
    input('setting-port').disabled = type !== 'ssh';
}
function storeEnvironmentEditor() {
    const e = editingEnvironments.find((e)=>e.id === editingID);
    if (!e) return;
    Object.assign(e, {
        name: input('environment-name').value.trim(),
        type: input('environment-type').value,
        distro: input('setting-distro').value.trim(),
        user: input('setting-user').value.trim(),
        host: input('setting-host').value.trim(),
        port: Number(input('setting-port').value) || 22,
        identity: input('setting-identity').value.trim(),
        claude: input('setting-claude').value.trim(),
        claude_model: input('setting-claude-model').value.trim(),
        harness: input('setting-harness').value.trim(),
        harness_model: input('setting-harness-model').value.trim(),
        harness_provider: input('setting-harness-provider').value.trim(),
        default_engine: input('setting-engine').value,
        codex: input('setting-codex').value.trim(),
        model: input('setting-model').value.trim(),
        workspaces: input('setting-workspaces').value.split('\n').map((s)=>s.trim()).filter(Boolean)
    });
}
function addEnvironment(type) {
    storeEnvironmentEditor();
    const id = 'env_' + Date.now().toString(36) + '_' + Math.random().toString(36).slice(2, 6);
    const e = {
        id,
        name: type === 'ssh' ? '新 SSH 环境' : type === 'wsl' ? '新 WSL 环境' : '新 Windows 环境',
        type,
        distro: '',
        user: '',
        host: '',
        port: 22,
        identity: '',
        codex: type === 'windows' ? 'codex.exe' : 'codex',
        harness: type === 'windows' ? 'dsh.cmd' : 'dsh',
        harness_model: 'deepseek-flash',
        harness_provider: 'deepseek-official',
        model: '',
        model_cache: '',
        models: [],
        workspaces: []
    };
    editingEnvironments.push(e);
    editingID = id;
    environmentPickers();
    loadEnvironmentEditor();
    input('environment-name').focus();
}
function removeEnvironment() {
    if (editingEnvironments.length < 2) return;
    if (!confirm('删除这个环境配置？已有任务仍使用各自保存的环境。')) return;
    editingEnvironments = editingEnvironments.filter((e)=>e.id !== editingID);
    editingID = editingEnvironments[0].id;
    environmentPickers();
    loadEnvironmentEditor();
}
function updateFeishuStatus() {
    element('feishu-state').textContent = '状态：' + settings.feishu_status;
    element('feishu-owner').textContent = settings.config.feishu.owner ? '已配对你的飞书账号' : '尚未配对';
    button('pair-code').disabled = !!settings.config.feishu.owner;
    button('setup-feishu').disabled = !!settings.config.feishu.app_id || settings.secret_configured;
}
async function saveSettings(e) {
    e.preventDefault();
    const epoch = shellEpoch;
    button('settings-save').disabled = true;
    element('settings-saved').textContent = '';
    storeEnvironmentEditor();
    const c = {
        ...settings.config,
        access: {
            lan: input("access-lan").value.trim(),
            tailscale: input("access-tailscale").value.trim()
        },
        environments: editingEnvironments,
        default_environment: input('default-environment').value,
        feishu: {
            enabled: input('feishu-enabled').checked,
            app_id: input('feishu-id').value.trim(),
            secret: input('feishu-secret').value.trim()
        }
    };
    try {
        const saved = await api('settings', 'PUT', c);
        if (!shellCurrent(epoch)) return;
        settings = saved;
        invalidateModelCatalogs();
        input('feishu-secret').value = '';
        updateFeishuStatus();
        element('settings-error').textContent = '';
        element('settings-saved').textContent = '设置已保存';
        notify('设置已保存');
    } catch (e) {
        if (shellCurrent(epoch)) element('settings-error').textContent = e.message;
    } finally{
        if (shellCurrent(epoch)) button('settings-save').disabled = false;
    }
}
async function pollSettingsStatus() {
    if (!authenticated || settingsPolling || !element('settings-dialog')?.open) return;
    const epoch = shellEpoch;
    settingsPolling = true;
    try {
        const latest = await api('settings', 'GET', undefined, shellController.signal);
        if (!shellCurrent(epoch) || !element('settings-dialog')?.open) return;
        settings = {
            ...settings,
            feishu_status: latest.feishu_status,
            secret_configured: latest.secret_configured,
            chat: latest.chat,
            config: {
                ...settings.config,
                feishu: {
                    ...settings.config.feishu,
                    owner: latest.config.feishu.owner
                }
            }
        };
        updateFeishuStatus();
    } catch  {} finally{
        if (shellCurrent(epoch)) settingsPolling = false;
    }
}
async function openBinding() {
    try {
        settings = await api('settings');
        element('bind-status').textContent = settings.chat ? '已配对你的飞书私聊。' + (detail?.chat ? '当前任务已连接。' : '点击连接后可继续此任务。') : '可扫码创建机器人并绑定此任务，或到设置配对已有应用。';
        button('bind-setup').classList.toggle('hidden', !!settings.config.feishu.app_id || settings.secret_configured);
        button('bind').disabled = !settings.chat;
        button('unbind').disabled = !detail?.chat;
        element('bind-dialog').showModal();
    } catch (e) {
        notify(e.message);
    }
}
async function setBinding(bind) {
    try {
        await api('tasks/' + chosen + '/bind', bind ? 'POST' : 'DELETE', bind ? {
            chat: settings.chat
        } : {});
        element('bind-dialog').close();
        notify(bind ? '已连接到此任务' : '已断开此任务');
        await poll();
    } catch (e) {
        notify(e.message);
    }
}
window.addEventListener('beforeunload', (e)=>{
    if (dirty) {
        e.preventDefault();
        e.returnValue = '';
    }
});
document.addEventListener('visibilitychange', ()=>{
    if (!document.hidden) void poll();
});
setInterval(()=>void poll(), 1000);
setInterval(()=>void pollSettingsStatus(), 5000);
const initialShellEpoch = shellEpoch;
void boot().catch((e)=>{
    if (!shellCurrent(initialShellEpoch)) return;
    element('root').innerHTML = '<div class="empty"><h2>暂时无法连接工作台</h2><p>' + escapeHTML(e.message) + '</p><button id="retry">重新连接</button></div>';
    button('retry').onclick = ()=>location.reload();
});
let setupID = sessionStorage.getItem('feishu-setup') || '', setupTask = '', setupPolling = false, setupApplied = '';
function openSetup(task) {
    setupTask = task;
    element('bind-dialog').close();
    element('settings-dialog').close();
    element('setup-dialog').showModal();
    if (setupID) void pollSetup();
}
function renderSetup(s) {
    const busy = [
        'starting',
        'scanning',
        'configuring'
    ].includes(s.phase);
    element('setup-display').innerHTML = `<p role="status">${escapeHTML(s.message)}</p>${s.qr ? `<img class="feishu-qr" src="${escapeHTML(s.qr)}" alt="使用飞书扫描此二维码创建机器人"><p>有效至 ${new Date(s.expires).toLocaleTimeString()}</p><a href="${escapeHTML(s.url || '')}" target="_blank" rel="noreferrer noopener">在当前设备打开飞书授权</a>` : ''}<p>菜单：${escapeHTML(s.menu)}</p><p>连接：${escapeHTML(s.connection)}</p>`;
    button('setup-start').classList.toggle('hidden', busy || s.phase === 'complete' || s.phase === 'partial');
    button('setup-start').textContent = '重新生成二维码';
    button('setup-cancel').classList.toggle('hidden', ![
        'starting',
        'scanning'
    ].includes(s.phase));
}
async function startSetup() {
    button('setup-start').disabled = true;
    try {
        const s = await api('feishu/setup', 'POST', {
            task_id: setupTask
        });
        setupID = s.id;
        sessionStorage.setItem('feishu-setup', s.id);
        renderSetup(s);
    } catch (e) {
        element('setup-display').textContent = e.message;
    } finally{
        button('setup-start').disabled = false;
    }
}
async function cancelSetup() {
    try {
        await api('feishu/setup/' + setupID + '/cancel', 'POST', {});
        await pollSetup();
    } catch (e) {
        notify(e.message);
    }
}
async function pollSetup() {
    if (!authenticated || !setupID || setupPolling || !element('setup-dialog')?.open) return;
    setupPolling = true;
    try {
        const s = await api('feishu/setup/' + setupID);
        renderSetup(s);
        if ([
            'complete',
            'partial'
        ].includes(s.phase) && setupApplied !== s.id) {
            setupApplied = s.id;
            settings = await api('settings');
            input('feishu-id').value = settings.config.feishu.app_id;
            input('feishu-secret').value = '';
            input('feishu-enabled').checked = settings.config.feishu.enabled;
            updateFeishuStatus();
            await poll();
        }
    } catch (e) {
        element('setup-display').textContent = e.message;
        button('setup-start').classList.remove('hidden');
        button('setup-cancel').classList.add('hidden');
        setupID = '';
        sessionStorage.removeItem('feishu-setup');
    } finally{
        setupPolling = false;
    }
}
setInterval(()=>void pollSetup(), 2000);
const sidebarLimits = {
    min: 180,
    max: 420
};
const toolLimits = {
    min: 22,
    max: 75
};
const stickyLimits = {
    min: 80,
    max: 900
};
const hardwareLimits = {
    min: 96,
    max: 900
};
function clampPanel(value, limits) {
    return Math.max(limits.min, Math.min(limits.max, value));
}
function readPanelSize(key, limits) {
    try {
        const value = Number(localStorage.getItem(key));
        if (Number.isFinite(value) && value >= limits.min && value <= limits.max) return Math.round(value);
    } catch  {}
    return 0;
}
function writePanelSize(key, value) {
    try {
        localStorage.setItem(key, String(Math.round(value)));
    } catch  {}
}
function clearPanelSize(key) {
    try {
        localStorage.removeItem(key);
    } catch  {}
}
function persistPanelSizes() {
    try {
        localStorage.setItem('jianzuo-appearance-v1', JSON.stringify(appearance));
        localStorage.setItem('jianzuo-theme', appearance.theme);
    } catch  {}
}
function draggablePanel(handle, axis, callbacks) {
    handle.addEventListener('pointerdown', (event)=>{
        if (event.button !== 0 || event.defaultPrevented) return;
        event.preventDefault();
        try {
            handle.setPointerCapture(event.pointerId);
        } catch  {}
        handle.classList.add('dragging');
        document.body.classList.add(axis === 'x' ? 'resizing-x' : 'resizing-y');
        callbacks.start?.();
    });
    handle.addEventListener('pointermove', (event)=>{
        if (handle.hasPointerCapture(event.pointerId)) callbacks.move(event);
    });
    const finish = (event)=>{
        if (!handle.hasPointerCapture(event.pointerId)) return;
        try {
            handle.releasePointerCapture(event.pointerId);
        } catch  {}
        handle.classList.remove('dragging');
        document.body.classList.remove('resizing-x', 'resizing-y');
        callbacks.end?.();
    };
    handle.addEventListener('pointerup', finish);
    handle.addEventListener('pointercancel', finish);
    handle.addEventListener('lostpointercapture', finish);
}
function panelHandle(id, label, hint, orientation) {
    const handle = document.createElement('div');
    handle.id = id;
    handle.className = 'panel-resizer';
    handle.tabIndex = 0;
    handle.setAttribute('role', 'separator');
    handle.setAttribute('aria-orientation', orientation);
    handle.setAttribute('aria-label', label);
    handle.title = hint + ' · 方向键微调 · 双击恢复默认';
    return handle;
}
function setSidebarWidth(value, save) {
    appearance.sidebar = Math.round(clampPanel(value, sidebarLimits));
    applyAppearance(appearance);
    document.getElementById('sidebar-resizer')?.setAttribute('aria-valuenow', String(appearance.sidebar));
    input('appearance-sidebar').value = String(appearance.sidebar);
    if (save) persistPanelSizes();
}
function installSidebarResizer() {
    const sidebar = element('sidebar');
    if (!sidebar) return;
    let handle = document.getElementById('sidebar-resizer');
    if (!handle) {
        handle = panelHandle('sidebar-resizer', '调整任务侧栏宽度', '拖动调整任务侧栏宽度', 'vertical');
        sidebar.append(handle);
    }
    handle.setAttribute('aria-valuemin', String(sidebarLimits.min));
    handle.setAttribute('aria-valuemax', String(sidebarLimits.max));
    handle.setAttribute('aria-valuenow', String(Math.round(appearance.sidebar)));
    draggablePanel(handle, 'x', {
        move: (event)=>setSidebarWidth(event.clientX - sidebar.getBoundingClientRect().left, false),
        end: ()=>persistPanelSizes()
    });
    handle.ondblclick = ()=>setSidebarWidth(defaultAppearance.sidebar, true);
    handle.onkeydown = (event)=>{
        const step = event.shiftKey ? 24 : 8;
        if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
            event.preventDefault();
            setSidebarWidth(appearance.sidebar + (event.key === 'ArrowLeft' ? -step : step), true);
        } else if (event.key === 'Home' || event.key === 'Enter') {
            event.preventDefault();
            setSidebarWidth(defaultAppearance.sidebar, true);
        }
    };
}
function setToolWidth(value, save) {
    appearance.tool = Math.round(clampPanel(value, toolLimits) * 10) / 10;
    applyAppearance(appearance);
    for (const id of [
        'tool-resizer',
        'dock-left-resizer',
        'dock-right-resizer'
    ])document.getElementById(id)?.setAttribute('aria-valuenow', String(Math.round(appearance.tool)));
    const slider = document.getElementById('appearance-tool');
    if (slider) slider.value = String(appearance.tool);
    if (save) persistPanelSizes();
}
function installDockResizers() {
    const workspace = element('workspace');
    if (!workspace) return;
    for (const side of [
        'left',
        'right'
    ]){
        const zone = element('dock-' + side);
        if (!zone) continue;
        const id = 'dock-' + side + '-resizer';
        let handle = document.getElementById(id);
        if (!handle) {
            handle = panelHandle(id, '调整' + (side === 'left' ? '左侧' : '右侧') + '工具栏宽度', '拖动调整这一栏的宽度', 'vertical');
            zone.append(handle);
        }
        handle.classList.add('panel-resizer');
        handle.setAttribute('aria-valuemin', String(toolLimits.min));
        handle.setAttribute('aria-valuemax', String(toolLimits.max));
        handle.setAttribute('aria-valuenow', String(Math.round(appearance.tool)));
        draggablePanel(handle, 'x', {
            move: (event)=>{
                const rect = workspace.getBoundingClientRect();
                if (!rect.width) return;
                setToolWidth((side === 'left' ? event.clientX - rect.left : rect.right - event.clientX) / rect.width * 100, false);
            },
            end: ()=>persistPanelSizes()
        });
        handle.ondblclick = ()=>setToolWidth(defaultAppearance.tool, true);
        handle.onkeydown = (event)=>{
            const grow = side === 'left' ? 1 : -1, step = event.shiftKey ? 4 : 2;
            if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
                event.preventDefault();
                setToolWidth(appearance.tool + (event.key === 'ArrowRight' ? grow : -grow) * step, true);
            } else if (event.key === 'Home' || event.key === 'Enter') {
                event.preventDefault();
                setToolWidth(defaultAppearance.tool, true);
            }
        };
    }
    document.getElementById('tool-resizer')?.classList.add('hidden');
}
function installStickyResizer() {
    const board = element('sticky-board');
    if (!board) return;
    let handle = document.getElementById('sticky-resizer');
    if (!handle) {
        handle = panelHandle('sticky-resizer', '调整便签板高度', '上下拖动调整便签板高度', 'horizontal');
        board.prepend(handle);
    }
    const ceiling = ()=>Math.max(160, Math.round(innerHeight * 0.55));
    const apply = (value, save)=>{
        const height = Math.round(clampPanel(value, {
            min: stickyLimits.min,
            max: ceiling()
        }));
        board.dataset.sized = '1';
        board.style.setProperty('--sticky-height', height + 'px');
        handle.setAttribute('aria-valuenow', String(height));
        if (save) writePanelSize('jianzuo-sticky-height', height);
    };
    const saved = readPanelSize('jianzuo-sticky-height', stickyLimits);
    if (saved) apply(saved, false);
    let bottom = 0;
    draggablePanel(handle, 'y', {
        start: ()=>{
            bottom = board.getBoundingClientRect().bottom;
        },
        move: (event)=>apply(bottom - event.clientY, false),
        end: ()=>{
            if (board.dataset.sized) writePanelSize('jianzuo-sticky-height', Math.round(board.getBoundingClientRect().height));
        }
    });
    handle.ondblclick = ()=>{
        delete board.dataset.sized;
        board.style.removeProperty('--sticky-height');
        clearPanelSize('jianzuo-sticky-height');
        notify('便签板高度已恢复默认。');
    };
    handle.onkeydown = (event)=>{
        const step = event.shiftKey ? 40 : 16, current = Math.round(board.getBoundingClientRect().height);
        if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
            event.preventDefault();
            apply(current + (event.key === 'ArrowUp' ? step : -step), true);
        } else if (event.key === 'Home' || event.key === 'Enter') {
            event.preventDefault();
            handle.ondblclick?.(new MouseEvent('dblclick'));
        }
    };
}
function installHardwareResizer() {
    const panel = element('hardware-panel'), details = element('hardware-send-details');
    if (!panel || !details) return;
    let handle = document.getElementById('hardware-resizer');
    if (!handle) {
        handle = panelHandle('hardware-resizer', '调整发送区高度', '上下拖动调整发送区高度', 'horizontal');
        details.before(handle);
    }
    const sync = ()=>handle.classList.toggle('hidden', details.classList.contains('hidden') || !details.open);
    details.addEventListener('toggle', sync);
    const observer = new MutationObserver(sync);
    observer.observe(details, {
        attributes: true,
        attributeFilter: [
            'class'
        ]
    });
    disposeWithShell(()=>observer.disconnect());
    sync();
    const apply = (value, save)=>{
        const limit = Math.max(hardwareLimits.min, Math.round(panel.getBoundingClientRect().height - 190));
        const height = Math.round(clampPanel(value, {
            min: hardwareLimits.min,
            max: limit
        }));
        panel.dataset.sized = '1';
        panel.style.setProperty('--hardware-height', height + 'px');
        handle.setAttribute('aria-valuenow', String(height));
        if (save) writePanelSize('jianzuo-hardware-height', height);
    };
    const saved = readPanelSize('jianzuo-hardware-height', hardwareLimits);
    if (saved) apply(saved, false);
    let bottom = 0;
    draggablePanel(handle, 'y', {
        start: ()=>{
            bottom = panel.getBoundingClientRect().bottom;
        },
        move: (event)=>apply(bottom - event.clientY, false),
        end: ()=>{
            if (details.open) writePanelSize('jianzuo-hardware-height', Math.round(details.getBoundingClientRect().height));
        }
    });
    handle.ondblclick = ()=>{
        delete panel.dataset.sized;
        panel.style.removeProperty('--hardware-height');
        clearPanelSize('jianzuo-hardware-height');
        notify('发送区高度已恢复默认。');
    };
    handle.onkeydown = (event)=>{
        const step = event.shiftKey ? 40 : 16, current = Math.round(details.getBoundingClientRect().height);
        if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
            event.preventDefault();
            apply(current + (event.key === 'ArrowUp' ? step : -step), true);
        } else if (event.key === 'Home' || event.key === 'Enter') {
            event.preventDefault();
            handle.ondblclick?.(new MouseEvent('dblclick'));
        }
    };
}
function resetPanelLayout() {
    const board = element('sticky-board'), panel = element('hardware-panel');
    if (board) {
        delete board.dataset.sized;
        board.style.removeProperty('--sticky-height');
    }
    if (panel) {
        delete panel.dataset.sized;
        panel.style.removeProperty('--hardware-height');
    }
    clearPanelSize('jianzuo-sticky-height');
    clearPanelSize('jianzuo-hardware-height');
    appearance.sidebar = defaultAppearance.sidebar;
    appearance.tool = defaultAppearance.tool;
    applyAppearance(appearance);
    persistPanelSizes();
    for (const [id, value] of [
        [
            'sidebar-resizer',
            appearance.sidebar
        ],
        [
            'dock-left-resizer',
            appearance.tool
        ],
        [
            'dock-right-resizer',
            appearance.tool
        ]
    ])document.getElementById(id)?.setAttribute('aria-valuenow', String(Math.round(value)));
    input('appearance-sidebar').value = String(appearance.sidebar);
    input('appearance-tool').value = String(appearance.tool);
    dockLayout = {
        ...defaultDockLayout
    };
    applyDockLayout(true);
    notify('面板布局已恢复默认。');
}
const dockPanelIDs = {
    sticky: 'sticky-board',
    tools: 'tool-dock'
};
const dockPanelLabels = {
    sticky: '便签栏',
    tools: '工具面板'
};
const dockSideLabels = {
    sidebar: '任务栏',
    left: '左栏',
    right: '右栏',
    bottom: '对话下方'
};
const dockPanelSides = {
    sticky: [
        'sidebar',
        'left',
        'right',
        'bottom'
    ],
    tools: [
        'left',
        'right'
    ]
};
const dockZoneSides = {
    sidebar: 'sidebar',
    'dock-left': 'left',
    'dock-right': 'right',
    'dock-bottom': 'bottom'
};
const defaultDockLayout = {
    sticky: 'sidebar',
    tools: 'right'
};
let dockLayout = {
    ...defaultDockLayout
}, dockDrag = '';
const dockNarrow = matchMedia('(max-width:760px)');
function parseDockLayout(raw) {
    const value = (()=>{
        try {
            const parsed = JSON.parse(raw || '{}');
            return parsed && typeof parsed === 'object' ? parsed : {};
        } catch  {
            return {};
        }
    })();
    const out = {
        ...defaultDockLayout
    };
    for (const panel of [
        'sticky',
        'tools'
    ]){
        const side = value[panel];
        if (dockPanelSides[panel].includes(side)) out[panel] = side;
    }
    return out;
}
function loadDockLayout() {
    try {
        return parseDockLayout(localStorage.getItem('jianzuo-dock-layout-v1'));
    } catch  {
        return {
            ...defaultDockLayout
        };
    }
}
function persistDockLayout() {
    try {
        localStorage.setItem('jianzuo-dock-layout-v1', JSON.stringify(dockLayout));
    } catch  {}
}
function dockZoneID(side) {
    return side === 'sidebar' ? 'sidebar' : 'dock-' + side;
}
function effectiveDockSide(panel) {
    const side = dockLayout[panel];
    if (panel === 'sticky' && side !== 'bottom' && dockNarrow.matches) return 'bottom';
    return side;
}
function syncDocks() {
    const visible = (zone)=>Array.from(zone.children).some((node)=>{
            const child = node;
            return !child.classList.contains('panel-resizer') && !child.classList.contains('hidden') && !child.hidden;
        });
    for (const side of [
        'left',
        'right',
        'bottom'
    ]){
        const zone = element(dockZoneID(side));
        if (zone) zone.classList.toggle('hidden', !visible(zone));
    }
}
function applyDockLayout(save = false) {
    for (const panel of [
        'sticky',
        'tools'
    ]){
        const node = element(dockPanelIDs[panel]), zone = element(dockZoneID(effectiveDockSide(panel)));
        if (!node || !zone) continue;
        if (zone.id === 'sidebar') {
            const footer = element('sidebar-footer');
            footer ? zone.insertBefore(node, footer) : zone.append(node);
        } else zone.append(node);
    }
    for (const side of [
        'sidebar',
        'left',
        'right',
        'bottom'
    ]){
        const zone = element(dockZoneID(side));
        if (!zone) continue;
        const panels = [
            'sticky',
            'tools'
        ].filter((panel)=>effectiveDockSide(panel) === side);
        if (panels.length) zone.dataset.dockPanels = panels.join(' ');
        else delete zone.dataset.dockPanels;
    }
    document.getElementById('tool-resizer')?.classList.add('hidden');
    syncDocks();
    syncDockSelects();
    if (save) persistDockLayout();
}
function syncDockSelects() {
    for (const panel of [
        'sticky',
        'tools'
    ]){
        const select = document.getElementById('appearance-' + panel + '-zone');
        if (select) select.value = dockLayout[panel];
    }
}
function moveDockPanel(panel, side) {
    if (!dockPanelSides[panel].includes(side)) return;
    dockLayout = {
        ...dockLayout,
        [panel]: side
    };
    applyDockLayout(true);
    notify(dockPanelLabels[panel] + '已移到' + dockSideLabels[side] + '。');
}
function cycleDockSide(panel) {
    const sides = dockPanelSides[panel];
    moveDockPanel(panel, sides[(sides.indexOf(dockLayout[panel]) + 1) % sides.length]);
}
function dockGrip(panel, host) {
    if (!host) return null;
    let grip = host.querySelector('[data-dock-grip]');
    if (!grip) {
        grip = document.createElement('span');
        grip.className = 'dock-grip';
        grip.dataset.dockGrip = panel;
        grip.draggable = true;
        grip.tabIndex = 0;
        grip.textContent = '⠿';
        host.prepend(grip);
    }
    grip.setAttribute('aria-label', '移动' + dockPanelLabels[panel]);
    grip.title = '拖动到目标栏 · 双击按顺序换栏 · 回车依次切换';
    return grip;
}
function installDockDrag() {
    const markTargets = (panel)=>{
        for (const zone of document.querySelectorAll('[data-dock-zone]')){
            const side = dockZoneSides[zone.id];
            zone.classList.toggle('dock-target', !!panel && !!side && dockPanelSides[panel].includes(side));
        }
    };
    const hosts = [
        [
            'sticky',
            element('sticky-board')?.querySelector('header') || null
        ],
        [
            'tools',
            element('tool-dock')?.querySelector('.tool-dock-head') || null
        ]
    ];
    for (const [panel, host] of hosts){
        const grip = dockGrip(panel, host);
        if (!grip || grip.dataset.dockBound) continue;
        grip.dataset.dockBound = '1';
        grip.addEventListener('dragstart', (event)=>{
            dockDrag = panel;
            document.body.classList.add('dock-dragging');
            markTargets(panel);
            event.dataTransfer?.setData('text/plain', panel);
            if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
        });
        grip.addEventListener('dragend', ()=>{
            dockDrag = '';
            document.body.classList.remove('dock-dragging');
            markTargets('');
            document.querySelectorAll('.dock-over').forEach((zone)=>zone.classList.remove('dock-over'));
        });
        grip.addEventListener('dblclick', ()=>cycleDockSide(panel));
        grip.addEventListener('keydown', (event)=>{
            if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                cycleDockSide(panel);
            }
        });
    }
    for (const zone of document.querySelectorAll('[data-dock-zone]')){
        if (zone.dataset.dockBound) continue;
        zone.dataset.dockBound = '1';
        zone.addEventListener('dragover', (event)=>{
            const side = dockZoneSides[zone.id];
            if (!dockDrag || !side || !dockPanelSides[dockDrag].includes(side)) return;
            event.preventDefault();
            if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
            zone.classList.add('dock-over');
        });
        zone.addEventListener('dragleave', ()=>zone.classList.remove('dock-over'));
        zone.addEventListener('drop', (event)=>{
            event.preventDefault();
            zone.classList.remove('dock-over');
            const side = dockZoneSides[zone.id];
            if (dockDrag && side) moveDockPanel(dockDrag, side);
        });
    }
}
function installDockUI() {
    for (const [id, side] of Object.entries(dockZoneSides)){
        const zone = element(id);
        if (zone) zone.dataset.dockZone = side;
    }
    const form = element('appearance-form');
    if (form && !document.getElementById('appearance-sticky-zone')) {
        const section = document.createElement('div');
        section.className = 'dock-layout-section';
        const options = (panel)=>dockPanelSides[panel].map((side)=>`<option value="${side}">${dockSideLabels[side]}</option>`).join('');
        section.innerHTML = `<h3 class="section-title">面板位置</h3><div class="form-grid"><div><label for="appearance-sticky-zone">便签栏</label><select id="appearance-sticky-zone">${options('sticky')}</select></div><div><label for="appearance-tools-zone">工具面板</label><select id="appearance-tools-zone">${options('tools')}</select></div></div><p>拖动面板标题左侧的 ⠿ 手柄可以直接换栏，双击手柄按顺序切换。窄窗口沿用单栏布局。</p>`;
        const footer = form.querySelector('.dialog-footer');
        form.insertBefore(section, footer);
        for (const panel of [
            'sticky',
            'tools'
        ]){
            const select = element('appearance-' + panel + '-zone');
            select.onchange = ()=>moveDockPanel(panel, select.value);
        }
    }
    installDockDrag();
    syncDockSelects();
}
function installDockLayout() {
    dockLayout = loadDockLayout();
    installDockUI();
    applyDockLayout(false);
    for (const id of [
        'sticky-board',
        'tool-dock'
    ]){
        const node = element(id);
        const observer = new MutationObserver(()=>syncDocks());
        observer.observe(node, {
            attributes: true,
            attributeFilter: [
                'class'
            ]
        });
        disposeWithShell(()=>observer.disconnect());
    }
    listenWithShell(dockNarrow, 'change', ()=>applyDockLayout(false));
}
function installPanelLayout() {
    installSidebarResizer();
    installDockResizers();
    installStickyResizer();
    installHardwareResizer();
    installDockLayout();
}
