import { cn } from "@/lib/utils";
import { TriangleAlert, Check, Copy, Info, Lightbulb, OctagonAlert, type LucideIcon } from "lucide-react";
import { useState, type ReactNode } from "react";

/**
 * A small, safe markdown renderer for the in-app guides. It builds React elements only (never
 * raw HTML), so a guide cannot inject markup or scripts. Supported: headings, paragraphs, fenced
 * code, lists, block quotes, tables, rules, inline code, bold, italics and links. A link to
 * another guide (foo.md) calls onDocLink so the docs page can open it in place.
 */
export interface MarkdownProps {
	source: string;
	onDocLink?: (name: string, anchor?: string) => void;
	className?: string;
	testId?: string;
}

type Block =
	| { kind: "heading"; level: number; text: string }
	| { kind: "para"; text: string }
	| { kind: "code"; lang: string; text: string }
	| { kind: "list"; ordered: boolean; items: string[] }
	| { kind: "quote"; text: string }
	| { kind: "table"; head: string[]; rows: string[][] }
	| { kind: "rule" };

const LIST_ITEM = /^\s*(?:[-*+]|\d+[.)])\s+(.*)$/;
const ORDERED = /^\s*\d+[.)]\s+/;
const TABLE_SEP = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/;

function splitRow(line: string): string[] {
	let s = line.trim();
	if (s.startsWith("|")) s = s.slice(1);
	if (s.endsWith("|")) s = s.slice(0, -1);
	return s.split("|").map((c) => c.trim());
}

/** Splits the source into blocks. Exported for tests. */
export function parseBlocks(source: string): Block[] {
	const lines = source.replace(/\r\n?/g, "\n").split("\n");
	const blocks: Block[] = [];
	let i = 0;
	while (i < lines.length) {
		const line = lines[i];
		const trimmed = line.trim();
		if (trimmed === "") {
			i++;
			continue;
		}
		const fence = /^(`{3,}|~{3,})\s*([\w+-]*)/.exec(trimmed);
		if (fence) {
			const body: string[] = [];
			i++;
			while (i < lines.length && !lines[i].trim().startsWith(fence[1])) body.push(lines[i++]);
			i++;
			blocks.push({ kind: "code", lang: fence[2] ?? "", text: body.join("\n") });
			continue;
		}
		const heading = /^(#{1,6})\s+(.*?)\s*#*$/.exec(trimmed);
		if (heading) {
			blocks.push({ kind: "heading", level: heading[1].length, text: heading[2] });
			i++;
			continue;
		}
		if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) {
			blocks.push({ kind: "rule" });
			i++;
			continue;
		}
		if (trimmed.startsWith("|") && i + 1 < lines.length && TABLE_SEP.test(lines[i + 1])) {
			const head = splitRow(trimmed);
			const rows: string[][] = [];
			i += 2;
			while (i < lines.length && lines[i].trim().startsWith("|")) rows.push(splitRow(lines[i++]));
			blocks.push({ kind: "table", head, rows });
			continue;
		}
		if (trimmed.startsWith(">")) {
			const body: string[] = [];
			while (i < lines.length && lines[i].trim().startsWith(">")) body.push(lines[i++].trim().replace(/^>\s?/, ""));
			blocks.push({ kind: "quote", text: body.join(" ") });
			continue;
		}
		if (LIST_ITEM.test(line)) {
			const ordered = ORDERED.test(line);
			const items: string[] = [];
			while (i < lines.length && lines[i].trim() !== "") {
				const m = LIST_ITEM.exec(lines[i]);
				if (m) items.push(m[1]);
				else if (items.length > 0) items[items.length - 1] += " " + lines[i].trim();
				i++;
			}
			blocks.push({ kind: "list", ordered, items });
			continue;
		}
		const body: string[] = [];
		while (i < lines.length) {
			const l = lines[i];
			const t = l.trim();
			if (t === "" || /^(#{1,6})\s/.test(t) || /^(`{3,}|~{3,})/.test(t) || t.startsWith(">") || LIST_ITEM.test(l) || t.startsWith("|")) break;
			body.push(t);
			i++;
		}
		if (body.length === 0) {
			// A line no rule took (for example a lone "|"): keep it as text, never loop.
			body.push(trimmed);
			i++;
		}
		blocks.push({ kind: "para", text: body.join(" ") });
	}
	return blocks;
}

