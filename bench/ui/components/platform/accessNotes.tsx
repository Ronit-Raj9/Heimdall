import { canUse, useSession } from "@/lib/session/sessionContext";
import "@/lib/types/u19I18n";
import "@/lib/types/welcomeI18n";
import type { Role } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Lock } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { roleWords } from "./forbiddenNote";

export { ForbiddenNote, isRoleRefusal } from "./forbiddenNote";

/** One line under a control the session may not use: the role it needs, or a sign-in note. */
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
	/** What a signed-in member with too small a role sees instead. Default: a RoleNote. Visitors who are not signed in see nothing. */
	fallback?: ReactNode;
}

/**
 * Write controls behind a role. Local mode always shows them (the write token is the gate). A visitor
 * who is not signed in gets nothing at all; a signed-in member with too small a role gets the fallback.
 */
export function RoleGate({ min, children, fallback }: RoleGateProps) {
	const session = useSession();
	if (canUse(session, min)) return <>{children}</>;
	if (!session.signedIn) return null;
	return <>{fallback === undefined ? <RoleNote min={min} /> : fallback}</>;
}