import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { SiteHeader } from "@/components/site/publicFrame";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { signupOffered, useSession } from "@/lib/session/sessionContext";
import { useGetPublicLibraryQuery } from "@/lib/store/apis/publicSiteApi";
import type { PublicBenchmark, PublicLibrary } from "@/lib/types/site";
import "@/lib/types/siteI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Library, Search } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

/** The benchmarks of pack (all for ""), whose id, title or description holds query. */
export function filterBenchmarks(lib: PublicLibrary, pack: string, query: string): PublicBenchmark[] {
	const q = query.trim().toLowerCase();
	return lib.benchmarks.filter(
		(b) =>
			(!pack || b.packs.includes(pack)) &&
			(!q || b.id.toLowerCase().includes(q) || b.title.toLowerCase().includes(q) || b.description.toLowerCase().includes(q)),
	);
}

/**
 * /library (lane Y08, audit G4-03): the built-in benchmark library, read-only and without sign-in.
 * Descriptions, task and ready counts, licences and packs, as this server reports them; task
 * content stays inside organizations. ?pack=<id> filters by evaluation pack.
 */
export default function LibraryView() {
	const { t } = useTranslation("site");
	const session = useSession();
	const lib = useGetPublicLibraryQuery();
	const [pack, setPack] = useQueryState("pack", parseAsString.withDefault(""));
	const [query, setQuery] = useState("");
	const shown = useMemo(() => (lib.data ? filterBenchmarks(lib.data, pack, query) : []), [lib.data, pack, query]);
	const cta = session.signedIn || session.authMode !== "hosted" ? "/evaluate" : signupOffered(session) ? "/signup" : "/login";

	return (
		<section className="flex min-w-0 flex-col" data-testid="library-view">
			<SiteHeader title={t("library.title")} subtitle={t("library.subtitle")} testId="library-header">
				{lib.data?.available && (
					<p className="text-muted-foreground font-mono text-xs" data-testid="library-totals">
						{t("library.totals", { benchmarks: lib.data.totals.benchmarks, tasks: lib.data.totals.tasks.toLocaleString(), ready: lib.data.totals.ready.toLocaleString() })}
					</p>
				)}
			</SiteHeader>
			{lib.isLoading ? (
				<BlockSkeleton rows={6} testId="library-loading" />
			) : lib.error ? (
				<LoadError title={t("common.loadError")} error={lib.error} onRetry={lib.refetch} testId="library-error" />
			) : !lib.data?.available || lib.data.benchmarks.length === 0 ? (
				<div className="bg-card text-muted-foreground flex items-center gap-3 rounded-xl border px-5 py-6 text-sm" data-testid="library-none">
					<Library className="size-5 shrink-0" aria-hidden="true" />
					{t("library.none")}
				</div>
			) : (
				<div className="flex flex-col gap-5">
					<div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
						<div className="flex flex-wrap gap-1.5" role="group" aria-label={t("library.packs")} data-testid="library-packs">
							{[{ id: "", name: t("library.allPacks") }, ...lib.data.packs].map((p) => (
								<button
									key={p.id || "all"}
									type="button"
									aria-pressed={pack === p.id}
									onClick={() => void setPack(p.id || null)}
									className={cn(
										"rounded-full border px-3 py-1 text-[13px] transition-colors",
										pack === p.id ? "bg-foreground text-background border-foreground" : "bg-card text-muted-foreground hover:text-foreground",
									)}
									data-testid={`library-pack-${p.id || "all"}`}
								>
									{p.name}
								</button>
							))}
						</div>
						<div className="relative sm:w-72">
							<Search className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" aria-hidden="true" />
							<Input
								value={query}
								onChange={(e) => setQuery(e.target.value)}
								placeholder={t("library.search")}
								aria-label={t("library.search")}
								className="bg-card h-9 pl-8"
								data-testid="library-search"
							/>
						</div>
					</div>
					{shown.length === 0 ? (
						<p className="text-muted-foreground py-8 text-center text-sm" data-testid="library-no-match">
							{t("library.noMatch")}
						</p>
					) : (
						<ul className="grid gap-4 md:grid-cols-2" data-testid="library-list">
							{shown.map((b) => (
								<BenchmarkCard key={b.id} b={b} packs={lib.data?.packs ?? []} />
							))}
						</ul>
					)}
					<p className="text-muted-foreground text-xs">{t("library.readyHint")}</p>
					<div>
						<Link to={cta} className={buttonVariants()} data-testid="library-cta">
							{t("library.cta")}
						</Link>
					</div>
				</div>
			)}
		</section>
	);
}

function BenchmarkCard({ b, packs }: { b: PublicBenchmark; packs: PublicLibrary["packs"] }) {
	const { t } = useTranslation("site");
	const packName = (id: string) => packs.find((p) => p.id === id)?.name ?? id;
	const grading = b.grading === "verified" ? t("library.verified") : b.grading === "judged" ? t("library.judged") : undefined;
	return (
		<li className="bg-card flex flex-col gap-4 rounded-xl border p-5" data-testid={`library-benchmark-${b.id}`}>
			<div className="flex flex-col gap-1">
				<div className="flex flex-wrap items-baseline justify-between gap-2">
					<h2 className="font-semibold">{b.title}</h2>
					<span className="text-muted-foreground font-mono text-xs">{b.id}</span>
				</div>
				{b.description && <p className="text-muted-foreground line-clamp-3 text-sm leading-relaxed">{b.description}</p>}
				{b.error && (
					<p className="text-chart-error text-sm" data-testid={`library-error-${b.id}`}>
						{t("library.unreadable")}
					</p>
				)}
			</div>
			<dl className="grid grid-cols-3 gap-3 text-sm">
				<div>
					<dt className="text-muted-foreground text-xs">{t("library.tasks")}</dt>
					<dd className="font-semibold tabular-nums">{b.task_count.toLocaleString()}</dd>
				</div>
				<div>
					<dt className="text-muted-foreground text-xs">{t("library.ready")}</dt>
					<dd className="font-semibold tabular-nums" data-testid={`library-ready-${b.id}`}>
						{b.ready_count.toLocaleString()}
					</dd>
				</div>
				<div>
					<dt className="text-muted-foreground text-xs">{t("library.licence")}</dt>
					<dd className="truncate" title={b.licenses.join(", ")}>
						{b.licenses.length > 0 ? b.licenses.join(", ") : <span className="text-muted-foreground">{t("library.licenceUnknown")}</span>}
					</dd>
				</div>
			</dl>
			<div className="mt-auto flex flex-wrap gap-1.5">
				{grading && <Badge variant="secondary">{grading}</Badge>}
				{b.track && <Badge variant="outline">{b.track}</Badge>}
				{b.packs.map((p) => (
					<Badge key={p} variant="outline">
						{packName(p)}
					</Badge>
				))}
				{(b.held_out_count ?? 0) > 0 && (
					<Badge variant="outline">
						{t("library.heldOut")}: {b.held_out_count}
					</Badge>
				)}
			</div>
			{b.source && (
				<p className="text-muted-foreground truncate text-xs" title={b.source}>
					{t("library.source")}: {b.source}
				</p>
			)}
		</li>
	);
}
