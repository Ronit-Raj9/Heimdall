import { RoleBadge } from "@/components/platform/badges";
import { ApiTokensTab } from "@/components/settings/apiTokensTab";
import { AuditTab } from "@/components/settings/auditTab";
import { BillingTab } from "@/components/settings/billingTab";
import { GithubTab } from "@/components/settings/githubTab";
import { MembersTab } from "@/components/settings/membersTab";
import { OrgTab } from "@/components/settings/orgTab";
import { PasswordTab } from "@/components/settings/passwordTab";
import { RunnersTab } from "@/components/settings/runnersTab";
import { SecretsTab } from "@/components/settings/secretsTab";
import { EmptyNote } from "@/components/runs/screenStates";
import { EmbeddedPage, RouteTabs } from "@/components/shell";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useSession, type SessionInfo } from "@/lib/session/sessionContext";
import AdminView from "@/app/admin/views/adminView";
import FilesView from "@/app/files/views/filesView";
import IntegrationsView from "@/app/integrations/views/integrationsView";
import JobsView from "@/app/jobs/index/views/jobsView";
import ModelsView from "@/app/models/views/modelsView";
import OperateView from "@/app/operate/views/operateView";
import UploadView from "@/app/upload/views/uploadView";
import UsageView from "@/app/usage/views/usageView";
import WorkersView from "@/app/workers/views/workersView";
import { Building2, CreditCard, FolderTree, Gauge, Github, KeyRound, KeySquare, ListChecks, LockKeyhole, Plug, ScrollText, Server, Shield, UserRound, Users, Wrench } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Link } from "@tanstack/react-router";
import type { ComponentType } from "react";
import { useTranslation } from "react-i18next";

/**
 * Every Settings tab, in menu order. "runners" is the Workers tab (its id predates the rename and
 * stays for old links). Members, tokens, GitHub, activity, organization and account exist only in
 * hosted mode; Server admin only for server admins.
 */
export const SETTINGS_TABS = [
	"keys",
	"runners",
	"usage",
	"billing",
	"files",
	"jobs",
	"integrations",
	"operations",
	"members",
	"tokens",
	"secrets",
	"github",
	"audit",
	"org",
	"account",
	"admin",
] as const;
export type SettingsTab = (typeof SETTINGS_TABS)[number];

/** The tab a plain /settings opens. */
export const DEFAULT_SETTINGS_TAB: SettingsTab = "keys";

const HOSTED_ONLY: readonly SettingsTab[] = ["members", "tokens", "github", "audit", "org", "account", "billing"];

const TAB_ICON: Record<SettingsTab, ComponentType<{ className?: string }>> = {
	keys: KeyRound,
	runners: Server,
	usage: Gauge,
	billing: CreditCard,
	files: FolderTree,
	jobs: ListChecks,
	integrations: Plug,
	operations: Wrench,
	members: Users,
	tokens: KeySquare,
	secrets: LockKeyhole,
	github: Github,
	audit: ScrollText,
	org: Building2,
	account: UserRound,
	admin: Shield,
};

/** A tab name from the URL, or the default (API keys) for anything else. */
export function settingsTab(raw: string | null | undefined): SettingsTab {
	if (raw === "workers") return "runners";
	return SETTINGS_TABS.includes(raw as SettingsTab) ? (raw as SettingsTab) : DEFAULT_SETTINGS_TAB;
}

/** The tabs this session sees: team tabs only in hosted mode, Server admin only for server admins. */
export function settingsTabsFor(session: SessionInfo): SettingsTab[] {
	const hosted = session.authMode !== "local";
	return SETTINGS_TABS.filter((id) => (hosted || !HOSTED_ONLY.includes(id)) && (id !== "admin" || session.serverAdmin === true));
}

export interface SettingsViewProps {
	tab: SettingsTab;
	onTabChange: (tab: SettingsTab) => void;
}

/**
 * The one Settings page: API keys, workers, usage, project files, background jobs,
 * integrations, operations, secrets and, in hosted mode, members, tokens, GitHub, the activity
 * log, the organization and the signed-in user's password. The old pages (/models, /workers,
 * /usage, /files, /jobs, /integrations, /operate, /admin) redirect to their tab here.
 */
