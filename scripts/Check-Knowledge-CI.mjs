import {spawnSync} from 'node:child_process';

// Native stderr (including Go dependency-download messages) must not be
// interpreted as a terminating PowerShell 5 error before its exit code exists.
const result=spawnSync('go',['test','./...','-run','TestLibrary|TestVault|TestAutomaticKnowledge|TestSticky|TestScratch|TestWorkspace|TestCodex|TestInterruptFollowup|TestQueue|TestStop|TestHarnessRuntime','-count=1','-timeout=3m'],{encoding:'utf8',windowsHide:true,timeout:600000,maxBuffer:8*1024*1024});
if(result.stdout)process.stdout.write(result.stdout);
if(result.stderr)process.stderr.write(result.stderr);
if(result.status!==0){
 const tail=[result.stdout,result.stderr,result.error?.message].filter(Boolean).join('\n').slice(-1800);
 console.log('::error title=Knowledge regression::'+tail.replaceAll('%','%25').replaceAll('\r','%0D').replaceAll('\n','%0A'));
 process.exitCode=1;
}
