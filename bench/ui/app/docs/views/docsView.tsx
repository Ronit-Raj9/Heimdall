import "@/components/share/uf8bI18n";
import { hostedDocSource, Markdown, parseBlocks, slugify } from "@/components/docs/markdown";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, PageHeader } from "@/components/shell";
import { Alert } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { useSession } from "@/lib/session/sessionContext";
import { errorStatus } from "@/lib/store/apis/session";
import { useGetPublicDocQuery, useListPublicDocsQuery } from "@/lib/store/apis/publicSiteApi";
import { useGetDocQuery, useListDocsQuery } from "@/lib/store/apis/welcomeApi";
import "@/lib/types/welcomeI18n";
import { cn } from "@/lib/utils";
import { BookOpen, ChevronDown, Search } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

/** The guide opened when the page has none selected. */
export const START_DOC = "web-quickstart";

/** Guides worth reading first, pinned to the top of the list in this order. */
const PINNED = [
	"web-quickstart",
	"glossary",
	"web-agents",
	"web-suites",
	"web-experiments",
	"web-analysis",
	"web-keys-secrets",
	"web-runners",
	"web-integrations",
	"web-sync",
	"web-admin",
	"quickstart",
	"adding-an-agent",
	"bring-your-own",
	"frameworks",
	"hosting",
	"runners",
];

/** The text the guides list searches on the server for (2 to 80 characters), or undefined for none. */
export function docsSearchTerm(filter: string): string | undefined {
	const q = filter.trim();
	return q.length >= 2 && q.length <= 80 ? q : undefined;
}

export type DocGroup = "start" | "web" | "other";

/**
 * Splits the sorted guide list into collapsible groups: the pinned guides, the website guides
 * (web-*), then the rest. Groups with no guide are left out. Exported for tests.
 */
export function groupDocs<T extends { name: string }>(docs: T[]): { group: DocGroup; docs: T[] }[] {
	const by: Record<DocGroup, T[]> = { start: [], web: [], other: [] };
	for (const d of docs) by[PINNED.includes(d.name) ? "start" : d.name.startsWith("web-") ? "web" : "other"].push(d);
	return (["start", "web", "other"] as const).filter((g) => by[g].length > 0).map((g) => ({ group: g, docs: by[g] }));
}

/** guide name -> title, so a link to another guide (foo.md) reads as its title. */
export function docTitles(docs: { name: string; title: string }[]): Record<string, string> {
	return Object.fromEntries(docs.map((d) => [d.name, d.title]));
}

/** How long typing settles before the server search runs. */
const SEARCH_DEBOUNCE_MS = 250;

