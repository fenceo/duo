using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Security.Principal;
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
    public static string DataPointer { get { return Path.Combine(Root,".duo-data-location.json"); } }
    public static string Service { get { return Path.Combine(Root,"duo-service.exe"); } }
    public static string Q(string s) { return "\"" + s.TrimEnd('\\') + "\""; }
    public static Dictionary<string,object> Config() { return Json.Deserialize<Dictionary<string,object>>(File.ReadAllText(Path.Combine(Data,"config.json"),Encoding.UTF8)); }
    public static int Port { get { string s=Convert.ToString(Config()["listen"]); int p; if(!Int32.TryParse(s.Substring(s.LastIndexOf(':')+1),out p)||p<1||p>65535)throw new Exception("配置中的监听端口无效");return p; } }
    public static string URL { get { return "http://127.0.0.1:"+Port+"/"; } }
    public static bool IsUsableData(string path){
        try{
            if(String.IsNullOrWhiteSpace(path)||!Path.IsPathRooted(path)||path.StartsWith("\\\\")||path.StartsWith("//"))return false;
            path=Path.GetFullPath(path).TrimEnd('\\');if(!Directory.Exists(path))return false;
            var dir=new DirectoryInfo(path);if((dir.Attributes&FileAttributes.ReparsePoint)!=0)return false;
            return File.Exists(Path.Combine(path,"config.json"))&&File.Exists(Path.Combine(path,"jianzuo.db"));
        }catch{return false;}
    }
    public static string ResolveData(string requested){
        try{
            if(File.Exists(DataPointer)){
                var map=Json.Deserialize<Dictionary<string,object>>(File.ReadAllText(DataPointer,Encoding.UTF8));
                string pointer=Convert.ToString(map["data_dir"]);
                if(IsUsableData(pointer))return Path.GetFullPath(pointer);
            }
        }catch{}
        return Path.GetFullPath(requested);
    }
    public static void WriteDataPointer(string path){
        if(!IsUsableData(path))throw new Exception("目标数据目录无法作为 Duo 启动目录");
        string tmp=DataPointer+"."+Guid.NewGuid().ToString("N")+".tmp";
        File.WriteAllText(tmp,Json.Serialize(new Dictionary<string,object>{{"protocol",1},{"data_dir",Path.GetFullPath(path)}}),new UTF8Encoding(false));
        if(File.Exists(DataPointer))File.Replace(tmp,DataPointer,null);else File.Move(tmp,DataPointer);
    }
    public static void UpdateInstalledDataDirectory(string path){
        try{using(var key=Registry.CurrentUser.OpenSubKey("Software\\Jianzuo",true)){if(key==null)return;string install=Convert.ToString(key.GetValue("InstallDir"));if(String.Equals(Path.GetFullPath(install).TrimEnd('\\'),Root.TrimEnd('\\'),StringComparison.OrdinalIgnoreCase))key.SetValue("DataDir",Path.GetFullPath(path));}}catch{}
    }
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
            Data=Path.Combine(Root,"data");int d=Array.IndexOf(args,"--data");if(d>=0){if(d+1>=args.Length)throw new Exception("--data 缺少目录");Data=Path.GetFullPath(args[d+1]);}Data=ResolveData(Data);
            if(!File.Exists(Service))throw new Exception("请先完整解压，再启动Duo.exe；缺少 duo-service.exe。");
            Directory.CreateDirectory(Data);
            string key;using(var h=SHA256.Create()){key=BitConverter.ToString(h.ComputeHash(Encoding.UTF8.GetBytes(Path.GetFullPath(Data).ToLowerInvariant()))).Replace("-","");}
            bool created;using(Mutex mutex=new Mutex(true,"Local\\JianzuoPortable_"+key,out created)){
                if(!created){if(smoke)return 3;if(File.Exists(Path.Combine(Data,"config.json")))Open(URL);else MessageBox.Show("首次配置窗口已经打开。","Duo");return 0;}
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
        }catch(Exception e){if(smoke)Console.Error.WriteLine(e.Message);else MessageBox.Show(e.Message,"Duo未启动",MessageBoxButtons.OK,MessageBoxIcon.Error);return 1;}
    }
}

