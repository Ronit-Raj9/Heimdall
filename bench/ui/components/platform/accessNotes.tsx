import { Button } from "@/components/ui/button";
import { canUse, useSession } from "@/lib/session/sessionContext";
import type { Role } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link, useRouterState } from "@tanstack/react-router";
import { Eye, Lock } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { roleWords } from "./forbiddenNote";

export { ForbiddenNote, isRoleRefusal } from "./forbiddenNote";

/** Top banner for anonymous visitors of a public-read organization. */
export function DemoBanner() {
	const { t } = useTranslation("platform");
	const session = useSession();
	const href = useRouterState({ select: (state) => state.location.href });
	if (!session.demo) return null;
	return (
		<div
			role="note"
			className="border-chart-info/40 bg-chart-info/10 text-foreground flex flex-wrap items-center gap-x-3 gap-y-1 border-b px-4 py-2 text-sm md:px-5"
			data-testid="demo-banner"
		>
			<span className="text-chart-info-ink flex items-center gap-2 font-semibold">
				<Eye className="size-4 shrink-0" aria-hidden="true" />
				{t("demo.banner")}
			</span>
			{/* U1 shell: on a narrow screen the detail takes its own line under the title and the button. */}
			<span className="text-muted-foreground order-last min-w-0 basis-full text-[13px] sm:order-none sm:flex-1 sm:basis-0">
				{t("demo.detail")}
			</span>
			<Button asChild variant="outline" size="sm" className="ml-auto h-7 sm:ml-0">
				<Link to="/login" search={{ return_to: href }} data-testid="demo-banner-sign-in">
					{t("demo.signIn")}
				</Link>
			</Button>
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