/** /docs: every guide in bench/docs, served by GET /docs and rendered in place. ?doc=<name> selects one. */
export default function DocsView() {
	const { t } = useTranslation("welcome");
	const { t: tu } = useTranslation("uf8b");
	// A hosted visitor who is not signed in reads the public guides (GET /public/docs, internal
	// notes left out) in the public frame; everyone else the in-app guides.
	const session = useSession();
	const visitor = session.authMode === "hosted" && !session.signedIn;
	const appList = useListDocsQuery(undefined, { skip: visitor });
	const publicList = useListPublicDocsQuery(undefined, { skip: !visitor });
	const list = visitor ? publicList : appList;
	const [selected, setSelected] = useQueryState("doc", parseAsString.withDefault(START_DOC));
	// The filter lives in the address (?q=), so a search can be shared and the 404 page can start one.
	const [filterParam, setFilterParam] = useQueryState("q", parseAsString.withDefault(""));
	const filter = filterParam ?? "";
	const setFilter = (v: string) => void setFilterParam(v || null);
	// Phones: the guide list folds away under the article once a guide is open.
	const [listOpen, setListOpen] = useState(false);
	// Two or more characters also search the guides' bodies on the server (debounced).
	const [term, setTerm] = useState<string | undefined>();
	useEffect(() => {
		const next = docsSearchTerm(filter);
		const timer = setTimeout(() => setTerm(next), next ? SEARCH_DEBOUNCE_MS : 0);
		return () => clearTimeout(timer);
	}, [filter]);
	const appSearch = useListDocsQuery(term, { skip: visitor || !term });
	const publicSearch = useListPublicDocsQuery(term, { skip: !visitor || !term });
	const search = term ? (visitor ? publicSearch : appSearch) : undefined;
	const [anchor, setAnchor] = useState<string | undefined>();
	const appDoc = useGetDocQuery(selected, { skip: !selected || visitor });
	const publicDoc = useGetPublicDocQuery(selected, { skip: !selected || !visitor });
	const doc = visitor ? publicDoc : appDoc;

	const docs = useMemo(() => {
		// Server hits come ranked (title matches first), each with its snippet.
		if (search?.data) return search.data.docs;
		const all = [...(list.data?.docs ?? [])];
		const rank = (n: string) => {
			const i = PINNED.indexOf(n);
			return i < 0 ? PINNED.length : i;
		};
		all.sort((a, b) => rank(a.name) - rank(b.name) || a.title.localeCompare(b.title));
		const f = filter.trim().toLowerCase();
		return f ? all.filter((d) => d.title.toLowerCase().includes(f) || d.name.includes(f)) : all;
	}, [list.data, filter, search?.data]);

	// On a hosted server the guides' local server addresses (http://127.0.0.1:8090) point here.
	const hosted = session.authMode === "hosted";
	const source = useMemo(() => {
		if (!doc.data) return undefined;
		if (!hosted || typeof window === "undefined") return { text: doc.data.markdown, replaced: 0 };
		return hostedDocSource(doc.data.markdown, window.location.origin);
	}, [doc.data, hosted]);
	// The tab names the guide and the page word the header shows (M8-3).
	usePageTitle(doc.data ? `${doc.data.title} · ${t("docs.title")} · Heimdall` : undefined);
	const titles = useMemo(() => docTitles(list.data?.docs ?? []), [list.data]);
	const reading = Boolean(doc.data);
	const noGuide = Boolean(doc.error) && errorStatus(doc.error) === 404;

	const toc = useMemo(() => {
		if (!doc.data) return [];
		return parseBlocks(doc.data.markdown)
			.filter((b): b is { kind: "heading"; level: number; text: string } => b.kind === "heading" && (b.level === 2 || b.level === 3))
			.map((b) => ({ id: slugify(b.text), text: b.text, level: b.level }));
	}, [doc.data]);

	// After a cross-guide link with an anchor, scroll to that heading once the guide renders.
	useEffect(() => {
		if (!anchor || !doc.data) return;
		document.getElementById(anchor)?.scrollIntoView({ block: "start" });
		setAnchor(undefined);
	}, [anchor, doc.data]);

	const open = (name: string, at?: string) => {
		void setSelected(name);
		setAnchor(at);
		setListOpen(false);
		document.querySelector<HTMLElement>("[data-bench-content]")?.scrollTo?.({ top: 0 });
	};

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="docs-view">
			{reading ? (
				// With a guide open its title is the page's h1, so "Docs" is a small label above it (M8-65).
				<p className="text-muted-foreground text-xs font-medium tracking-wide uppercase" data-testid="docs-header">
					{t("docs.title")}
				</p>
			) : (
				<PageHeader title={t("docs.title")} subtitle={t("docs.subtitle")} testId="docs-header" />
			)}
			{list.isLoading ? (
				<BlockSkeleton rows={6} testId="docs-loading" />
			) : list.error ? (
				<LoadError title={t("docs.loadError")} error={list.error} onRetry={list.refetch} testId="docs-error" />
			) : (list.data?.docs.length ?? 0) === 0 ? (
				<EmptyState icon={BookOpen} message={t("docs.none")} testId="docs-none" />
			) : (
				<div className="grid min-w-0 gap-8 md:grid-cols-[15rem_minmax(0,1fr)] xl:grid-cols-[15rem_minmax(0,1fr)_12rem]">
					<nav className="flex min-w-0 flex-col gap-3 md:sticky md:top-0 md:max-h-[calc(100dvh-10rem)]" aria-label={t("docs.title")}>
						{reading && (
							<button
								type="button"
								className="hover:bg-accent/60 flex items-center justify-between rounded-lg border px-3 py-2 text-sm font-medium md:hidden"
								aria-expanded={listOpen}
								onClick={() => setListOpen((o) => !o)}
								data-testid="docs-list-toggle"
							>
								{tu("docs.allGuides", { count: list.data?.docs.length ?? 0 })}
								<ChevronDown className={cn("size-4 transition-transform", listOpen && "rotate-180")} aria-hidden="true" />
							</button>
						)}
						<div className={cn("min-h-0 flex-col gap-3", reading && !listOpen ? "hidden md:flex" : "flex")} data-testid="docs-list-panel">
						<div className="relative">
							<Search className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" aria-hidden="true" />
							<Input
								value={filter}
								onChange={(e) => setFilter(e.target.value)}
								placeholder={t("docs.search")}
								aria-label={t("docs.search")}
								className="bg-card h-9 rounded-lg pl-8 text-sm"
								data-testid="docs-filter"
							/>
						</div>
						<div className="flex min-h-0 flex-col gap-2 overflow-y-auto pr-1" data-testid="docs-list">
							{docs.length === 0 && (
								<p className="text-muted-foreground px-2 py-1 text-xs" data-testid="docs-empty">
									{term && search?.data ? t("docs.noMatch", { q: term }) : t("docs.empty")}
								</p>
							)}
							{/* A search shows its hits ranked in one list; otherwise the guides are grouped (M8-64). */}
							{filter.trim()
								? docs.length > 0 && <DocList docs={docs} selected={selected} open={open} startLabel={t("docs.start")} />
								: groupDocs(docs).map((g) => (
										<details
											key={g.group}
											className="group/docs"
											open={g.group === "start" || g.docs.some((d) => d.name === selected)}
											data-testid={`docs-group-${g.group}`}
										>
											<summary className="text-muted-foreground hover:text-foreground flex cursor-pointer list-none items-center justify-between rounded-md px-2.5 py-1.5 text-xs font-medium tracking-wide uppercase">
												{tu(`docs.groups.${g.group}`)} ({g.docs.length})
												<ChevronDown className="size-3.5 transition-transform group-open/docs:rotate-180" aria-hidden="true" />
											</summary>
											<DocList docs={g.docs} selected={selected} open={open} startLabel={t("docs.start")} />
										</details>
									))}
						</div>
						</div>
					</nav>
					<div className={cn("max-w-3xl min-w-0", reading && "order-first md:order-none")}>
						{doc.isFetching && !doc.data ? (
							<BlockSkeleton rows={8} testId="doc-loading" />
						) : noGuide ? (
							<p className="text-muted-foreground text-sm" role="status" data-testid="doc-not-found">
								{t("docs.noGuide", { name: selected })}
							</p>
						) : doc.error ? (
							<LoadError title={t("docs.docError")} error={doc.error} onRetry={doc.refetch} testId="doc-error" />
						) : doc.data && source ? (
							<>
								{source.replaced > 0 && (
									<Alert tone="info" className="mb-4" testId="docs-local-note">
										{t("docs.localAddressNote")}
									</Alert>
								)}
								<Markdown source={source.text} onDocLink={open} docTitles={titles} testId="doc-body" />
							</>
						) : (
							<EmptyState icon={BookOpen} message={t("docs.pick")} testId="doc-pick" />
						)}
					</div>
					{toc.length > 1 && (
						<aside className="hidden xl:block" aria-label={t("docs.onThisPage")}>
							<div className="sticky top-0 flex flex-col gap-2 text-[13px]">
								<p className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{t("docs.onThisPage")}</p>
								<ul className="flex flex-col gap-1 border-l">
									{toc.map((h) => (
										<li key={h.id}>
											<a
												href={`#${h.id}`}
												onClick={(e) => {
													e.preventDefault();
													document.getElementById(h.id)?.scrollIntoView({ block: "start", behavior: "smooth" });
												}}
												className={cn(
													"text-muted-foreground hover:text-foreground -ml-px block truncate border-l border-transparent py-1.5 transition-colors hover:border-foreground/40",
													h.level === 3 ? "pl-6" : "pl-3",
												)}
												title={h.text}
											>
												{h.text.replace(/[`*_]/g, "")}
											</a>
										</li>
									))}
								</ul>
							</div>
						</aside>
					)}
				</div>
			)}
		</section>
	);
}
/** One list of guide links; the open guide is marked by a filled row, not a side stripe (X-64). */
function DocList({
	docs,
	selected,
	open,
	startLabel,
}: {
	docs: { name: string; title: string; snippet?: string }[];
	selected: string;
	open: (name: string) => void;
	startLabel: string;
}) {
	return (
		<ul className="flex flex-col gap-0.5">
			{docs.map((d) => (
				<li key={d.name}>
					<a
						href={`/docs?doc=${encodeURIComponent(d.name)}`}
						onClick={(e) => {
							// A plain left click opens the guide in place; a modified or middle click opens a new tab.
							if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
							e.preventDefault();
							open(d.name);
						}}
						aria-current={d.name === selected ? "page" : undefined}
						className={cn(
							"hover:bg-accent/60 hover:text-foreground focus-visible:ring-ring/60 flex w-full items-center gap-2 truncate rounded-md px-2.5 py-1.5 text-left text-sm transition-colors focus-visible:ring-2 focus-visible:outline-none",
							d.name === selected ? "bg-muted text-foreground font-medium" : "text-foreground/80",
						)}
						title={d.title}
						data-testid={`docs-item-${d.name}`}
					>
						{d.name === START_DOC && (
							<span className="bg-primary/10 text-primary shrink-0 rounded-full px-1.5 py-0.5 text-[11px] font-semibold">{startLabel}</span>
						)}
						<span className="flex min-w-0 flex-col">
							<span className="truncate">{d.title}</span>
							{d.snippet && (
								<span className="text-muted-foreground truncate text-xs font-normal" data-testid={`docs-snippet-${d.name}`}>
									{d.snippet}
								</span>
							)}
						</span>
					</a>
				</li>
			))}
		</ul>
	);
}
