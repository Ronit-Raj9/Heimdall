import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { getLocale } from "@/lib/i18n";
import { ROLES, roleAtLeast } from "@/lib/store/apis/session";
import type { PlatformTime, Role } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { formatRelative, formatWhen } from "../platform/platform.utils";

/** Roles a caller may hand out: never more than its own. */
export function grantableRoles(caller: Role | undefined): Role[] {
	return ROLES.filter((role) => roleAtLeast(caller, role));
}

/**
 * An admin may change a member only when it holds at least both the member's role and the new one
 * (handlers/members.go mayManage): admins manage members, viewers and admins; owners also owners.
 */
export function mayManage(caller: Role | undefined, from: Role, to?: Role): boolean {
	return roleAtLeast(caller, from) && (to === undefined || roleAtLeast(caller, to));
}

export interface RoleSelectProps {
	value: Role;
	onChange: (role: Role) => void;
	/** Roles offered, least privileged first. */
	roles: readonly Role[];
	label: string;
	disabled?: boolean;
	testId: string;
	size?: "sm" | "default";
	className?: string;
}

/** A role picker with each role's one-line meaning under its name. */
export function RoleSelect({ value, onChange, roles, label, disabled, testId, size = "sm", className }: RoleSelectProps) {
	const { t } = useTranslation("platform");
	return (
		<Select value={value} onValueChange={(next) => onChange(next as Role)} disabled={disabled}>
			<SelectTrigger size={size} className={cn("min-w-28", className)} aria-label={label} data-testid={testId}>
				<SelectValue />
			</SelectTrigger>
			<SelectContent position="popper" className="w-64">
				{roles.map((role) => (
					<SelectItem key={role} value={role} data-testid={`${testId}-${role}`}>
						{t(`roles.${role}`)}
						<span className="text-muted-foreground sr-only">{t(`roles.${role}Hint`)}</span>
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

/** A time as "3 days ago" with the exact time on hover; "Never" (or the given word) when absent. */
export function When({ value, empty, testId }: { value: PlatformTime | null | undefined; empty?: string; testId?: string }) {
	const { t } = useTranslation("platform");
	const locale = getLocale();
	const relative = formatRelative(value, locale);
	if (!relative) return <span className="text-muted-foreground">{empty ?? t("time.never")}</span>;
	return (
		<time
			className="tabular-nums"
			title={formatWhen(value, locale) ?? undefined}
			dateTime={typeof value === "string" ? value : undefined}
			data-testid={testId}
		>
			{relative}
		</time>
	);
}

/** A settings block: title, one line, an action at the right, and its body. */
export function SettingsSection({
	title,
	description,
	actions,
	children,
	testId,
	className,
}: {
	title: string;
	description?: ReactNode;
	actions?: ReactNode;
	children: ReactNode;
	testId: string;
	className?: string;
}) {
	return (
		<section className={cn("flex flex-col gap-4", className)} data-testid={testId}>
			<div className="flex flex-wrap items-end justify-between gap-3">
				<div className="flex max-w-2xl flex-col gap-1">
					<h2 className="text-foreground text-[15px] font-semibold tracking-tight">{title}</h2>
					{description && <p className="text-muted-foreground max-w-prose text-[13px] leading-relaxed text-pretty">{description}</p>}
				</div>
				{actions}
			</div>
			{children}
		</section>
	);
}

/** The bordered frame tables sit in on the settings page. */
export function TableFrame({ children, testId }: { children: ReactNode; testId?: string }) {
	return (
		<div className="overflow-hidden rounded-lg border" data-testid={testId}>
			{children}
		</div>
	);
}
/** Settings forms label their inputs with the shared Field: optional fields say "(optional)", no asterisks (M1-49). */
export { Field, type FieldProps } from "@/components/ui/field";
