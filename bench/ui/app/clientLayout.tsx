import { BenchHeader } from "@/components/benchHeader";
import { BenchSidebar } from "@/components/benchSidebar";
import { PlanLimitPrompt } from "@/components/billing/planLimitPrompt";
import { AuthShell } from "@/components/platform/authShell";
import { SharedFrame } from "@/components/share/sharedFrame";
import { LandingView } from "@/components/site/landingView";
import { PublicFrame } from "@/components/site/publicFrame";
import { StatusFrame } from "@/components/status/statusFrame";
import { ConfirmProvider } from "@/components/platform/confirmDialog";
import { SessionProvider } from "@/components/platform/sessionProvider";
import { VerifyEmailBanner } from "@/components/platform/verifyEmailBanner";
import { ThemeProvider } from "@/components/themeProvider";
import { activeNavItem } from "@/lib/constants/navigation";
import i18n from "@/lib/i18n";
import { frameFor, useSession } from "@/lib/session/sessionContext";
import { ReduxProvider } from "@/lib/store";
import { Navigate, useRouterState } from "@tanstack/react-router";
import { NuqsAdapter } from "nuqs/adapters/tanstack-router";
import { useCallback, useEffect, useRef, useState } from "react";
import { I18nextProvider, useTranslation } from "react-i18next";

/**
 * Providers and shell for the standalone bench app. When the screens move into ui/, ui/'s own
 * ClientLayout provides all of this and only the route folders and components move.
 */
export function ClientLayout({ children }: { children: React.ReactNode }) {
	return (
		<I18nextProvider i18n={i18n}>
			<ThemeProvider attribute="class" defaultTheme="system" enableSystem>
				<ReduxProvider>
					<SessionProvider>
						<NuqsAdapter>
							<ConfirmProvider>
								<Frame>{children}</Frame>
							</ConfirmProvider>
						</NuqsAdapter>
					</SessionProvider>
				</ReduxProvider>
			</ThemeProvider>
		</I18nextProvider>
	);
}

/**
 * Sign-in, sign-up and invitation pages render without the app shell. In hosted mode, a visitor who
 * is not signed in and is not reading a public demo goes to the login page, which brings them back.
 */
function Frame({ children }: { children: React.ReactNode }) {
	const session = useSession();
	const location = useRouterState({ select: (state) => state.location });
	switch (frameFor(session, location.pathname)) {
		case "share":
			return <SharedFrame>{children}</SharedFrame>;
		case "status":
			return <StatusFrame>{children}</StatusFrame>;
		case "auth":
			return <AuthShell>{children}</AuthShell>;
		case "public":
			return <PublicFrame>{children}</PublicFrame>;
		case "landing":
			// A hosted visitor at "/": the landing page, not Home (which reads org data).
			return (
				<PublicFrame>
					<LandingView />
				</PublicFrame>
			);
		case "login":
			return <Navigate to="/login" search={{ return_to: location.href }} replace />;
		case "error":
			return <SessionError />;
		default:
			return <Shell>{children}</Shell>;
	}
}

// /auth/me failed before any answer (a transport error, a 5xx): the server's mode is unknown, so
// neither local nor hosted controls are shown, only a retry.
function SessionError() {
	const { t } = useTranslation("platform");
	return (
		<div className="bg-background flex min-h-dvh items-center justify-center px-4" data-testid="session-error">
			<div role="alert" className="flex max-w-sm flex-col items-center gap-3 text-center">
				<p className="text-sm">{t("session.discoveryError")}</p>
				<button
					type="button"
					className="bg-primary text-primary-foreground rounded-md px-3 py-1.5 text-sm"
					onClick={() => window.location.reload()}
					data-testid="session-retry"
				>
					{t("session.retry")}
				</button>
			</div>
		</div>
	);
}

// Frame: the sidebar tone behind the nav and the slim top bar, the page in a soft grey panel
// (white cards sit on it). Below md the sidebar is a drawer the header's menu button opens and
// the panel runs edge to edge.
function Shell({ children }: { children: React.ReactNode }) {
	const { t } = useTranslation("bench");
	const [navOpen, setNavOpen] = useState(false);
	const openNav = useCallback(() => setNavOpen(true), []);
	const closeNav = useCallback(() => setNavOpen(false), []);
	useRouteChange();
	return (
		<div className="bg-sidebar flex min-h-dvh flex-col md:h-dvh">
			{/* First in the tab order, before the navigation. */}
			<a
				href="#bench-main"
				className="bg-primary text-primary-foreground sr-only z-50 rounded-md px-3 py-2 text-sm focus:not-sr-only focus:fixed focus:top-3 focus:left-3"
				data-testid="skip-to-content"
			>
				{t("app.skipToContent")}
			</a>
			<div className="flex min-h-0 flex-1 flex-col md:flex-row">
				<BenchSidebar drawerOpen={navOpen} onDrawerClose={closeNav} />
				<div className="flex min-h-0 w-full min-w-0 flex-1 flex-col">
					<BenchHeader onOpenNav={openNav} navOpen={navOpen} />
					<div
						className="bg-background mx-0 min-h-0 min-w-0 flex-1 overflow-auto border-t [scrollbar-gutter:stable] md:mr-2 md:mb-2 md:rounded-xl md:border md:shadow-(--shadow-card)"
						data-bench-content
					>
						<main
							id="bench-main"
							tabIndex={-1}
							className="animate-in fade-in relative mx-auto flex min-h-0 w-full max-w-[1440px] flex-col px-4 py-6 outline-none duration-300 md:px-8 md:py-8 lg:px-10"
							data-bench-main
						>
							<VerifyEmailBanner />
							{children}
						</main>
					</div>
				</div>
			</div>
			<PlanLimitPrompt />
		</div>
	);
}
/**
 * On every page change (not a tab or filter change, which keep the path): the tab title names the
 * section, the content starts at its top, and keyboard focus moves to the content so the next Tab
 * continues on the new page instead of in the navigation. The first load leaves focus alone.
 */
function useRouteChange() {
	const { t } = useTranslation("bench");
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	// The path focus was last handled for; the first load and a repeated effect run do nothing.
	const handled = useRef(pathname);
	useEffect(() => {
		const item = activeNavItem(pathname);
		document.title = item ? `${t(item.labelKey)} · ${t("app.name")}` : t("app.name");
		if (handled.current === pathname) return;
		handled.current = pathname;
		const content = document.querySelector<HTMLElement>("[data-bench-content]");
		if (content) content.scrollTop = 0;
		document.getElementById("bench-main")?.focus({ preventScroll: true });
	}, [pathname, t]);
}