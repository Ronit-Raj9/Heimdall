import { RoleBadge } from "@/components/platform/badges";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@/components/ui/select";
import { useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useSwitchOrgMutation } from "@/lib/store/apis/platformApi";
import { cn } from "@/lib/utils";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { Building2, Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";

/** First letters of an organization name, for its square. */
function orgMark(name: string): string {
	return name.trim().charAt(0).toUpperCase() || "?";
}

/**
 * The organization the session acts in, and a switcher when the user belongs to more than one.
 * Switching refetches everything (every bench route is per organization) and drops the page's
 * query string, whose ids belonged to the other organization.
 */
export function OrgSwitcher() {
	const { t } = useTranslation("platform");
	const session = useSession();
	const navigate = useNavigate();
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	const [switchOrg, state] = useSwitchOrgMutation();
	const org = session.org;
	if (session.authMode !== "hosted") return null;
	// Signed in with memberships but no current org (the current one was just left): requests
	// resolve no org until one is chosen, so the picker is offered instead of nothing.
	if (!org && !(session.signedIn && session.orgs.length > 0)) return null;

	const square = org && (
		<span
			aria-hidden="true"
			className="bg-primary/12 text-primary flex size-6 shrink-0 items-center justify-center rounded-md text-[11px] font-semibold"
		>
			{orgMark(org.name)}
		</span>
	);

	// A member of one organization see its name, nothing to pick.
	if (org && (!session.signedIn || session.orgs.length <= 1)) {
		return (
			<div className="flex min-w-0 items-center gap-2 px-1" data-testid="org-current">
				{square}
				<span className="text-foreground truncate text-sm font-medium">{org.name}</span>
				{session.role && <RoleBadge role={session.role} className="hidden sm:inline-flex" />}
			</div>
		);
	}

	const onChange = async (orgId: string) => {
		if (orgId === org?.id) return;
		try {
			await switchOrg({ org: orgId }).unwrap();
			void navigate({ href: pathname });
		} catch {
			// state.error renders the failure.
		}
	};

	return (
		<div className="flex min-w-0 items-center gap-2">
			<Select value={org?.id} onValueChange={(value) => void onChange(value)} disabled={state.isLoading}>
				<SelectTrigger
					size="sm"
					className="hover:bg-accent h-8 max-w-64 min-w-0 gap-2 border-transparent bg-transparent px-1.5 shadow-none dark:bg-transparent"
					aria-label={t("header.switchOrg")}
					data-testid="org-switcher"
				>
					{square}
					<span className="truncate text-sm font-medium">{org ? org.name : t("header.pickOrg")}</span>
					{state.isLoading && <Loader2 className="size-3.5 animate-spin" aria-label={t("header.switching")} />}
				</SelectTrigger>
				<SelectContent position="popper" align="start" className="w-72">
					<div className="text-muted-foreground flex items-center gap-1.5 px-2 pt-1.5 pb-1 text-[11px] font-medium tracking-[0.06em] uppercase">
						<Building2 className="size-3" aria-hidden="true" />
						{t("header.org")}
					</div>
					{session.orgs.map((option) => (
						<SelectItem key={option.id} value={option.id} data-testid={`org-option-${option.slug}`}>
							<span className="flex w-full min-w-0 items-center gap-2">
								<span className="truncate">{option.name}</span>
								<span className="text-muted-foreground font-mono text-[11px]">{option.slug}</span>
								{option.role && <RoleBadge role={option.role} className="ml-auto" />}
							</span>
						</SelectItem>
					))}
				</SelectContent>
			</Select>
			{session.role && <RoleBadge role={session.role} className={cn("hidden lg:inline-flex")} />}
			{state.error && (
				<span role="alert" className="text-chart-error-ink truncate text-xs" data-testid="org-switch-error">
					{t("header.switchError", { error: getBenchErrorMessage(state.error) })}
				</span>
			)}
		</div>
	);
}