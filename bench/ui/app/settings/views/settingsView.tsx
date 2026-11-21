import { RoleBadge } from "@/components/platform/badges";
import { ApiTokensTab } from "@/components/settings/apiTokensTab";
import { AuditTab } from "@/components/settings/auditTab";
import { GithubTab } from "@/components/settings/githubTab";
import { MembersTab } from "@/components/settings/membersTab";
import { OrgTab } from "@/components/settings/orgTab";
import { PasswordTab } from "@/components/settings/passwordTab";
import { RunnersTab } from "@/components/settings/runnersTab";
import { SecretsTab } from "@/components/settings/secretsTab";
import { EmptyNote } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useSession } from "@/lib/session/sessionContext";
import { Building2, Github, KeyRound, LockKeyhole, ScrollText, Server, UserRound, Users } from "lucide-react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

export const SETTINGS_TABS = ["members", "tokens", "secrets", "runners", "github", "audit", "org", "account"] as const;
export type SettingsTab = (typeof SETTINGS_TABS)[number];

const TAB_ICON = { members: Users, tokens: KeyRound, secrets: LockKeyhole, runners: Server, github: Github, audit: ScrollText, org: Building2, account: UserRound } as const;

/** A tab name from the URL, or members for anything else. */
export function settingsTab(raw: string | null | undefined): SettingsTab {
	return SETTINGS_TABS.includes(raw as SettingsTab) ? (raw as SettingsTab) : "members";
}

export interface SettingsViewProps {
	tab: SettingsTab;
	onTabChange: (tab: SettingsTab) => void;
}

/**
 * Settings of the current organization: members and invitations, API tokens, runners and runner
 * tokens, the audit log, the organization itself, and the signed-in user's password (account). Hosted mode only; local mode says why there
 * is nothing to set.
 */
export default function SettingsView({ tab, onTabChange }: SettingsViewProps) {
	const { t } = useTranslation("platform");
	const { t: tSecrets } = useTranslation("secrets");
	const { t: tGithub } = useTranslation("github");
	const session = useSession();
	const org = session.org;

	if (session.authMode === "local") {
		return (
			<section className="flex flex-col gap-6" data-testid="settings-screen" data-mode="local">
				<h1 className="text-foreground">{t("settings.localTitle")}</h1>
				<EmptyNote testId="settings-local">{t("settings.localBody")}</EmptyNote>
				<div className="flex flex-wrap gap-2" data-testid="settings-local-links">
					<Button asChild size="sm" variant="outline">
						<Link to="/models">{tSecrets("localModelsLink")}</Link>
					</Button>
					<Button asChild size="sm" variant="outline">
						<Link to="/workers">{tSecrets("localWorkersLink")}</Link>
					</Button>
				</div>
				<SecretsTab />
			</section>
		);
	}
	if (!session.signedIn || !org) {
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

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="settings-screen" data-mode="hosted">
			<header className="flex flex-col gap-1.5">
				<div className="flex flex-wrap items-center gap-3">
					<h1 className="text-foreground">{t("settings.title")}</h1>
					{session.role && <RoleBadge role={session.role} />}
				</div>
				<p className="text-muted-foreground text-sm">{t("settings.subtitle", { org: org.name })}</p>
			</header>
			<Tabs value={tab} onValueChange={(value) => onTabChange(settingsTab(value))} className="gap-6">
				<div className="-mx-1 overflow-x-auto px-1 pb-1">
					<TabsList className="h-10" aria-label={t("settings.title")}>
						{SETTINGS_TABS.map((id) => {
							const Icon = TAB_ICON[id];
							return (
								<TabsTrigger key={id} value={id} className="gap-1.5 px-3" data-testid={`settings-tab-${id}`}>
									<Icon className="size-3.5" aria-hidden="true" />
									{id === "secrets" ? tSecrets("tab") : id === "github" ? tGithub("tabLabel") : t(`settings.tabs.${id}`)}
								</TabsTrigger>
							);
						})}
					</TabsList>
				</div>
				<TabsContent value="members">
					<MembersTab org={org} />
				</TabsContent>
				<TabsContent value="tokens">
					<ApiTokensTab />
				</TabsContent>
				<TabsContent value="secrets">
					<SecretsTab />
				</TabsContent>
				<TabsContent value="runners">
					<RunnersTab org={org} />
				</TabsContent>
				<TabsContent value="github">
					<GithubTab />
				</TabsContent>
				<TabsContent value="audit">
					<AuditTab org={org} />
				</TabsContent>
				<TabsContent value="org">
					<OrgTab org={org} />
				</TabsContent>
				<TabsContent value="account">
					<PasswordTab />
				</TabsContent>
			</Tabs>
		</section>
	);
}