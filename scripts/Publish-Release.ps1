[CmdletBinding()]
param([string]$Go='go',[string]$GitHubCLI='gh',[string]$Notes='')
$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot
Push-Location $root
try {
    $package=Get-Content -Raw package.json | ConvertFrom-Json
    $tag='v'+$package.version
    if($tag -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$'){throw 'Invalid stable version'}
    if(!$Notes){$Notes=Join-Path $root ('docs\releases\'+$tag+'.md')}
    if(!(Test-Path -LiteralPath $Notes)){throw 'Release notes are required'}
    $status=& git status --porcelain
    if($LASTEXITCODE -ne 0 -or $status){throw 'Commit source changes before publishing'}
    $sourceHead=& git rev-parse HEAD
    if($LASTEXITCODE -ne 0){throw 'Cannot resolve source commit'}
    $repoJSON=& $GitHubCLI repo view --json nameWithOwner,isPrivate
    if($LASTEXITCODE -ne 0){throw 'GitHub authentication or repository lookup failed'}
    $repo=$repoJSON | ConvertFrom-Json
    if($repo.isPrivate){throw 'This update channel expects a public repository'}
    $updateSource=Get-Content -Raw updates.go
    if($updateSource -notmatch 'var releaseRepository = "([^"]+)"' -or $Matches[1] -ine $repo.nameWithOwner){throw 'Set releaseRepository in updates.go to this public repository before publishing'}
    # Windows PowerShell turns native stderr into terminating errors under Stop,
    # including gh's expected "release not found" for a new version.
    $previousErrorPreference=$ErrorActionPreference
    try {
        $ErrorActionPreference='Continue'
        $existing=& $GitHubCLI release view $tag --repo $repo.nameWithOwner --json tagName 2>$null
        $releaseLookupExitCode=$LASTEXITCODE
    } finally { $ErrorActionPreference=$previousErrorPreference }
    if($releaseLookupExitCode -eq 0){throw 'Release already exists; use a new version instead of overwriting'}
    & scripts/Build-Portable.ps1 -Go $Go
    if($LASTEXITCODE -ne 0){throw 'Build failed'}
    $builtStatus=& git status --porcelain
    if($LASTEXITCODE -ne 0 -or $builtStatus){throw 'Build changed source or generated assets; review and commit them before publishing'}
    $service=Join-Path $root 'dist\Jianzuo-portable-windows-x64\jianzuo-service.exe'
    # windowsgui executables do not reliably attach stdout to a PowerShell
    # expression; explicitly pipe the child output for this release guard.
    & node --input-type=module -e "import {spawnSync} from 'node:child_process'; const r=spawnSync(process.argv[1],['--version'],{encoding:'utf8',windowsHide:true}); if(r.error||r.status!==0||r.stdout.trim()!==process.argv[2]){console.error('Packaged service version differs from release version');process.exit(1)} console.log('Packaged service version: '+r.stdout.trim());" $service ($package.version+'-portable')
    if($LASTEXITCODE -ne 0){throw 'Packaged service version validation failed'}
    & node scripts/Test-Codex-Native-Build.mjs $service
    if($LASTEXITCODE -ne 0){throw 'Packaged HTTP acceptance failed; no release created'}
    $head=& git rev-parse HEAD
    if($LASTEXITCODE -ne 0 -or $head -ne $sourceHead){throw 'Source commit changed during build; retry from a stable checkout'}
    $branch=& git branch --show-current
    if(!$branch){throw 'A named branch is required'}
    & git push origin $branch
    if($LASTEXITCODE -ne 0){throw 'Push failed; no release created'}
    & git show-ref --verify --quiet ('refs/tags/'+$tag)
    if($LASTEXITCODE -ne 0){& git tag -a $tag -m ('Jianzuo '+$tag);if($LASTEXITCODE -ne 0){throw 'Tag creation failed'}}else{$tagCommit=& git rev-parse ($tag+'^{commit}');if($LASTEXITCODE -ne 0 -or $tagCommit -ne $head){throw 'Tag refers to a different commit'}}
    & git push origin $tag
    if($LASTEXITCODE -ne 0){throw 'Tag push failed'}
    $zip=Join-Path $root 'dist\Jianzuo-portable-windows-x64.zip'
    $installer=Join-Path $root 'dist\Jianzuo-Setup-User-x64.exe'
    foreach($path in @($zip,($zip+'.sha256'),$installer,($installer+'.sha256'))){if(!(Test-Path -LiteralPath $path -PathType Leaf)){throw ('Release asset is missing: '+$path)}}
    & $GitHubCLI release create $tag $zip ($zip+'.sha256') $installer ($installer+'.sha256') --repo $repo.nameWithOwner --verify-tag --draft --title ('简作 '+$tag) --notes-file $Notes
    if($LASTEXITCODE -ne 0){throw 'Draft upload failed; inspect the draft before retrying'}
    $releaseJSON=& $GitHubCLI release view $tag --repo $repo.nameWithOwner --json assets,isDraft
    if($LASTEXITCODE -ne 0){throw 'Cannot verify draft release'}
    $release=$releaseJSON | ConvertFrom-Json
    foreach($path in @($zip,($zip+'.sha256'),$installer,($installer+'.sha256'))){$file=Get-Item -LiteralPath $path;$asset=@($release.assets | Where-Object name -EQ $file.Name);if($asset.Count -ne 1 -or $asset[0].size -ne $file.Length){throw 'Uploaded asset size mismatch; draft left unpublished'}}
    & $GitHubCLI release edit $tag --repo $repo.nameWithOwner --draft=false --latest
    if($LASTEXITCODE -ne 0){throw 'Publish failed; draft remains available'}
    $publishedJSON=& $GitHubCLI release view $tag --repo $repo.nameWithOwner --json url,tagName,isDraft,isPrerelease
    if($LASTEXITCODE -ne 0){throw 'Cannot verify published release'}
    $published=$publishedJSON | ConvertFrom-Json
    if($published.isDraft -or $published.isPrerelease -or $published.tagName -ne $tag){throw 'Release is not a stable published version'}
    $latest=& $GitHubCLI api ('repos/'+$repo.nameWithOwner+'/releases/latest') --jq .tag_name
    if($LASTEXITCODE -ne 0 -or $latest -ne $tag){throw 'Release was published but latest-version verification failed'}
    $publishedJSON
} finally { Pop-Location }
