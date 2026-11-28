import { cn } from "@/lib/utils";
import { pageWidthClass } from "@/components/shell/pageWidth";
import { ImportFound, importedAgentName, importRequest, missingImportFields } from "@/app/agents_/new/views/platformImport";
import { PlatformFields, suggestedPlatformSecrets } from "@/components/connect/platformFields";
import { agentNameProblem } from "@/components/agents/agentName";
import { Section } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell/pageHeader";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { PaidButton } from "@/components/ui/paidButton";
import { Input } from "@/components/ui/input";
import { WhyDisabled, firstReason } from "@/components/ui/whyDisabled";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetAgentPlatformsQuery, useImportAgentFromPlatformMutation } from "@/lib/store/apis/agentImportApi";
import { getBenchErrorMessage, useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import {
	useDetectAgentMutation,
	useGetConnectCatalogQuery,
	useGetVerifyQuery,
	useSaveConnectedAgentMutation,
	useStartVerifyMutation,
} from "@/lib/store/apis/connectApi";
import { useListSecretsQuery } from "@/lib/store/apis/secretsApi";
import type { AgentImportResponse, AgentPlatform } from "@/lib/types/agentImport";
import type {
	ConnectCandidate,
	ConnectDetectResponse,
	ConnectFailure,
	ConnectQuestion,
	ConnectSaveResponse,
	ConnectVerifyStatus,
} from "@/lib/types/connect";
import { Link, useNavigate } from "@tanstack/react-router";
import { CircleCheck, Plug } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import "@/components/improvements/uf2bI18n";
import { useTranslation } from "react-i18next";
import {
	VERIFY_POLL_MS,
	bestCandidate,
	canSave,
	connectionOK,
	importPlatformFor,
	isProven,
	isTerminalJob,
	otherCandidates,
	platformChoice,
	resultTitle,
	secretQuestions,
	showDetectButton,
	suggestedName,
	validAgentName,
	verifyBenchmarkKey,
	withAnswerPath,
} from "./connect.utils";
import { FailureNote } from "./failureNote";
import { FirstLookLauncher } from "./firstLookLauncher";
import { MappingPreview } from "./mappingPreview";
import { SecretCapture } from "./secretCapture";
import { VerifyRows } from "./verifyRows";
import { VerifyOptionsFields } from "./verifyOptions";
import { AgentFileCheck, ExplainError } from "@/components/connect/connectTools";
import { type VerifyOptions, verifyRequestBody } from "@/lib/store/apis/ufl6ConnectApi";

const NO_PLATFORM = "none";

/** Example inputs the chips under the paste box fill in, one per kind Heimdall detects. */
export const CONNECT_EXAMPLES = [
	{ key: "exampleUrl", value: "https://bot.example.com/chat" },
	{ key: "exampleCurl", value: `curl -X POST https://bot.example.com/chat -H "Content-Type: application/json" -d '{"message":"hi"}'` },
	{ key: "exampleImage", value: "ghcr.io/example/agent:1" },
] as const;

/**
 * The name to show after a detect: the suggestion, unless the user already typed one of their own
 * (anything other than the starting name or the previous suggestion).
 */
export function nameAfterDetect(current: string, previousSuggestion: string, suggestion: string): string {
	return current === "my-agent" || current === previousSuggestion || current.trim() === "" ? suggestion : current;
}

/** N105: how sure, only for a proven candidate; otherwise why it is not proven yet. */
export function CandidateStatus({ cand }: { cand: ConnectCandidate }) {
	const { t } = useTranslation("connect");
	const proven = isProven(cand);
	return (
		<div className="flex flex-wrap items-center gap-2 text-sm" data-testid="connect-candidate-status">
			{proven && <Badge variant="outline">{t("result.confidence", { pct: Math.round(cand.confidence * 100) })}</Badge>}
			{proven ? (
				<span className="text-chart-success-ink flex items-center gap-1.5">
					<CircleCheck className="size-4" aria-hidden />
					{t("result.nonceFound")}
				</span>
			) : (
				<span className="text-muted-foreground">{t("result.nonceMissing")}</span>
			)}
		</div>
	);
}

/** Candidates that cannot be connected here; input that was not recognised shows only what to paste (N106). */
export function UnsupportedCandidates({ det }: { det: ConnectDetectResponse }) {
	const unknown = det.class === "unknown";
	return (
		<>
			{det.candidates.map((c, i) => (
				<div key={`${c.label}-${i}`} className="flex flex-col gap-2 rounded-lg border p-3 text-sm" data-testid="connect-unsupported">
					{!unknown && <div className="font-medium">{c.label}</div>}
					{c.message && <p>{c.message}</p>}
					{c.command && <code className="bg-muted rounded px-2 py-1 text-xs">{c.command}</code>}
				</div>
			))}
		</>
	);
}

/** N107: while the picked platform's form loads, say so; if the list fails, show why and link to Import from a platform. */
export function PlatformCatalogNote({ label, loading, error }: { label: string; loading: boolean; error: string | undefined }) {
	const { t } = useTranslation("connect");
	if (loading) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="connect-platform-loading">
				{t("paste.loadingPlatform", { label })}
			</p>
		);
	}
	if (!error) return null;
	return (
		<p className="text-chart-error-ink text-sm" data-testid="connect-platform-error">
			{t("paste.platformError", { error })}{" "}
			<Link to="/agents/new" className="underline">
				{t("paste.importLink")}
			</Link>
		</p>
	);
}