sealed class ServiceHost:IDisposable {
    Process process;readonly StringBuilder errors=new StringBuilder();volatile bool updateRequested, dataSwitchRequested;
    public bool Running { get { return process!=null&&!process.HasExited; } }
    public bool UpdateRequested { get { return updateRequested; } }
    public bool DataSwitchRequested { get { return dataSwitchRequested; } }
    public void Start(){
        if(Running)return;
        if(!Portable.FreePort(Portable.Port))throw new Exception("端口 "+Portable.Port+" 已被占用，未启动第二份服务。\n若旧版Duo正在运行，可继续使用旧版，或退出旧版后再启动本便携版。\n要并行运行，请在本便携版 data/config.json 中设置不同的 listen 端口。");
        dataSwitchRequested=false;process=new Process();process.StartInfo=Portable.StartInfo("--data "+Portable.Q(Portable.Data)+" --managed");
        process.ErrorDataReceived+=(s,e)=>{if(e.Data!=null)lock(errors){if(errors.Length<16000)errors.AppendLine(e.Data);}};
        process.OutputDataReceived+=(s,e)=>{if(e.Data!=null){if(e.Data.StartsWith("JIANZUO_UPDATE ",StringComparison.Ordinal))updateRequested=true;if(e.Data.Trim()=="JIANZUO_SWITCH")dataSwitchRequested=true;}};process.Start();process.BeginErrorReadLine();process.BeginOutputReadLine();
        for(int i=0;i<80;i++){
            if(process.HasExited)throw new Exception("服务启动失败：\n"+errors+"\n日志："+Path.Combine(Portable.Data,"service.log"));
            try{var req=(HttpWebRequest)WebRequest.Create(Portable.URL+"healthz");req.Proxy=null;req.Timeout=350;using(var res=req.GetResponse())using(var reader=new StreamReader(res.GetResponseStream())){var obj=Portable.Json.Deserialize<Dictionary<string,object>>(reader.ReadToEnd());if(Convert.ToString(obj["app"])=="jianzuo"&&!String.IsNullOrEmpty(Convert.ToString(obj["version"])))return;}}catch(WebException){}
            Thread.Sleep(150);
        }
        if(Running){process.StandardInput.Close();process.WaitForExit(20000);}throw new Exception("启动超时，请查看 data/service.log。");
    }
    public void ApplyDataSwitch(){
        if(Running)throw new Exception("服务尚未退出，暂不能切换数据目录");
        string marker=Path.Combine(Portable.Root,".duo-data-switch.json");
        if(!File.Exists(marker))throw new Exception("未找到数据目录切换请求，原目录保持不变");
        var request=Portable.Json.Deserialize<Dictionary<string,object>>(File.ReadAllText(marker,Encoding.UTF8));
        string oldData=Convert.ToString(request["old_data"]),newData=Convert.ToString(request["new_data"]);
        if(String.IsNullOrWhiteSpace(oldData)||!String.Equals(Path.GetFullPath(oldData).TrimEnd('\\'),Path.GetFullPath(Portable.Data).TrimEnd('\\'),StringComparison.OrdinalIgnoreCase))throw new Exception("切换请求来源已变化，原目录保持不变");
        if(String.IsNullOrWhiteSpace(newData)||!Portable.IsUsableData(newData))throw new Exception("切换请求目标不是有效的数据目录");
        bool startupEnabled=false;try{using(var task=new UserStartup(StartupCommand()))startupEnabled=task.IsConfigured();}catch{}
        dataSwitchRequested=false;string old=Portable.Data;Portable.WriteDataPointer(newData);Portable.Data=Path.GetFullPath(newData);
        try{Portable.UpdateInstalledDataDirectory(Portable.Data);if(startupEnabled)using(var task=new UserStartup(StartupCommand()))task.Install();Start();File.Delete(marker);}
        catch(Exception e){Portable.Data=old;try{Portable.WriteDataPointer(old);}catch{}try{Portable.UpdateInstalledDataDirectory(old);if(startupEnabled)using(var task=new UserStartup(StartupCommand()))task.Install();}catch{}try{File.Delete(marker);}catch{}try{Start();}catch{}throw new Exception("载入新数据目录失败，已恢复原目录："+e.Message);}
    }
    static string StartupCommand(){return Portable.Q(Application.ExecutablePath)+" --data "+Portable.Q(Portable.Data)+" --background";}
    public void Stop(){if(!Running)return;process.StandardInput.WriteLine("stop");process.StandardInput.Flush();if(!process.WaitForExit(25000))throw new Exception("服务仍在结束任务，请稍后再点退出。此时不要移动数据目录。");}
    public void Dispose(){if(process!=null){if(Running){try{process.StandardInput.Close();process.WaitForExit(25000);}catch{}}process.Dispose();}}
}

