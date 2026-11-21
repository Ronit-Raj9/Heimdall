import { cn } from "@/lib/utils";
import type { ReactNode } from "react";

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
				<code key={key} className="bg-muted rounded px-1 py-0.5 font-mono text-[0.85em]">
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
	"text-2xl font-semibold tracking-tight mt-2",
	"text-xl font-semibold tracking-tight mt-6 border-b pb-1",
	"text-base font-semibold mt-5",
	"text-sm font-semibold mt-4",
	"text-sm font-semibold mt-3",
	"text-sm font-medium mt-3",
];

export function Markdown({ source, onDocLink, className, testId = "markdown" }: MarkdownProps) {
	const blocks = parseBlocks(source);
	return (
		<article className={cn("flex min-w-0 flex-col gap-3 text-sm leading-6", className)} data-testid={testId}>
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
						return (
							<pre key={k} className="bg-muted/70 overflow-x-auto rounded-md border p-3 font-mono text-xs leading-5">
								<code>{b.text}</code>
							</pre>
						);
					case "list": {
						const Tag = b.ordered ? "ol" : "ul";
						return (
							<Tag key={k} className={cn("flex flex-col gap-1 pl-5", b.ordered ? "list-decimal" : "list-disc")}>
								{b.items.map((it, j) => (
									<li key={j}>{renderInline(it, onDocLink, `${k}-${j}`)}</li>
								))}
							</Tag>
						);
					}
					case "quote":
						return (
							<blockquote key={k} className="text-muted-foreground border-l-2 pl-3">
								{renderInline(b.text, onDocLink, k)}
							</blockquote>
						);
					case "table":
						return (
							<div key={k} className="overflow-x-auto rounded-md border">
								<table className="w-full text-left text-xs">
									<thead className="bg-muted/60">
										<tr>
											{b.head.map((h, j) => (
												<th key={j} className="px-2 py-1.5 font-medium">
													{renderInline(h, onDocLink, `${k}-h${j}`)}
												</th>
											))}
										</tr>
									</thead>
									<tbody>
										{b.rows.map((r, ri) => (
											<tr key={ri} className="border-t">
												{r.map((c, ci) => (
													<td key={ci} className="px-2 py-1.5 align-top">
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
						return <hr key={k} className="my-2" />;
					default:
						return <p key={k}>{renderInline(b.text, onDocLink, k)}</p>;
				}
			})}
		</article>
	);
}

export default Markdown;
