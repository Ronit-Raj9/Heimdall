import { NativeSelect } from "@/components/ui/select";
import { CopyBlock, OneTimeSecret } from "@/components/platform/oneTimeSecret";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetAgentVersionsQuery, useGetEvaluationPacksQuery } from "@/lib/store/apis/evaluationsApi";
import { useGetCIWorkflowQuery } from "@/lib/store/apis/integrationsApi";
import { useCreateApiTokenMutation } from "@/lib/store/apis/platformApi";
import type { CIWorkflowParams } from "@/lib/types/integrations";
import { Link } from "@tanstack/react-router";
import { Check, Copy, Download, KeyRound } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { RoleNote } from "@/components/platform/accessNotes";
import "./uf2cI18n";

export type CIProvider = "github" | "gitlab";
export type CISource = "config" | "image" | "endpoint";

/** Faults a pull request evaluation can add besides clean. */
export const CI_FAULTS = ["model-down", "broken-tool", "misleading-instruction"] as const;

export interface CIPanelState {
	provider: CIProvider;
	source: CISource;
	config: string;
	pack: string;
	faults: string[];
	failInconclusive: boolean;
}

/** The workflow preview query for the panel's choices. url is this Heimdall's address when hosted. */
export function ciPanelParams(agentId: string, s: CIPanelState, url: string): CIWorkflowParams {
	return {
		mode: "hosted",
		agent: agentId,
		provider: s.provider,
		source: s.source,
		config: s.source === "config" ? s.config.trim() : undefined,
		pack: s.pack,
		faults: s.faults.join(","),
		fail_on: s.failInconclusive ? "regressed,inconclusive,failed" : "regressed,failed",
		url: url || undefined,
	};
}


/**
 * "Test on every pull request": generates the GitHub Actions or GitLab CI file that registers each pull
 * request's agent config as a new version, evaluates it against the live version on this Heimdall and
 * posts the report card summary, plus the token and variable steps.
 */
