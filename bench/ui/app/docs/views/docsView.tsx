import { Markdown } from "@/components/docs/markdown";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, PageHeader } from "@/components/shell";
import { Input } from "@/components/ui/input";
import { useGetDocQuery, useListDocsQuery } from "@/lib/store/apis/welcomeApi";
import "@/lib/types/welcomeI18n";
import { cn } from "@/lib/utils";
import { BookOpen, Search } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

/** The guide opened when the page has none selected. */
export const START_DOC = "quickstart";

/** Guides worth reading first, pinned to the top of the list in this order. */
const PINNED = ["quickstart", "adding-an-agent", "bring-your-own", "frameworks", "hosting", "runners"];

/** /docs: every guide in bench/docs, served by GET /docs and rendered in place. ?doc=<name> selects one. */
export default function DocsView() {
	const { t } = useTranslation("welcome");
	const list = useListDocsQuery();
	const [selected, setSelected] = useQueryState("doc", parseAsString.withDefault(START_DOC));
	const [filter, setFilter] = useState("");
	const [anchor, setAnchor] = useState<string | undefined>();
	const doc = useGetDocQuery(selected, { skip: !selected });

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
				<div className="grid min-w-0 gap-6 md:grid-cols-[16rem_minmax(0,1fr)]">
					<nav className="flex min-w-0 flex-col gap-2 md:sticky md:top-0 md:max-h-[calc(100dvh-10rem)]" aria-label={t("docs.title")}>
						<div className="relative">
							<Search className="text-muted-foreground absolute top-1/2 left-2 size-3.5 -translate-y-1/2" aria-hidden="true" />
							<Input
								value={filter}
								onChange={(e) => setFilter(e.target.value)}
								placeholder={t("docs.search")}
								aria-label={t("docs.search")}
								className="h-8 pl-7 text-sm"
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
											"hover:bg-accent focus-visible:ring-ring/60 w-full truncate rounded-md px-2 py-1 text-left text-[13px] transition-colors focus-visible:ring-2 focus-visible:outline-none",
											d.name === selected ? "bg-accent text-foreground font-medium" : "text-muted-foreground",
										)}
										title={d.title}
										data-testid={`docs-item-${d.name}`}
									>
										{d.name === START_DOC && (
											<span className="bg-primary text-primary-foreground mr-1.5 rounded px-1 text-[10px] font-semibold">{t("docs.start")}</span>
										)}
										{d.title}
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
				</div>
			)}
		</section>
	);
}
