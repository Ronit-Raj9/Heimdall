import { Brain, Code2, Cpu, FlaskConical, Globe, MessagesSquare, Mic, Monitor, Search, ShieldCheck, SquareTerminal, Users, Wrench, type LucideIcon } from "lucide-react";

// The suite's category icon, guessed from its id: the experiment status carries only the id.
const RULES: [RegExp, LucideIcon][] = [
	[/terminal|shell|bash|cli/, SquareTerminal],
	[/memory|notes|recall/, Brain],
	[/computer|desktop|gui|cua/, Monitor],
	[/browser|web/, Globe],
	[/voice|speech|audio/, Mic],
	[/support|chat|customer/, MessagesSquare],
	[/tool|mcp/, Wrench],
	[/research|search/, Search],
	[/safety|security|guard/, ShieldCheck],
	[/multi|agents|swarm/, Users],
	[/gpu|cuda|kernel/, Cpu],
	[/code|coding|swe|repo/, Code2],
];

export function suiteIcon(suite: string | undefined): LucideIcon {
	const id = (suite ?? "").toLowerCase();
	for (const [rule, icon] of RULES) if (rule.test(id)) return icon;
	return FlaskConical;
}