export default function SettingsView({ tab, onTabChange }: SettingsViewProps) {
	const { t } = useTranslation("platform");
	const { t: tSecrets } = useTranslation("secrets");
	const session = useSession();
	const org = session.org;
	const local = session.authMode === "local";

	if (!local && (!session.signedIn || !org)) {
		return (
			<section className="flex flex-col gap-6" data-testid="settings-screen">
				<h1 className="text-foreground">{t("settings.title")}</h1>
				<EmptyNote testId="settings-signed-out">{t("settings.notSignedIn")}</EmptyNote>
				<div>
					<Button asChild size="sm" data-testid="settings-sign-in">
						<Link to="/login" search={{ return_to: "/settings" }}>
							{tSecrets("signIn")}
						</Link>
					</Button>
				</div>
			</section>
		);
	}

	const tabs = settingsTabsFor(session);
	const active = tabs.includes(tab) ? tab : DEFAULT_SETTINGS_TAB;

	return (
		<section
			className="mx-auto flex w-full max-w-[1280px] min-w-0 flex-col gap-8"
			data-testid="settings-screen"
			data-mode={local ? "local" : "hosted"}
		>
			<header className="flex flex-col gap-1.5">
				<div className="flex flex-wrap items-center gap-3">
					<h1 className="text-foreground text-2xl font-semibold tracking-tight">{t("settings.title")}</h1>
					{!local && session.role && <RoleBadge role={session.role} />}
				</div>
				<p className="text-muted-foreground text-sm" data-testid={local ? "settings-local" : undefined}>
					{local ? t("settings.localBody") : t("settings.subtitle", { org: org?.name ?? "" })}
				</p>
			</header>
			<Tabs
				value={active}
				onValueChange={(value) => onTabChange(settingsTab(value))}
				orientation="vertical"
				className="gap-6 md:grid md:grid-cols-[200px_minmax(0,1fr)] md:items-start md:gap-10"
			>
				<div className="-mx-1 overflow-x-auto px-1 pb-1 md:sticky md:top-4 md:mx-0 md:overflow-visible md:p-0">
					<TabsList className="h-auto w-max gap-1 bg-transparent p-0 md:w-full md:flex-col md:items-stretch" aria-label={t("settings.title")}>
						{tabs.map((id) => {
							const Icon = TAB_ICON[id];
							return (
								<TabsTrigger
									key={id}
									value={id}
									className="text-muted-foreground hover:bg-muted hover:text-foreground data-[state=active]:bg-muted data-[state=active]:text-foreground h-9 flex-none justify-start gap-2.5 rounded-lg px-3 data-[state=active]:shadow-none md:w-full dark:data-[state=active]:border-transparent dark:data-[state=active]:bg-muted"
									data-testid={`settings-tab-${id}`}
								>
									<Icon className="size-4" aria-hidden="true" />
									{t(`settings.tabs.${id}`)}
								</TabsTrigger>
							);
						})}
					</TabsList>
				</div>
				<div className="min-w-0">
					<EmbeddedPage>
						<TabsContent value={active}>
							<SettingsTabBody tab={active} />
						</TabsContent>
					</EmbeddedPage>
				</div>
			</Tabs>
		</section>
	);
}

/** One tab's content. Only the active tab renders, so the other pages' queries stay idle. */
function SettingsTabBody({ tab }: { tab: SettingsTab }) {
	const { t } = useTranslation("platform");
	const session = useSession();
	const org = session.org;
	const local = session.authMode === "local";
	switch (tab) {
		case "keys":
			return <ModelsView />;
		case "runners":
			return local || !org ? <WorkersView tokensLink={false} /> : <RunnersTab org={org} />;
		case "usage":
			return <UsageView />;
		case "billing":
			return <BillingTab />;
		case "files":
			return (
				<RouteTabs
					tabs={[
						{ id: "browse", label: t("settings.files.browse") },
						{ id: "upload", label: t("settings.files.upload") },
					]}
					param="files"
					label={t("settings.tabs.files")}
					testId="settings-files-tabs"
				>
					{(view) => (view === "upload" ? <UploadView /> : <FilesView />)}
				</RouteTabs>
			);
		case "jobs":
			return <JobsView />;
		case "integrations":
			return <IntegrationsView />;
		case "operations":
			return <OperateView />;
		case "secrets":
			return <SecretsTab />;
		case "admin":
			return <AdminView param="section" />;
		case "github":
			return <GithubTab />;
		case "members":
		case "tokens":
		case "audit":
		case "org":
		case "account":
			if (!org) return null;
			if (tab === "members") return <MembersTab org={org} />;
			if (tab === "tokens") return <ApiTokensTab />;
			if (tab === "audit") return <AuditTab org={org} />;
			if (tab === "org") return <OrgTab org={org} />;
			return <PasswordTab />;
	}
}
