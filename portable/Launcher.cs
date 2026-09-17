using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using System.Web.Script.Serialization;
using System.Windows.Forms;
using Microsoft.Win32;

static class Portable {
    public static readonly JavaScriptSerializer Json = new JavaScriptSerializer();
    public static readonly string Root = AppDomain.CurrentDomain.BaseDirectory;
    public static string Data;
    public static string Service { get { return Path.Combine(Root,"jianzuo-service.exe"); } }
    public static string Q(string s) { return "\"" + s.TrimEnd('\\') + "\""; }
    public static Dictionary<string,object> Config() { return Json.Deserialize<Dictionary<string,object>>(File.ReadAllText(Path.Combine(Data,"config.json"),Encoding.UTF8)); }
    public static int Port { get { string s=Convert.ToString(Config()["listen"]); int p; if(!Int32.TryParse(s.Substring(s.LastIndexOf(':')+1),out p)||p<1||p>65535)throw new Exception("配置中的监听端口无效");return p; } }
    public static string URL { get { return "http://127.0.0.1:"+Port+"/"; } }
    public static void Open(string target) { Process.Start(new ProcessStartInfo(target){UseShellExecute=true}); }
    public static bool FreePort(int port) { TcpListener l=new TcpListener(IPAddress.Any,port);try{l.Start();return true;}catch(SocketException){return false;}finally{l.Stop();} }
    public static ProcessStartInfo StartInfo(string args) { return new ProcessStartInfo(Service,args){WorkingDirectory=Root,UseShellExecute=false,CreateNoWindow=true,RedirectStandardInput=true,RedirectStandardError=true,RedirectStandardOutput=true,StandardErrorEncoding=Encoding.UTF8,StandardOutputEncoding=Encoding.UTF8}; }
    public static string Initialize(string json) {
        using(Process p=Process.Start(StartInfo("--data "+Q(Data)+" --portable-init"))){
            byte[] bytes=new UTF8Encoding(false).GetBytes(json);p.StandardInput.BaseStream.Write(bytes,0,bytes.Length);p.StandardInput.BaseStream.Flush();p.StandardInput.Close();string error=p.StandardError.ReadToEnd();p.StandardOutput.ReadToEnd();
            if(!p.WaitForExit(20000))throw new Exception("初始化超时，请检查数据目录");if(p.ExitCode!=0)throw new Exception(error);return error;
        }
    }
    [STAThread] public static int Main(string[] args) {
        bool smoke=args.Contains("--smoke"),background=args.Contains("--background");
        try {
            Application.EnableVisualStyles();Application.SetCompatibleTextRenderingDefault(false);
            Data=Path.Combine(Root,"data");int d=Array.IndexOf(args,"--data");if(d>=0){if(d+1>=args.Length)throw new Exception("--data 缺少目录");Data=Path.GetFullPath(args[d+1]);}
            if(!File.Exists(Service))throw new Exception("请先完整解压，再启动简作.exe；缺少 jianzuo-service.exe。");
            Directory.CreateDirectory(Data);
            string key;using(var h=SHA256.Create()){key=BitConverter.ToString(h.ComputeHash(Encoding.UTF8.GetBytes(Path.GetFullPath(Data).ToLowerInvariant()))).Replace("-","");}
            bool created;using(Mutex mutex=new Mutex(true,"Local\\JianzuoPortable_"+key,out created)){
                if(!created){if(smoke)return 3;if(File.Exists(Path.Combine(Data,"config.json")))Open(URL);else MessageBox.Show("首次配置窗口已经打开。","简作");return 0;}
                try{
                    if(!File.Exists(Path.Combine(Data,"jianzuo.db"))){if(smoke)throw new Exception("Smoke test requires initialized data");using(var wizard=new SetupForm()){if(wizard.ShowDialog()!=DialogResult.OK)return 0;}}
                    using(var host=new ServiceHost()){
                        host.Start();
                        if(smoke){using(var wizard=new SetupForm()){wizard.VerifyDetectedBinding();}host.Stop();Console.WriteLine("portable-start-stop-ok");return 0;}
                        using(var tray=new TrayApp(host,background)){Application.Run(tray);}
                    }
                }finally{mutex.ReleaseMutex();}
            }
            return 0;
        }catch(Exception e){if(smoke)Console.Error.WriteLine(e.Message);else MessageBox.Show(e.Message,"简作未启动",MessageBoxButtons.OK,MessageBoxIcon.Error);return 1;}
    }
}

