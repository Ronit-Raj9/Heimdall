import { cn } from "@/lib/utils";
import { pageWidthClass } from "@/components/shell/pageWidth";
import "@/components/settings/uf8aI18n";
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
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { canUse, useSession, type SessionInfo } from "@/lib/session/sessionContext";
import { OrgShareLinks } from "@/components/share/orgShareLinks";
import "@/components/platform/ufintI18n";
import AdminView from "@/app/admin/views/adminView";
import FilesView from "@/app/files/views/filesView";
import IntegrationsView from "@/app/integrations/views/integrationsView";
import JobsView from "@/app/jobs/index/views/jobsView";
import ModelsView from "@/app/models/views/modelsView";
import OperateView from "@/app/operate/views/operateView";
import UploadView from "@/app/upload/views/uploadView";
import UsageView from "@/app/usage/views/usageView";
import WorkersView from "@/app/workers/views/workersView";
import {
	Building2,
	CreditCard,
	Link2,
	FolderTree,
	Gauge,
	Github,
	KeyRound,
	KeySquare,
	ListChecks,
	LockKeyhole,
	Plug,
	ScrollText,
	Server,
	Shield,
	UserRound,
	Users,
	Wrench,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Link } from "@tanstack/react-router";
import { Fragment, type ComponentType } from "react";
import { useTranslation } from "react-i18next";

/**
 * Every Settings tab, in menu order. "runners" is the Workers tab (its id predates the rename and
 * stays for old links). Members, tokens, GitHub, activity, organization and account exist only in
 * hosted mode; Server admin only for server admins. The rail shows them in SETTINGS_GROUPS order.
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
	"shares",
	"account",
	"admin",
] as const;
export type SettingsTab = (typeof SETTINGS_TABS)[number];

export type SettingsGroup = "models" | "credentials" | "running" | "project" | "team" | "you" | "server";

/** Rail groups, in rail order: money first, the credential kinds together. */
export const SETTINGS_GROUPS: readonly { id: SettingsGroup; tabs: readonly SettingsTab[] }[] = [
	{ id: "models", tabs: ["keys", "usage", "billing"] },
	{ id: "credentials", tabs: ["secrets", "tokens", "github"] },
	{ id: "running", tabs: ["runners", "jobs", "integrations", "operations"] },
	{ id: "project", tabs: ["files"] },
	{ id: "team", tabs: ["members", "org", "shares", "audit"] },
	{ id: "you", tabs: ["account"] },
	{ id: "server", tabs: ["admin"] },
];

/** The groups holding the visible tabs; a group with no visible tab drops out (no empty heading). */
export function settingsGroupsFor(visible: readonly SettingsTab[]): { id: SettingsGroup; tabs: SettingsTab[] }[] {
	return SETTINGS_GROUPS.map((g) => ({ id: g.id, tabs: g.tabs.filter((id) => visible.includes(id)) })).filter((g) => g.tabs.length > 0);
}

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
	shares: Link2,
	account: UserRound,
	admin: Shield,
};

/** A tab name from the URL, or the default (Model keys) for anything else. */
export function settingsTab(raw: string | null | undefined): SettingsTab {
	if (raw === "workers") return "runners";
	return SETTINGS_TABS.includes(raw as SettingsTab) ? (raw as SettingsTab) : DEFAULT_SETTINGS_TAB;
}

/**
 * The tabs this session sees: team tabs only in hosted mode, Server admin only for server admins,
 * Shared links (every share link of the organization) only for organization admins.
 */
export function settingsTabsFor(session: SessionInfo): SettingsTab[] {
	const hosted = session.authMode !== "local";
	return SETTINGS_TABS.filter(
		(id) =>
			(hosted || !HOSTED_ONLY.includes(id)) &&
			(id !== "admin" || session.serverAdmin === true) &&
			(id !== "shares" || canUse(session, "admin")),
	);
}

export interface SettingsViewProps {
	tab: SettingsTab;
	onTabChange: (tab: SettingsTab) => void;
}

