import { Section } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell/pageHeader";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useDetectAgentMutation,
	useGetConnectCatalogQuery,
	useGetVerifyQuery,
	useSaveConnectedAgentMutation,
	useStartVerifyMutation,
} from "@/lib/store/apis/connectApi";
import type {
	ConnectCandidate,
	ConnectDetectResponse,
	ConnectFailure,
	ConnectQuestion,
	ConnectSaveResponse,
	ConnectVerifyStatus,
} from "@/lib/types/connect";
import { useNavigate } from "@tanstack/react-router";
import { CircleCheck, Plug } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	VERIFY_POLL_MS,
	bestCandidate,
	connectionOK,
	isTerminalJob,
	otherCandidates,
	secretQuestions,
	suggestedName,
	validAgentName,
	withAnswerPath,
} from "./connect.utils";
import { FailureNote } from "./failureNote";
import { FirstLookLauncher } from "./firstLookLauncher";
import { MappingPreview } from "./mappingPreview";
import { SecretCapture } from "./secretCapture";
import { VerifyRows } from "./verifyRows";

const NO_PLATFORM = "none";

/**
 * /connect (lane Z01): one paste box for any agent. Detect shows what was found and why, the
 * request Heimdall will send and the agent file; secrets are stored inline; Save writes the agent;
 * Verify streams its rows (the agent.verify job) and ends with one real task; then the first look
 * starts on the fastest compatible benchmark.
 */