sealed class TrayApp:ApplicationContext,IDisposable {
    readonly NotifyIcon icon;readonly ServiceHost host;readonly System.Windows.Forms.Timer timer;readonly ToolStripMenuItem startup,status;
    const string RunKey="Software\\Microsoft\\Windows\\CurrentVersion\\Run",RunName="JianzuoPortable";
    string StartupCommand {get{return Portable.Q(Application.ExecutablePath)+" --data "+Portable.Q(Portable.Data)+" --background";}}
    public TrayApp(ServiceHost service,bool background){
        host=service;ContextMenuStrip menu=new ContextMenuStrip();
        status=new ToolStripMenuItem("Duo · 正在运行"){Enabled=false};menu.Items.Add(status);
        menu.Items.Add("打开工作台",null,(s,e)=>Safe(()=>Portable.Open(Portable.URL)));
        menu.Items.Add("访问地址 / 手机连接",null,(s,e)=>Safe(()=>{Portable.Open(Portable.URL+"?settings=access");}));
        menu.Items.Add("打开数据目录",null,(s,e)=>Safe(()=>Portable.Open(Portable.Data)));
        menu.Items.Add("查看服务日志",null,(s,e)=>Safe(()=>Portable.Open(Path.Combine(Portable.Data,"service.log"))));
        menu.Items.Add(new ToolStripSeparator());
        startup=new ToolStripMenuItem("登录 Windows 后自动启动"){CheckOnClick=false};startup.Checked=IsStartup();startup.Click+=(s,e)=>Safe(ToggleStartup);menu.Items.Add(startup);
        menu.Items.Add("重新启动服务",null,(s,e)=>Safe(()=>{if(ConfirmStop("重新启动")){host.Stop();host.Start();}}));
        menu.Items.Add("退出Duo",null,(s,e)=>Safe(()=>{if(ConfirmStop("退出")){host.Stop();icon.Visible=false;ExitThread();}}));
        icon=new NotifyIcon{Icon=SystemIcons.Application,Text="Duo · 本地任务工作台",ContextMenuStrip=menu,Visible=true};
        icon.DoubleClick+=(s,e)=>Safe(()=>Portable.Open(Portable.URL));
        timer=new System.Windows.Forms.Timer{Interval=500};timer.Tick+=(s,e)=>{
            if(host.DataSwitchRequested&&!host.Running){try{host.ApplyDataSwitch();status.Text="Duo · 已载入新数据目录";}catch(Exception err){status.Text="数据目录切换失败";MessageBox.Show(err.Message,"Duo",MessageBoxButtons.OK,MessageBoxIcon.Error);}}
            if(host.UpdateRequested&&!host.Running){timer.Stop();status.Text="Duo · 正在自动更新";icon.Text=status.Text;icon.Visible=false;ExitThread();return;}
            status.Text=host.UpdateRequested?"Duo · 正在准备自动更新":(host.Running?"Duo · 正在运行":"服务已停止 · 请查看日志");icon.Text=status.Text;
        };timer.Start();
        if(!background)Portable.Open(Portable.URL);
    }
    bool ConfirmStop(string action){return MessageBox.Show(action+"会停止正在执行的任务并断开硬件连接。是否继续？","Duo",MessageBoxButtons.YesNo,MessageBoxIcon.Question)==DialogResult.Yes;}
    bool IsStartup(){try{using(var task=new UserStartup(StartupCommand))return task.IsConfigured();}catch{return false;}}
    void ToggleStartup(){
        using(var task=new UserStartup(StartupCommand)){
            string old=LegacyRunCommand();
            if((task.Exists()&&!task.IsOwned())||(old!=""&&old!=StartupCommand))throw new Exception("检测到另一个目录或无法确认归属的自启动入口。请运行新版安装包，在迁移页面核对原程序和数据后处理；不会直接覆盖旧入口。");
            if(task.IsConfigured()){task.Remove();DeleteLegacyRun();startup.Checked=false;return;}
            task.Install();DeleteLegacyRun();startup.Checked=task.IsConfigured();
        }
    }
    static string LegacyRunCommand(){using(var key=Registry.CurrentUser.OpenSubKey(RunKey))return key==null?"":Convert.ToString(key.GetValue(RunName));}
    void DeleteLegacyRun(){using(var key=Registry.CurrentUser.OpenSubKey(RunKey,true)){if(key!=null&&Convert.ToString(key.GetValue(RunName))==StartupCommand)key.DeleteValue(RunName,false);}}
    static void Safe(Action action){try{action();}catch(Exception e){MessageBox.Show(e.Message,"Duo",MessageBoxButtons.OK,MessageBoxIcon.Error);}}
    protected override void Dispose(bool disposing){if(disposing){timer.Dispose();icon.Visible=false;icon.Dispose();}base.Dispose(disposing);}
}

