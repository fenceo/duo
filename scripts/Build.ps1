$ErrorActionPreference = 'Stop'
Push-Location (Join-Path $PSScriptRoot '..')
try {
 npm run check
 if ($LASTEXITCODE -ne 0) { throw 'TypeScript 检查失败' }
 npm run build
 if ($LASTEXITCODE -ne 0) { throw '前端构建失败' }
 go test ./...
 if ($LASTEXITCODE -ne 0) { throw 'Go 测试失败' }
 go build -ldflags '-H windowsgui' -o build/jianzuo.exe .
 if ($LASTEXITCODE -ne 0) { throw 'Go 构建失败' }
} finally { Pop-Location }
