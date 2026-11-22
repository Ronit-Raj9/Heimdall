import { Button } from "@/components/ui/button";
import { BENCH_NAV } from "@/lib/constants/navigation";
import i18n from "@/lib/i18n";
import "@/lib/routing/gw15I18n";
import { aliasRedirect, labelSlug } from "@/lib/routing/pathAliases";
import { Link, Navigate, useRouterState } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/**
 * Sidebar pages whose label shares a word with an unknown address, at most three
 * ("/live-stuff" suggests Live sessions). Exported for tests.
 */
export function suggestPages(pathname: string): { to: string; label: string }[] {
	const words = new Set(
		pathname
			.toLowerCase()
			.split(/[^a-z0-9]+/)
			.filter((w) => w.length >= 3),
	);
	if (words.size === 0) return [];
	const out: { to: string; label: string }[] = [];
	for (const item of BENCH_NAV) {
		const label = i18n.t(item.labelKey, { ns: "bench" });
		const own = [...labelSlug(label).slice(1).split("-"), ...item.to.slice(1).split(/[-/]/)].filter((w) => w.length >= 3);
		if (own.some((w) => words.has(w))) out.push({ to: item.to, label });
		if (out.length === 3) break;
	}
	return out;
}

export function NotFoundComponent() {
	const { t } = useTranslation("bench");
	const { t: tg } = useTranslation("gw15");
	const location = useRouterState({ select: (state) => state.location });
	// An address shaped like a sidebar label (/live-sessions, /labelling) goes to its page.
	const alias = aliasRedirect(location.pathname, (location.search ?? {}) as Record<string, unknown>);
	if (alias) return <Navigate to={alias.to as "/"} search={alias.search as never} replace />;
	const suggestions = suggestPages(location.pathname);
	return (
		<section className="flex flex-col items-start gap-3" data-testid="not-found-view">
			<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("notFound.title")}</h1>
			<p className="text-muted-foreground text-sm">{t("notFound.description")}</p>
			{suggestions.length > 0 && (
				<p className="text-muted-foreground flex flex-wrap items-center gap-2 text-sm" data-testid="not-found-suggestions">
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
			<Button asChild variant="outline" size="sm">
				<Link to="/" data-testid="not-found-link-home">
					{t("shell.backHome")}
				</Link>
			</Button>
		</section>
	);
}