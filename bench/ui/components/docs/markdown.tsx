import { BENCH_NAV } from "@/lib/constants/navigation";
import { DOCS_REPO_DIR, REPO_BLOB_URL } from "@/lib/constants/repo";
import i18n from "@/lib/i18n";
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
	/** guide name -> title: a link to another guide whose text is its file name shows the title instead. */
	docTitles?: Record<string, string>;
	className?: string;
	testId?: string;
}

/**
 * Internal design and planning notes (DESIGN, any *-design note, the spike log, the CLI and web
 * parity checklist). The server neither lists nor serves them in-app (docs.Internal in
 * bench/docs/embed.go), so a link to one renders as plain text instead of a dead link.
 */
export function isInternalDoc(name: string): boolean {
	return name === "DESIGN" || name === "spike" || name === "CLI_WEB_PARITY" || name.endsWith("-design");
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
			if (t === "" || /^(#{1,6})\s/.test(t) || /^(`{3,}|~{3,})/.test(t) || t.startsWith(">") || LIST_ITEM.test(l) || t.startsWith("|"))
				break;
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

/**
 * Files outside the docs folder that guides link to with a relative path. The server serves no
 * such path, so each known one opens an in-app guide built from the same file (GET /docs/{name}).
 */
const RELATIVE_DOCS: Record<string, string> = {
	"../spec/agent-event.schema.json": "agent-event-schema",
	"../examples/acme-node/README.md": "example-acme-node",
};

/** The in-app guide a relative "../" link opens, or null when there is none. Exported for tests. */
export function relativeDocTarget(href: string): string | null {
	const path = href.split("#")[0] ?? "";
	return RELATIVE_DOCS[path] ?? null;
}

/**
 * A relative link to a repository file that is not a guide (`../examples/acme-web/README.md`,
 * `probes/tool-poison.yaml`), as the file's address on GitHub: resolved from bench/docs/, anchor
 * kept. Null for guide links (`foo.md`), the in-app files of RELATIVE_DOCS, absolute or scheme
 * links, and paths that leave the repository. Exported for tests (N137).
 */
export function repoFileUrl(href: string): string | null {
	const h = href.trim();
	if (!h || h.startsWith("#") || h.startsWith("/") || /^[a-z][a-z0-9+.-]*:/i.test(h)) return null;
	if (/^(?:\.\/)?[\w.-]+\.md(?:#[\w-]+)?$/.test(h)) return null;
	if (relativeDocTarget(h) !== null) return null;
	const hash = h.indexOf("#");
	const path = hash >= 0 ? h.slice(0, hash) : h;
	const anchor = hash >= 0 ? h.slice(hash) : "";
	const parts = DOCS_REPO_DIR.split("/").filter(Boolean);
	for (const seg of path.split("/")) {
		if (seg === "" || seg === ".") continue;
		if (seg === "..") {
			if (parts.length === 0) return null;
			parts.pop();
		} else {
			parts.push(seg);
		}
	}
	if (parts.length === 0) return null;
	return `${REPO_BLOB_URL}${parts.join("/")}${anchor}`;
}

/** The local server addresses guides use in samples; on a hosted server they point at this server instead. */
const LOCAL_SERVER = /http:\/\/(?:127\.0\.0\.1|localhost):8090(?![0-9])/g;

/**
 * A guide's markdown for a hosted reader: every `http://127.0.0.1:8090` and `http://localhost:8090`
 * becomes this server's origin, with the count of replacements (N136). Exported for tests.
 */
export function hostedDocSource(markdown: string, origin: string): { text: string; replaced: number } {
	let replaced = 0;
	const base = origin.replace(/\/+$/, "");
	const text = markdown.replace(LOCAL_SERVER, () => {
		replaced++;
		return base;
	});
	return { text, replaced };
}
/** First address parts of the app's own pages (the sidebar plus a few not in it). */
const APP_ROUTES = new Set([...BENCH_NAV.map((n) => n.to.split("/")[1]).filter(Boolean), "docs", "settings", "jobs", "usage", "inbox", "files"]);

/**
 * The app address an inline code span names (`/settings?tab=keys`), or null when it is not one of
 * the app's pages (an API path such as `/api/bench/docs` stays code). Exported for tests.
 */
export function appRoute(code: string): string | null {
	const m = /^\/([a-z][a-z0-9-]*)(?:\/[\w./-]*)?(?:\?[\w=&.-]*)?$/.exec(code.trim());
	return m && APP_ROUTES.has(m[1]) ? code.trim() : null;
}

/** A link label that is just the target's file name ("web-keys-secrets.md") reads as the guide title. Exported for tests. */
export function docLinkLabel(label: string, name: string, titles?: Record<string, string>): string {
	const title = titles?.[name];
	if (!title) return label;
	const bare = label.replace(/`/g, "").trim();
	return bare === `${name}.md` || bare === name ? title : label;
}

interface InlineCtx {
	onDocLink: MarkdownProps["onDocLink"];
	titles?: Record<string, string>;
}

const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\[[^\]]+\]\([^)\s]+\))|(\*[^*\s][^*]*\*)|(_[^_\s][^_]*_)/g;

function renderInline(text: string, ctx: InlineCtx, keyBase: string): ReactNode[] {
	const { onDocLink } = ctx;
	const out: ReactNode[] = [];
	let last = 0;
	let n = 0;
	for (const m of text.matchAll(INLINE)) {
		const at = m.index ?? 0;
		if (at > last) out.push(text.slice(last, at));
		const tok = m[0];
		const key = `${keyBase}-${n++}`;
		const route = m[1] ? appRoute(tok.slice(1, -1)) : null;
		if (route) {
			// An app address in a guide opens that page.
			out.push(
				<a key={key} href={route} className="text-primary font-mono text-[0.85em] underline underline-offset-2" data-testid="doc-app-link">
					{route}
				</a>,
			);
		} else if (m[1]) {
			out.push(
				<code key={key} className="bg-muted rounded-md border px-1.5 py-0.5 font-mono text-[0.85em]">
					{tok.slice(1, -1)}
				</code>,
			);
		} else if (m[2]) {
			out.push(<strong key={key}>{renderInline(tok.slice(2, -2), ctx, key)}</strong>);
		} else if (m[3]) {
			const lm = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(tok);
			const label = lm?.[1] ?? tok;
			const href = safeHref(lm?.[2] ?? "");
			const doc = href ? /^(?:\.\/)?([\w.-]+)\.md(?:#([\w-]+))?$/.exec(href) : null;
			const outside = href?.startsWith("../") ? relativeDocTarget(href) : undefined;
			const repoFile = href ? repoFileUrl(href) : null;
			if (repoFile) {
				// A repository file outside the guides (an example's README, a probe file): it opens
				// on GitHub, since this server does not serve it.
				out.push(
					<a key={key} href={repoFile} className="text-primary underline underline-offset-2" target="_blank" rel="noreferrer noopener">
						{label}
						<span className="text-muted-foreground ml-1 text-[0.8em] no-underline">{i18n.t("docs.onGitHub", { ns: "welcome" })}</span>
					</a>,
				);
			} else if (outside !== undefined) {
				// A link out of the docs folder: a known file opens its in-app guide; any other
				// would be a broken link, so it is plain text.
				if (outside === null) {
					out.push(label);
				} else {
					out.push(
						<a
							key={key}
							href={`/docs?doc=${outside}`}
							className="text-primary underline underline-offset-2"
							onClick={
								onDocLink
									? (e) => {
											e.preventDefault();
											onDocLink(outside);
										}
									: undefined
							}
						>
							{docLinkLabel(label, outside, ctx.titles)}
						</a>,
					);
				}
			} else if (doc && isInternalDoc(doc[1])) {
				out.push(label);
			} else if (doc && onDocLink) {
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
						{docLinkLabel(label, doc[1], ctx.titles)}
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
	note: { icon: Info, box: "border-chart-info/30 bg-chart-info/5", ink: "text-chart-info-ink" },
	tip: { icon: Lightbulb, box: "border-chart-success/30 bg-chart-success/5", ink: "text-chart-success-ink" },
	warning: { icon: TriangleAlert, box: "border-chart-warning/40 bg-chart-warning/5", ink: "text-chart-warning-ink" },
	danger: { icon: OctagonAlert, box: "border-chart-error/30 bg-chart-error/5", ink: "text-chart-error-ink" },
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

export function Markdown({ source, onDocLink, docTitles, className, testId = "markdown" }: MarkdownProps) {
	const blocks = parseBlocks(source);
	const ctx: InlineCtx = { onDocLink, titles: docTitles };
	return (
		<article className={cn("text-foreground/90 flex min-w-0 flex-col gap-4 text-[15px] leading-7", className)} data-testid={testId}>
			{blocks.map((b, i) => {
				const k = `b${i}`;
				switch (b.kind) {
					case "heading": {
						const Tag = `h${b.level}` as "h1";
						return (
							<Tag key={k} id={slugify(b.text)} className={HEADING_CLASS[b.level - 1]}>
								{renderInline(b.text, ctx, k)}
							</Tag>
						);
					}
					case "code":
						return <CodeBlock key={k} lang={b.lang} text={b.text} />;
					case "list": {
						const Tag = b.ordered ? "ol" : "ul";
						return (
							<Tag
								key={k}
								className={cn("marker:text-muted-foreground flex flex-col gap-1.5 pl-6", b.ordered ? "list-decimal" : "list-disc")}
							>
								{b.items.map((it, j) => (
									<li key={j}>{renderInline(it, ctx, `${k}-${j}`)}</li>
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
										<p>{renderInline(c.body, ctx, k)}</p>
									</div>
								</aside>
							);
						}
						return (
							<blockquote key={k} className="text-muted-foreground border-primary/40 border-l-4 py-0.5 pl-4 italic">
								{renderInline(b.text, ctx, k)}
							</blockquote>
						);
					}
					case "table":
						return (
							<div key={k} className="overflow-x-auto rounded-lg border">
								<table className="w-full min-w-[28rem] text-left text-sm">
									<thead className="bg-muted/60 text-muted-foreground text-xs">
										<tr>
											{b.head.map((h, j) => (
												<th key={j} className="px-3 py-2 font-medium">
													{renderInline(h, ctx, `${k}-h${j}`)}
												</th>
											))}
										</tr>
									</thead>
									<tbody>
										{b.rows.map((r, ri) => (
											<tr key={ri} className="hover:bg-muted/30 border-t transition-colors">
												{r.map((c, ci) => (
													<td key={ci} className="px-3 py-2 align-top">
														{renderInline(c, ctx, `${k}-${ri}-${ci}`)}
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
						return <p key={k}>{renderInline(b.text, ctx, k)}</p>;
				}
			})}
		</article>
	);
}

export default Markdown;