sealed class ServiceHost:IDisposable {
    Process process;readonly StringBuilder errors=new StringBuilder();
    public bool Running { get { return process!=null&&!process.HasExited; } }
    public void Start(){
        if(Running)return;
        if(!Portable.FreePort(Portable.Port))throw new Exception("端口 "+Portable.Port+" 已被占用，未启动第二份服务。\n若旧版简作正在运行，可继续使用旧版，或退出旧版后再启动本便携版。\n要并行运行，请在本便携版 data/config.json 中设置不同的 listen 端口。");
        process=new Process();process.StartInfo=Portable.StartInfo("--data "+Portable.Q(Portable.Data)+" --managed");
        process.ErrorDataReceived+=(s,e)=>{if(e.Data!=null)lock(errors){if(errors.Length<16000)errors.AppendLine(e.Data);}};
        process.OutputDataReceived+=(s,e)=>{};process.Start();process.BeginErrorReadLine();process.BeginOutputReadLine();
        for(int i=0;i<80;i++){
            if(process.HasExited)throw new Exception("服务启动失败：\n"+errors+"\n日志："+Path.Combine(Portable.Data,"service.log"));
            try{var req=(HttpWebRequest)WebRequest.Create(Portable.URL+"healthz");req.Proxy=null;req.Timeout=350;using(var res=req.GetResponse())using(var reader=new StreamReader(res.GetResponseStream())){var obj=Portable.Json.Deserialize<Dictionary<string,object>>(reader.ReadToEnd());if(Convert.ToString(obj["app"])=="jianzuo"&&Convert.ToString(obj["version"])=="0.14.0-portable")return;}}catch(WebException){}
            Thread.Sleep(150);
        }
        if(Running){process.StandardInput.Close();process.WaitForExit(20000);}throw new Exception("启动超时，请查看 data/service.log。");
    }
    public void Stop(){if(!Running)return;process.StandardInput.WriteLine("stop");process.StandardInput.Flush();if(!process.WaitForExit(25000))throw new Exception("服务仍在结束任务，请稍后再点退出。此时不要移动数据目录。");}
    public void Dispose(){if(process!=null){if(Running){try{process.StandardInput.Close();process.WaitForExit(25000);}catch{}}process.Dispose();}}
}

