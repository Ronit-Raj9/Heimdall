import { useConfirm } from "@/components/platform/confirmDialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EmptyState } from "@/components/shell/emptyState";
import { useSession } from "@/lib/session/sessionContext";
import { useGetAdminOverviewQuery, useSetKillSwitchMutation } from "@/lib/store/apis/adminApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { Activity, Building2, OctagonX, PlayCircle, ShieldAlert, Users } from "lucide-react";
import { PageHeader } from "@/components/shell";
import { StatCard } from "@/app/operate/views/uxBits";
import { Button } from "@/components/ui/button";
import "@/components/settings/uf8aI18n";
import { cn } from "@/lib/utils";
import { parseAsString, useQueryState } from "nuqs";
import { useScrollEdges } from "@/lib/hooks/useScrollEdges";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { AdminContainersPanel, AdminImagesPanel, AdminOrgsPanel, AdminUsagePanel, AdminUsersPanel } from "./adminPanels";
import { AdminSignupPanel } from "./adminSignupPanel";

export const ADMIN_TABS = ["overview", "users", "orgs", "signup", "images", "usage", "workers"] as const;
export type AdminTab = (typeof ADMIN_TABS)[number];

/** Old tab ids that still open their tab (N152: the Workers tab was "containers"); the URL is rewritten to the current id. */
const ADMIN_TAB_ALIASES: Record<string, AdminTab> = { containers: "workers" };

/** A tab from the URL, overview when unknown. */
export function adminTab(value: string | null | undefined): AdminTab {
	const v = value ?? "";
	if (v in ADMIN_TAB_ALIASES) return ADMIN_TAB_ALIASES[v];
	return (ADMIN_TABS as readonly string[]).includes(v) ? (v as AdminTab) : "overview";
}

/** Server admin page: only server admins (by user id, CLI grant, or verified HEIMDALL_SERVER_ADMINS email) reach the panels. */
export default function AdminView({
	param = "tab",
}: { /** Search parameter of the panel tab; "section" inside Settings, whose own tab is ?tab=. */ param?: string } = {}) {
	const { t } = useTranslation("admin");
	const session = useSession();
	const [tabParam, setTab] = useQueryState(param, parseAsString);
	const tab = adminTab(tabParam);
	useEffect(() => {
		if (tabParam && tabParam in ADMIN_TAB_ALIASES) void setTab(ADMIN_TAB_ALIASES[tabParam]);
	}, [tabParam, setTab]);
	// On a phone the sub tab strip scrolls sideways: fade the hidden edge and keep the active tab in view (M8-2).
	const strip = useRef<HTMLDivElement>(null);
	useScrollEdges(strip);
	useEffect(() => {
		strip.current?.querySelector<HTMLElement>('[data-state="active"]')?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
	}, [tab]);

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
				<TabsList ref={strip} className="max-w-full justify-start overflow-x-auto">
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
				<TabsContent value="signup">{tab === "signup" && <AdminSignupPanel />}</TabsContent>
				<TabsContent value="images">{tab === "images" && <AdminImagesPanel />}</TabsContent>
				<TabsContent value="usage">{tab === "usage" && <AdminUsagePanel />}</TabsContent>
				<TabsContent value="workers">{tab === "workers" && <AdminContainersPanel />}</TabsContent>
			</Tabs>
		</div>
	);
}

/** What the emergency stop asks before it flips: stopping and allowing again both confirm. */
export function killAsk(on: boolean): {
	ns: "admin" | "uf8a";
	question: "kill.confirmOn" | "kill.confirmOff";
	label: "kill.stop" | "kill.allow";
	destructive: boolean;
} {
	return on
		? { ns: "uf8a", question: "kill.confirmOff", label: "kill.allow", destructive: false }
		: { ns: "admin", question: "kill.confirmOn", label: "kill.stop", destructive: true };
}

/** Totals and the platform kill switch. */
export function AdminOverviewPanel() {
	const { t } = useTranslation("admin");
	const { t: tl } = useTranslation("uf8a");
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
	const on = data.kill_switch;
	// Both directions ask: stopping blocks every organization, allowing again starts queued runs at once.
	const toggle = async () => {
		const ask = killAsk(on);
		const ok = await confirm(on ? tl("kill.confirmOff") : t("kill.confirmOn"), {
			confirmLabel: tl(ask.label),
			destructive: ask.destructive,
		});
		if (!ok) return;
		void setKill(!on);
	};
	return (
		<div className="flex flex-col gap-6">
			<section
				className={cn(
					"border-chart-error/40 flex flex-col gap-4 rounded-xl border p-6 sm:flex-row sm:items-center sm:justify-between",
					on ? "bg-chart-error/5" : "bg-card",
				)}
				data-testid="admin-kill-switch"
				data-on={on}
			>
				<div className="flex min-w-0 items-start gap-4">
					<span className="bg-chart-error/15 text-chart-error-ink flex size-11 shrink-0 items-center justify-center rounded-xl">
						{on ? <OctagonX className="size-5" aria-hidden="true" /> : <PlayCircle className="size-5" aria-hidden="true" />}
					</span>
					<div className="min-w-0">
						<h2 className="text-foreground text-lg font-semibold tracking-tight">{t("kill.title")}</h2>
						<p
							className={cn("text-sm", on ? "text-chart-error-ink font-medium" : "text-muted-foreground")}
							role="status"
							data-testid="admin-kill-state"
						>
							{on ? tl("kill.stateOn") : tl("kill.stateOff")}
						</p>
						{killState.error && <p className="text-destructive mt-1 text-sm">{getBenchErrorMessage(killState.error)}</p>}
					</div>
				</div>
				<Button
					variant={on ? "outline" : "destructive"}
					onClick={() => void toggle()}
					isLoading={killState.isLoading}
					className="shrink-0 self-start sm:self-center"
					dataTestId="admin-kill-toggle"
				>
					{on ? tl("kill.allow") : tl("kill.stop")}
				</Button>
			</section>
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
		</div>
	);
}