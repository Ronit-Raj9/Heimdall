import { Button } from "@/components/ui/button";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { githubStartUrl } from "@/lib/store/apis/platformApi";
import "@/lib/types/u19I18n";
import "@/lib/types/welcomeI18n";
import type { Role } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link, useRouterState } from "@tanstack/react-router";
import { ArrowRight, Eye, Github, Lock } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { roleWords } from "./forbiddenNote";

export { ForbiddenNote, isRoleRefusal } from "./forbiddenNote";

/** Slim top banner for anonymous visitors of a public-read organization: what this is, and one way in. */
export function DemoBanner() {
	const { t } = useTranslation("platform");
	const { t: tw } = useTranslation("welcome");
	const { t: tu } = useTranslation("u19");
	const session = useSession();
	const href = useRouterState({ select: (state) => state.location.href });
	if (!session.demo) return null;
	return (
		<div
			role="note"
			className="bg-primary/[0.06] text-foreground border-primary/15 flex min-h-10 items-center gap-3 border-b px-4 py-1.5 text-[13px] md:px-5"
			data-testid="demo-banner"
		>
			<span className="bg-primary/10 text-primary flex size-6 shrink-0 items-center justify-center rounded-full">
				<Eye className="size-3.5" aria-hidden="true" />
			</span>
			<span className="min-w-0 flex-1 truncate">
				<span className="font-medium">{t("demo.banner")}</span>
				<span className="text-muted-foreground hidden sm:inline"> · {tu("banner.detail")}</span>
			</span>
			{/* L21 onboarding: the one guest CTA leads with GitHub when the server offers it. */}
			{session.github ? (
				<Button asChild size="sm" className="h-7 shrink-0 rounded-full px-3">
					<a href={githubStartUrl(href)} data-testid="demo-banner-github" title={tw("guest.cta")}>
						<Github className="size-3.5" aria-hidden="true" />
						<span className="hidden sm:inline">{tw("guest.cta")}</span>
						<span className="sm:hidden">{t("demo.signIn")}</span>
					</a>
				</Button>
			) : (
				<Button asChild size="sm" className="h-7 shrink-0 rounded-full px-3">
					<Link to="/login" search={{ return_to: href }} data-testid="demo-banner-sign-in">
						{t("demo.signIn")}
						<ArrowRight className="size-3.5" aria-hidden="true" />
					</Link>
				</Button>
			)}
		</div>
	);
}

/** One line under a control the session may not use: the role it needs, or the demo note. */
export function RoleNote({ min, className, testId = "role-note" }: { min: Role; className?: string; testId?: string }) {
	const { t } = useTranslation("platform");
	const session = useSession();
	if (canUse(session, min)) return null;
	const words = roleWords(session, min, t);
	return (
		<p className={cn("text-muted-foreground flex items-center gap-1.5 text-xs", className)} data-testid={testId}>
			<Lock className="size-3 shrink-0" aria-hidden="true" />
			{session.signedIn ? t("roleNote.needs", { min: words.min, role: words.role }) : t("roleNote.demo")}
		</p>
	);
}

export interface RoleGateProps {
	/** The least role that may use the children. */
	min: Role;
	children: ReactNode;
	/** What a signed-in member with too small a role sees instead. Default: a RoleNote. Demo visitors see nothing. */
	fallback?: ReactNode;
}

/**
 * Write controls behind a role. Local mode always shows them (the write token is the gate). A demo
 * visitor gets nothing at all; a signed-in member with too small a role gets the fallback.
 */
export function RoleGate({ min, children, fallback }: RoleGateProps) {
	const session = useSession();
	if (canUse(session, min)) return <>{children}</>;
	if (!session.signedIn) return null;
	return <>{fallback === undefined ? <RoleNote min={min} /> : fallback}</>;
}