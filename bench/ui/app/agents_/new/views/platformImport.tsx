import { PlatformFields, suggestedPlatformSecrets } from "@/components/connect/platformFields";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetAgentPlatformsQuery, useImportAgentFromPlatformMutation } from "@/lib/store/apis/agentImportApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useListSecretsQuery } from "@/lib/store/apis/secretsApi";
import type { AgentImportRequest, AgentImportResponse, AgentPlatform } from "@/lib/types/agentImport";
import { cn } from "@/lib/utils";
import { useNavigate } from "@tanstack/react-router";
import { ChevronRight, CloudDownload } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

const ID_RE = /^[a-z0-9][a-z0-9._-]{0,62}$/;

/** A free default Heimdall name for an imported agent: the platform name, slugged. */
export function importedAgentName(platform: string, name: string): string {
	const slug = name
		.toLowerCase()
		.replace(/[^a-z0-9._-]+/g, "-")
		.replace(/^[^a-z0-9]+/, "")
		.replace(/-+$/, "")
		.slice(0, 50);
	return slug ? `${platform.replace(/_/g, "-")}-${slug}`.slice(0, 63) : `${platform.replace(/_/g, "-")}-agent`;
}

/** The request of the form; secrets left empty are dropped. */
export function importRequest(
	p: AgentPlatform,
	id: string,
	agentId: string,
	inputs: Record<string, string>,
	secrets: Record<string, string>,
	dryRun: boolean,
): AgentImportRequest {
	const ins: Record<string, string> = {};
	for (const i of p.inputs ?? []) if (inputs[i.name]?.trim()) ins[i.name] = inputs[i.name].trim();
	const sec: Record<string, string> = {};
	for (const s of p.secrets) if (secrets[s.role]?.trim()) sec[s.role] = secrets[s.role].trim();
	return { id, platform: p.id, agent_id: agentId.trim(), inputs: ins, secrets: sec, dry_run: dryRun || undefined };
}

/** What is still missing before the platform can be read. */
export function missingImportFields(
	p: AgentPlatform,
	agentId: string,
	inputs: Record<string, string>,
	secrets: Record<string, string>,
): string[] {
	const out: string[] = [];
	if (!agentId.trim()) out.push(p.id_label);
	for (const i of p.inputs ?? []) if (i.required && !inputs[i.name]?.trim()) out.push(i.label);
	for (const s of p.secrets) if (s.required && !secrets[s.role]?.trim()) out.push(s.label);
	return out;
}

/**
 * "Import from a platform" (lane Z16): pick the platform, paste the agent id and name the org
 * secrets; Read shows what Heimdall found (prompt, first message, tools, voice, warnings) and the
 * config it will save; Create saves it and opens the agent.
 */
