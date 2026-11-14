// Offline unit tests for agents/images/acme-agent/agent.mjs: the agent runs
// as a child process against the scripted server in fake-chat-server.mjs.
// No model, no API key, no network beyond loopback.
//
//   node --test tools/acme-agent.test.mjs
//
// AGENT=/path/to/agent.mjs tests another copy (the agent image runs it at
// /opt/acme-agent/agent.mjs).

import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { startFakeChatServer, toolCall } from './fake-chat-server.mjs';

const AGENT = process.env.AGENT ?? fileURLToPath(new URL('../agents/images/acme-agent/agent.mjs', import.meta.url));

/** A fresh workspace with project notes, and an instruction file outside it. */
function setup() {
  const root = mkdtempSync(join(tmpdir(), 'acme-agent-test-'));
  const workspace = join(root, 'workspace');
  const instruction = join(root, 'instruction.md');
  mkdirSync(workspace);
  writeFileSync(instruction, 'Write notes/hello.txt saying hi.\n');
  writeFileSync(join(workspace, 'AGENTS.md'), 'Project rule: keep files small.\n');
  return { root, workspace, instruction };
}

/** Run the agent with the bench's environment contract; resolves { code, stdout, stderr }. */
function runAgent(url, paths, extraEnv = {}) {
  const env = {
    PATH: process.env.PATH,
    BENCH_BASE_URL: `${url}/v1`,
    BENCH_API_KEY: 'test-run-key',
    BENCH_MODEL: 'anthropic/test-model',
    BENCH_INSTRUCTION_FILE: paths.instruction,
    BENCH_SESSION_ID: 'sess-123',
    BENCH_MAX_TURNS: '10',
    BENCH_SHELL_TIMEOUT_S: '20',
    BENCH_WORKSPACE: paths.workspace,
    ...extraEnv,
  };
  return new Promise((ok, fail) => {
    const child = spawn(process.execPath, [AGENT], { env, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => (stdout += d));
    child.stderr.on('data', (d) => (stderr += d));
    child.on('error', fail);
    child.on('close', (code) => ok({ code, stdout, stderr }));
  });
}

/** Split stdout into the JSON step lines and the plain final text. */
function parseOutput(stdout) {
  const lines = stdout.trimEnd().split('\n');
  const events = [];
  let i = 0;
  for (; i < lines.length; i++) {
    try {
      events.push(JSON.parse(lines[i]));
    } catch {
      break;
    }
  }
  return { events, tail: lines.slice(i).join('\n') };
}

const toolMessages = (request) => request.body.messages.filter((m) => m.role === 'tool').map((m) => m.content);

describe('acme-agent', () => {
  test('runs scripted tool calls, sends the bench headers and prints a final message', async () => {
    const paths = setup();
    const server = await startFakeChatServer([
      {
        content: 'Writing the file.',
        tool_calls: [
          toolCall('write_file', { path: 'notes/hello.txt', content: 'hi from the model\n' }, 'c1'),
          toolCall('run_shell', { command: 'cat notes/hello.txt; echo "key=${BENCH_API_KEY:-unset}"' }, 'c2'),
        ],
      },
      {
        tool_calls: [
          toolCall('read_file', { path: 'notes/hello.txt' }, 'c3'),
          toolCall('write_file', { path: '../escape.txt', content: 'x' }, 'c4'),
          toolCall('delete_everything', {}, 'c5'),
        ],
      },
      { content: 'Done: wrote notes/hello.txt.' },
    ]);
    try {
      const r = await runAgent(server.url, paths);
      assert.equal(r.code, 0, r.stderr);
      assert.equal(server.requests.length, 3);
      for (const req of server.requests) {
        assert.equal(req.method, 'POST');
        assert.equal(req.path, '/v1/chat/completions');
        assert.equal(req.headers.authorization, 'Bearer test-run-key');
        assert.equal(req.headers['x-bf-session-id'], 'sess-123');
        assert.equal(req.headers['x-bf-session-affinity'], 'off');
        assert.match(req.headers['content-type'], /^application\/json/);
        assert.equal(req.body.model, 'anthropic/test-model');
        assert.deepEqual(req.body.tools.map((t) => t.function.name), ['run_shell', 'read_file', 'write_file']);
      }
      const [first, second, third] = server.requests;
      assert.equal(first.body.messages[0].role, 'system');
      assert.match(first.body.messages[0].content, /Project rule: keep files small\./);
      assert.deepEqual(first.body.messages[1], { role: 'user', content: 'Write notes/hello.txt saying hi.\n' });

      const [wrote, shell] = toolMessages(second);
      assert.equal(wrote, 'wrote 18 characters to notes/hello.txt');
      assert.match(shell, /^exit 0\nhi from the model\nkey=unset\n$/);
      const assistant = second.body.messages.find((m) => m.role === 'assistant');
      assert.deepEqual(assistant.tool_calls.map((c) => c.id), ['c1', 'c2']);

      const [read, escape, unknown] = toolMessages(third).slice(2);
      assert.equal(read, 'hi from the model\n');
      assert.match(escape, /^refused: \.\.\/escape\.txt is outside /);
      assert.equal(unknown, 'unknown tool delete_everything');

      assert.equal(readFileSync(join(paths.workspace, 'notes/hello.txt'), 'utf8'), 'hi from the model\n');
      assert.equal(existsSync(join(paths.root, 'escape.txt')), false);

      const { events, tail } = parseOutput(r.stdout);
      assert.deepEqual(events.map((e) => e.type), ['step', 'tool_result', 'tool_result', 'step', 'tool_result', 'tool_result', 'tool_result', 'step', 'final']);
      assert.deepEqual(events[0].tool_calls, [{ id: 'c1', name: 'write_file' }, { id: 'c2', name: 'run_shell' }]);
      assert.deepEqual(events[0].usage, { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 });
      assert.deepEqual(events.at(-1), { type: 'final', turns: 3 });
      assert.equal(tail, 'Done: wrote notes/hello.txt.');
    } finally {
      await server.close();
    }
  });

  test('stops with exit 1 and no success claim when the model is unavailable', async () => {
    const paths = setup();
    const server = await startFakeChatServer([{ status: 503, body: { error: { message: 'model unavailable' } } }]);
    try {
      const r = await runAgent(server.url, paths);
      assert.equal(r.code, 1);
      assert.equal(server.requests.length, 1);
      const { events, tail } = parseOutput(r.stdout);
      assert.equal(events.length, 1);
      assert.equal(events[0].type, 'error');
      assert.equal(events[0].turn, 1);
      assert.match(events[0].error, /^HTTP 503/);
      assert.match(events[0].detail, /model unavailable/);
      assert.match(tail, /^Stopped: model call failed on turn 1: HTTP 503/);
    } finally {
      await server.close();
    }
  });

  test('stops at the turn cap', async () => {
    const paths = setup();
    const server = await startFakeChatServer([{ tool_calls: [toolCall('run_shell', { command: 'true' })] }]);
    try {
      const r = await runAgent(server.url, paths, { BENCH_MAX_TURNS: '2' });
      assert.equal(r.code, 0, r.stderr);
      assert.equal(server.requests.length, 2);
      assert.equal(parseOutput(r.stdout).tail, 'Stopped: reached the turn cap of 2 without a final answer.');
    } finally {
      await server.close();
    }
  });

  test('a shell command that runs too long is stopped', async () => {
    const paths = setup();
    const server = await startFakeChatServer([
      { tool_calls: [toolCall('run_shell', { command: 'sleep 5; echo late' }, 's1')] },
      { content: 'Gave up waiting.' },
    ]);
    try {
      const started = Date.now();
      const r = await runAgent(server.url, paths, { BENCH_SHELL_TIMEOUT_S: '1' });
      assert.equal(r.code, 0, r.stderr);
      assert.ok(Date.now() - started < 4500, 'the command was not stopped');
      assert.deepEqual(toolMessages(server.requests[1]), ['timed out after 1s']);
    } finally {
      await server.close();
    }
  });

  test('refuses to start without the bench environment', async () => {
    const paths = setup();
    const r = await runAgent('http://127.0.0.1:9', paths, { BENCH_BASE_URL: '', BENCH_API_KEY: '' });
    assert.equal(r.code, 2);
    assert.equal(r.stdout.trim(), 'Stopped: missing environment: BENCH_BASE_URL, BENCH_API_KEY');
  });
});
