import { useConfirm } from "@/components/platform/confirmDialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EmptyState } from "@/components/shell/emptyState";
import { useSession } from "@/lib/session/sessionContext";
import { useGetAdminOverviewQuery, useSetKillSwitchMutation } from "@/lib/store/apis/adminApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { Activity, Building2, OctagonX, PlayCircle, ShieldAlert, Users } from "lucide-react";
import { PageHeader } from "@/components/shell";
import { StatCard, TonePill } from "@/app/operate/views/uxBits";
import { cn } from "@/lib/utils";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";
import { AdminContainersPanel, AdminImagesPanel, AdminOrgsPanel, AdminUsagePanel, AdminUsersPanel } from "./adminPanels";

export const ADMIN_TABS = ["overview", "users", "orgs", "images", "usage", "containers"] as const;
export type AdminTab = (typeof ADMIN_TABS)[number];

/** A tab from the URL, overview when unknown. */
export function adminTab(value: string | null | undefined): AdminTab {
	return (ADMIN_TABS as readonly string[]).includes(value ?? "") ? (value as AdminTab) : "overview";
}

/** Server admin page: only server admins (HEIMDALL_SERVER_ADMINS) reach the panels. */
export default function AdminView({ param = "tab" }: { /** Search parameter of the panel tab; "section" inside Settings, whose own tab is ?tab=. */ param?: string } = {}) {
	const { t } = useTranslation("admin");
	const session = useSession();
	const [tabParam, setTab] = useQueryState(param, parseAsString);
	const tab = adminTab(tabParam);

	if (session.authMode === "local") {
		return (
			<div className="p-6">
				<EmptyState message={t("localMode")} action={{ label: t("localAction"), to: "/operate" }} testId="admin-local" />
			</div>
		);
	}
	if (!session.serverAdmin) {
		return (
			<div className="p-6">
				<EmptyState icon={ShieldAlert} message={t("notAdmin")} action={{ label: t("notAdminAction"), to: "/" }} testId="admin-forbidden" />
			</div>
		);
	}
	return (
		<div className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-6 p-4 sm:p-6" data-testid="admin-view">
			<PageHeader title={t("title")} subtitle={t("subtitle")} />
			<Tabs value={tab} onValueChange={(value) => void setTab(adminTab(value) === "overview" ? null : value)} className="gap-6">
				<TabsList className="max-w-full overflow-x-auto">
					{ADMIN_TABS.map((key) => (
						<TabsTrigger key={key} value={key} data-testid={`admin-tab-${key}`}>
							{t(`tabs.${key}`)}
						</TabsTrigger>
					))}
				</TabsList>
				<TabsContent value="overview">
					<AdminOverviewPanel />
				</TabsContent>
				<TabsContent value="users">{tab === "users" && <AdminUsersPanel />}</TabsContent>
				<TabsContent value="orgs">{tab === "orgs" && <AdminOrgsPanel />}</TabsContent>
				<TabsContent value="images">{tab === "images" && <AdminImagesPanel />}</TabsContent>
				<TabsContent value="usage">{tab === "usage" && <AdminUsagePanel />}</TabsContent>
				<TabsContent value="containers">{tab === "containers" && <AdminContainersPanel />}</TabsContent>
			</Tabs>
		</div>
	);
}

/** Totals and the platform kill switch. */
export function AdminOverviewPanel() {
	const { t } = useTranslation("admin");
	const confirm = useConfirm();
	const { data, error, isLoading, refetch } = useGetAdminOverviewQuery();
	const [setKill, killState] = useSetKillSwitchMutation();
	if (isLoading) {
		return (
			<div className="grid grid-cols-1 gap-4 sm:grid-cols-3" data-testid="admin-overview-loading">
				{[0, 1, 2].map((i) => (
					<div key={i} className="bg-muted h-28 animate-pulse rounded-xl" />
				))}
			</div>
		);
	}
	if (error || !data) {
		return (
			<EmptyState
				compact
				message={t("loadFailed", { message: getBenchErrorMessage(error) })}
				action={{ label: t("retry"), onClick: () => void refetch() }}
				testId="admin-overview-error"
			/>
		);
	}
	const toggle = async () => {
		if (!data.kill_switch && !(await confirm(t("kill.confirmOn")))) return;
		void setKill(!data.kill_switch);
	};
	const on = data.kill_switch;
	return (
		<div className="flex flex-col gap-6">
			{!data.quota_persisted && (
				<p className="border-chart-warning/60 bg-chart-warning/10 rounded-lg border px-4 py-3 text-sm" data-testid="admin-quota-stub">
					{t("overview.stubQuota")}
				</p>
			)}
			<div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
				<StatCard label={t("overview.users")} value={data.users} icon={Users} testId="admin-stat-users" />
				<StatCard label={t("overview.orgs")} value={data.orgs} icon={Building2} testId="admin-stat-orgs" />
				<StatCard label={t("overview.slots")} value={data.running_slots} icon={Activity} testId="admin-stat-slots" />
			</div>
			<section
				className={cn(
					"flex flex-col gap-4 rounded-xl border p-6 transition-colors sm:flex-row sm:items-center sm:justify-between",
					on ? "border-chart-error/50 bg-chart-error/5" : "bg-card",
				)}
				data-testid="admin-kill-switch"
				data-on={on}
			>
				<div className="flex min-w-0 items-start gap-4">
					<span
						className={cn(
							"flex size-11 shrink-0 items-center justify-center rounded-xl",
							on ? "bg-chart-error/15 text-chart-error-ink" : "bg-chart-success/10 text-chart-success-ink",
						)}
					>
						{on ? <OctagonX className="size-5" aria-hidden="true" /> : <PlayCircle className="size-5" aria-hidden="true" />}
					</span>
					<div className="min-w-0">
						<div className="flex flex-wrap items-center gap-2">
							<h2 className="text-foreground text-lg font-semibold tracking-tight">{t("kill.title")}</h2>
							<TonePill tone={on ? "bad" : "good"} pulse={on}>
								{on ? t("kill.badgeOn") : t("kill.badgeOff")}
							</TonePill>
						</div>
						<p className="text-muted-foreground text-sm">{on ? t("kill.on") : t("kill.off")}</p>
						{killState.error && <p className="text-destructive mt-1 text-sm">{getBenchErrorMessage(killState.error)}</p>}
					</div>
				</div>
				<button
					type="button"
					role="switch"
					aria-checked={on}
					aria-label={on ? t("kill.turnOff") : t("kill.turnOn")}
					onClick={() => void toggle()}
					disabled={killState.isLoading}
					className="group flex shrink-0 items-center gap-3 self-start disabled:opacity-60 sm:self-center"
					data-testid="admin-kill-toggle"
				>
					<span className="text-foreground text-sm font-medium">{on ? t("kill.turnOff") : t("kill.turnOn")}</span>
					<span
						className={cn(
							"relative inline-flex h-7 w-12 items-center rounded-full transition-colors",
							on ? "bg-chart-error" : "bg-muted-foreground/30 group-hover:bg-muted-foreground/40",
						)}
					>
						<span className={cn("bg-background inline-block size-5 rounded-full shadow transition-transform", on ? "translate-x-6" : "translate-x-1")} />
					</span>
				</button>
			</section>
		</div>
	);
}
