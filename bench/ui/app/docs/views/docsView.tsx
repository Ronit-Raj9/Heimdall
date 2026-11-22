import { Markdown, parseBlocks, slugify } from "@/components/docs/markdown";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, PageHeader } from "@/components/shell";
import { Input } from "@/components/ui/input";
import { useSession } from "@/lib/session/sessionContext";
import { useGetPublicDocQuery, useListPublicDocsQuery } from "@/lib/store/apis/publicSiteApi";
import { useGetDocQuery, useListDocsQuery } from "@/lib/store/apis/welcomeApi";
import "@/lib/types/welcomeI18n";
import { cn } from "@/lib/utils";
import { BookOpen, Search } from "lucide-react";
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

/** /docs: every guide in bench/docs, served by GET /docs and rendered in place. ?doc=<name> selects one. */
export default function DocsView() {
	const { t } = useTranslation("welcome");
	// A hosted visitor who is not signed in reads the public guides (GET /public/docs, internal
	// notes left out) in the public frame; everyone else the in-app guides.
	const session = useSession();
	const visitor = session.authMode === "hosted" && !session.signedIn;
	const appList = useListDocsQuery(undefined, { skip: visitor });
	const publicList = useListPublicDocsQuery(undefined, { skip: !visitor });
	const list = visitor ? publicList : appList;
	const [selected, setSelected] = useQueryState("doc", parseAsString.withDefault(START_DOC));
	const [filter, setFilter] = useState("");
	const [anchor, setAnchor] = useState<string | undefined>();
	const appDoc = useGetDocQuery(selected, { skip: !selected || visitor });
	const publicDoc = useGetPublicDocQuery(selected, { skip: !selected || !visitor });
	const doc = visitor ? publicDoc : appDoc;

	const docs = useMemo(() => {
		const all = [...(list.data?.docs ?? [])];
		const rank = (n: string) => {
			const i = PINNED.indexOf(n);
			return i < 0 ? PINNED.length : i;
		};
		all.sort((a, b) => rank(a.name) - rank(b.name) || a.title.localeCompare(b.title));
		const f = filter.trim().toLowerCase();
		return f ? all.filter((d) => d.title.toLowerCase().includes(f) || d.name.includes(f)) : all;
	}, [list.data, filter]);

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
		document.querySelector<HTMLElement>("[data-bench-content]")?.scrollTo?.({ top: 0 });
	};

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="docs-view">
			<PageHeader title={t("docs.title")} subtitle={t("docs.subtitle")} testId="docs-header" />
			{list.isLoading ? (
				<BlockSkeleton rows={6} testId="docs-loading" />
			) : list.error ? (
				<LoadError title={t("docs.loadError")} error={list.error} onRetry={list.refetch} testId="docs-error" />
			) : (list.data?.docs.length ?? 0) === 0 ? (
				<EmptyState icon={BookOpen} message={t("docs.none")} testId="docs-none" />
			) : (
				<div className="grid min-w-0 gap-8 md:grid-cols-[15rem_minmax(0,1fr)] xl:grid-cols-[15rem_minmax(0,1fr)_12rem]">
					<nav className="flex min-w-0 flex-col gap-3 md:sticky md:top-0 md:max-h-[calc(100dvh-10rem)]" aria-label={t("docs.title")}>
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
						<ul className="flex min-h-0 flex-col gap-0.5 overflow-y-auto pr-1" data-testid="docs-list">
							{docs.length === 0 && <li className="text-muted-foreground px-2 py-1 text-xs">{t("docs.empty")}</li>}
							{docs.map((d) => (
								<li key={d.name}>
									<button
										type="button"
										onClick={() => open(d.name)}
										aria-current={d.name === selected ? "page" : undefined}
										className={cn(
											"hover:bg-accent/60 hover:text-foreground focus-visible:ring-ring/60 flex w-full items-center gap-2 truncate rounded-md border-l-2 px-2.5 py-1.5 text-left text-[13px] transition-colors focus-visible:ring-2 focus-visible:outline-none",
											d.name === selected ? "border-primary bg-accent text-foreground font-medium" : "text-muted-foreground border-transparent",
										)}
										title={d.title}
										data-testid={`docs-item-${d.name}`}
									>
										{d.name === START_DOC && (
											<span className="bg-primary/10 text-primary shrink-0 rounded-full px-1.5 py-0.5 text-[10px] font-semibold">{t("docs.start")}</span>
										)}
										<span className="truncate">{d.title}</span>
									</button>
								</li>
							))}
						</ul>
					</nav>
					<div className="min-w-0 max-w-3xl">
						{doc.isFetching && !doc.data ? (
							<BlockSkeleton rows={8} testId="doc-loading" />
						) : doc.error ? (
							<LoadError title={t("docs.docError")} error={doc.error} onRetry={doc.refetch} testId="doc-error" />
						) : doc.data ? (
							<Markdown source={doc.data.markdown} onDocLink={open} testId="doc-body" />
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
												className={cn("text-muted-foreground hover:text-foreground -ml-px block truncate border-l border-transparent py-0.5 transition-colors hover:border-foreground/40", h.level === 3 ? "pl-6" : "pl-3")}
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