/**
 * /connect (lane Z01): one paste box for any agent. Detect shows what was found and why, the
 * request Heimdall will send and the agent file; secrets are stored inline; Save writes the agent;
 * Verify streams its rows (the agent.verify job) and ends with one real task; then the first look
 * starts on the fastest compatible benchmark.
 */
export default function ConnectView() {
	const { t } = useTranslation("connect");
	const { t: tu } = useTranslation("uf2b");
	const navigate = useNavigate();
	const catalog = useGetConnectCatalogQuery();
	// Existing agent ids, so the name check says "already exists" before Save fails.
	const agentsQ = useGetAgentsQuery();
	const takenIds = useMemo(() => new Set((agentsQ.data?.agents ?? []).map((a) => a.id)), [agentsQ.data]);
	const lastSuggestion = useRef("");
	const [emptyPress, setEmptyPress] = useState(false);
	const [input, setInput] = useState("");
	const [platform, setPlatform] = useState(NO_PLATFORM);
	const [probeSecrets, setProbeSecrets] = useState<Record<string, string>>({});
	const [detect, detectState] = useDetectAgentMutation();
	const [det, setDet] = useState<ConnectDetectResponse | undefined>();
	const [cand, setCand] = useState<ConnectCandidate | undefined>();
	const [name, setName] = useState("my-agent");
	// Why the name cannot be saved, shown under the box and under Save (p02-3); "" when it can.
	const nameProblem = agentNameProblem(name, takenIds);
	const saveWhy = firstReason(nameProblem);
	const [save, saveState] = useSaveConnectedAgentMutation();
	const [saved, setSaved] = useState<ConnectSaveResponse | undefined>();
	const [startVerify, startState] = useStartVerifyMutation();
	const [verifyOpts, setVerifyOpts] = useState<VerifyOptions>({ suite: "", task: "", connectionOnly: false });
	const { t: t6 } = useTranslation("ufl6");
	const member = canUse(useSession(), "member");
	const [inline, setInline] = useState<ConnectVerifyStatus | undefined>();
	const [jobId, setJobId] = useState<string | undefined>();
	const [polling, setPolling] = useState(false);
	// N105: an answer field the user picked counts as a mapping Save may write.
	const [userMapped, setUserMapped] = useState(false);
	const polled = useGetVerifyQuery(jobId ?? "", {
		skip: !jobId,
		pollingInterval: polling ? VERIFY_POLL_MS : 0,
		skipPollingIfUnfocused: true,
	});
	const verify = jobId ? (polled.data ?? inline) : inline;
	const verifyDone = verify ? isTerminalJob(verify.status) : false;
	useEffect(() => {
		// Rows stream in while the job runs; polling stops once it ends.
		if (polling && verifyDone) setPolling(false);
	}, [polling, verifyDone]);

	const runDetect = async (secrets: Record<string, string> = probeSecrets) => {
		if (!input.trim() && platform === NO_PLATFORM) {
			setEmptyPress(true);
			return;
		}
		setEmptyPress(false);
		setSaved(undefined);
		setInline(undefined);
		setJobId(undefined);
		setUserMapped(false);
		const body = platform !== NO_PLATFORM ? { input, platform, id: input, secrets } : { input, secrets };
		const res = await detect(body);
		if ("data" in res && res.data) {
			setDet(res.data);
			setCand(bestCandidate(res.data));
			const suggestion = suggestedName(res.data);
			setName((cur) => nameAfterDetect(cur, lastSuggestion.current, suggestion));
			lastSuggestion.current = suggestion;
		}
	};

	const onStored = (secretName: string, value: string, q: ConnectQuestion) => {
		const next = { ...probeSecrets, [secretName]: value };
		setProbeSecrets(next);
		if (cand && q.param === "auth_secret") setCand({ ...cand, params: { ...(cand.params ?? {}), auth_secret: secretName } });
		if (cand && q.param === "api_key_secret") setCand({ ...cand, params: { ...(cand.params ?? {}), api_key_secret: secretName } });
	};

	const onPick = (path: string) => {
		if (cand) {
			setCand(withAnswerPath(cand, path));
			setUserMapped(true);
		}
	};

	const onFix = (f: ConnectFailure) => {
		switch (f.fix?.action) {
			case "open_workers":
				void navigate({ to: "/workers" });
				return;
			case "edit_secret":
			case "inline_secret":
				document.getElementById("connect-secrets")?.scrollIntoView({ behavior: "smooth" });
				return;
			default:
				document.getElementById("connect-mapping")?.scrollIntoView({ behavior: "smooth" });
		}
	};

	const doSave = async () => {
		if (!cand) return;
		// The candidate's questions carry the pasted values (prefill); they are not part of the config.
		const candidate: ConnectCandidate = { ...cand, questions: (cand.questions ?? []).map((q) => ({ ...q, prefill: undefined })) };
		const res = await save({ id: name, candidate, suggest: verify?.result?.suggest, overwrite: Boolean(saved) });
		if ("data" in res && res.data) setSaved(res.data);
	};

	const doVerify = async () => {
		if (!saved) return;
		setInline(undefined);
		setJobId(undefined);
		const res = await startVerify(verifyRequestBody(saved.id, verifyOpts));
		if ("data" in res && res.data) {
			if (res.data.job_id) {
				setInline(res.data);
				setJobId(res.data.job_id);
				setPolling(true);
			} else setInline(res.data);
		}
	};

	// B6: picking a platform starts clean (nothing from an earlier detect or verify stays on screen).
	const choosePlatform = (id: string) => {
		const c = platformChoice(id);
		setPlatform(c.platform);
		setDet(c.det);
		setCand(c.cand);
		setSaved(c.saved);
		setInline(c.inline);
		setJobId(c.jobId);
		setPolling(c.polling);
		setProbeSecrets(c.probeSecrets);
		setUserMapped(false);
	};

	const platforms = catalog.data?.platforms ?? [];
	// The platform's own fields come from the import catalog, the same form Import from a platform uses.
	const importPlatforms = useGetAgentPlatformsQuery(undefined, { skip: platform === NO_PLATFORM });
	const importable = platform === NO_PLATFORM ? undefined : importPlatformFor(platform, importPlatforms.data?.platforms ?? []);
	const others = otherCandidates(det);
	const title = det ? resultTitle(det, cand) : undefined;
	const titleText = (() => {
		if (!title) return "";
		switch (title.key) {
			case "result.found":
				return t("result.found", { label: title.vars?.label ?? "" });
			case "result.foundUnproven":
				return t("result.foundUnproven", { label: title.vars?.label ?? "" });
			case "result.couldNotConnect":
				return t("result.couldNotConnect", { reason: title.vars?.reason ?? "" });
			default:
				return t(title.key);
		}
	})();
	const pickedLabel = platforms.find((p) => p.id === platform)?.label ?? platform;
	const verified = verify && verifyDone && connectionOK(verify.rows) && verify.status === "succeeded";

	return (
		<div className={cn(pageWidthClass("read"), "flex flex-col gap-6")} data-testid="connect-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				icon={Plug}
				breadcrumbs={[{ label: t("crumb"), to: "/agents" }, { label: t("title") }]}
			/>

			<section className="bg-card flex flex-col gap-3 rounded-xl border p-5" data-testid="connect-paste">
				{!importable && (
					<>
						<h2 className="text-base font-semibold tracking-tight">
							<label htmlFor="connect-input">{t("paste.label")}</label>
						</h2>
						<p className="text-muted-foreground text-sm" id="connect-input-help">
							{tu("connect.help")}
						</p>
						<textarea
							id="connect-input"
							className="border-input bg-card focus-visible:ring-ring/40 min-h-24 w-full rounded-md border px-3 py-2 font-mono text-sm outline-none focus-visible:ring-2"
							placeholder={t("paste.placeholder")}
							value={input}
							onChange={(e) => {
								setInput(e.target.value);
								setEmptyPress(false);
							}}
							aria-describedby="connect-input-help"
							spellCheck={false}
							data-testid="connect-input"
						/>
					</>
				)}
				<div className="flex flex-wrap items-end gap-3">
					<div className="flex min-w-0 flex-col gap-1">
						<label className="text-sm font-medium" id="connect-platform-label">
							{t("paste.platform")}
						</label>
						<Select value={platform} onValueChange={choosePlatform}>
							<SelectTrigger className="w-full min-w-0 sm:min-w-56" aria-labelledby="connect-platform-label">
								<SelectValue placeholder={t("paste.platform")} />
							</SelectTrigger>
							<SelectContent>
								<SelectItem value={NO_PLATFORM}>{t("paste.platformNone")}</SelectItem>
								{platforms.map((p) => (
									<SelectItem key={p.id} value={p.id}>
										{p.label} ({p.id_label})
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>
					{showDetectButton(platform, importable, importPlatforms, NO_PLATFORM) && (
						<Button onClick={() => void runDetect()} isLoading={detectState.isLoading} data-testid="connect-detect">
							{detectState.isLoading ? t("paste.detecting") : t("paste.detect")}
						</Button>
					)}
				</div>
				{platform !== NO_PLATFORM && !importable && (importPlatforms.isLoading || importPlatforms.isError) && (
					<PlatformCatalogNote
						label={pickedLabel}
						loading={importPlatforms.isLoading}
						error={importPlatforms.isError ? getBenchErrorMessage(importPlatforms.error) : undefined}
					/>
				)}
				{emptyPress && (
					<p role="alert" className="text-chart-error-ink text-sm" data-testid="connect-empty-press">
						{tu("connect.emptyPress")}
					</p>
				)}
				{importable ? (
					<ConnectPlatformForm key={importable.id} platform={importable} />
				) : (
					<p className="text-muted-foreground text-sm">{t("paste.hint")}</p>
				)}
				{!importable && detectState.error ? (
					<p className="text-chart-error-ink text-sm">{getBenchErrorMessage(detectState.error)}</p>
				) : null}
			</section>

			{!det && !importable && (
				<section className="flex flex-col gap-4" data-testid="connect-next">
					<div className="flex flex-col gap-2">
						<h2 className="text-sm font-semibold">{tu("connect.stepsTitle")}</h2>
						<ol className="grid grid-cols-1 gap-2 sm:grid-cols-3">
							{(["step1", "step2", "step3"] as const).map((k, i) => (
								<li key={k} className="flex items-start gap-2 rounded-lg border p-3 text-sm">
									<span
										className="bg-muted flex size-6 shrink-0 items-center justify-center rounded-full text-xs font-medium"
										aria-hidden="true"
									>
										{i + 1}
									</span>
									<span>{tu(`connect.${k}`)}</span>
								</li>
							))}
						</ol>
					</div>
					<div className="flex flex-wrap items-center gap-2">
						<span className="text-muted-foreground text-sm">{tu("connect.examples")}</span>
						{CONNECT_EXAMPLES.map((ex) => (
							<Button
								key={ex.key}
								type="button"
								size="sm"
								variant="outline"
								onClick={() => {
									setInput(ex.value);
									setEmptyPress(false);
								}}
								data-testid={`connect-example-${ex.key}`}
							>
								{tu(`connect.${ex.key}`)}
							</Button>
						))}
					</div>
				</section>
			)}

			{det && (
				<Section title={titleText} testId="connect-result">
					<div className="flex flex-col gap-4">
						{cand && <CandidateStatus cand={cand} />}
						{!cand && <UnsupportedCandidates det={det} />}
						{cand?.message && <p className="text-sm">{cand.message}</p>}
						{(cand?.evidence ?? []).length > 0 && (
							<div>
								<h3 className="text-muted-foreground mb-1 text-xs font-medium uppercase">{t("result.evidence")}</h3>
								<ul className="list-disc pl-5 text-sm" data-testid="connect-evidence">
									{cand?.evidence.map((e) => (
										<li key={e}>{e}</li>
									))}
								</ul>
							</div>
						)}
						{(det.failures ?? []).map((f, i) => (
							<FailureNote key={`${f.code}-${i}`} failure={f} onPick={onPick} onFix={onFix} />
						))}
						<details className="text-sm" data-testid="connect-probes">
							<summary className="text-muted-foreground cursor-pointer text-xs">{t("result.probes", { count: det.probes.length })}</summary>
							<ul className="mt-2 flex flex-col gap-1 font-mono text-xs">
								{det.probes.map((p, i) => (
									<li key={`${p.url}-${i}`}>
										{p.method} {p.url} {p.status ? `-> ${p.status}` : ""} {p.ms} ms {p.note ? `(${p.note})` : ""}
									</li>
								))}
							</ul>
						</details>
						{others.length > 0 && cand && (
							<details className="text-sm">
								<summary className="text-muted-foreground cursor-pointer text-xs">{t("result.other")}</summary>
								<ul className="mt-2 flex flex-col gap-1">
									{others.map((o) => (
										<li key={o.label}>
											{o.label}
											{o.message ? `: ${o.message}` : ""}
										</li>
									))}
								</ul>
							</details>
						)}
					</div>
				</Section>
			)}

			{cand && (
				<div id="connect-secrets">
					<SecretCapture questions={secretQuestions(cand)} onStored={onStored} />
					{Object.keys(probeSecrets).length > 0 && !cand.nonce_found && (
						<Button className="mt-3" variant="outline" onClick={() => void runDetect(probeSecrets)} data-testid="connect-retry">
							{t("secret.retry")}
						</Button>
					)}
				</div>
			)}

			{cand && cand.template && (
				<Section title={t("mapping.title")} testId="connect-mapping-section">
					<div id="connect-mapping" className="flex flex-col gap-4">
						<MappingPreview candidate={cand} />
						{(cand.questions ?? [])
							.filter((q) => q.kind === "pick_field" && (q.options ?? []).length > 0)
							.map((q) => (
								<div key={q.param} className="flex flex-col gap-2">
									<span className="text-sm font-medium">{t("mapping.pickField")}</span>
									<div className="flex flex-wrap gap-1.5">
										{(q.options ?? []).map((o) => (
											<Button
												key={o}
												size="sm"
												variant={cand.mapping?.answer_path === o ? "default" : "outline"}
												aria-pressed={cand.mapping?.answer_path === o}
												onClick={() => onPick(o)}
											>
												<code className="text-xs">{o}</code>
											</Button>
										))}
									</div>
								</div>
							))}
						{det?.preview?.yaml && !saved && (
							<details className="text-sm">
								<summary className="text-muted-foreground cursor-pointer text-xs">{t("mapping.yaml")}</summary>
								<pre className="bg-muted mt-2 overflow-x-auto rounded p-3 text-xs" data-testid="connect-yaml">
									{det.preview.yaml}
								</pre>
							</details>
						)}
						{det?.preview?.error && (
							<p className="text-chart-warning-ink text-sm">{t("mapping.yamlError", { error: det.preview.error })}</p>
						)}
						<div className="flex flex-wrap items-end gap-3">
							<label className="flex flex-col gap-1 text-sm">
								<span className="font-medium">{t("mapping.name")}</span>
								<Input
									value={name}
									onChange={(e) => setName(e.target.value.toLowerCase())}
									className="w-full sm:w-64"
									aria-invalid={nameProblem !== "" || undefined}
									aria-describedby={nameProblem ? "connect-name-problem" : undefined}
									data-testid="connect-name"
								/>
								{nameProblem && (
									<span id="connect-name-problem" className="text-chart-error-ink text-xs" data-testid="connect-name-problem">
										{nameProblem}
									</span>
								)}
							</label>
							<div className="flex flex-col gap-1">
								<Button
									onClick={() => void doSave()}
									disabled={!validAgentName(name) || !canSave(cand, userMapped)}
									isLoading={saveState.isLoading}
									aria-describedby={saveWhy ? "connect-save-why" : undefined}
									data-testid="connect-save"
								>
									{saveState.isLoading ? t("mapping.saving") : t("mapping.save")}
								</Button>
								<WhyDisabled reason={saveState.isLoading ? null : saveWhy} testId="connect-save-why" />
							</div>
						</div>
						{!canSave(cand, userMapped) && (
							<p className="text-muted-foreground text-sm" data-testid="connect-save-needs-proof">
								{t("mapping.saveNeedsProof")}
							</p>
						)}
						{saved && <p className="text-chart-success-ink text-sm">{t("mapping.saved", { path: saved.path })}</p>}
						{saveState.error ? <p className="text-chart-error-ink text-sm">{getBenchErrorMessage(saveState.error)}</p> : null}
					</div>
				</Section>
			)}

			{saved && (
				<Section title={t("verify.title")} description={t("verify.description")} testId="connect-verify">
					<div className="flex flex-col gap-4">
						<VerifyOptionsFields value={verifyOpts} onChange={setVerifyOpts} />
						<div>
							{/* One real task runs the agent, which calls its own model: paid, so it asks first. */}
							<PaidButton
								verb={verify ? t("verify.again") : t("verify.start")}
								capUsd={null}
								confirm="auto"
								kind="form"
								onConfirm={doVerify}
								isLoading={startState.isLoading || (Boolean(verify) && !verifyDone)}
								testId="connect-verify-start"
							/>
						</div>
						{startState.error ? <p className="text-chart-error-ink text-sm">{getBenchErrorMessage(startState.error)}</p> : null}
						{verify && <p className="text-muted-foreground text-sm">{t(verifyBenchmarkKey(verify.suite), { id: verify.suite ?? "" })}</p>}
						{verify && <VerifyRows rows={verify.rows} onPick={onPick} />}
						{verify?.result?.suggest?.history && (
							<p className="text-sm">{t("verify.history", { mode: t(`verify.historyMode.${verify.result.suggest.history}`) })}</p>
						)}
						{verifyDone && (
							<p className={verified ? "text-chart-success-ink text-sm" : "text-chart-error-ink text-sm"}>
								{verified ? t("verify.ok") : t("verify.failed")}
							</p>
						)}
						{verifyDone && verify?.error && !verified ? <p className="text-muted-foreground text-xs">{verify.error}</p> : null}
					</div>
				</Section>
			)}

			{saved && verified && (
				<Section title={t("firstLook.title")} testId="connect-first-look">
					<FirstLookLauncher agent={saved.id} />
				</Section>
			)}

			<details className="group rounded-lg border p-4" data-testid="connect-explain-fold">
				<summary className="cursor-pointer text-sm font-medium">{t6("explain.title")}</summary>
				<div className="pt-3">
					<ExplainError disabled={!member} />
				</div>
			</details>
			<details className="group rounded-lg border p-4" data-testid="connect-agent-file-fold">
				<summary className="cursor-pointer text-sm font-medium">{t6("file.title")}</summary>
				<div className="pt-3">
					<AgentFileCheck disabled={!member} />
				</div>
			</details>
		</div>
	);
}

/**
 * B6: a platform picked on Connect shows that platform's fields (id, inputs, secret names, help and
 * docs) and reads the agent as Import from a platform does: Read shows what was found, Create saves it.
 */
export function ConnectPlatformForm({ platform: p }: { platform: AgentPlatform }) {
	const { t } = useTranslation("connect");
	const canWrite = canUse(useSession(), "member");
	const navigate = useNavigate();
	const secretList = useListSecretsQuery();
	const [agentId, setAgentId] = useState("");
	const [inputs, setInputs] = useState<Record<string, string>>({});
	const [secrets, setSecrets] = useState<Record<string, string>>(() => suggestedPlatformSecrets(p));
	const [found, setFound] = useState<AgentImportResponse | null>(null);
	const [read, readState] = useImportAgentFromPlatformMutation();
	const [create, createState] = useImportAgentFromPlatformMutation();
	const missing = missingImportFields(p, agentId, inputs, secrets);
	const name = importedAgentName(p.id, found?.import.definition.name || agentId);
	const secretNames = secretList.data?.secrets.map((s) => s.name) ?? [];
	const onRead = async () => {
		setFound(null);
		const res = await read(importRequest(p, name, agentId, inputs, secrets, true));
		if ("data" in res && res.data) setFound(res.data);
	};
	const onCreate = async () => {
		const res = await create(importRequest(p, name, agentId, inputs, secrets, false));
		if ("data" in res && res.data) void navigate({ to: "/agents/$agentId", params: { agentId: res.data.id } });
	};
	const error = createState.error ?? readState.error;
	return (
		<div className="flex flex-col gap-4" data-testid="connect-platform-form">
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
					onAgentId={(v) => {
						setAgentId(v);
						setFound(null);
					}}
					inputs={inputs}
					onInputs={setInputs}
					secrets={secrets}
					onSecrets={setSecrets}
					secretNames={secretNames}
					listId="connect-platform-secrets"
				/>
				{missing.length > 0 && (
					<p className="text-muted-foreground text-xs md:col-span-2">{t("platform.missing", { list: missing.join(", ") })}</p>
				)}
				{error ? (
					<p role="alert" className="text-chart-error-ink text-sm md:col-span-2">
						{getBenchErrorMessage(error)}
					</p>
				) : null}
				<div className="flex flex-wrap gap-2 md:col-span-2">
					<Button
						type="submit"
						variant="outline"
						disabled={!canWrite || missing.length > 0}
						isLoading={readState.isLoading}
						data-testid="connect-platform-read"
					>
						{readState.isLoading ? t("platform.reading") : t("platform.read")}
					</Button>
					<Button
						type="button"
						disabled={!canWrite || !found || !!found.validation_error}
						isLoading={createState.isLoading}
						onClick={() => void onCreate()}
						data-testid="connect-platform-create"
					>
						{createState.isLoading ? t("platform.creating") : t("platform.create")}
					</Button>
				</div>
			</form>
			{found && <ImportFound res={found} platform={p} />}
		</div>
	);
}