import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EmptyState } from "@/components/shell/emptyState";
import { useSession } from "@/lib/session/sessionContext";
import { useGetAdminOverviewQuery, useSetKillSwitchMutation } from "@/lib/store/apis/adminApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { ShieldAlert } from "lucide-react";
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
export default function AdminView() {
	const { t } = useTranslation("admin");
	const session = useSession();
	const [tabParam, setTab] = useQueryState("tab", parseAsString);
	const tab = adminTab(tabParam);

	if (session.authMode === "local") {
		return (
			<div className="p-6">
				<EmptyState message={t("localMode")} testId="admin-local" />
			</div>
		);
	}
	if (!session.serverAdmin) {
		return (
			<div className="p-6">
				<EmptyState icon={ShieldAlert} message={t("notAdmin")} testId="admin-forbidden" />
			</div>
		);
	}
	return (
		<div className="flex flex-col gap-6 p-6" data-testid="admin-view">
			<div>
				<h1 className="text-xl font-semibold">{t("title")}</h1>
				<p className="text-muted-foreground text-sm">{t("subtitle")}</p>
			</div>
			<Tabs value={tab} onValueChange={(value) => void setTab(adminTab(value) === "overview" ? null : value)} className="gap-6">
				<TabsList>
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
	const { data, error, isLoading, refetch } = useGetAdminOverviewQuery();
	const [setKill, killState] = useSetKillSwitchMutation();
	if (isLoading) return <p className="text-muted-foreground text-sm">...</p>;
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
	const toggle = () => {
		if (!data.kill_switch && !window.confirm(t("kill.confirmOn"))) return;
		void setKill(!data.kill_switch);
	};
	return (
		<div className="flex flex-col gap-4">
			{!data.quota_persisted && (
				<p className="border-chart-warning/60 bg-chart-warning/10 rounded-md border px-3 py-2 text-sm" data-testid="admin-quota-stub">
					{t("overview.stubQuota")}
				</p>
			)}
			<div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
				<Stat label={t("overview.users")} value={data.users} testId="admin-stat-users" />
				<Stat label={t("overview.orgs")} value={data.orgs} testId="admin-stat-orgs" />
				<Stat label={t("overview.slots")} value={data.running_slots} testId="admin-stat-slots" />
			</div>
			<Card data-testid="admin-kill-switch">
				<CardHeader>
					<CardTitle className="flex items-center gap-2">
						{t("kill.title")}
						<Badge variant={data.kill_switch ? "destructive" : "success"}>{data.kill_switch ? "ON" : "OFF"}</Badge>
					</CardTitle>
					<CardDescription>{data.kill_switch ? t("kill.on") : t("kill.off")}</CardDescription>
				</CardHeader>
				<CardContent className="flex items-center gap-3">
					<Button
						variant={data.kill_switch ? "default" : "destructive"}
						onClick={toggle}
						disabled={killState.isLoading}
						data-testid="admin-kill-toggle"
					>
						{data.kill_switch ? t("kill.turnOff") : t("kill.turnOn")}
					</Button>
					{killState.error && <span className="text-destructive text-sm">{getBenchErrorMessage(killState.error)}</span>}
				</CardContent>
			</Card>
		</div>
	);
}

function Stat({ label, value, testId }: { label: string; value: number; testId: string }) {
	return (
		<Card data-testid={testId}>
			<CardHeader>
				<CardDescription>{label}</CardDescription>
				<CardTitle className="text-2xl tabular-nums">{value}</CardTitle>
			</CardHeader>
		</Card>
	);
}
