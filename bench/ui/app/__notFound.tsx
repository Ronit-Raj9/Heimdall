import "@/components/share/uf8bI18n";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { BENCH_NAV } from "@/lib/constants/navigation";
import i18n from "@/lib/i18n";
import "@/lib/routing/gw15I18n";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { aliasRedirect, labelSlug } from "@/lib/routing/pathAliases";
import { Link, Navigate, useRouterState } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { useTranslation } from "react-i18next";

/** The pages a lost visitor most likely wants, always offered on the 404. */
export const NOT_FOUND_LINKS = [
	{ to: "/evaluations", key: "evaluations" },
	{ to: "/agents", key: "agents" },
	{ to: "/runs", key: "runs" },
	{ to: "/docs", key: "docs" },
] as const;

/** True when an address word and a page word share a start ("eval" and "evaluations"). */
function wordsMeet(a: string, b: string): boolean {
	return a.startsWith(b) || b.startsWith(a);
}

/**
 * Sidebar pages whose label or address shares a word start with an unknown address, at most
 * three ("/live-stuff" suggests Live sessions, "/eval" suggests Evaluations). Exported for tests.
 */
export function suggestPages(pathname: string): { to: string; label: string }[] {
	const words = [
		...new Set(
			pathname
				.toLowerCase()
				.split(/[^a-z0-9]+/)
				.filter((w) => w.length >= 3),
		),
	];
	if (words.length === 0) return [];
	const out: { to: string; label: string }[] = [];
	for (const item of BENCH_NAV) {
		const label = i18n.t(item.labelKey, { ns: "bench" });
		const own = [...labelSlug(label).slice(1).split("-"), ...item.to.slice(1).split(/[-/]/)].filter((w) => w.length >= 3);
		if (own.some((w) => words.some((x) => wordsMeet(w, x)))) out.push({ to: item.to, label });
		if (out.length === 3) break;
	}
	return out;
}

export function NotFoundComponent() {
	const { t } = useTranslation("bench");
	const { t: tg } = useTranslation("gw15");
	const { t: tu } = useTranslation("uf8b");
	usePageTitle(`${t("notFound.title")} · Heimdall`);
	const location = useRouterState({ select: (state) => state.location });
	// An address shaped like a sidebar label (/live-sessions, /labelling) goes to its page.
	const alias = aliasRedirect(location.pathname, (location.search ?? {}) as Record<string, unknown>);
	if (alias) return <Navigate to={alias.to as "/"} search={alias.search as never} replace />;
	const suggestions = suggestPages(location.pathname);
	return (
		<section className="flex max-w-xl flex-col items-start gap-4" data-testid="not-found-view">
			<h1 className="text-foreground text-2xl font-semibold tracking-tight">{t("notFound.title")}</h1>
			<p className="text-foreground/80 text-sm">{tu("notFound.description")}</p>
			{suggestions.length > 0 && (
				<p className="text-foreground/80 flex flex-wrap items-center gap-2 text-sm" data-testid="not-found-suggestions">
					<span>{tg("notFound.didYouMean")}</span>
					{suggestions.map((s) => (
						<Link
							key={s.to}
							to={s.to as "/"}
							className="text-primary underline underline-offset-2"
							data-testid={`not-found-suggest-${s.to.slice(1) || "home"}`}
						>
							{s.label}
						</Link>
					))}
				</p>
			)}
			<nav className="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm" aria-label={tu("notFound.goTo")} data-testid="not-found-links">
				<span className="text-foreground/80">{tu("notFound.goTo")}</span>
				{NOT_FOUND_LINKS.map((l) => (
					<Link key={l.to} to={l.to as "/"} className="text-primary underline underline-offset-2" data-testid={`not-found-go-${l.key}`}>
						{tu(`notFound.links.${l.key}`)}
					</Link>
				))}
			</nav>
			{/* A plain GET form: the docs page reads ?q= into its search box. */}
			<form action="/docs" method="get" role="search" className="flex w-full max-w-sm items-center gap-2" data-testid="not-found-docs-search">
				<div className="relative min-w-0 flex-1">
					<Search className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" aria-hidden="true" />
					<Input name="q" type="search" placeholder={tu("notFound.searchDocs")} aria-label={tu("notFound.searchDocs")} className="h-9 pl-8 text-sm" />
				</div>
				<Button type="submit" variant="outline">
					{tu("notFound.search")}
				</Button>
			</form>
			<Button asChild>
				<Link to="/" data-testid="not-found-link-home">
					{t("shell.backHome")}
				</Link>
			</Button>
		</section>
	);
}
