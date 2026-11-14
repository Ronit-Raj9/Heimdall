// A tiny scripted OpenAI-compatible chat completions server, for testing
// agents offline: no model, no network beyond loopback, no API key.
//
//   import { startFakeChatServer, toolCall } from './fake-chat-server.mjs';
//   const server = await startFakeChatServer([
//     { tool_calls: [toolCall('run_shell', { command: 'ls' })] },
//     { content: 'Done.' },
//   ]);
//   // point the agent at `${server.url}/v1`; server.requests records each call
//   await server.close();
//
// Each script entry answers one POST /v1/chat/completions, in order:
//   { content, tool_calls }    a 200 response with that assistant message
//   { status, body }           a raw response (for example a 503 outage)
//   (request) => entry         computed from the recorded request
// After the script runs out, the last entry is repeated.
//
// Run it directly (`node fake-chat-server.mjs [PORT]`) to get a server that
// answers every call with a final message, for poking at an agent by hand.

import { createServer } from 'node:http';
import { pathToFileURL } from 'node:url';

/** An assistant tool call in the OpenAI chat completions shape. */
export function toolCall(name, args, id = `call_${name}_${Math.random().toString(36).slice(2, 8)}`) {
  return { id, type: 'function', function: { name, arguments: JSON.stringify(args) } };
}

/** Start the server on 127.0.0.1 (port 0 picks a free one). */
export async function startFakeChatServer(script, { port = 0 } = {}) {
  const requests = [];
  const server = createServer((req, res) => {
    let raw = '';
    req.setEncoding('utf8');
    req.on('data', (chunk) => {
      raw += chunk;
    });
    req.on('end', () => {
      let body = null;
      try {
        body = JSON.parse(raw);
      } catch {
        body = raw;
      }
      const request = { method: req.method, path: req.url, headers: req.headers, body };
      requests.push(request);
      if (req.method !== 'POST' || req.url !== '/v1/chat/completions') {
        return send(res, 404, { error: { message: `no route ${req.method} ${req.url}` } });
      }
      let entry = script[Math.min(requests.length, script.length) - 1] ?? { content: '' };
      if (typeof entry === 'function') entry = entry(request);
      if (entry.status) return send(res, entry.status, entry.body ?? {});
      const message = { role: 'assistant', content: entry.content ?? null };
      if (entry.tool_calls) message.tool_calls = entry.tool_calls;
      return send(res, 200, {
        id: `chatcmpl-${requests.length}`,
        object: 'chat.completion',
        created: 0,
        model: body?.model ?? 'fake',
        choices: [{ index: 0, message, finish_reason: entry.tool_calls ? 'tool_calls' : 'stop' }],
        usage: { prompt_tokens: 10 * requests.length, completion_tokens: 5, total_tokens: 10 * requests.length + 5 },
      });
    });
  });
  await new Promise((ok) => server.listen(port, '127.0.0.1', ok));
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    requests,
    close: () => new Promise((ok) => server.close(ok)),
  };
}

function send(res, status, body) {
  const data = JSON.stringify(body);
  res.writeHead(status, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(data) });
  res.end(data);
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) {
  const server = await startFakeChatServer([{ content: 'Nothing to do: this is the fake chat server.' }], {
    port: Number(process.argv[2] ?? 0),
  });
  console.log(`fake chat completions at ${server.url}/v1 (Ctrl-C to stop)`);
}
