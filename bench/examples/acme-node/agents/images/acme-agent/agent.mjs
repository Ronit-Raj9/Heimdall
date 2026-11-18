#!/usr/bin/env node
// acme-agent: Acme's minimal coding agent, one dependency-free Node.js file.
//
// It follows the three rules every Heimdall (bifrost-bench) agent follows:
//   1. Read the task from the instruction file (BENCH_INSTRUCTION_FILE).
//   2. Edit /workspace, then exit. The bench grades the diff, not the output.
//   3. Send every model call to BENCH_BASE_URL with BENCH_API_KEY, the run's
//      Bifrost virtual key. Nothing else is reachable from the container.
//
// It speaks OpenAI chat completions with tool calling to Bifrost's
// /openai/v1 route, so any provider Bifrost has configured can serve it
// (model ids are written provider/model, e.g. anthropic/claude-sonnet-4-5).
// The environment and headers are the ones the custom-python template uses,
// so gateway evidence (session id, run key) works the same way.
//
// stdout is one JSON line per step, then the final message as plain text, so
// the bench can read a completion claim from the tail of stdout without a
// parser. Exit codes: 0 when the model stopped or the turn cap was reached,
// 1 when a model call failed, 2 when the environment is incomplete.

import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { dirname, join, relative, resolve, sep } from 'node:path';

const env = process.env;
// A run with realism neutral names the bench's variables AGENT_* instead of
// BENCH_* (docs/eval-awareness.md); the neutral name is read first.
const benchEnv = (name) => env[`AGENT_${name}`] || env[`BENCH_${name}`];
const BASE_URL = (benchEnv('BASE_URL') ?? '').replace(/\/+$/, '');
const API_KEY = benchEnv('API_KEY') ?? '';
const MODEL = benchEnv('MODEL') ?? '';
const INSTRUCTION_FILE = benchEnv('INSTRUCTION_FILE') || '/tmp/bench/instruction.md';
const SESSION_ID = benchEnv('SESSION_ID') ?? '';
const MAX_TURNS = positiveInt(benchEnv('MAX_TURNS'), 40);
const SHELL_TIMEOUT_S = positiveInt(benchEnv('SHELL_TIMEOUT_S'), 120);
// The bench always uses /workspace; the override exists for the offline tests.
const WORKSPACE = resolve(benchEnv('WORKSPACE') || '/workspace');
const NOTES_FILE = 'AGENTS.md'; // the project notes file this agent reads (notes.project_file)
const OUTPUT_MAX = 8000; // characters of shell output sent back to the model
const READ_MAX = 20000; // characters of a file sent back to the model
const REQUEST_TIMEOUT_MS = 600000;

const TOOLS = [
  {
    type: 'function',
    function: {
      name: 'run_shell',
      description: 'Run a shell command in /workspace and return its exit code and output.',
      parameters: { type: 'object', properties: { command: { type: 'string' } }, required: ['command'] },
    },
  },
  {
    type: 'function',
    function: {
      name: 'read_file',
      description: 'Read a whole text file. The path is relative to /workspace.',
      parameters: { type: 'object', properties: { path: { type: 'string' } }, required: ['path'] },
    },
  },
  {
    type: 'function',
    function: {
      name: 'write_file',
      description: 'Write a whole file, creating folders as needed. The path is relative to /workspace.',
      parameters: {
        type: 'object',
        properties: { path: { type: 'string' }, content: { type: 'string' } },
        required: ['path', 'content'],
      },
    },
  },
];

const SYSTEM =
  'You are a coding agent working in the repository at /workspace. Use read_file to read files, run_shell to ' +
  'inspect the repository and run tests, and write_file to change files. When the task is finished, reply with ' +
  'a short summary and no tool call.';

process.exitCode = await main();

async function main() {
  const missing = ['BASE_URL', 'API_KEY', 'MODEL'].filter((name) => !benchEnv(name)).map((name) => `BENCH_${name}`);
  if (missing.length > 0) {
    console.log(`Stopped: missing environment: ${missing.join(', ')}`);
    return 2;
  }
  const messages = initialMessages();
  for (let turn = 1; turn <= MAX_TURNS; turn++) {
    let resp;
    try {
      resp = await chat(messages);
    } catch (err) {
      emit({ type: 'error', turn, error: err.message, detail: err.detail ?? '' });
      console.log(`Stopped: model call failed on turn ${turn}: ${err.message}`);
      return 1;
    }
    const msg = resp?.choices?.[0]?.message ?? {};
    const calls = Array.isArray(msg.tool_calls) ? msg.tool_calls : [];
    emit({
      type: 'step',
      turn,
      text: msg.content ?? '',
      usage: resp?.usage ?? null,
      tool_calls: calls.map((c) => ({ id: c.id, name: c.function?.name })),
    });
    messages.push({ role: 'assistant', content: msg.content ?? null, ...(calls.length > 0 ? { tool_calls: calls } : {}) });
    if (calls.length === 0) {
      emit({ type: 'final', turns: turn });
      console.log(msg.content ?? '');
      return 0;
    }
    for (const call of calls) {
      const name = call.function?.name;
      const result = callTool(name, call.function?.arguments);
      emit({ type: 'tool_result', turn, id: call.id, name, output: result.slice(0, 200) });
      messages.push({ role: 'tool', tool_call_id: call.id, content: result });
    }
  }
  console.log(`Stopped: reached the turn cap of ${MAX_TURNS} without a final answer.`);
  return 0;
}