sealed class TrayApp:ApplicationContext,IDisposable {
    readonly NotifyIcon icon;readonly ServiceHost host;readonly System.Windows.Forms.Timer timer;readonly ToolStripMenuItem startup,status;
    const string RunKey="Software\\Microsoft\\Windows\\CurrentVersion\\Run",RunName="JianzuoPortable";
    string StartupCommand {get{return Portable.Q(Application.ExecutablePath)+" --data "+Portable.Q(Portable.Data)+" --background";}}
    public TrayApp(ServiceHost service,bool background){
        host=service;ContextMenuStrip menu=new ContextMenuStrip();
        status=new ToolStripMenuItem("简作 · 正在运行"){Enabled=false};menu.Items.Add(status);
        menu.Items.Add("打开工作台",null,(s,e)=>Safe(()=>Portable.Open(Portable.URL)));
        menu.Items.Add("访问地址 / 手机连接",null,(s,e)=>Safe(()=>{Portable.Open(Portable.URL+"?settings=access");}));
        menu.Items.Add("打开数据目录",null,(s,e)=>Safe(()=>Portable.Open(Portable.Data)));
        menu.Items.Add("查看服务日志",null,(s,e)=>Safe(()=>Portable.Open(Path.Combine(Portable.Data,"service.log"))));
        menu.Items.Add(new ToolStripSeparator());
        startup=new ToolStripMenuItem("登录 Windows 后自动启动"){CheckOnClick=false};startup.Checked=IsStartup();startup.Click+=(s,e)=>Safe(ToggleStartup);menu.Items.Add(startup);
        menu.Items.Add("重新启动服务",null,(s,e)=>Safe(()=>{if(ConfirmStop("重新启动")){host.Stop();host.Start();}}));
        menu.Items.Add("退出简作",null,(s,e)=>Safe(()=>{if(ConfirmStop("退出")){host.Stop();icon.Visible=false;ExitThread();}}));
        icon=new NotifyIcon{Icon=SystemIcons.Application,Text="简作 · 本地任务工作台",ContextMenuStrip=menu,Visible=true};
        icon.DoubleClick+=(s,e)=>Safe(()=>Portable.Open(Portable.URL));
        timer=new System.Windows.Forms.Timer{Interval=2000};timer.Tick+=(s,e)=>{status.Text=host.Running?"简作 · 正在运行":"服务已停止 · 请查看日志";icon.Text=status.Text;};timer.Start();
        if(!background)Portable.Open(Portable.URL);
    }
    bool ConfirmStop(string action){return MessageBox.Show(action+"会停止正在执行的任务并断开硬件连接。是否继续？","简作",MessageBoxButtons.YesNo,MessageBoxIcon.Question)==DialogResult.Yes;}
    bool IsStartup(){using(var key=Registry.CurrentUser.OpenSubKey(RunKey)){return key!=null&&Convert.ToString(key.GetValue(RunName))==StartupCommand;}}
    void ToggleStartup(){using(var key=Registry.CurrentUser.CreateSubKey(RunKey)){if(IsStartup())key.DeleteValue(RunName,false);else{string old=Convert.ToString(key.GetValue(RunName));if(old!=""&&old!=StartupCommand&&MessageBox.Show("将替换另一目录的简作便携版自启记录，继续？","简作",MessageBoxButtons.YesNo)!=DialogResult.Yes)return;key.SetValue(RunName,StartupCommand);}}startup.Checked=IsStartup();}
    static void Safe(Action action){try{action();}catch(Exception e){MessageBox.Show(e.Message,"简作",MessageBoxButtons.OK,MessageBoxIcon.Error);}}
    protected override void Dispose(bool disposing){if(disposing){timer.Dispose();icon.Visible=false;icon.Dispose();}base.Dispose(disposing);}
}

