// Shared setup for the hidden tests. Not a test file itself (the verifier
// runs /grader/tests/**/*.test.mjs only).
//
// The grader mounts the workspace under test read-only at /workspace and
// these files at /grader/tests, so the package is imported by absolute path
// through its public entry point, src/index.js. Set ACME_WORKSPACE to run the
// hidden tests against another checkout, for example while writing a task.

import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

export const WORKSPACE = process.env.ACME_WORKSPACE ?? '/workspace';
export const ENTRY = pathToFileURL(join(WORKSPACE, 'src', 'index.js')).href;
export const acme = await import(ENTRY);

/**
 * Run the acme-orders CLI in a fresh temporary folder holding files
 * (name -> text, or a value written as JSON). nodeFlags go before the script.
 */
export function runCli(args, files = {}, nodeFlags = []) {
  const dir = mkdtempSync(join(tmpdir(), 'acme-cli-'));
  for (const [name, content] of Object.entries(files)) {
    writeFileSync(join(dir, name), typeof content === 'string' ? content : JSON.stringify(content));
  }
  return spawnSync(process.execPath, [...nodeFlags, join(WORKSPACE, 'bin', 'acme-orders.js'), ...args], {
    cwd: dir,
    encoding: 'utf8',
    timeout: 60000,
  });
}

/**
 * Run an ES module snippet in a new Node process, with the package's public
 * API in scope as `acme`. Returns { status, stdout, stderr }.
 */
export function runSnippet(body, nodeFlags = []) {
  const source = `import * as acme from ${JSON.stringify(ENTRY)};\n${body}\n`;
  return spawnSync(process.execPath, [...nodeFlags, '--input-type=module', '-e', source], {
    encoding: 'utf8',
    timeout: 60000,
  });
}