/** One model call. Retries are left to the gateway's policy, so a fault reaches the agent. */
async function chat(messages) {
  const headers = { Authorization: `Bearer ${API_KEY}`, 'Content-Type': 'application/json' };
  if (SESSION_ID) {
    headers['x-bf-session-id'] = SESSION_ID;
    headers['x-bf-session-affinity'] = 'off';
  }
  const res = await fetch(`${BASE_URL}/chat/completions`, {
    method: 'POST',
    headers,
    body: JSON.stringify({ model: MODEL, messages, tools: TOOLS }),
    signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
  });
  const text = await res.text();
  if (!res.ok) {
    const err = new Error(`HTTP ${res.status} ${res.statusText}`.trim());
    err.detail = text.slice(0, 500);
    throw err;
  }
  try {
    return JSON.parse(text);
  } catch {
    const err = new Error('response is not JSON');
    err.detail = text.slice(0, 500);
    throw err;
  }
}

function callTool(name, rawArgs) {
  try {
    const args = JSON.parse(rawArgs || '{}');
    switch (name) {
      case 'run_shell':
        return runShell(text(args.command, 'command'));
      case 'read_file':
        return readFile(text(args.path, 'path'));
      case 'write_file':
        return writeFile(text(args.path, 'path'), text(args.content, 'content'));
      default:
        return `unknown tool ${name}`;
    }
  } catch (err) {
    return `tool error: ${err.message}`;
  }
}

function runShell(command) {
  // The run key stays out of the commands' environment: the model's commands never need it.
  const { BENCH_API_KEY: _key, AGENT_API_KEY: _neutralKey, ...childEnv } = env;
  const r = spawnSync('/bin/sh', ['-c', command], {
    cwd: WORKSPACE,
    env: childEnv,
    encoding: 'utf8',
    timeout: SHELL_TIMEOUT_S * 1000,
    killSignal: 'SIGKILL',
    maxBuffer: 64 * 1024 * 1024,
  });
  if (r.error?.code === 'ETIMEDOUT') return `timed out after ${SHELL_TIMEOUT_S}s`;
  if (r.error) return `tool error: ${r.error.message}`;
  const status = r.status ?? `signal ${r.signal}`;
  return `exit ${status}\n${r.stdout}${r.stderr}`.slice(-OUTPUT_MAX);
}

function readFile(path) {
  const full = insideWorkspace(path);
  if (!full) return `refused: ${path} is outside ${WORKSPACE}`;
  const content = readFileSync(full, 'utf8');
  if (content.length <= READ_MAX) return content;
  return `${content.slice(0, READ_MAX)}\n[truncated: ${content.length - READ_MAX} more characters]`;
}

function writeFile(path, content) {
  const full = insideWorkspace(path);
  if (!full) return `refused: ${path} is outside ${WORKSPACE}`;
  mkdirSync(dirname(full), { recursive: true });
  writeFileSync(full, content);
  return `wrote ${content.length} characters to ${path}`;
}

// The absolute path for a workspace-relative path, or null when it leaves the
// workspace, symbolic links included (the deepest existing folder is resolved).
function insideWorkspace(path) {
  const root = realpathSync(WORKSPACE);
  const full = resolve(root, path);
  let existing = full;
  while (!existsSync(existing)) existing = dirname(existing);
  const real = join(realpathSync(existing), relative(existing, full));
  return real === root || real.startsWith(root + sep) ? real : null;
}

function initialMessages() {
  const task = readFileSync(INSTRUCTION_FILE, 'utf8');
  let system = SYSTEM;
  const notes = join(WORKSPACE, NOTES_FILE);
  if (existsSync(notes)) system += `\n\nProject notes from ${NOTES_FILE}:\n${readFileSync(notes, 'utf8')}`;
  return [
    { role: 'system', content: system },
    { role: 'user', content: task },
  ];
}

function emit(obj) {
  console.log(JSON.stringify(obj));
}

function text(value, name) {
  if (typeof value !== 'string') throw new TypeError(`${name} must be a string`);
  return value;
}

function positiveInt(value, fallback) {
  const n = Number.parseInt(value ?? '', 10);
  return Number.isInteger(n) && n > 0 ? n : fallback;
}