sealed class FoundTool { public string path {get;set;} public string state {get;set;} public string label {get;set;} }
sealed class FoundEnvironment { public string name {get;set;} public string type {get;set;} public string distro {get;set;} public string user {get;set;} public string codex {get;set;} public string claude {get;set;} public string default_engine {get;set;} public string[] workspaces {get;set;} }
sealed class FoundChoice {
    public FoundEnvironment environment {get;set;} public FoundTool codex {get;set;} public FoundTool claude {get;set;} public string message {get;set;}
    public override string ToString(){return environment.name+" · "+(environment.default_engine=="claude"?"Claude Code":"Codex");}
}
sealed class FoundResult { public FoundChoice[] items {get;set;} public string message {get;set;} }
sealed class SetupForm:Form {
    readonly TextBox password=new TextBox{UseSystemPasswordChar=true},confirm=new TextBox{UseSystemPasswordChar=true},workspace=new TextBox(),binary=new TextBox(),distro=new TextBox(),user=new TextBox(),sshHost=new TextBox();
    readonly ComboBox kind=new ComboBox{DropDownStyle=ComboBoxStyle.DropDownList},engine=new ComboBox{DropDownStyle=ComboBoxStyle.DropDownList},detected=new ComboBox{DropDownStyle=ComboBoxStyle.DropDownList};
    readonly NumericUpDown port=new NumericUpDown{Minimum=1024,Maximum=65535},sshPort=new NumericUpDown{Minimum=1,Maximum=65535,Value=22};
    readonly CheckBox network=new CheckBox{Text="允许局域网和 Tailscale 访问",AutoSize=true};
    readonly FlowLayoutPanel fields=new FlowLayoutPanel{Dock=DockStyle.Fill,FlowDirection=FlowDirection.TopDown,WrapContents=false,AutoScroll=true,Padding=new Padding(24)};
    readonly FlowLayoutPanel advanced=new FlowLayoutPanel{FlowDirection=FlowDirection.TopDown,WrapContents=false,AutoSize=true,Visible=false,Width=525,Margin=new Padding(0)};
    readonly List<Control> wslControls=new List<Control>(),sshControls=new List<Control>(),linuxControls=new List<Control>();
    readonly Label detectionStatus=new Label{AutoSize=true,MaximumSize=new Size(520,0),ForeColor=Color.DimGray,Margin=new Padding(0,8,0,6)};
    readonly Button detect=new Button{Text="重新检测",AutoSize=true},browse=new Button{Text="选择文件夹…",AutoSize=true};
    FoundChoice selected; bool applying;
    public SetupForm(){
        Text="简作 · 首次使用";StartPosition=FormStartPosition.CenterScreen;ClientSize=new Size(610,790);MinimumSize=new Size(580,560);Font=new Font("Microsoft YaHei UI",9);AutoScaleMode=AutoScaleMode.Dpi;BackColor=Color.White;
        fields.Controls.Add(new Label{Text="选好环境，就可以开始。",Font=new Font(Font.FontFamily,17,FontStyle.Bold),AutoSize=true,Margin=new Padding(0,0,0,8)});
        fields.Controls.Add(new Label{Text="自动查找本机 Windows / WSL 中的 Codex 和 Claude Code。",AutoSize=true,ForeColor=Color.DimGray});
        AddField(fields,"执行环境",detected,null);fields.Controls.Add(detectionStatus);fields.Controls.Add(detect);
        engine.Items.AddRange(new object[]{"Codex","Claude Code"});AddField(fields,"AI 工具",engine,null);engine.SelectedIndex=0;
        AddField(fields,"工作目录",workspace,null);fields.Controls.Add(browse);
        AddField(fields,"工作台密码（至少 6 字节）",password,null);AddField(fields,"再次输入密码",confirm,null);
        network.Margin=new Padding(0,15,0,5);fields.Controls.Add(network);
        Button more=new Button{Text="高级设置 / 手工添加 SSH",AutoSize=true,FlatStyle=FlatStyle.Flat};more.FlatAppearance.BorderSize=0;more.ForeColor=Color.DimGray;more.Click+=(s,e)=>{advanced.Visible=!advanced.Visible;};fields.Controls.Add(more);fields.Controls.Add(advanced);
        kind.Items.AddRange(new object[]{"本机 Windows","WSL","SSH · Linux 主机"});AddField(advanced,"执行方式",kind,null);
        AddField(advanced,"WSL 发行版",distro,wslControls);AddField(advanced,"SSH 主机",sshHost,sshControls);AddField(advanced,"SSH 端口",sshPort,sshControls);AddField(advanced,"Linux 用户名（留空使用默认用户）",user,linuxControls);
        AddField(advanced,"AI 工具程序位置",binary,null);
        int candidate=8789;while(candidate<8890&&!Portable.FreePort(candidate))candidate++;port.Value=candidate;AddField(advanced,"工作台端口",port,null);
        advanced.Controls.Add(new Label{Text="SSH 需预先配好密钥与主机信任。\nAI 工具、WSL 和 Tailscale 按需单独安装。",AutoSize=true,MaximumSize=new Size(520,0),ForeColor=Color.DimGray,Margin=new Padding(0,10,0,4)});
        Button create=new Button{Text="启动工作台",AutoSize=true,Padding=new Padding(18,6,18,6),Margin=new Padding(0,17,0,8)};create.Click+=(s,e)=>{
            create.Enabled=false;try{Create();DialogResult=DialogResult.OK;Close();}catch(Exception err){MessageBox.Show(err.Message,"请检查配置",MessageBoxButtons.OK,MessageBoxIcon.Warning);}finally{if(!create.IsDisposed)create.Enabled=true;}
        };fields.Controls.Add(create);fields.Controls.Add(new Label{Text="任务和设置保存在："+Portable.Data,AutoSize=true,MaximumSize=new Size(520,0),ForeColor=Color.DimGray,Font=new Font(Font.FontFamily,8)});Controls.Add(fields);AcceptButton=create;
        kind.SelectedIndexChanged+=(s,e)=>{
            bool win=kind.SelectedIndex==0;foreach(var c in wslControls)c.Visible=kind.SelectedIndex==1;foreach(var c in sshControls)c.Visible=kind.SelectedIndex==2;foreach(var c in linuxControls)c.Visible=!win;browse.Enabled=win;
            if(!applying){selected=null;binary.Text=DefaultBinary();workspace.Text=win?Environment.GetFolderPath(Environment.SpecialFolder.MyDocuments):"/home";detectionStatus.Text="手工配置 · 可重新检测本机环境";}
        };
        engine.SelectedIndexChanged+=(s,e)=>{if(applying)return;binary.Text=selected==null?DefaultBinary():(engine.SelectedIndex==1?selected.environment.claude:selected.environment.codex);};
        kind.SelectedIndex=0;detected.Items.Add("手工配置（Windows / WSL / SSH）");detected.SelectedIndex=0;
        detected.SelectedIndexChanged+=(s,e)=>{FoundChoice item=detected.SelectedItem as FoundChoice;if(item==null){selected=null;advanced.Visible=true;detectionStatus.Text="在高级设置中填写连接参数。";}else ApplyDetected(item);};
        browse.Click+=(s,e)=>{using(var picker=new FolderBrowserDialog{Description="选择任务的默认工作目录",SelectedPath=Directory.Exists(workspace.Text)?workspace.Text:"",ShowNewFolderButton=true}){if(picker.ShowDialog(this)==DialogResult.OK)workspace.Text=picker.SelectedPath;}};
        detect.Click+=async(s,e)=>await Detect();Shown+=async(s,e)=>await Detect();
    }
    public void VerifyDetectedBinding(){
        var fixture=new FoundChoice{environment=new FoundEnvironment{name="WSL test",type="wsl",distro="Ubuntu-test",user="dev",codex="/home/dev/bin/codex",claude="/home/dev/bin/claude",default_engine="claude",workspaces=new[]{"/home/dev/work"}},codex=new FoundTool{label="Codex test"},claude=new FoundTool{label="Claude test"}};
        var encoded=new FoundResult{items=new[]{fixture}};
        fixture=new JavaScriptSerializer().Deserialize<FoundResult>(new JavaScriptSerializer().Serialize(encoded)).items[0];
        ApplyDetected(fixture);
        if(kind.SelectedIndex!=1||distro.Text!="Ubuntu-test"||user.Text!="dev"||workspace.Text!="/home/dev/work"||binary.Text!="/home/dev/bin/claude")throw new Exception("Detected environment binding failed");
        engine.SelectedIndex=0;if(binary.Text!="/home/dev/bin/codex")throw new Exception("Engine switch lost detected path");
        kind.SelectedIndex=0;if(selected!=null||!Directory.Exists(workspace.Text))throw new Exception("Manual environment switch failed");
    }
    string DefaultBinary(){bool win=kind.SelectedIndex==0;return engine.SelectedIndex==1?(win?FindClaude():"claude"):(win?FindCodex():"codex");}
    void ApplyDetected(FoundChoice item){
        applying=true;try{selected=item;kind.SelectedIndex=item.environment.type=="wsl"?1:0;distro.Text=item.environment.distro??"";user.Text=item.environment.user??"";engine.SelectedIndex=item.environment.default_engine=="claude"?1:0;binary.Text=engine.SelectedIndex==1?item.environment.claude:item.environment.codex;workspace.Text=item.environment.workspaces[0];detectionStatus.Text="Codex："+item.codex.label+"    Claude："+item.claude.label+"\n"+(String.IsNullOrEmpty(item.message)?"沿用该环境的登录配置；模型是否可用以实际执行为准。":item.message);advanced.Visible=false;}finally{applying=false;}
    }
    async Task Detect(){
        detect.Enabled=false;detected.Enabled=false;detectionStatus.Text="正在检测（约 10–25 秒）… 可能启动已安装的 WSL。";
        try{
            FoundResult result=await Task.Run(()=>{
                using(Process p=Process.Start(Portable.StartInfo("--detect-environments"))){
                    p.StandardInput.Close();var output=p.StandardOutput.ReadToEndAsync();var error=p.StandardError.ReadToEndAsync();
                    if(!p.WaitForExit(32000)){try{p.Kill();}catch{}throw new Exception("检测超时，可改用手工配置。");}
                    Task.WaitAll(output,error);if(p.ExitCode!=0)throw new Exception("检测未完成，可改用手工配置。");return new JavaScriptSerializer().Deserialize<FoundResult>(output.Result);
                }
            });
            if(IsDisposed)return;
            detected.Items.Clear();foreach(var item in result.items)detected.Items.Add(item);detected.Items.Add("手工配置（Windows / WSL / SSH）");
            int preferred=Array.FindIndex(result.items,x=>x.codex.state=="configured"||x.claude.state=="configured");detected.SelectedIndex=preferred>=0?preferred:0;
        }catch(Exception e){if(!IsDisposed){detectionStatus.Text=e.Message;advanced.Visible=true;}}finally{if(!IsDisposed){detect.Enabled=true;detected.Enabled=true;}}
    }
    static void AddField(FlowLayoutPanel container,string label,Control control,List<Control> group){var l=new Label{Text=label,AutoSize=true,Margin=new Padding(0,12,0,4)};control.Width=520;container.Controls.Add(l);container.Controls.Add(control);if(group!=null){group.Add(l);group.Add(control);}}
    static string FindCodex(){string candidate=Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),"npm\\node_modules\\@openai\\codex\\node_modules\\@openai\\codex-win32-x64\\vendor\\x86_64-pc-windows-msvc\\bin\\codex.exe");return File.Exists(candidate)?candidate:"codex.exe";}
    static string FindClaude(){string home=Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);foreach(string relative in new[]{".local\\bin\\claude.exe","AppData\\Local\\Microsoft\\WinGet\\Links\\claude.exe"}){string path=Path.Combine(home,relative);if(File.Exists(path))return path;}return "claude.exe";}
    void Create(){
        if(password.Text!=confirm.Text)throw new Exception("两次密码不一致");int count=Encoding.UTF8.GetByteCount(password.Text);if(count<6||count>72||password.Text.Trim()!=password.Text)throw new Exception("密码须为 6–72 字节，且首尾没有空白。");
        if(!Portable.FreePort((int)port.Value))throw new Exception("端口已被占用，请换一个端口。");
        string type=kind.SelectedIndex==0?"windows":kind.SelectedIndex==1?"wsl":"ssh";
        if(binary.Text.Trim()=="")throw new Exception("请填写 AI 工具可执行文件。");
        if(type=="windows"&&!Directory.Exists(workspace.Text))throw new Exception("工作目录不存在，请填写已有文件夹的绝对路径。");
        if(type!="windows"&&!workspace.Text.StartsWith("/"))throw new Exception("请填写 Linux 绝对路径。");
        var env=new Dictionary<string,object>{{"id","default"},{"name",kind.SelectedIndex==1?"WSL · "+distro.Text.Trim():Convert.ToString(kind.SelectedItem)},{"type",type},{"distro",distro.Text.Trim()},{"user",user.Text.Trim()},{"host",sshHost.Text.Trim()},{"port",(int)sshPort.Value},{"default_engine",engine.SelectedIndex==1?"claude":"codex"},{"claude",engine.SelectedIndex==1?binary.Text.Trim():(selected==null?"":selected.environment.claude)},{"codex",engine.SelectedIndex==1?(selected==null?(type=="windows"?"codex.exe":"codex"):selected.environment.codex):binary.Text.Trim()},{"model",""},{"workspaces",new[]{workspace.Text.Trim()}}};
        string lan="",tail="";
        if(network.Checked){foreach(var nic in NetworkInterface.GetAllNetworkInterfaces().Where(n=>n.OperationalStatus==OperationalStatus.Up))foreach(var addr in nic.GetIPProperties().UnicastAddresses){if(addr.Address.AddressFamily!=AddressFamily.InterNetwork)continue;byte[] b=addr.Address.GetAddressBytes();string url="http://"+addr.Address+":"+port.Value;if(b[0]==100&&b[1]>=64&&b[1]<=127)tail=url;else if(lan==""&&(b[0]==10||(b[0]==192&&b[1]==168)||(b[0]==172&&b[1]>=16&&b[1]<=31)))lan=url;}}
        var config=new Dictionary<string,object>{{"listen",(network.Checked?"0.0.0.0:":"127.0.0.1:")+port.Value},{"environments",new[]{env}},{"default_environment","default"},{"feishu",new Dictionary<string,object>{{"enabled",false}}},{"access",new Dictionary<string,object>{{"lan",lan},{"tailscale",tail}}}};
        Portable.Initialize(Portable.Json.Serialize(new Dictionary<string,object>{{"password",password.Text},{"config",config}}));password.Clear();confirm.Clear();
    }
}
