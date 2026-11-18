import { useSession, type SessionInfo } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { isForbidden } from "@/lib/store/apis/session";
import type { Role } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Lock } from "lucide-react";
import { useTranslation } from "react-i18next";

// No router imports here: LoadError renders this note, and it is used on every screen.

/** The role names the session and a needed role read as, for the forbidden and role notes. */
export function roleWords(session: SessionInfo, min: Role | undefined, t: ReturnType<typeof useTranslation<"platform">>["t"]) {
	return {
		role: session.role ? t(`roles.${session.role}`).toLowerCase() : t("forbidden.noRole"),
		org: session.org?.name ?? t("forbidden.thisOrg"),
		min: min ? t(`roles.${min}`).toLowerCase() : undefined,
	};
}

export interface ForbiddenNoteProps {
	/** The role the action needs, when known. */
	min?: Role;
	/** The failed request, for the server's own message. */
	error?: unknown;
	testId?: string;
	className?: string;
}

/** A 403 in hosted mode: the caller's role cannot do this, said plainly, with the role needed. */
export function ForbiddenNote({ min, error, testId = "forbidden-note", className }: ForbiddenNoteProps) {
	const { t } = useTranslation("platform");
	const session = useSession();
	const words = roleWords(session, min, t);
	const server = error ? getBenchErrorMessage(error) : null;
	return (
		<div
			role="alert"
			className={cn("border-chart-warning/50 bg-chart-warning/10 flex items-start gap-3 rounded-lg border px-4 py-3 text-sm", className)}
			data-testid={testId}
		>
			<Lock className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<div className="flex min-w-0 flex-col gap-1">
				<p className="text-foreground font-medium">{t("forbidden.title")}</p>
				<p className="text-muted-foreground text-pretty">
					{words.min ? t("forbidden.body", { role: words.role, org: words.org, min: words.min }) : t("forbidden.bodyNoMin", words)}
				</p>
				{server && <p className="text-muted-foreground font-mono text-xs break-words">{t("forbidden.server", { message: server })}</p>}
			</div>
		</div>
	);
}

/** True when an error should read as "your role cannot do this": a 403 from a hosted server. */
export function isRoleRefusal(session: SessionInfo, error: unknown): boolean {
	return session.authMode === "hosted" && isForbidden(error);
}