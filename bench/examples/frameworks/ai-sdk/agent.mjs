#!/usr/bin/env node
// A small coding agent built with the Vercel AI SDK (Node).
//
// It is written the way the AI SDK docs show a tool-calling agent:
// generateText with tools defined by tool({ description, inputSchema,
// execute }) and zod schemas, and stopWhen: stepCountIs(n) as the step cap.
// The model comes from the OpenAI provider (@ai-sdk/openai), whose default
// instance reads OPENAI_BASE_URL and OPENAI_API_KEY, so pointing those at any
// compatible server (the bench points them at its Bifrost gateway) is all it
// takes. openai(model) uses the OpenAI Responses API, the provider's default.
// Nothing here knows about Heimdall or imports it.
//
// Telemetry: the AI SDK sends nothing by itself (experimental_telemetry is
// off unless asked for). The model is a provider instance, never a plain
// string, so the Vercel AI Gateway (the SDK's default for a string id) is
// never used.
//
// Usage: fw-agent <task-file>
// Env:   OPENAI_BASE_URL, OPENAI_API_KEY      the endpoint and its key
//        AGENT_MODEL       model id sent to the endpoint (default gpt-5.4-mini)
//        AGENT_MAX_STEPS   step cap: model turns (default 40)
//        AGENT_SESSION_ID  optional; sent as the x-bf-session-id header
//        BENCH_GATEWAY_HEADERS optional JSON object; extra headers on every model call
// Prints the final answer last on stdout. Exit 0 when the run ends (solved
// or not, step cap included), 1 when the model call fails.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { createOpenAI } from '@ai-sdk/openai';
import { generateText, stepCountIs, tool } from 'ai';
import { z } from 'zod';

const WORKSPACE = '/workspace';
const MODEL = process.env.AGENT_MODEL || 'gpt-5.4-mini';
const MAX_STEPS = parseInt(process.env.AGENT_MAX_STEPS || '40', 10);
const SESSION_ID = process.env.AGENT_SESSION_ID || '';
const COMMAND_TIMEOUT_MS = 120_000;
const OUTPUT_MAX = 8000;
const SYSTEM =
  'You are a coding agent working in the repository at /workspace. Use the tools to look at files, ' +
  'change them and run the tests. When the task is finished, reply with a short summary and no tool call.';

function inside(p) {
  const full = path.resolve(WORKSPACE, p || '.');
  const real = fs.existsSync(full) ? fs.realpathSync(full) : full;
  if (real !== WORKSPACE && !real.startsWith(WORKSPACE + '/')) throw new Error(`${p} is outside ${WORKSPACE}`);
  return real;
}

function walk(dir, out) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
    if (out.length >= 500) return;
    const full = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (e.name !== '.git') walk(full, out);
    } else {
      out.push(path.relative(WORKSPACE, full));
    }
  }
}

const tools = {
  list_files: tool({
    description: 'List the files under a directory of the repository (relative to /workspace).',
    inputSchema: z.object({ path: z.string().optional() }),
    execute: async ({ path: p }) => {
      try {
        const out = [];
        walk(inside(p), out);
        return out.join('\n') || '(no files)';
      } catch (e) {
        return `error: ${e.message}`;
      }
    },
  }),
  read_file: tool({
    description: 'Read a text file of the repository (path relative to /workspace).',
    inputSchema: z.object({ path: z.string() }),
    execute: async ({ path: p }) => {
      try {
        return fs.readFileSync(inside(p), 'utf8').slice(0, 20000);
      } catch (e) {
        return `error: ${e.message}`;
      }
    },
  }),
  write_file: tool({
    description: 'Write a whole file (path relative to /workspace), creating folders as needed.',
    inputSchema: z.object({ path: z.string(), content: z.string() }),
    execute: async ({ path: p, content }) => {
      try {
        const full = inside(p);
        fs.mkdirSync(path.dirname(full), { recursive: true });
        fs.writeFileSync(full, content);
        return `wrote ${content.length} characters to ${p}`;
      } catch (e) {
        return `error: ${e.message}`;
      }
    },
  }),
  run_command: tool({
    description: 'Run a shell command in /workspace (for example the tests) and return its exit code and output.',
    inputSchema: z.object({ command: z.string() }),
    execute: async ({ command }) => {
      const env = { ...process.env };
      delete env.OPENAI_API_KEY; // the key never reaches the model's commands
      try {
        const out = execFileSync('/bin/sh', ['-c', command || 'true'], {
          cwd: WORKSPACE, env, timeout: COMMAND_TIMEOUT_MS, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
        });
        return `exit 0\n${out}`.slice(-OUTPUT_MAX);
      } catch (e) {
        if (e.code === 'ETIMEDOUT') return `timed out after ${COMMAND_TIMEOUT_MS / 1000}s`;
        return `exit ${e.status ?? 1}\n${e.stdout ?? ''}${e.stderr ?? ''}`.slice(-OUTPUT_MAX);
      }
    },
  }),
};

function systemPrompt() {
  const notes = path.join(WORKSPACE, 'AGENTS.md');
  return fs.existsSync(notes) ? `${SYSTEM}\n\nProject notes from AGENTS.md:\n${fs.readFileSync(notes, 'utf8')}` : SYSTEM;
}

async function main() {
  const task = fs.readFileSync(process.argv[2] || '/tmp/bench/instruction.md', 'utf8');
  // BENCH_GATEWAY_HEADERS: the run's extra gateway headers (heimdall plugin fault plan and run tags, cache headers).
  const extra = JSON.parse(process.env.BENCH_GATEWAY_HEADERS || '{}');
  const merged = { ...(SESSION_ID ? { 'x-bf-session-id': SESSION_ID, 'x-bf-session-affinity': 'off' } : {}), ...extra };
  const headers = Object.keys(merged).length ? merged : undefined;
  const openai = createOpenAI({ headers }); // reads OPENAI_BASE_URL and OPENAI_API_KEY
  let result;
  try {
    result = await generateText({
      model: openai(MODEL),
      instructions: systemPrompt(),
      prompt: task,
      tools,
      stopWhen: stepCountIs(MAX_STEPS),
    });
  } catch (e) {
    // a failed model call ends the run; the bench reads the exit code
    console.error(`Stopped: model call failed: ${e.name}: ${e.message}`);
    console.log(`Stopped: model call failed: ${e.name}`);
    return 1;
  }
  if (result.finishReason === 'tool-calls') {
    console.log(`Stopped: reached the step cap of ${MAX_STEPS} model turns without a final answer.`);
  } else {
    console.log(result.text);
  }
  return 0;
}

process.exitCode = await main();
