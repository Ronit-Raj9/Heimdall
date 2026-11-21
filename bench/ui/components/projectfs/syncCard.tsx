import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useSession } from "@/lib/session/sessionContext";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { FILES_NS } from "./i18n";

/** The CLI sync commands for this server: the page's own origin, plus -org when hosted. */
export function syncCommands(origin: string, orgSlug?: string): { pull: string; push: string } {
	const org = orgSlug ? ` -org ${orgSlug}` : "";
	const base = `-server ${origin}${org} -token $HEIMDALL_TOKEN -dir .`;
	return { pull: `bifrost-bench pull ${base}`, push: `bifrost-bench push ${base}` };
}

/** "Sync with your machine": the exact push/pull commands and where to get a token. */
export function SyncCard() {
	const { t } = useTranslation(FILES_NS);
	const session = useSession();
	const hosted = session.authMode === "hosted";
	const origin = typeof window !== "undefined" ? window.location.origin : "http://127.0.0.1:8090";
	const cmds = syncCommands(origin, hosted ? session.org?.slug : undefined);
	return (
		<Card data-testid="files-sync-card">
			<CardHeader>
				<CardTitle className="text-base">{t("sync.title")}</CardTitle>
				<CardDescription>{t("sync.body")}</CardDescription>
			</CardHeader>
			<CardContent className="flex flex-col gap-2 text-sm">
				<pre className="bg-muted overflow-x-auto rounded-md p-2 font-mono text-xs" data-testid="files-sync-commands">
					{`${cmds.pull}\n${cmds.push}`}
				</pre>
				<p className="text-muted-foreground text-xs">{t("sync.rules")}</p>
				{hosted ? (
					<Link to="/settings" search={{ tab: "tokens" }} className="text-primary text-sm hover:underline" data-testid="files-sync-token">
						{t("sync.tokenLink")}
					</Link>
				) : (
					<p className="text-muted-foreground text-xs">{t("sync.localToken")}</p>
				)}
			</CardContent>
		</Card>
	);
}