sealed class UserStartup:IDisposable {
    const string TaskName="Jianzuo User";
    string command;readonly string identity;object service,root;
    public UserStartup(string taskCommand){
        command=NormalizeTaskArguments(taskCommand,Application.ExecutablePath);identity=WindowsIdentity.GetCurrent().Name;Connect();
    }
    void Connect(){
        Type type=Type.GetTypeFromProgID("Schedule.Service");
        if(type==null)throw new Exception("当前系统没有 Windows 任务计划服务。");
        service=Activator.CreateInstance(type);Call(service,"Connect");root=Call(service,"GetFolder","\\");
    }
    static object Get(object target,string name){return target.GetType().InvokeMember(name,BindingFlags.GetProperty,null,target,null);}
    static object GetIndexed(object target,string name,int index){return target.GetType().InvokeMember(name,BindingFlags.GetProperty,null,target,new object[]{index});}
    static void Set(object target,string name,object value){target.GetType().InvokeMember(name,BindingFlags.SetProperty,null,target,new object[]{value});}
    static object Call(object target,string name,params object[] args){return target.GetType().InvokeMember(name,BindingFlags.InvokeMethod,null,target,args);}
    object Find(){try{return Call(root,"GetTask",TaskName);}catch(Exception e){if(e.GetBaseException().HResult==unchecked((int)0x80070002))return null;throw;}}
    public bool Exists(){return Find()!=null;}
    public bool IsConfigured(){
        try{object task=Find();return task!=null&&Convert.ToInt32(Get(task,"State"))!=1&&IsOwnedTask(task);}catch{return false;}
    }
    public bool IsOwned(){try{return IsOwnedTask(Find());}catch{return false;}}
    bool IsOwnedTask(object task){
        try{
            if(task==null)return false;
            object definition=Get(task,"Definition"),actions=Get(definition,"Actions"),principal=Get(definition,"Principal");
            if(Convert.ToInt32(Get(actions,"Count"))!=1)return false;
            object action=GetIndexed(actions,"Item",1);
            string path=Convert.ToString(Get(action,"Path")),args=Convert.ToString(Get(action,"Arguments")),dir=Convert.ToString(Get(action,"WorkingDirectory"));
            bool sameUser=String.Equals(Convert.ToString(Get(principal,"UserId")),identity,StringComparison.OrdinalIgnoreCase)||SameSid(Convert.ToString(Get(principal,"UserId")));
            return TaskActionMatches(path,args,dir,Application.ExecutablePath,command,Portable.Root)&&sameUser&&Convert.ToInt32(Get(principal,"RunLevel"))==0;
        }catch{return false;}
    }
    bool IsOwnedExecutableTask(object task){
        try{
            if(task==null)return false;object definition=Get(task,"Definition"),actions=Get(definition,"Actions"),principal=Get(definition,"Principal");
            if(Convert.ToInt32(Get(actions,"Count"))!=1)return false;object action=GetIndexed(actions,"Item",1);
            bool sameUser=String.Equals(Convert.ToString(Get(principal,"UserId")),identity,StringComparison.OrdinalIgnoreCase)||SameSid(Convert.ToString(Get(principal,"UserId")));
            return String.Equals(Path.GetFullPath(Convert.ToString(Get(action,"Path"))),Path.GetFullPath(Application.ExecutablePath),StringComparison.OrdinalIgnoreCase)&&String.Equals(Path.GetFullPath(Convert.ToString(Get(action,"WorkingDirectory"))).TrimEnd('\\'),Path.GetFullPath(Portable.Root).TrimEnd('\\'),StringComparison.OrdinalIgnoreCase)&&sameUser&&Convert.ToInt32(Get(principal,"RunLevel"))==0;
        }catch{return false;}
    }
    // Legacy tray builds stored the executable again inside Arguments. Accept
    // that exact legacy prefix when inspecting our own action, but never write it.
    static string NormalizeTaskArguments(string value,string executable){
        string prefix=Portable.Q(executable)+" ";
        return value.StartsWith(prefix,StringComparison.OrdinalIgnoreCase)?value.Substring(prefix.Length):value;
    }
    static bool TaskActionMatches(string path,string args,string directory,string expectedPath,string expectedArguments,string expectedDirectory){
        try{return String.Equals(Path.GetFullPath(path),Path.GetFullPath(expectedPath),StringComparison.OrdinalIgnoreCase)&&
            NormalizeTaskArguments(args,expectedPath)==NormalizeTaskArguments(expectedArguments,expectedPath)&&
            String.Equals(Path.GetFullPath(directory).TrimEnd('\\'),Path.GetFullPath(expectedDirectory).TrimEnd('\\'),StringComparison.OrdinalIgnoreCase);
        }catch{return false;}
    }
    static bool SameSid(string value){
        try{return String.Equals(value,WindowsIdentity.GetCurrent().User.Value,StringComparison.OrdinalIgnoreCase)||String.Equals(new NTAccount(value).Translate(typeof(SecurityIdentifier)).Value,WindowsIdentity.GetCurrent().User.Value,StringComparison.OrdinalIgnoreCase);}catch{return false;}
    }
    public void Install(){
        object existing=Find();if(existing!=null&&!IsOwnedExecutableTask(existing))throw new Exception("自启动入口已改变，取消覆盖。请通过安装包迁移旧入口。");
        object definition=Call(service,"NewTask",0),settings=Get(definition,"Settings"),principal=Get(definition,"Principal");
        Set(Get(definition,"RegistrationInfo"),"Description","Duo独立任务工作台（当前用户登录后启动）");
        Set(settings,"StartWhenAvailable",true);Set(settings,"AllowDemandStart",true);Set(settings,"Hidden",true);
        Set(settings,"DisallowStartIfOnBatteries",false);Set(settings,"StopIfGoingOnBatteries",false);
        Set(settings,"ExecutionTimeLimit","PT0S");Set(settings,"RestartCount",3);Set(settings,"RestartInterval","PT1M");Set(settings,"MultipleInstances",2);
        Set(principal,"UserId",identity);Set(principal,"LogonType",3);Set(principal,"RunLevel",0);
        object trigger=Call(Get(definition,"Triggers"),"Create",9);Set(trigger,"UserId",identity);Set(trigger,"Enabled",true);
        object action=Call(Get(definition,"Actions"),"Create",0);
        Set(action,"Path",Application.ExecutablePath);Set(action,"Arguments",command);Set(action,"WorkingDirectory",Portable.Root);
        Call(root,"RegisterTaskDefinition",TaskName,definition,6,null,null,3,null);
    }
    public void Remove(){
        object task=Find();if(task!=null){if(!IsOwnedTask(task))throw new Exception("自启动入口已改变，不会删除其他安装的任务。");Call(root,"DeleteTask",TaskName,0);}
    }
    public void Dispose(){foreach(object item in new[]{root,service})if(item!=null&&Marshal.IsComObject(item))try{Marshal.FinalReleaseComObject(item);}catch{}root=null;service=null;}
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
    FoundChoice selected; bool applying, detecting;
    public SetupForm(){
        Text="Duo · 首次使用";StartPosition=FormStartPosition.CenterScreen;ClientSize=new Size(520,390);MinimumSize=new Size(500,360);Font=new Font("Microsoft YaHei UI",9);AutoScaleMode=AutoScaleMode.Dpi;BackColor=Color.White;
        fields.Controls.Add(new Label{Text="设置密码，马上开始。",Font=new Font(Font.FontFamily,17,FontStyle.Bold),AutoSize=true,Margin=new Padding(0,0,0,8)});
        fields.Controls.Add(new Label{Text="首次启动只需设置工作台密码。进入工作台后，可随时在“设置”中修改执行环境、AI 工具、工作目录、网络和端口。",AutoSize=true,MaximumSize=new Size(450,0),ForeColor=Color.DimGray,Margin=new Padding(0,0,0,14)});
        AddField(fields,"工作台密码（至少 6 字节）",password,null);AddField(fields,"再次输入密码",confirm,null);
        Label hint=new Label{Text="默认仅允许本机访问，检测和配置不会发送任务或调用模型。之后可在工作台设置中补充 Codex、Claude Code、Harness 或 SSH。",AutoSize=true,MaximumSize=new Size(450,0),ForeColor=Color.DimGray,Margin=new Padding(0,14,0,2)};fields.Controls.Add(hint);
        // Environment/tool fields remain available to the background detector and
        // compatibility smoke tests, but are intentionally not exposed in the
        // first-use wizard. Configuration belongs in the workbench settings.
        // Keep the configuration controls detached from the first-run form.
        // They are populated only for discovery/smoke-test compatibility;
        // users edit environment, tool, path and network settings later.
        kind.Items.AddRange(new object[]{"本机 Windows","WSL","SSH · Linux 主机"});AddField(advanced,"执行环境",detected,null);AddField(advanced,"执行方式",kind,null);
        engine.Items.AddRange(new object[]{"Codex","Claude Code"});AddField(advanced,"AI 工具",engine,null);engine.SelectedIndex=0;
        AddField(advanced,"工作目录",workspace,null);advanced.Controls.Add(browse);
        AddField(advanced,"WSL 发行版",distro,wslControls);AddField(advanced,"SSH 主机",sshHost,sshControls);AddField(advanced,"SSH 端口",sshPort,sshControls);AddField(advanced,"Linux 用户名（留空使用默认用户）",user,linuxControls);
        AddField(advanced,"AI 工具程序位置",binary,null);
        network.Margin=new Padding(0,15,0,5);advanced.Controls.Add(network);
        int candidate=FindAvailablePort();port.Value=candidate;AddField(advanced,"工作台端口",port,null);
        advanced.Controls.Add(new Label{Text="SSH 需预先配好密钥与主机信任。\nAI 工具、WSL 和 Tailscale 按需单独安装。",AutoSize=true,MaximumSize=new Size(520,0),ForeColor=Color.DimGray,Margin=new Padding(0,10,0,4)});
        Button create=new Button{Text="启动工作台",AutoSize=true,Padding=new Padding(18,6,18,6),Margin=new Padding(0,17,0,8)};create.Click+=(s,e)=>{
            create.Enabled=false;try{Create();DialogResult=DialogResult.OK;Close();}catch(Exception err){MessageBox.Show(err.Message,"请检查配置",MessageBoxButtons.OK,MessageBoxIcon.Warning);}finally{if(!create.IsDisposed)create.Enabled=true;}
        };fields.Controls.Add(create);Controls.Add(fields);AcceptButton=create;
        kind.SelectedIndexChanged+=(s,e)=>{
            bool win=kind.SelectedIndex==0;foreach(var c in wslControls)c.Visible=kind.SelectedIndex==1;foreach(var c in sshControls)c.Visible=kind.SelectedIndex==2;foreach(var c in linuxControls)c.Visible=!win;browse.Enabled=win;
            if(!applying){selected=null;binary.Text=DefaultBinary();workspace.Text=win?DefaultWorkspace():"/home";detectionStatus.Text="配置将在工作台设置中完成";}
        };
        engine.SelectedIndexChanged+=(s,e)=>{if(applying)return;binary.Text=selected==null?DefaultBinary():(engine.SelectedIndex==1?selected.environment.claude:selected.environment.codex);};
        kind.SelectedIndex=0;
        browse.Click+=(s,e)=>{using(var picker=new FolderBrowserDialog{Description="选择任务的默认工作目录",SelectedPath=Directory.Exists(workspace.Text)?workspace.Text:"",ShowNewFolderButton=true}){if(picker.ShowDialog(this)==DialogResult.OK)workspace.Text=picker.SelectedPath;}};
        // Do not probe the machine while the first-use dialog is open.  The
        // wizard must be usable offline and with only a password; discovery is
        // available from Settings after the workbench has started.
        detect.Click+=async(s,e)=>await Detect();
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
    static int FindAvailablePort(){for(int candidate=8789;candidate<=65535;candidate++)if(Portable.FreePort(candidate))return candidate;throw new Exception("没有可用的本机监听端口");}
    static string DefaultWorkspace(){string docs=Environment.GetFolderPath(Environment.SpecialFolder.MyDocuments);if(Directory.Exists(docs))return docs;string home=Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);if(Directory.Exists(home))return home;return Environment.CurrentDirectory;}
    string DefaultBinary(){bool win=kind.SelectedIndex==0;return engine.SelectedIndex==1?(win?FindClaude():"claude"):(win?FindCodex():"codex");}
    void ApplyDetected(FoundChoice item){
        string detectedWorkspace=item.environment.workspaces!=null&&item.environment.workspaces.Length>0?item.environment.workspaces[0]:(item.environment.type=="wsl"?"/home":DefaultWorkspace());
        applying=true;try{selected=item;kind.SelectedIndex=item.environment.type=="wsl"?1:0;distro.Text=item.environment.distro??"";user.Text=item.environment.user??"";engine.SelectedIndex=item.environment.default_engine=="claude"?1:0;binary.Text=engine.SelectedIndex==1?item.environment.claude:item.environment.codex;workspace.Text=detectedWorkspace;detectionStatus.Text="已选择："+item.environment.name+" · 默认使用"+(engine.SelectedIndex==1?"Claude Code":"Codex")+"\nCodex："+item.codex.label+"    Claude："+item.claude.label+"\n"+(String.IsNullOrEmpty(item.message)?"沿用该环境的登录配置；模型是否可用以实际执行为准。":item.message);advanced.Visible=false;}finally{applying=false;}
    }
    async Task Detect(){
        if(detecting)return;detecting=true;
        try{
            FoundResult result=await Task.Run(()=>{
                using(Process p=Process.Start(Portable.StartInfo("--detect-environments"))){
                    p.StandardInput.Close();var output=p.StandardOutput.ReadToEndAsync();var error=p.StandardError.ReadToEndAsync();
                    if(!p.WaitForExit(32000)){try{p.Kill();}catch{}throw new Exception("检测超时，可改用手工配置。");}
                    Task.WaitAll(output,error);if(p.ExitCode!=0)throw new Exception("检测未完成，可改用手工配置。");return new JavaScriptSerializer().Deserialize<FoundResult>(output.Result);
                }
            });
            if(IsDisposed)return;
            FoundChoice[] items=result.items??new FoundChoice[0];
            int preferred=Array.FindIndex(items,x=>x!=null&&((x.codex!=null&&x.codex.state=="configured")||(x.claude!=null&&x.claude.state=="configured")));
            if(items.Length>0)ApplyDetected(items[preferred>=0?preferred:0]);
        }catch(Exception e){if(!IsDisposed){selected=null;detectionStatus.Text=e.Message;}}
        finally{detecting=false;}
    }
    static void AddField(FlowLayoutPanel container,string label,Control control,List<Control> group){var l=new Label{Text=label,AutoSize=true,Margin=new Padding(0,12,0,4)};control.Width=520;container.Controls.Add(l);container.Controls.Add(control);if(group!=null){group.Add(l);group.Add(control);}}
    static string FindCodex(){string candidate=Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),"npm\\node_modules\\@openai\\codex\\node_modules\\@openai\\codex-win32-x64\\vendor\\x86_64-pc-windows-msvc\\bin\\codex.exe");return File.Exists(candidate)?candidate:"codex.exe";}
    static string FindClaude(){string home=Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);foreach(string relative in new[]{".local\\bin\\claude.exe","AppData\\Local\\Microsoft\\WinGet\\Links\\claude.exe"}){string path=Path.Combine(home,relative);if(File.Exists(path))return path;}return "claude.exe";}
    void Create(){
        if(password.Text!=confirm.Text)throw new Exception("两次密码不一致");int count=Encoding.UTF8.GetByteCount(password.Text);if(count<6||count>72||password.Text.Trim()!=password.Text)throw new Exception("密码须为 6–72 字节，且首尾没有空白。");
        // Initialization deliberately accepts only the password.  The service
        // creates a safe localhost/default-environment config; all discovery
        // and machine-specific overrides happen from Settings after login.
        int selectedPort=(int)port.Value;if(!Portable.FreePort(selectedPort))selectedPort=FindAvailablePort();
        var config=new Dictionary<string,object>{{"listen","127.0.0.1:"+selectedPort}};
        Portable.Initialize(Portable.Json.Serialize(new Dictionary<string,object>{{"password",password.Text},{"config",config}}));password.Clear();confirm.Clear();
    }
}