export default function PullRequestsTab({ agentId }: { agentId: string }) {
	const { t } = useTranslation("agentBuilder");
	const { t: tu } = useTranslation("uf2c");
	const session = useSession();
	const formId = useId();
	const hosted = session.authMode === "hosted";
	const origin = hosted && typeof window !== "undefined" ? window.location.origin : "";
	const packs = useGetEvaluationPacksQuery();
	const versions = useGetAgentVersionsQuery(agentId);
	const [state, setState] = useState<CIPanelState>({
		provider: "github",
		source: "config",
		config: `agents/${agentId}.yaml`,
		pack: "",
		faults: ["model-down"],
		failInconclusive: false,
	});
	const pack = state.pack || packs.data?.[0]?.id || "";
	const params = ciPanelParams(agentId, { ...state, pack }, origin);
	const preview = useGetCIWorkflowQuery(params, { skip: !pack });
	const set = <K extends keyof CIPanelState>(k: K, v: CIPanelState[K]) => setState((s) => ({ ...s, [k]: v }));
	const live = versions.data?.live;

	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="agent-ci-tab">
			<Section title={t("ci.title")} testId="agent-ci-section">
				<p className="text-muted-foreground text-sm" data-testid="agent-ci-live">
					{live ? t("ci.liveVersion", { version: live }) : t("ci.noLiveVersion")}
				</p>
				<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
					<label className="flex flex-col gap-1.5 text-sm" htmlFor={`${formId}-provider`}>
						{t("ci.provider")}
						<NativeSelect
							id={`${formId}-provider`}
							wrapperClassName="w-full"
							value={state.provider}
							onChange={(e) => set("provider", e.target.value as CIProvider)}
							data-testid="agent-ci-provider"
						>
							<option value="github">GitHub Actions</option>
							<option value="gitlab">GitLab CI</option>
						</NativeSelect>
					</label>
					<label className="flex flex-col gap-1.5 text-sm" htmlFor={`${formId}-source`}>
						{t("ci.source")}
						<NativeSelect
							id={`${formId}-source`}
							wrapperClassName="w-full"
							value={state.source}
							onChange={(e) => set("source", e.target.value as CISource)}
							data-testid="agent-ci-source"
						>
							<option value="config">{t("ci.sourceConfig")}</option>
							<option value="image">{t("ci.sourceImage")}</option>
							<option value="endpoint">{t("ci.sourceEndpoint")}</option>
						</NativeSelect>
					</label>
					{state.source === "config" && (
						<label className="flex flex-col gap-1.5 text-sm" htmlFor={`${formId}-config`}>
							{t("ci.configPath")}
							<Input id={`${formId}-config`} value={state.config} onChange={(e) => set("config", e.target.value)} data-testid="agent-ci-config" />
						</label>
					)}
					<label className="flex flex-col gap-1.5 text-sm" htmlFor={`${formId}-pack`}>
						{t("ci.pack")}
						<NativeSelect id={`${formId}-pack`} wrapperClassName="w-full" value={pack} onChange={(e) => set("pack", e.target.value)} data-testid="agent-ci-pack">
							{(packs.data ?? []).map((p) => (
								<option key={p.id} value={p.id}>
									{p.name}
								</option>
							))}
						</NativeSelect>
						{/* Which benchmarks the pack runs, so the pick is not blind (M2-66). */}
						{packBenchmarks(packs.data, pack).length > 0 && (
							<span className="text-muted-foreground text-xs" data-testid="agent-ci-pack-benchmarks">
								{tu("ci.packRuns", { list: packBenchmarks(packs.data, pack).join(", ") })}
							</span>
						)}
					</label>
				</div>
				<fieldset className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
					<legend className="mb-1 text-sm">{t("ci.faults")}</legend>
					{CI_FAULTS.map((f) => (
						<label key={f} className="inline-flex items-center gap-2">
							<input
								type="checkbox"
								checked={state.faults.includes(f)}
								onChange={(e) => set("faults", e.target.checked ? [...state.faults, f] : state.faults.filter((x) => x !== f))}
								data-testid={`agent-ci-fault-${f}`}
							/>
							{t(`ci.fault.${f}`)}
						</label>
					))}
					<label className="inline-flex items-center gap-2">
						<input
							type="checkbox"
							checked={state.failInconclusive}
							onChange={(e) => set("failInconclusive", e.target.checked)}
							data-testid="agent-ci-fail-inconclusive"
						/>
						{t("ci.failInconclusive")}
					</label>
				</fieldset>
				<p className="text-muted-foreground text-xs">{t("ci.gateRule")}</p>
			</Section>

			<Section title={t("ci.stepsTitle")} testId="agent-ci-steps">
				<ol className="flex list-decimal flex-col gap-4 pl-5 text-sm">
					<li data-testid="agent-ci-step-token">
						<TokenStep agentId={agentId} hosted={hosted} admin={canUse(session, "admin")} />
					</li>
					<li data-testid="agent-ci-step-secrets">
						<p>{state.provider === "github" ? t("ci.secretsGithub") : t("ci.secretsGitlab")}</p>
						<ul className="text-muted-foreground mt-1 flex flex-col gap-1 text-xs">
							<li>
								<code className="font-mono">HEIMDALL_TOKEN</code>: {t("ci.varToken")}
							</li>
							{!origin && (
								<li>
									<code className="font-mono">HEIMDALL_URL</code>: {t("ci.varUrl")}
								</li>
							)}
							{state.provider === "gitlab" && (
								<li>
									<code className="font-mono">GITLAB_TOKEN</code>: {t("ci.varGitlab")}
								</li>
							)}
						</ul>
					</li>
					<li data-testid="agent-ci-step-file">
						<p>
							{t("ci.commitFile")} {preview.data && <code className="font-mono text-xs">{preview.data.path}</code>}
							{state.provider === "gitlab" && <span className="text-muted-foreground"> {t("ci.gitlabInclude")}</span>}
						</p>
						{preview.isLoading && <BlockSkeleton testId="agent-ci-workflow-loading" />}
						{preview.error && (
							<LoadError title={t("ci.previewError")} error={preview.error} onRetry={() => void preview.refetch()} testId="agent-ci-preview-error" />
						)}
						{preview.data && <WorkflowBlock text={preview.data.workflow} path={preview.data.path} />}
					</li>
				</ol>
			</Section>
		</div>
	);
}

/** The pack's benchmarks, empty when the pack is not in the list. */
export function packBenchmarks(packs: ReadonlyArray<{ id: string; benchmarks: string[] }> | undefined, id: string): string[] {
	return packs?.find((p) => p.id === id)?.benchmarks ?? [];
}

