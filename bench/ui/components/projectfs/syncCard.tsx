import { useSession } from "@/lib/session/sessionContext";
import { Link } from "@tanstack/react-router";
import { ChevronDown, RefreshCw } from "lucide-react";
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
		<details className="bg-card group rounded-xl border" data-testid="files-sync-card">
			<summary className="hover:bg-accent/40 flex cursor-pointer list-none items-center gap-3 rounded-xl px-4 py-3 transition-colors [&::-webkit-details-marker]:hidden">
				<span className="bg-muted text-muted-foreground flex size-8 items-center justify-center rounded-full">
					<RefreshCw className="size-4" aria-hidden="true" />
				</span>
				<span className="flex min-w-0 flex-1 flex-col">
					<span className="text-sm font-medium">{t("sync.title")}</span>
					<span className="text-muted-foreground truncate text-xs">{t("sync.summary")}</span>
				</span>
				<ChevronDown className="text-muted-foreground size-4 transition-transform group-open:rotate-180" aria-hidden="true" />
			</summary>
			<div className="flex flex-col gap-2 border-t px-4 py-3 text-sm">
				<p className="text-muted-foreground text-sm">{t("sync.body")}</p>
				<pre className="bg-muted overflow-x-auto rounded-md p-3 font-mono text-xs leading-5" data-testid="files-sync-commands">
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
			</div>
		</details>
	);
}
