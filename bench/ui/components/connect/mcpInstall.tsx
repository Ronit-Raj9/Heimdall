import { Button } from "@/components/ui/button";
import { Check, Copy, ExternalLink } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		cliConnect: typeof import("@/locales/en/cliConnect.json");
	}
}

/** The local stdio MCP server's launch config. No secret: it reads the token heimdall login stored. */
export function mcpServerConfig(origin: string) {
	return { command: "heimdall", args: ["mcp", "serve"], env: { HEIMDALL_URL: origin } };
}

/** Cursor's one-click install link (cursor.com/docs/mcp/install-links): name plus base64 config. */
export function cursorInstallLink(origin: string): string {
	const config = JSON.stringify(mcpServerConfig(origin));
	const b64 = btoa(config);
	return `cursor://anysphere.cursor-deeplink/mcp/install?name=heimdall&config=${encodeURIComponent(b64)}`;
}

/** The Claude Code command that adds the server (claude mcp add, stdio transport). */
export function claudeCodeCommand(origin: string): string {
	return `claude mcp add heimdall -e HEIMDALL_URL=${origin} -- heimdall mcp serve`;
}

/** The .vscode/mcp.json body. */
export function vscodeConfig(origin: string): string {
	return JSON.stringify({ servers: { heimdall: { type: "stdio", ...mcpServerConfig(origin) } } }, null, 2);
}

function CopyLine({ text, testId }: { text: string; testId: string }) {
	const { t } = useTranslation("cliConnect");
	const [copied, setCopied] = useState(false);
	const copy = () => {
		void navigator.clipboard?.writeText(text).then(() => {
			setCopied(true);
			setTimeout(() => setCopied(false), 1500);
		});
	};
	return (
		<div className="bg-muted/50 flex items-start gap-2 rounded-md border px-3 py-2">
			<pre className="min-w-0 flex-1 overflow-x-auto font-mono text-xs whitespace-pre-wrap break-all" data-testid={testId}>
				{text}
			</pre>
			<Button type="button" variant="ghost" size="sm" onClick={copy} aria-label={t("ide.copy")} dataTestId={`${testId}-copy`}>
				{copied ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
				<span className="sr-only">{copied ? t("ide.copied") : t("ide.copy")}</span>
			</Button>
		</div>
	);
}

/**
 * "Add to Cursor" and "Add to Claude Code" (lane Z02): install the local Heimdall MCP server in
 * an editor. origin is this server's address; it defaults to the page's own.
 */
export function McpInstallPanel({ origin }: { origin?: string }) {
	const { t } = useTranslation("cliConnect");
	const base = (origin ?? (typeof window !== "undefined" ? window.location.origin : "")).replace(/\/+$/, "");
	const [showClaude, setShowClaude] = useState(false);
	return (
		<section className="bg-card flex flex-col gap-4 rounded-xl border p-5" data-testid="mcp-install">
			<div>
				<h2 className="text-base font-semibold">{t("ide.title")}</h2>
				<p className="text-muted-foreground mt-1 text-sm">{t("ide.subtitle")}</p>
			</div>
			<div className="flex flex-col gap-2">
				<p className="text-sm font-medium">{t("ide.step1")}</p>
				<CopyLine text={`heimdall login -url ${base}`} testId="mcp-login" />
			</div>
			<div className="flex flex-col gap-2">
				<p className="text-sm font-medium">{t("ide.step2")}</p>
				<div className="flex flex-wrap gap-2">
					<Button asChild dataTestId="mcp-add-cursor">
						<a href={cursorInstallLink(base)}>
							<ExternalLink className="size-4" aria-hidden="true" />
							{t("ide.cursor")}
						</a>
					</Button>
					<Button variant="outline" onClick={() => setShowClaude(true)} dataTestId="mcp-add-claude-code">
						{t("ide.claudeCode")}
					</Button>
				</div>
				{showClaude && (
					<div className="flex flex-col gap-1.5" data-testid="mcp-claude-code">
						<p className="text-muted-foreground text-xs">{t("ide.claudeCodeHint")}</p>
						<CopyLine text={claudeCodeCommand(base)} testId="mcp-claude-command" />
					</div>
				)}
			</div>
			<div className="flex flex-col gap-1.5">
				<p className="text-muted-foreground text-xs">{t("ide.vscode")}</p>
				<CopyLine text={vscodeConfig(base)} testId="mcp-vscode" />
			</div>
			<p className="text-muted-foreground text-xs">{t("ide.noSecret")}</p>
		</section>
	);
}
