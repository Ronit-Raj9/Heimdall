import { HeimdallMark } from "@/components/heimdallMark";
import { ThemeToggle } from "@/components/themeToggle";
import { buttonVariants } from "@/components/ui/button";
import { signupOffered, useSession } from "@/lib/session/sessionContext";
import { useGetPublicOverviewQuery } from "@/lib/store/apis/publicSiteApi";
import "@/lib/types/siteI18n";
import { cn } from "@/lib/utils";
import { Link, useRouterState } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** The public site's sections, in header order. */
export const SITE_LINKS = [
	{ to: "/", key: "product" },
	{ to: "/library", key: "library" },
	{ to: "/docs", key: "docs" },
	{ to: "/pricing", key: "pricing" },
	{ to: "/downloads", key: "downloads" },
	{ to: "/security", key: "security" },
	{ to: "/reference", key: "reference" },
] as const;

/** A site link's label; the API reference link's text lives in the ufs1 namespace. */
function siteLinkLabel(t: (key: never, opts?: never) => string, key: (typeof SITE_LINKS)[number]["key"]): string {
	if (key === "reference") return t("siteNav.reference" as never, { ns: "ufs1" } as never);
	return t(`nav.${key}` as never);
}

/**
 * The frame of the public site (lane Y08): the landing page, docs, benchmark library, pricing,
 * downloads, security and legal pages. A header with the sections and the sign-in actions, the
 * page, and a footer with the legal links and the server's version. It needs no sign-in and reads
 * only /public/*; a signed-in user sees "Open the app" instead of the sign-in actions.
 */
export function PublicFrame({ children }: { children: ReactNode }) {
	const { t } = useTranslation("site");
	const session = useSession();
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	const overview = useGetPublicOverviewQuery();
	const hosted = session.authMode === "hosted";
	const active = (to: string) => (to === "/" ? pathname === "/" : pathname === to || pathname.startsWith(`${to}/`));

	return (
		<div className="bg-background flex min-h-dvh flex-col" data-testid="public-frame">
			<a
				href="#bench-main"
				className="bg-primary text-primary-foreground sr-only z-50 rounded-md px-3 py-2 text-sm focus:not-sr-only focus:fixed focus:top-3 focus:left-3"
			>
				{t("nav.label")}
			</a>
			<header className="bg-background/85 supports-[backdrop-filter]:bg-background/70 sticky top-0 z-40 border-b backdrop-blur">
				<div className="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4 md:px-8">
					<Link to="/" className="flex shrink-0 items-center gap-2 text-[15px] font-semibold tracking-[-0.01em]" data-testid="public-brand">
						<span className="bg-foreground text-background flex size-7 items-center justify-center rounded-lg">
							<HeimdallMark className="size-4.5" />
						</span>
						Heimdall
					</Link>
					<nav aria-label={t("nav.label")} className="hidden min-w-0 items-center gap-1 md:flex" data-testid="public-nav">
						{SITE_LINKS.map((l) => (
							<Link
								key={l.to}
								to={l.to}
								aria-current={active(l.to) ? "page" : undefined}
								className={cn(
									"rounded-md px-2.5 py-1.5 text-sm transition-colors",
									active(l.to) ? "text-foreground bg-accent font-medium" : "text-muted-foreground hover:text-foreground",
								)}
								data-testid={`public-nav-${l.key}`}
							>
								{siteLinkLabel(t, l.key)}
							</Link>
						))}
					</nav>
					<div className="ml-auto flex items-center gap-2">
						<ThemeToggle />
						{session.signedIn || !hosted ? (
							<Link to="/" className={buttonVariants({ size: "sm" })} data-testid="public-open-app">
								{t("nav.openApp")}
							</Link>
						) : (
							<>
								<Link to="/login" className={buttonVariants({ variant: "ghost", size: "sm" })} data-testid="public-sign-in">
									{t("nav.signIn")}
								</Link>
								{signupOffered(session) && (
									<Link to="/signup" className={buttonVariants({ size: "sm" })} data-testid="public-sign-up">
										{t("nav.signUp")}
									</Link>
								)}
							</>
						)}
					</div>
				</div>
				<nav aria-label={t("nav.menu")} className="flex gap-1 overflow-x-auto px-4 pb-2 md:hidden" data-testid="public-nav-mobile">
					{SITE_LINKS.map((l) => (
						<Link
							key={l.to}
							to={l.to}
							aria-current={active(l.to) ? "page" : undefined}
							className={cn(
								"shrink-0 rounded-md px-2.5 py-1 text-[13px]",
								active(l.to) ? "bg-accent text-foreground font-medium" : "text-muted-foreground",
							)}
						>
							{siteLinkLabel(t, l.key)}
						</Link>
					))}
				</nav>
			</header>
			<main id="bench-main" tabIndex={-1} className="mx-auto flex w-full max-w-6xl flex-1 flex-col px-4 py-10 outline-none md:px-8 md:py-14" data-bench-content>
				{children}
			</main>
			<footer className="border-t" data-testid="public-footer">
				<div className="text-muted-foreground mx-auto grid max-w-6xl gap-8 px-4 py-10 text-sm md:grid-cols-[1.5fr_1fr_1fr] md:px-8">
					<div className="flex flex-col gap-2">
						<span className="text-foreground flex items-center gap-2 font-semibold">
							<HeimdallMark className="size-4" />
							Heimdall
						</span>
						<p>{t("footer.tagline")}</p>
						{overview.data && (
							<p className="font-mono text-xs" data-testid="public-version">
								{t("footer.version", { version: overview.data.version })}
								{overview.data.commit ? ` · ${t("footer.commit", { commit: overview.data.commit.slice(0, 12) })}` : ""}
							</p>
						)}
					</div>
					<div className="flex flex-col gap-2">
						<p className="text-foreground text-xs font-medium tracking-wide uppercase">{t("footer.product")}</p>
						{SITE_LINKS.slice(1).map((l) => (
							<Link key={l.to} to={l.to} className="hover:text-foreground w-fit">
								{siteLinkLabel(t, l.key)}
							</Link>
						))}
					</div>
					<div className="flex flex-col gap-2">
						<p className="text-foreground text-xs font-medium tracking-wide uppercase">{t("footer.legal")}</p>
						<Link to="/terms" className="hover:text-foreground w-fit" data-testid="public-terms">
							{t("footer.terms")}
						</Link>
						<Link to="/privacy" className="hover:text-foreground w-fit" data-testid="public-privacy">
							{t("footer.privacy")}
						</Link>
						<Link to="/security" className="hover:text-foreground w-fit">
							{t("footer.security")}
						</Link>
						<a href="/.well-known/security.txt" className="hover:text-foreground w-fit font-mono text-xs">
							{t("footer.securityTxt")}
						</a>
					</div>
				</div>
			</footer>
		</div>
	);
}

/** A public page's title block. */
export function SiteHeader({ title, subtitle, children, testId }: { title: string; subtitle?: string; children?: ReactNode; testId?: string }) {
	return (
		<header className="flex flex-col gap-3 pb-8" data-testid={testId}>
			<h1 className="text-3xl font-semibold tracking-[-0.025em] text-balance md:text-4xl">{title}</h1>
			{subtitle && <p className="text-muted-foreground max-w-2xl text-base leading-relaxed text-pretty">{subtitle}</p>}
			{children}
		</header>
	);
}
