import fs from 'node:fs';
import path from 'node:path';

if (process.env.GITHUB_ACTIONS !== 'true' || !process.env.RUNNER_TEMP || !process.env.GITHUB_ENV) {
  throw new Error('This preparation script requires a GitHub Actions runner.');
}

// Runner TEMP can contain a short-name alias or redirected parent. Security
// fixtures require a physical path, just like user-selected data and vaults.
// Resolve the runner-owned temporary root before creating an isolated folder;
// the runner disposes of it with the rest of RUNNER_TEMP after the job.
const root = fs.realpathSync.native(process.env.RUNNER_TEMP);
const directory = fs.realpathSync.native(fs.mkdtempSync(path.join(root, 'duo-release-')));
for (let current = directory; ; current = path.dirname(current)) {
  const info = fs.lstatSync(current);
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error('Release temporary directory must have only ordinary directory parents.');
  }
  if (path.dirname(current) === current) break;
}
fs.appendFileSync(process.env.GITHUB_ENV, `TEMP=${directory}\nTMP=${directory}\n`, 'utf8');
console.log('Release fixture directory: ' + directory);
