// Pull mode with a plain function, TypeScript edition: your agent runs here, Heimdall sends it turns.
//
//   HEIMDALL_URL=https://heimdall.example.com HEIMDALL_TOKEN=hm_... OPENAI_API_KEY=sk-... \
//       node --experimental-strip-types pull/plain-function.ts
//
// Register an agent with adapter pull (pull: {name: support-bot}) and start an
// evaluation on it. Each turn is one OpenAI-compatible chat call with the
// task's tools offered as function tools; Heimdall runs every tool call the
// answer carries through the task's tools and sends the results back as the
// next turn. The model endpoint is yours: OPENAI_BASE_URL, OPENAI_API_KEY,
// AGENT_MODEL. No dependencies (Node 18+). Stop with Ctrl-C.
import { serve, type PullAnswer, type Turn } from "../../typescript/src/index.ts";

const env = (globalThis as unknown as { process: { env: Record<string, string | undefined> } }).process.env;
const AGENT = env.HEIMDALL_AGENT ?? "support-bot";
const BASE_URL = (env.OPENAI_BASE_URL ?? "https://api.openai.com/v1").replace(/\/+$/, "");
const MODEL = env.AGENT_MODEL ?? "gpt-5.4-mini";
const SYSTEM = "You are a helpful support agent. Use the tools when they help, then answer briefly.";

type ChatMessage = { content?: string | null; tool_calls?: Array<{ id: string; function: { name: string; arguments?: string } }> };

async function solve(turn: Turn): Promise<PullAnswer> {
	const tools = turn.openaiTools();
	const resp = await fetch(`${BASE_URL}/chat/completions`, {
		method: "POST",
		headers: { "Content-Type": "application/json", Authorization: `Bearer ${env.OPENAI_API_KEY ?? ""}` },
		body: JSON.stringify({ model: MODEL, messages: [{ role: "system", content: SYSTEM }, ...turn.messages], ...(tools.length ? { tools } : {}) }),
	});
	if (!resp.ok) throw new Error(`model call failed: HTTP ${resp.status} ${(await resp.text()).slice(0, 200)}`);
	const doc = (await resp.json()) as { choices: Array<{ message: ChatMessage }>; usage?: { prompt_tokens?: number; completion_tokens?: number } };
	const msg = doc.choices[0]?.message ?? {};
	return {
		text: msg.content ?? "",
		toolCalls: (msg.tool_calls ?? []).map((c) => ({ id: c.id, name: c.function.name, arguments: JSON.parse(c.function.arguments || "{}") })),
		usage: { input_tokens: doc.usage?.prompt_tokens ?? 0, output_tokens: doc.usage?.completion_tokens ?? 0 },
	};
}

const handled = await serve(AGENT, solve, { concurrency: Number(env.CONCURRENCY ?? "4"), log: (m) => console.error(m) });
console.error(`handled ${handled} turns`);