/** Heading text as a URL anchor, GitHub style. */
export function slugify(text: string): string {
	return text
		.toLowerCase()
		.replace(/[`*_[\]()]/g, "")
		.replace(/[^\w\s-]/g, "")
		.trim()
		.replace(/\s+/g, "-");
}

/** Only web, mail, in-page and relative links render as links; anything else (javascript:, data:) is text. */
export function safeHref(href: string): string | null {
	const h = href.trim();
	if (/^(https?:|mailto:)/i.test(h)) return h;
	if (/^[a-z][a-z0-9+.-]*:/i.test(h)) return null;
	return h;
}

const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\[[^\]]+\]\([^)\s]+\))|(\*[^*\s][^*]*\*)|(_[^_\s][^_]*_)/g;

function renderInline(text: string, onDocLink: MarkdownProps["onDocLink"], keyBase: string): ReactNode[] {
	const out: ReactNode[] = [];
	let last = 0;
	let n = 0;
	for (const m of text.matchAll(INLINE)) {
		const at = m.index ?? 0;
		if (at > last) out.push(text.slice(last, at));
		const tok = m[0];
		const key = `${keyBase}-${n++}`;
		if (m[1]) {
			out.push(
				<code key={key} className="bg-muted rounded-md border px-1.5 py-0.5 font-mono text-[0.85em]">
					{tok.slice(1, -1)}
				</code>,
			);
		} else if (m[2]) {
			out.push(<strong key={key}>{renderInline(tok.slice(2, -2), onDocLink, key)}</strong>);
		} else if (m[3]) {
			const lm = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(tok);
			const label = lm?.[1] ?? tok;
			const href = safeHref(lm?.[2] ?? "");
			const doc = href ? /^(?:\.\/)?([\w.-]+)\.md(?:#([\w-]+))?$/.exec(href) : null;
			if (doc && onDocLink) {
				out.push(
					<a
						key={key}
						href={`?doc=${doc[1]}`}
						className="text-primary underline underline-offset-2"
						onClick={(e) => {
							e.preventDefault();
							onDocLink(doc[1], doc[2]);
						}}
					>
						{label}
					</a>,
				);
			} else if (href) {
				const external = /^https?:/i.test(href);
				out.push(
					<a
						key={key}
						href={href}
						className="text-primary underline underline-offset-2"
						{...(external ? { target: "_blank", rel: "noreferrer noopener" } : {})}
					>
						{label}
					</a>,
				);
			} else {
				out.push(label);
			}
		} else {
			out.push(<em key={key}>{tok.slice(1, -1)}</em>);
		}
		last = at + tok.length;
	}
	if (last < text.length) out.push(text.slice(last));
	return out;
}

const HEADING_CLASS = [
	"text-3xl font-semibold tracking-tight mb-2 scroll-mt-20",
	"text-xl font-semibold tracking-tight mt-8 border-b pb-2 scroll-mt-20",
	"text-base font-semibold mt-6 scroll-mt-20",
	"text-sm font-semibold mt-4 scroll-mt-20",
	"text-sm font-semibold mt-3 scroll-mt-20",
	"text-sm font-medium mt-3 scroll-mt-20",
];

type CalloutKind = "note" | "tip" | "warning" | "danger";

const CALLOUT_WORDS: Record<string, CalloutKind> = {
	note: "note",
	info: "note",
	important: "note",
	tip: "tip",
	hint: "tip",
	warning: "warning",
	caution: "danger",
	danger: "danger",
};

const CALLOUT_STYLE: Record<CalloutKind, { icon: LucideIcon; box: string; ink: string }> = {
	note: { icon: Info, box: "border-sky-500/30 bg-sky-500/5", ink: "text-sky-700 dark:text-sky-400" },
	tip: { icon: Lightbulb, box: "border-emerald-500/30 bg-emerald-500/5", ink: "text-emerald-700 dark:text-emerald-400" },
	warning: { icon: TriangleAlert, box: "border-amber-500/40 bg-amber-500/5", ink: "text-amber-700 dark:text-amber-400" },
	danger: { icon: OctagonAlert, box: "border-red-500/30 bg-red-500/5", ink: "text-red-700 dark:text-red-400" },
};

/** A quote that opens with [!NOTE], **Note:** or Warning: becomes a callout. Exported for tests. */
export function parseCallout(text: string): { kind: CalloutKind; label: string; body: string } | null {
	const m = /^(?:\[!(\w+)\]|\*\*(\w+):?\*\*:?|(\w+):)\s*(.*)$/s.exec(text.trim());
	if (!m) return null;
	const word = (m[1] ?? m[2] ?? m[3] ?? "").toLowerCase();
	const kind = CALLOUT_WORDS[word];
	if (!kind) return null;
	return { kind, label: word.charAt(0).toUpperCase() + word.slice(1), body: m[4] ?? "" };
}

function CodeBlock({ lang, text }: { lang: string; text: string }) {
	const [copied, setCopied] = useState(false);
	const copy = () => {
		void navigator.clipboard?.writeText(text).then(() => {
			setCopied(true);
			setTimeout(() => setCopied(false), 1500);
		});
	};
	return (
		<div className="group bg-muted/50 relative min-w-0 overflow-hidden rounded-lg border">
			<div className="text-muted-foreground flex items-center justify-between border-b px-3 py-1 text-[11px]">
				<span className="font-mono tracking-wide">{lang || "text"}</span>
				<button
					type="button"
					onClick={copy}
					className="hover:bg-accent hover:text-foreground focus-visible:ring-ring/60 flex items-center gap-1 rounded px-1.5 py-0.5 transition-colors focus-visible:ring-2 focus-visible:outline-none"
					aria-label={copied ? "Copied" : "Copy code"}
					data-testid="code-copy"
				>
					{copied ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
					{copied ? "Copied" : "Copy"}
				</button>
			</div>
			<pre className="overflow-x-auto p-4 font-mono text-xs leading-5">
				<code>{text}</code>
			</pre>
		</div>
	);
}

export function Markdown({ source, onDocLink, className, testId = "markdown" }: MarkdownProps) {
	const blocks = parseBlocks(source);
	return (
		<article className={cn("text-foreground/90 flex min-w-0 flex-col gap-4 text-[15px] leading-7", className)} data-testid={testId}>
			{blocks.map((b, i) => {
				const k = `b${i}`;
				switch (b.kind) {
					case "heading": {
						const Tag = `h${b.level}` as "h1";
						return (
							<Tag key={k} id={slugify(b.text)} className={HEADING_CLASS[b.level - 1]}>
								{renderInline(b.text, onDocLink, k)}
							</Tag>
						);
					}
					case "code":
						return <CodeBlock key={k} lang={b.lang} text={b.text} />;
					case "list": {
						const Tag = b.ordered ? "ol" : "ul";
						return (
							<Tag key={k} className={cn("marker:text-muted-foreground flex flex-col gap-1.5 pl-6", b.ordered ? "list-decimal" : "list-disc")}>
								{b.items.map((it, j) => (
									<li key={j}>{renderInline(it, onDocLink, `${k}-${j}`)}</li>
								))}
							</Tag>
						);
					}
					case "quote": {
						const c = parseCallout(b.text);
						if (c) {
							const st = CALLOUT_STYLE[c.kind];
							const Icon = st.icon;
							return (
								<aside key={k} className={cn("flex gap-3 rounded-lg border px-4 py-3", st.box)} data-callout={c.kind}>
									<Icon className={cn("mt-1 size-4 shrink-0", st.ink)} aria-hidden="true" />
									<div className="min-w-0">
										<p className={cn("text-xs font-semibold tracking-wide uppercase", st.ink)}>{c.label}</p>
										<p>{renderInline(c.body, onDocLink, k)}</p>
									</div>
								</aside>
							);
						}
						return (
							<blockquote key={k} className="text-muted-foreground border-primary/40 border-l-4 py-0.5 pl-4 italic">
								{renderInline(b.text, onDocLink, k)}
							</blockquote>
						);
					}
					case "table":
						return (
							<div key={k} className="overflow-x-auto rounded-lg border">
								<table className="w-full text-left text-[13px]">
									<thead className="bg-muted/60 text-muted-foreground text-xs">
										<tr>
											{b.head.map((h, j) => (
												<th key={j} className="px-3 py-2 font-medium">
													{renderInline(h, onDocLink, `${k}-h${j}`)}
												</th>
											))}
										</tr>
									</thead>
									<tbody>
										{b.rows.map((r, ri) => (
											<tr key={ri} className="hover:bg-muted/30 border-t transition-colors">
												{r.map((c, ci) => (
													<td key={ci} className="px-3 py-2 align-top">
														{renderInline(c, onDocLink, `${k}-${ri}-${ci}`)}
													</td>
												))}
											</tr>
										))}
									</tbody>
								</table>
							</div>
						);
					case "rule":
						return <hr key={k} className="my-4" />;
					default:
						return <p key={k}>{renderInline(b.text, onDocLink, k)}</p>;
				}
			})}
		</article>
	);
}

export default Markdown;