export function PlatformImportCard({ returnTo, openSignal = 0 }: { returnTo?: string | null; openSignal?: number }) {
	const { t } = useTranslation("agentImport");
	const canWrite = canUse(useSession(), "member");
	const navigate = useNavigate();
	const [open, setOpen] = useState(false);
	// The page's top "Import from a platform" button: each press opens the form and brings it into view.
	useEffect(() => {
		if (!openSignal) return;
		setOpen(true);
		document.getElementById("platform-import")?.scrollIntoView({ behavior: "smooth", block: "start" });
	}, [openSignal]);
	const platforms = useGetAgentPlatformsQuery(undefined, { skip: !open });
	const secretList = useListSecretsQuery(undefined, { skip: !open });
	const list = useMemo(() => platforms.data?.platforms ?? [], [platforms.data]);
	const [platformId, setPlatformId] = useState<string>("");
	const p = list.find((x) => x.id === platformId) ?? list[0] ?? null;
	const [agentId, setAgentId] = useState("");
	const [inputs, setInputs] = useState<Record<string, string>>({});
	const [secrets, setSecrets] = useState<Record<string, string>>({});
	const [name, setName] = useState("");
	const [found, setFound] = useState<AgentImportResponse | null>(null);
	const [read, readState] = useImportAgentFromPlatformMutation();
	const [create, createState] = useImportAgentFromPlatformMutation();

	// A new platform starts clean, with the suggested secret names filled in.
	useEffect(() => {
		if (!p) return;
		setInputs({});
		setSecrets(suggestedPlatformSecrets(p));
		setFound(null);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [p?.id]);

	const missing = p ? missingImportFields(p, agentId, inputs, secrets) : ["platform"];
	const nameOk = ID_RE.test(name);
	const secretNames = secretList.data?.secrets.map((s) => s.name) ?? [];

	const onRead = async () => {
		if (!p) return;
		const draftName = nameOk ? name : importedAgentName(p.id, agentId);
		const res = await read(importRequest(p, draftName, agentId, inputs, secrets, true));
		if ("data" in res && res.data) {
			setFound(res.data);
			if (!nameOk) setName(importedAgentName(p.id, res.data.import.definition.name || agentId));
		}
	};
	const onCreate = async () => {
		if (!p || !nameOk) return;
		const res = await create(importRequest(p, name, agentId, inputs, secrets, false));
		if ("data" in res && res.data) {
			if (returnTo === "/evaluate") void navigate({ to: "/evaluate", search: { agent: res.data.id, keep: "1" } as never });
			else void navigate({ to: "/agents/$agentId", params: { agentId: res.data.id } });
		}
	};
	const error = createState.error ?? readState.error;

	return (
		<div id="platform-import" className="bg-card flex min-w-0 scroll-mt-4 flex-col gap-4 rounded-xl border p-4" data-testid="platform-import">
			<div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-start">
				<div className="flex min-w-0 flex-1 items-start gap-3">
					<span className="bg-muted flex size-9 shrink-0 items-center justify-center rounded-lg">
						<CloudDownload className="text-muted-foreground size-4" aria-hidden="true" />
					</span>
					<div className="flex min-w-0 flex-1 flex-col gap-1">
						<h3 className="text-sm font-semibold">{t("title")}</h3>
						<p className="text-muted-foreground text-sm">{t("subtitle")}</p>
					</div>
				</div>
				<Button
					type="button"
					variant="outline"
					size="sm"
					className="self-start sm:ml-auto"
					onClick={() => setOpen(!open)}
					aria-expanded={open}
					data-testid="platform-import-toggle"
				>
					{open ? t("close") : t("open")}
				</Button>
			</div>
			{open && (
				<div className="flex flex-col gap-4 border-t pt-4" data-testid="platform-import-form">
					{platforms.isError && (
						<p role="alert" className="text-destructive text-sm">
							{getBenchErrorMessage(platforms.error)}
						</p>
					)}
					<div className="flex flex-wrap gap-2" role="radiogroup" aria-label={t("platform")}>
						{list.map((x) => (
							<button
								key={x.id}
								type="button"
								role="radio"
								aria-checked={x.id === p?.id}
								onClick={() => setPlatformId(x.id)}
								className={cn(
									"hover:border-primary/50 rounded-lg border px-3 py-1.5 text-xs font-medium transition",
									x.id === p?.id && "border-primary ring-primary/30 ring-2",
								)}
								data-testid={`platform-${x.id}`}
							>
								{x.label}
							</button>
						))}
					</div>
					{p && (
						<form
							className="grid grid-cols-1 gap-4 md:grid-cols-2"
							onSubmit={(e) => {
								e.preventDefault();
								void onRead();
							}}
						>
							<PlatformFields
								platform={p}
								agentId={agentId}
								onAgentId={setAgentId}
								inputs={inputs}
								onInputs={setInputs}
								secrets={secrets}
								onSecrets={setSecrets}
								secretNames={secretNames}
								listId="platform-import-secrets"
							/>
							<label className="flex flex-col gap-1 text-sm md:col-span-2">
								<span className="font-medium">{t("name")}</span>
								<Input
									value={name}
									placeholder={importedAgentName(p.id, agentId)}
									onChange={(e) => setName(e.target.value.toLowerCase())}
									data-testid="platform-agent-name"
								/>
								<span className="text-muted-foreground text-xs">{t("nameHelp")}</span>
							</label>
							{error && (
								<p role="alert" className="text-destructive text-sm md:col-span-2" data-testid="platform-import-error">
									{getBenchErrorMessage(error)}
								</p>
							)}
							{!canWrite && <p className="text-muted-foreground text-xs md:col-span-2">{t("memberOnly")}</p>}
							{canWrite && (
								<div className="flex flex-wrap gap-2 md:col-span-2">
									<Button type="submit" variant="outline" disabled={missing.length > 0 || readState.isLoading} data-testid="platform-read">
										{readState.isLoading ? t("reading") : t("read")}
									</Button>
									<Button
										type="button"
										disabled={!found || !nameOk || !!found.validation_error || createState.isLoading}
										onClick={() => void onCreate()}
										data-testid="platform-create"
									>
										{createState.isLoading ? t("creating") : t("create")}
									</Button>
								</div>
							)}
						</form>
					)}
					{found && p && <ImportFound res={found} platform={p} />}
				</div>
			)}
		</div>
	);
}

/** What the platform said about the agent, and the config Heimdall will save. */
export function ImportFound({ res, platform }: { res: AgentImportResponse; platform: AgentPlatform }) {
	const { t } = useTranslation("agentImport");
	const d = res.import.definition;
	const rows: [string, string | undefined][] = [
		[t("found.name"), d.name],
		[t("found.description"), d.description],
		[t("found.model"), d.model],
		[t("found.language"), d.language],
		[t("found.protocol"), d.protocol],
		[t("found.status"), d.status],
		[t("found.version"), d.version],
		[t("found.preset"), res.import.preset],
		[t("found.voice"), d.voice ? [d.voice.provider, d.voice.voice_id, d.voice.model].filter(Boolean).join(" / ") : undefined],
		[t("found.firstMessage"), d.first_message],
	];
	return (
		<div className="flex min-w-0 flex-col gap-3 rounded-lg border p-4" data-testid="platform-import-found">
			<h4 className="text-sm font-semibold">{t("found.title", { platform: platform.label })}</h4>
			<dl className="grid grid-cols-[minmax(0,10rem)_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs">
				{rows
					.filter(([, v]) => v)
					.map(([k, v]) => (
						<div key={k} className="contents">
							<dt className="text-muted-foreground">{k}</dt>
							<dd className="min-w-0 break-words">{v}</dd>
						</div>
					))}
				{res.import.capability_hints.length > 0 && (
					<div className="contents">
						<dt className="text-muted-foreground">{t("found.hints")}</dt>
						<dd className="flex flex-wrap gap-1">
							{res.import.capability_hints.map((h) => (
								<Badge key={h} variant="info">
									{h}
								</Badge>
							))}
						</dd>
					</div>
				)}
			</dl>
			{d.prompt && (
				<details>
					<summary className="text-muted-foreground flex w-fit cursor-pointer list-none items-center gap-1 text-xs">
						<ChevronRight className="size-3.5" aria-hidden="true" />
						{t("found.prompt")}
					</summary>
					<pre
						className="bg-muted mt-2 max-h-48 overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap"
						data-testid="platform-import-prompt"
					>
						{d.prompt}
					</pre>
				</details>
			)}
			{(d.tools?.length ?? 0) > 0 && (
				<div className="flex flex-col gap-1 text-xs">
					<span className="text-muted-foreground">{t("found.tools", { count: d.tools?.length ?? 0 })}</span>
					<ul className="flex flex-wrap gap-1" data-testid="platform-import-tools">
						{d.tools?.map((x, i) => (
							<li key={`${x.name}-${i}`} title={x.description}>
								<Badge variant="outline">{x.name}</Badge>
							</li>
						))}
					</ul>
				</div>
			)}
			{res.import.warnings.length > 0 && (
				<div className="flex flex-col gap-1 text-xs" data-testid="platform-import-warnings">
					<span className="font-medium">{t("found.warnings")}</span>
					<ul className="text-muted-foreground list-disc pl-4">
						{res.import.warnings.map((w) => (
							<li key={w}>{w}</li>
						))}
					</ul>
				</div>
			)}
			{res.validation_error && (
				<p role="alert" className="text-destructive text-xs" data-testid="platform-import-invalid">
					{t("found.notValid", { error: res.validation_error })}
				</p>
			)}
			<details>
				<summary className="text-muted-foreground flex w-fit cursor-pointer list-none items-center gap-1 text-xs">
					<ChevronRight className="size-3.5" aria-hidden="true" />
					{t("found.config")}
				</summary>
				<pre className="bg-muted mt-2 max-h-72 overflow-auto rounded-md p-3 font-mono text-xs" data-testid="platform-import-yaml">
					{res.yaml}
				</pre>
			</details>
		</div>
	);
}