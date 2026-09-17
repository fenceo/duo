type LANNetwork={name:string;address:string;cidr:string;suggested:string};
type SSHEndpoint={host:string;port:number;ssh:boolean;banner:string};
type SSHScan={id:string;cidr:string;status:string;completed:number;total:number;results:SSHEndpoint[]};
let discoveryEpoch=0,discoveryID='',discoveryTimer:ReturnType<typeof setTimeout>|undefined,discoveryPick:((endpoint:SSHEndpoint)=>void)|null=null;
function installDiscovery(){
 button('add-ssh').insertAdjacentHTML('afterend','<button type="button" id="discover-environment">发现局域网 SSH</button>');
 element('device-protocol').insertAdjacentHTML('afterend','<button type="button" id="discover-device" class="subtle">发现局域网 SSH</button>');
 element('root').insertAdjacentHTML('beforeend',`<dialog id="discovery-dialog"><h2>发现局域网 SSH</h2><p class="muted">从简作所在电脑扫描，选中后填入连接地址。</p><label for="discovery-network">本机网络</label><select id="discovery-network"></select><div class="form-grid"><div><label for="discovery-cidr">扫描网段</label><input id="discovery-cidr" placeholder="192.168.50.0/24"></div><div><label for="discovery-ports">端口</label><input id="discovery-ports" value="22,2222" placeholder="22,2222"></div></div><div class="actions"><button class="primary" id="discovery-start">开始扫描</button><button id="discovery-stop" disabled>停止</button></div><p id="discovery-status" role="status"></p><div id="discovery-results" class="discovery-results"></div><p class="muted">发现服务不代表已登录；用户名、密钥或密码仍需配置。</p><p class="error" id="discovery-error"></p><div class="dialog-footer"><button id="discovery-close">关闭</button></div></dialog>`);
 button('discover-environment').onclick=()=>void openDiscovery(endpoint=>{
  if(input('environment-type').value!=='ssh')addEnvironment('ssh');
  input('setting-host').value=endpoint.host;input('setting-port').value=String(endpoint.port);
  if(input('environment-name').value==='新 SSH 环境')input('environment-name').value='SSH · '+endpoint.host;
  showEnvironmentFields();input('setting-user').focus();notify('已填入地址，补全用户名和工作目录后保存。');
 });
 button('discover-device').onclick=()=>void openDiscovery(endpoint=>{
  if(input('device-host').value!==endpoint.host||Number(input('device-port').value)!==endpoint.port)input('device-fingerprint').value='';
  input('device-protocol').value='ssh';input('device-host').value=endpoint.host;input('device-port').value=String(endpoint.port);
  if(!input('device-name').value)input('device-name').value='SSH · '+endpoint.host;
  deviceFields();input('device-user').focus();
 });
 button('discovery-start').onclick=()=>void startDiscovery();
 button('discovery-stop').onclick=async()=>{const id=discoveryID;button('discovery-stop').disabled=true;try{if(id)await api('ssh-discovery/scans/'+id,'DELETE',{})}catch(e){element('discovery-error').textContent=(e as Error).message}};
 button('discovery-close').onclick=()=>element<HTMLDialogElement>('discovery-dialog').close();
 element<HTMLDialogElement>('discovery-dialog').addEventListener('close',closeDiscovery);
}
function closeDiscovery(){
 ++discoveryEpoch;clearTimeout(discoveryTimer);const id=discoveryID;discoveryID='';discoveryPick=null;
 if(id&&authenticated)void api('ssh-discovery/scans/'+id,'DELETE',{}).catch(()=>{});
}
async function openDiscovery(pick:(endpoint:SSHEndpoint)=>void){
 closeDiscovery();discoveryPick=pick;const token=discoveryEpoch;
 element('discovery-error').textContent='';element('discovery-status').textContent='读取本机网络…';element('discovery-results').replaceChildren();
 button('discovery-start').disabled=true;button('discovery-stop').disabled=true;
 for(const id of ['discovery-network','discovery-cidr','discovery-ports'])input(id).disabled=true;
 input('discovery-network').innerHTML='';input('discovery-cidr').value='';input('discovery-ports').value='22,2222';
 element<HTMLDialogElement>('discovery-dialog').showModal();
 try{const networks=await api<LANNetwork[]>('ssh-discovery/networks');if(token!==discoveryEpoch)return;
  input('discovery-network').innerHTML=networks.map((n,i)=>`<option value="${i}">${escapeHTML(n.name)} · ${escapeHTML(n.address)}</option>`).join('');
  const chooseNetwork=()=>{input('discovery-cidr').value=networks[Number(input('discovery-network').value)]?.suggested||''};input('discovery-network').onchange=chooseNetwork;chooseNetwork();
  for(const id of ['discovery-network','discovery-cidr','discovery-ports'])input(id).disabled=!networks.length;
  button('discovery-start').disabled=!networks.length;element('discovery-status').textContent=networks.length?'选择网段后开始扫描，最多 256 个地址、4 个端口。':'未发现已连接的 IPv4 局域网。';
 }catch(e){if(token===discoveryEpoch)element('discovery-error').textContent=(e as Error).message}
}
async function startDiscovery(){
 const token=discoveryEpoch,ports=input('discovery-ports').value.split(/[,，\s]+/).filter(Boolean).map(Number);
 if(!ports.length||ports.length>4||ports.some(p=>!Number.isInteger(p)||p<1||p>65535)){element('discovery-error').textContent='请输入 1–4 个端口，范围 1–65535。';return}
 button('discovery-start').disabled=true;element('discovery-error').textContent='';element('discovery-results').replaceChildren();
 try{const scan=await api<SSHScan>('ssh-discovery/scans','POST',{cidr:input('discovery-cidr').value.trim(),ports});
  if(token!==discoveryEpoch){void api('ssh-discovery/scans/'+scan.id,'DELETE',{}).catch(()=>{});return}
  discoveryID=scan.id;renderDiscovery(scan);if(scan.status==='running')scheduleDiscovery(token,scan.id);
 }catch(e){if(token===discoveryEpoch){element('discovery-error').textContent=(e as Error).message;button('discovery-start').disabled=false}}
}
function scheduleDiscovery(token:number,id:string){
 clearTimeout(discoveryTimer);discoveryTimer=setTimeout(async()=>{try{const scan=await api<SSHScan>('ssh-discovery/scans/'+id);if(token!==discoveryEpoch||id!==discoveryID)return;renderDiscovery(scan);if(scan.status==='running')scheduleDiscovery(token,id)}catch(e){if(token===discoveryEpoch){element('discovery-error').textContent=(e as Error).message;button('discovery-start').disabled=false;button('discovery-stop').disabled=true}}},650);
}
function renderDiscovery(scan:SSHScan){
 const running=scan.status==='running';button('discovery-start').disabled=running;button('discovery-stop').disabled=!running;
 for(const id of ['discovery-network','discovery-cidr','discovery-ports'])input(id).disabled=running;
 const status:Record<string,string>={running:'扫描中',done:'扫描完成',cancelled:'已停止',timeout:'扫描超时'};
 element('discovery-status').textContent=`${status[scan.status]||scan.status} · ${scan.completed}/${scan.total} · 发现 ${scan.results.filter(r=>r.ssh).length} 个 SSH 服务`;
 element('discovery-results').innerHTML=scan.results.map((r,i)=>`<div class="discovery-row"><div><strong>${escapeHTML(r.host)}:${r.port}</strong><small>${escapeHTML(r.ssh?r.banner:'端口开放，未确认是 SSH')}</small></div><button data-ssh-result="${i}" ${r.ssh?'':'disabled'}>选择</button></div>`).join('')||(!running?'<p class="muted">此范围未发现开放端口，可以检查网段或添加设备的 SSH 端口。</p>':'');
 element('discovery-results').querySelectorAll<HTMLButtonElement>('[data-ssh-result]').forEach(b=>b.onclick=()=>{const endpoint=scan.results[Number(b.dataset.sshResult)],pick=discoveryPick;if(!endpoint?.ssh||!pick)return;element<HTMLDialogElement>('discovery-dialog').close();pick(endpoint)});
}