/** The file name a downloaded workflow gets: the last part of its repository path. */
export function workflowFileName(path: string | undefined): string {
	const name = (path ?? "").split("/").pop() ?? "";
	return name || "heimdall.yml";
}

/**
 * The workflow file with Copy and Download inside its top right corner (M2-65). The block grows with
 * the file (no inner scroll down); long lines scroll sideways, with a fade at the right edge.
 */
function WorkflowBlock({ text, path }: { text: string; path?: string }) {
	const { t } = useTranslation("agentBuilder");
	const { t: tu } = useTranslation("uf2c");
	const [copied, setCopied] = useState(false);
	const download = () => {
		const url = URL.createObjectURL(new Blob([text], { type: "text/yaml" }));
		const a = document.createElement("a");
		a.href = url;
		a.download = workflowFileName(path);
		a.click();
		URL.revokeObjectURL(url);
	};
	return (
		<div className="relative mt-2 min-w-0 rounded-lg border" data-testid="agent-ci-workflow-block">
			<div className="bg-background/90 absolute top-2 right-2 z-10 flex gap-1 rounded-md">
				<Button
					type="button"
					size="sm"
					variant="outline"
					className="h-7 gap-1.5 px-2 text-xs"
					onClick={() => {
						void navigator.clipboard?.writeText(text).then(() => setCopied(true));
					}}
					dataTestId="agent-ci-copy"
				>
					{copied ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
					{copied ? t("ci.copied") : t("ci.copy")}
				</Button>
				<Button type="button" size="sm" variant="outline" className="h-7 gap-1.5 px-2 text-xs" onClick={download} dataTestId="agent-ci-download">
					<Download className="size-3.5" aria-hidden="true" />
					{tu("ci.download")}
				</Button>
			</div>
			<pre
				className="bg-muted/50 overflow-x-auto rounded-lg p-4 pt-11 font-mono text-xs leading-5 [mask-image:linear-gradient(to_right,black_calc(100%-2rem),transparent)] sm:[mask-image:none]"
				tabIndex={0}
				data-testid="agent-ci-workflow"
			>
				{text}
			</pre>
		</div>
	);
}

function TokenStep({ agentId, hosted, admin }: { agentId: string; hosted: boolean; admin: boolean }) {
	const { t } = useTranslation("agentBuilder");
	const { t: tu } = useTranslation("uf2c");
	const [create, state] = useCreateApiTokenMutation();
	const [secret, setSecret] = useState<string | null>(null);
	if (!hosted) {
		return <p data-testid="agent-ci-token-local">{t("ci.tokenLocal")}</p>;
	}
	if (secret) {
		return (
			<OneTimeSecret title={t("ci.tokenCreated")} secret={secret} secretLabel="HEIMDALL_TOKEN" onDone={() => setSecret(null)} testId="agent-ci-token">
				<CopyBlock value="HEIMDALL_TOKEN" label={t("ci.tokenName")} testId="agent-ci-token-name" />
			</OneTimeSecret>
		);
	}
	return (
		<div className="flex flex-col gap-2">
			<p>{t("ci.tokenIntro")}</p>
			<div className="flex flex-wrap items-center gap-2">
				{admin && (
					<Button
						type="button"
						size="sm"
						disabled={state.isLoading}
						onClick={async () => {
							try {
								const r = await create({
									name: `ci-${agentId}`,
									role: "member",
									expires_in_days: 365,
								}).unwrap();
								setSecret(r.token);
							} catch {
								// state.error renders below.
							}
						}}
						dataTestId="agent-ci-create-token"
					>
						<KeyRound aria-hidden="true" />
						{t("ci.createToken")}
					</Button>
				)}
				{/* Says who may make the token (M2-67): only admins see the button. */}
				{admin ? (
					<span className="text-muted-foreground text-xs" data-testid="agent-ci-token-admins">
						{tu("ci.adminsOnly")}
					</span>
				) : (
					<RoleNote min="admin" testId="agent-ci-token-admins" />
				)}
				<Link to="/settings" search={{ tab: "tokens" } as never} className="text-primary text-sm hover:underline" data-testid="agent-ci-tokens-link">
					{t("ci.manageTokens")}
				</Link>
			</div>
			{state.error && (
				<p className="text-destructive text-sm" role="alert" data-testid="agent-ci-token-error">
					{getBenchErrorMessage(state.error)}
				</p>
			)}
		</div>
	);
}
