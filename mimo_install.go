package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const mimoDistributionRoot = "https://mimocode.cnbj1.mi-fds.com/mimocode/mimocode/releases/"

func mimoDistributionRequest(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || r.URL.Host != "mimocode.cnbj1.mi-fds.com" || len(via) > 3 {
			return errors.New("MiMo 官方下载重定向无效")
		}
		return nil
	}}
	return client.Do(req)
}

func extractMimoBinary(archive, directory string) (string, error) {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return "", err
	}
	defer z.Close()
	var binary *zip.File
	for _, f := range z.File {
		if !filepath.IsLocal(filepath.FromSlash(f.Name)) || strings.ContainsAny(f.Name, `\:`) || f.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("MiMo 安装包路径无效")
		}
		if !f.FileInfo().IsDir() {
			if f.Name != "mimo.exe" || binary != nil || f.UncompressedSize64 == 0 || f.UncompressedSize64 > 400*1024*1024 {
				return "", errors.New("MiMo 安装包不是预期的单个可执行程序")
			}
			binary = f
		}
	}
	if binary == nil {
		return "", errors.New("MiMo 安装包缺少程序")
	}
	in, err := binary.Open()
	if err != nil {
		return "", err
	}
	defer in.Close()
	path := filepath.Join(directory, "mimo.exe")
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(out, io.LimitReader(in, int64(binary.UncompressedSize64)+1))
	closeErr := out.Close()
	if err != nil || closeErr != nil || n != int64(binary.UncompressedSize64) {
		return "", errors.New("MiMo 安装包解压失败")
	}
	return path, nil
}

func installManagedMimo(ctx context.Context, directory string, progress func(string)) (string, error) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return "", errors.New("此入口支持 Windows x64；远程环境使用对应原生安装")
	}
	progress("正在读取小米官方 MiMo Code 二进制版本（不使用旧 npm alpha 包）…")
	res, err := mimoDistributionRequest(ctx, mimoDistributionRoot+"latest")
	if err != nil {
		return "", errors.New("无法访问 MiMo 官方版本接口")
	}
	latest, readErr := io.ReadAll(io.LimitReader(res.Body, 100))
	res.Body.Close()
	ver := strings.TrimSpace(string(latest))
	if readErr != nil || res.StatusCode != 200 || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(ver) {
		return "", errors.New("MiMo 官方版本响应无效")
	}
	if err = os.MkdirAll(filepath.Dir(directory), 0700); err != nil {
		return "", err
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		return "", errors.New("MiMo 安装目录已存在或无法创建")
	}
	progress("正在下载官方 Windows x64 baseline 程序…")
	res, err = mimoDistributionRequest(ctx, mimoDistributionRoot+"v"+ver+"/mimocode-windows-x64-baseline.zip")
	if err != nil {
		return "", errors.New("MiMo 官方程序下载失败")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", errors.New("MiMo 官方安装包暂不可用")
	}
	archive := filepath.Join(directory, "mimo.zip")
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	n, copyErr := io.Copy(f, io.LimitReader(res.Body, 400*1024*1024+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n > 400*1024*1024 {
		return "", errors.New("MiMo 下载未完成或超过大小限制")
	}
	path, err := extractMimoBinary(archive, directory)
	if err != nil {
		return "", err
	}
	env := Environment{Type: "windows"}
	got, err := readEngineVersion(ctx, exactEngineProbe, env, path)
	if err != nil || got != ver {
		return "", errors.New("MiMo 官方程序版本验证失败，未切换环境配置")
	}
	return path, nil
}

const remoteMimoInstall = `import sys,os,json,pathlib,urllib.request,zipfile,tarfile,platform,re,subprocess
identifier=sys.argv[1]
if not re.fullmatch(r'[A-Za-z0-9_-]{1,48}',identifier): raise ValueError('invalid id')
base='https://mimocode.cnbj1.mi-fds.com/mimocode/mimocode/releases/'
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs): raise ValueError('redirect denied')
opener=urllib.request.build_opener(NoRedirect)
version=opener.open(base+'latest',timeout=25).read(100).decode().strip()
if not re.fullmatch(r'\d+\.\d+\.\d+',version): raise ValueError('invalid version')
machine=platform.machine().lower()
arch='x64-baseline' if machine in ['x86_64','amd64'] else 'arm64' if machine in ['aarch64','arm64'] else None
if not arch: raise ValueError('unsupported architecture')
if platform.libc_ver()[0]=='musl' or any(pathlib.Path('/lib').glob('ld-musl-*.so.1')): arch+='-musl'
root=pathlib.Path.home()/'.local'/'share'/'duo'/'engine-tools'/identifier
for parent in [root,*root.parents]:
 if parent.is_symlink() or (parent.exists() and not parent.is_dir()): raise ValueError('redirecting directory')
os.umask(0o077);root.parent.mkdir(parents=True,exist_ok=True,mode=0o700);root.mkdir(mode=0o700)
archive=root/'mimo.tar.gz'
with opener.open(base+'v'+version+'/mimocode-linux-'+arch+'.tar.gz',timeout=60) as response,archive.open('xb') as target:
 count=0
 while True:
  part=response.read(1024*1024)
  if not part: break
  count+=len(part)
  if count>419430400: raise ValueError('oversized archive')
  target.write(part)
binary=root/'mimo'
with tarfile.open(archive,'r:gz') as package:
 files=[f for f in package.getmembers() if not f.isdir()]
 if len(files)!=1 or files[0].name not in ['mimo','./mimo'] or not files[0].isfile() or files[0].size>419430400: raise ValueError('unexpected payload')
 with package.extractfile(files[0]) as source,binary.open('xb') as target:
  import shutil
  shutil.copyfileobj(source,target)
binary.chmod(0o700)
got=subprocess.check_output([str(binary),'--version'],timeout=20,stderr=subprocess.DEVNULL).decode().strip()
if got!=version: raise ValueError('version mismatch')
print(json.dumps({'path':str(binary)}))
`

func installRemoteMimo(ctx context.Context, env Environment, id string, progress func(string)) (string, error) {
	if (env.Type != "wsl" && env.Type != "ssh") || !safeWorkbenchID(id) {
		return "", errors.New("MiMo 远程安装目标无效")
	}
	progress("正在目标环境下载官方 MiMo Code 并验证版本；需要 Python 3，不依赖 npm…")
	cmd := commandWithContext(ctx, environmentProbeCommand(env, "sh", "-lc", "exec python3 -c "+posixQuote(remoteMimoInstall)+" "+posixQuote(id)))
	cmd.Stderr = io.Discard
	hideCommand(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("MiMo 目标环境安装失败，请检查连接、Python 3、官方下载站和权限；原有 CLI 未更改")
	}
	var result struct{ Path string }
	if json.Unmarshal(out, &result) != nil || !strings.HasPrefix(result.Path, "/") || !strings.HasSuffix(result.Path, "/duo/engine-tools/"+id+"/mimo") {
		return "", errors.New("MiMo 目标环境返回无效的安装路径")
	}
	return result.Path, nil
}
