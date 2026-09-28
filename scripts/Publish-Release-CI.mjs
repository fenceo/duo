// Preserve native PowerShell output while making a failed step diagnosable
// through public check annotations. Never include authentication values.
import { spawn } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const powershell = path.join(process.env.SystemRoot || 'C:/Windows', 'System32/WindowsPowerShell/v1.0/powershell.exe');
const secrets = ['GH_TOKEN', 'GITHUB_TOKEN', 'ACTIONS_RUNTIME_TOKEN']
  .map(name => process.env[name]).filter(Boolean);
function redact(text) {
  for (const secret of secrets) text = text.split(secret).join('[REDACTED]');
  return text;
}
function annotation(text) {
  return redact(text).replaceAll('%', '%25').replaceAll('\r', '%0D').replaceAll('\n', '%0A');
}
let tail = '';
function capture(stream, destination) {
  stream.setEncoding('utf8');
  stream.on('data', text => {
    destination.write(text);
    tail = redact(tail + text).slice(-64000);
  });
}
const child = spawn(powershell, ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', path.join(root, 'scripts/Publish-Release.ps1')], {
  cwd: root, env: process.env, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true,
});
capture(child.stdout, process.stdout);
capture(child.stderr, process.stderr);
child.on('error', () => {
  process.stdout.write('::error title=Release process failed::Could not start Windows PowerShell.\n');
  process.exitCode = 1;
});
child.on('close', code => {
  if (code !== 0) {
    const summary = redact(tail).slice(-12000);
    process.stdout.write('\n::error title=Release process failed::' + annotation(summary || 'No release process output was captured.') + '\n');
    process.exitCode = code || 1;
  }
});