/**
 * The one Settings page: model keys, workers, usage, project files, background jobs,
 * integrations, operations, secrets and, in hosted mode, members, tokens, GitHub, the activity
 * log, the organization and the signed-in user's password. The old pages (/models, /workers,
 * /usage, /files, /jobs, /integrations, /operate, /admin) redirect to their tab here.
 */
export default function SettingsView({ tab, onTabChange }: SettingsViewProps) {
	const { t } = useTranslation("platform");
	const { t: tSecrets } = useTranslation("secrets");
	const { t: tl } = useTranslation("uf8a");
	const { t: ti } = useTranslation("ufint");
	const session = useSession();
	const org = session.org;
	const local = session.authMode === "local";
	const tabs = settingsTabsFor(session);
	const active = tabs.includes(tab) ? tab : DEFAULT_SETTINGS_TAB;
	// Renamed tabs (Model keys, Password) read from this lane's catalog; the rest keep their names.
	const tabLabel = (id: SettingsTab) =>
		id === "keys" ? tl("settings.tabKeys") : id === "account" ? tl("settings.tabAccount") : id === "shares" ? ti("settings.tabShares") : t(`settings.tabs.${id}`);
	usePageTitle(`${tabLabel(active)} · ${t("settings.title")} · Heimdall`);

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

	const groups = settingsGroupsFor(tabs);

	return (
		<section
			className={cn(pageWidthClass("wide"), "flex flex-col gap-8")}
			data-testid="settings-screen"
			data-mode={local ? "local" : "hosted"}
		>
			<header className="flex flex-col gap-1.5">
				<h1 className="text-foreground text-2xl font-semibold tracking-tight">{t("settings.title")}</h1>
				{local ? (
					<p className="text-muted-foreground text-sm" data-testid="settings-local">
						{t("settings.localBody")}
					</p>
				) : null}
			</header>
			<Tabs
				value={active}
				onValueChange={(value) => onTabChange(settingsTab(value))}
				orientation="vertical"
				className="gap-6 md:grid md:grid-cols-[200px_minmax(0,1fr)] md:items-start md:gap-10"
			>
				{/* Phones: one select, so the active section is always visible. */}
				<div className="md:hidden">
					<Select value={active} onValueChange={(value) => onTabChange(settingsTab(value))}>
						<SelectTrigger className="w-full" aria-label={tl("settings.section")} data-testid="settings-section-select">
							<span className="text-muted-foreground">{tl("settings.section")}:</span>
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							{groups.map((g) => (
								<SelectGroup key={g.id}>
									<SelectLabel>{tl(`settings.groups.${g.id}`)}</SelectLabel>
									{g.tabs.map((id) => (
										<SelectItem key={id} value={id}>
											{tabLabel(id)}
										</SelectItem>
									))}
								</SelectGroup>
							))}
						</SelectContent>
					</Select>
				</div>
				<div className="hidden md:sticky md:top-4 md:block">
					<TabsList className="h-auto w-full flex-col items-stretch gap-1 bg-transparent p-0" aria-label={t("settings.title")}>
						{groups.map((g, i) => (
							<Fragment key={g.id}>
								<p
									className={`text-muted-foreground px-3 text-[11px] font-medium tracking-wide uppercase ${i === 0 ? "" : "pt-4"}`}
									role="presentation"
									data-testid={`settings-group-${g.id}`}
								>
									{tl(`settings.groups.${g.id}`)}
								</p>
								{g.tabs.map((id) => {
									const Icon = TAB_ICON[id];
									return (
										<TabsTrigger
											key={id}
											value={id}
											className="text-muted-foreground hover:bg-muted hover:text-foreground data-[state=active]:bg-muted data-[state=active]:text-foreground dark:data-[state=active]:bg-muted h-9 w-full flex-none justify-start gap-2.5 rounded-lg px-3 data-[state=active]:shadow-none dark:data-[state=active]:border-transparent"
											data-testid={`settings-tab-${id}`}
										>
											<Icon className="size-4" aria-hidden="true" />
											{tabLabel(id)}
										</TabsTrigger>
									);
								})}
							</Fragment>
						))}
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
		case "shares":
			return <OrgShareLinks />;
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