export default function ConnectView() {
	const { t } = useTranslation("connect");
	const navigate = useNavigate();
	const catalog = useGetConnectCatalogQuery();
	const [input, setInput] = useState("");
	const [platform, setPlatform] = useState(NO_PLATFORM);
	const [probeSecrets, setProbeSecrets] = useState<Record<string, string>>({});
	const [detect, detectState] = useDetectAgentMutation();
	const [det, setDet] = useState<ConnectDetectResponse | undefined>();
	const [cand, setCand] = useState<ConnectCandidate | undefined>();
	const [name, setName] = useState("my-agent");
	const [save, saveState] = useSaveConnectedAgentMutation();
	const [saved, setSaved] = useState<ConnectSaveResponse | undefined>();
	const [startVerify, startState] = useStartVerifyMutation();
	const [inline, setInline] = useState<ConnectVerifyStatus | undefined>();
	const [jobId, setJobId] = useState<string | undefined>();
	const [polling, setPolling] = useState(false);
	const polled = useGetVerifyQuery(jobId ?? "", { skip: !jobId, pollingInterval: polling ? VERIFY_POLL_MS : 0 });
	const verify = jobId ? (polled.data ?? inline) : inline;
	const verifyDone = verify ? isTerminalJob(verify.status) : false;
	useEffect(() => {
		// Rows stream in while the job runs; polling stops once it ends.
		if (polling && verifyDone) setPolling(false);
	}, [polling, verifyDone]);

	const runDetect = async (secrets: Record<string, string> = probeSecrets) => {
		setSaved(undefined);
		setInline(undefined);
		setJobId(undefined);
		const body = platform !== NO_PLATFORM ? { input, platform, id: input, secrets } : { input, secrets };
		const res = await detect(body);
		if ("data" in res && res.data) {
			setDet(res.data);
			setCand(bestCandidate(res.data));
			setName(suggestedName(res.data));
		}
	};

	const onStored = (secretName: string, value: string, q: ConnectQuestion) => {
		const next = { ...probeSecrets, [secretName]: value };
		setProbeSecrets(next);
		if (cand && q.param === "auth_secret") setCand({ ...cand, params: { ...(cand.params ?? {}), auth_secret: secretName } });
		if (cand && q.param === "api_key_secret") setCand({ ...cand, params: { ...(cand.params ?? {}), api_key_secret: secretName } });
	};

	const onPick = (path: string) => {
		if (cand) setCand(withAnswerPath(cand, path));
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
		const res = await startVerify({ agent: saved.id });
		if ("data" in res && res.data) {
			if (res.data.job_id) {
				setInline(res.data);
				setJobId(res.data.job_id);
				setPolling(true);
			} else setInline(res.data);
		}
	};

	const platforms = catalog.data?.platforms ?? [];
	const others = otherCandidates(det);
	const verified = verify && verifyDone && connectionOK(verify.rows) && verify.status === "succeeded";

	return (
		<div className="mx-auto flex w-full max-w-[1100px] flex-col gap-6" data-testid="connect-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				icon={Plug}
				breadcrumbs={[{ label: t("crumb"), to: "/agents" }, { label: t("title") }]}
			/>

			<section className="bg-card flex flex-col gap-3 rounded-xl border p-5" data-testid="connect-paste">
				<label className="text-sm font-medium" htmlFor="connect-input">
					{t("paste.label")}
				</label>
				<textarea
					id="connect-input"
					className="border-input bg-card focus-visible:ring-ring/40 min-h-24 w-full rounded-md border px-3 py-2 font-mono text-sm outline-none focus-visible:ring-2"
					placeholder={t("paste.placeholder")}
					value={input}
					onChange={(e) => setInput(e.target.value)}
					spellCheck={false}
					data-testid="connect-input"
				/>
				<div className="flex flex-wrap items-center gap-3">
					<Select value={platform} onValueChange={setPlatform}>
						<SelectTrigger className="min-w-56" aria-label={t("paste.platform")}>
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
					<Button
						onClick={() => void runDetect()}
						disabled={!input.trim() && platform === NO_PLATFORM}
						isLoading={detectState.isLoading}
						data-testid="connect-detect"
					>
						{detectState.isLoading ? t("paste.detecting") : t("paste.detect")}
					</Button>
				</div>
				<p className="text-muted-foreground text-xs">{t("paste.hint")}</p>
				{detectState.error ? <p className="text-chart-error-ink text-sm">{getBenchErrorMessage(detectState.error)}</p> : null}
			</section>

			{det && (
				<Section title={cand ? t("result.found", { label: cand.label }) : t("result.notSupported")} testId="connect-result">
					<div className="flex flex-col gap-4">
						{cand && (
							<div className="flex flex-wrap items-center gap-2 text-sm">
								<Badge variant="outline">{t("result.confidence", { pct: Math.round(cand.confidence * 100) })}</Badge>
								{cand.nonce_found ? (
									<span className="text-chart-success-ink flex items-center gap-1.5">
										<CircleCheck className="size-4" aria-hidden />
										{t("result.nonceFound")}
									</span>
								) : (
									<span className="text-muted-foreground">{t("result.nonceMissing")}</span>
								)}
							</div>
						)}
						{!cand &&
							det.candidates.map((c) => (
								<div key={c.label} className="flex flex-col gap-2 rounded-lg border p-3 text-sm" data-testid="connect-unsupported">
									<div className="font-medium">{c.label}</div>
									{c.message && <p>{c.message}</p>}
									{c.command && <code className="bg-muted rounded px-2 py-1 text-xs">{c.command}</code>}
								</div>
							))}
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
							<summary className="text-muted-foreground cursor-pointer text-xs">
								{t("result.probes", { from: det.from, count: det.probes.length })}
							</summary>
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
											<Button key={o} size="sm" variant="outline" onClick={() => onPick(o)}>
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
								<Input value={name} onChange={(e) => setName(e.target.value.toLowerCase())} className="w-64" data-testid="connect-name" />
							</label>
							<Button
								onClick={() => void doSave()}
								disabled={!validAgentName(name)}
								isLoading={saveState.isLoading}
								data-testid="connect-save"
							>
								{saveState.isLoading ? t("mapping.saving") : t("mapping.save")}
							</Button>
						</div>
						{saved && <p className="text-chart-success-ink text-sm">{t("mapping.saved", { path: saved.path })}</p>}
						{saveState.error ? <p className="text-chart-error-ink text-sm">{getBenchErrorMessage(saveState.error)}</p> : null}
					</div>
				</Section>
			)}

			{saved && (
				<Section title={t("verify.title")} description={t("verify.description")} testId="connect-verify">
					<div className="flex flex-col gap-4">
						<div>
							<Button
								onClick={() => void doVerify()}
								isLoading={startState.isLoading || (Boolean(verify) && !verifyDone)}
								data-testid="connect-verify-start"
							>
								{verify ? t("verify.again") : t("verify.start")}
							</Button>
						</div>
						{startState.error ? <p className="text-chart-error-ink text-sm">{getBenchErrorMessage(startState.error)}</p> : null}
						{verify && (
							<p className="text-muted-foreground text-sm">
								{verify.suite ? t("verify.benchmark", { id: verify.suite }) : t("verify.noBenchmark")}
							</p>
						)}
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
		</div>
	);
}