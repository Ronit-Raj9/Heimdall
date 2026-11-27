import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { CopyCodeBlock } from "@/components/suites/copyCodeBlock";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import {
	useGetDataPolicyQuery,
	useGetDatasetVersionsQuery,
	useGetImportPlatformsQuery,
	useImportFromPlatformMutation,
	useSetDataPolicyMutation,
} from "@/lib/store/apis/annotationApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import type {
	AccessMode,
	DataPolicyView,
	DatasetVersion,
	ImportPlatform,
	PIIMode,
	PlatformImportRequest,
	PlatformImportResult,
} from "@/lib/types/annotation";
import type { SuiteSummary } from "@/lib/types/bench";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "@/components/suites/uf6aI18n";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		benchData: typeof import("@/locales/en/benchData.json");
	}
}

type T = ReturnType<typeof useTranslation<"benchData">>["t"];

const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";
const PII_MODES: PIIMode[] = ["warn", "mask", "redact", "block", "off"];
const PII_KINDS = ["email", "phone", "card", "national_id", "bank_account", "name"] as const;

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
	return (
		<label className="flex min-w-0 flex-col gap-1 text-sm">
			<span className="text-muted-foreground">{label}</span>
			{children}
			{hint && <span className="text-muted-foreground text-xs">{hint}</span>}
		</label>
	);
}

function ErrorLine({ error, testId }: { error: unknown; testId: string }) {
	if (!error) return null;
	return (
		<p role="alert" className="text-chart-error-ink text-xs" data-testid={testId}>
			{getBenchErrorMessage(error as never)}
		</p>
	);
}

/**
 * Data tab of a benchmark (lane Y15): the personal data policy and who may read the tasks, the
 * version log of tasks made from rows, imports from LangSmith, Langfuse, Braintrust and Datadog,
 * and the same through the API.
 */
export function DataTab({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("benchData");
	const session = useSession();
	const writable = canUse(session, "member");
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suite-data-tab">
			<p className="text-muted-foreground max-w-3xl text-sm">{t("intro")}</p>
			<PolicySection suite={suite.id} writable={writable} t={t} />
			{writable && <ImportSection suite={suite.id} t={t} />}
			<VersionsSection suite={suite.id} t={t} />
			<Section title={t("api.title")} description={t("api.description")} testId="suite-data-api">
				<CopyCodeBlock code={apiSnippet(suite.id)} label={t("api.title")} kind="code" testId="suite-data-api-code" />
			</Section>
		</div>
	);
}

export function apiSnippet(id: string): string {
	return [
		"import os",
		"from heimdall import V1Client",
		'hd = V1Client(os.environ["HEIMDALL_URL"], token=os.environ["HEIMDALL_TOKEN"])',
		'commit = os.environ.get("GIT_COMMIT", "local")',
		`hd.upsert_tasks("${id}", [{"id": "refund-window", "input": "How long do I have to ask for a refund?", "expected": ["30 days"]}], source="git:" + commit)`,
		`print(hd.dataset_versions("${id}")["versions"][0])`,
	].join("\n");
}

// Policy and access

function PolicySection({ suite, writable, t }: { suite: string; writable: boolean; t: T }) {
	const policy = useGetDataPolicyQuery(suite);
	if (policy.isLoading) return <BlockSkeleton rows={3} testId="suite-data-policy-loading" />;
	if (policy.error || !policy.data)
		return (
			<LoadError
				title={t("policy.loadError")}
				error={policy.error}
				onRetry={() => void policy.refetch()}
				testId="suite-data-policy-error"
			/>
		);
	return <PolicyForm key={policy.data.policy.updated_at ?? "default"} view={policy.data} writable={writable} t={t} />;
}

/** The PUT body: the personal data part always, the access part only for those who may change it. */
export function policyRequest(
	form: { mode: PIIMode; kinds: string[]; access: AccessMode; users: string },
	canEditAccess: boolean,
): { pii: { mode: PIIMode; kinds?: string[] }; access?: { mode: AccessMode; users?: string[] } } {
	const body: { pii: { mode: PIIMode; kinds?: string[] }; access?: { mode: AccessMode; users?: string[] } } = {
		pii: form.kinds.length ? { mode: form.mode, kinds: form.kinds } : { mode: form.mode },
	};
	if (canEditAccess) {
		const users = Array.from(
			new Set(
				form.users
					.split(/[\s,]+/)
					.map((s) => s.trim())
					.filter(Boolean),
			),
		);
		body.access = form.access === "restricted" && users.length ? { mode: "restricted", users } : { mode: form.access };
	}
	return body;
}

export function PolicyForm({ view, writable, t }: { view: DataPolicyView; writable: boolean; t: T }) {
	// The success state outlives the remount the refetch after a save causes (PolicySection keys
	// the form on updated_at), so "Saved." stays on screen (p11-3).
	const [save, saved] = useSetDataPolicyMutation({ fixedCacheKey: `data-policy-${view.benchmark}` });
	const [mode, setMode] = useState<PIIMode>(view.policy.pii.mode || "warn");
	const [kinds, setKinds] = useState<string[]>(view.policy.pii.kinds ?? []);
	const [access, setAccess] = useState<AccessMode>(view.policy.access.mode || "org");
	const [users, setUsers] = useState((view.policy.access.users ?? []).join(", "));
	const kindList = view.kinds && view.kinds.length ? view.kinds : [...PII_KINDS];
	const modeList = view.modes && view.modes.length ? PII_MODES.filter((m) => view.modes!.includes(m)) : PII_MODES;
	const toggle = (k: string) => setKinds((cur) => (cur.includes(k) ? cur.filter((x) => x !== k) : [...cur, k]));
	const { t: tf } = useTranslation("uf6a");
	// M6-32: each card has its own Save; both send the whole form, so nothing typed in the other card is lost.
	const saveRow = (testId: string) =>
		writable && (
			<div className="flex flex-wrap items-center justify-end gap-3">
				{saved.isSuccess && <span className="text-muted-foreground text-xs">{t("policy.saved")}</span>}
				<Button type="submit" variant="outline" disabled={saved.isLoading} data-testid={testId}>
					{t("policy.save")}
				</Button>
			</div>
		);
	return (
		<form
			className="grid min-w-0 grid-cols-1 gap-6 lg:grid-cols-2"
			onSubmit={(e) => {
				e.preventDefault();
				void save({ suite: view.benchmark, ...policyRequest({ mode, kinds, access, users }, view.can_edit_access) });
			}}
			data-testid="suite-data-policy"
		>
			<Section title={t("policy.title")} description={t("policy.description")} testId="suite-data-pii">
				<div className="bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5">
					<Field label={t("policy.mode")}>
						<select
							className={selectClass}
							value={mode}
							disabled={!writable}
							onChange={(e) => setMode(e.target.value as PIIMode)}
							data-testid="suite-data-pii-mode"
						>
							{modeList.map((m) => (
								<option key={m} value={m}>
									{t(`policy.modes.${m}`)}
								</option>
							))}
						</select>
					</Field>
					<fieldset className="flex flex-col gap-2 text-sm" disabled={!writable || mode === "off"}>
						<legend className="text-muted-foreground mb-1">{t("policy.kinds")}</legend>
						<div className="grid grid-cols-1 gap-x-3 sm:grid-cols-2">
							{/* M6-34: "Everything" is a real first choice; ticking it clears the others. */}
							<label className="flex min-h-11 items-center gap-2 sm:col-span-2">
								<input
									type="checkbox"
									className="size-4 shrink-0"
									checked={kinds.length === 0}
									onChange={() => setKinds([])}
									data-testid="suite-data-pii-kind-all"
								/>
								{tf("data.everything")}
							</label>
							{kindList.map((k) => (
								<label key={k} className="flex min-h-11 items-center gap-2">
									<input
										type="checkbox"
										className="size-4 shrink-0"
										checked={kinds.includes(k)}
										onChange={() => toggle(k)}
										data-testid={`suite-data-pii-kind-${k}`}
									/>
									{t(`policy.kind.${k}` as "policy.kind.email", { defaultValue: k })}
								</label>
							))}
						</div>
					</fieldset>
					{saveRow("suite-data-pii-save")}
				</div>
			</Section>
			<Section title={t("access.title")} description={t("access.description")} testId="suite-data-access">
				<div className="bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5">
					<div className="flex flex-col gap-2 text-sm" role="radiogroup" aria-label={t("access.title")}>
						{(["org", "restricted"] as AccessMode[]).map((m) => (
							<label key={m} className="flex min-h-11 items-center gap-2">
								<input
									type="radio"
									className="size-4 shrink-0"
									name={`access-${view.benchmark}`}
									checked={access === m}
									disabled={!view.can_edit_access}
									onChange={() => setAccess(m)}
									data-testid={`suite-data-access-${m}`}
								/>
								{t(`access.${m}`)}
							</label>
						))}
					</div>
					{access === "restricted" && (
						<Field label={t("access.users")}>
							<Input
								value={users}
								onChange={(e) => setUsers(e.target.value)}
								disabled={!view.can_edit_access}
								data-testid="suite-data-access-users"
							/>
						</Field>
					)}
					{!view.can_edit_access && (
						<p className="text-muted-foreground text-xs" data-testid="suite-data-access-admin-only">
							{t("access.adminOnly")}
						</p>
					)}
					{view.can_edit_access && saveRow("suite-data-policy-save")}
				</div>
			</Section>
			{writable && (
				<div className="flex flex-wrap items-center gap-3 lg:col-span-2">
					{(view.updated_by_name || view.policy.updated_by) && (
						<span
							className="text-muted-foreground text-xs"
							title={view.policy.updated_by || undefined}
							data-testid="suite-data-policy-updated"
						>
							{t("policy.updated", { who: view.updated_by_name || view.policy.updated_by })}
						</span>
					)}
					<ErrorLine error={saved.error} testId="suite-data-policy-save-error" />
				</div>
			)}
		</form>
	);
}

// Versions

function VersionsSection({ suite, t }: { suite: string; t: T }) {
	const versions = useGetDatasetVersionsQuery(suite);
	return (
		<Section title={t("versions.title")} description={t("versions.description")} testId="suite-data-versions">
			{versions.isLoading ? (
				<BlockSkeleton rows={3} testId="suite-data-versions-loading" />
			) : versions.error ? (
				<LoadError
					title={t("versions.loadError")}
					error={versions.error}
					onRetry={() => void versions.refetch()}
					testId="suite-data-versions-error"
				/>
			) : (
				<VersionTable versions={versions.data?.versions ?? []} t={t} />
			)}
		</Section>
	);
}

export function VersionTable({ versions, t }: { versions: DatasetVersion[]; t: T }) {
	if (versions.length === 0) return <EmptyNote testId="suite-data-versions-empty">{t("versions.empty")}</EmptyNote>;
	return (
		<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
			<table className="w-full text-sm">
				<thead className="text-muted-foreground border-b text-left text-xs">
					<tr>
						<th className="px-4 py-2 font-medium">{t("versions.version")}</th>
						<th className="px-4 py-2 font-medium">{t("versions.when")}</th>
						<th className="hidden px-4 py-2 font-medium sm:table-cell">{t("versions.by")}</th>
						<th className="hidden px-4 py-2 font-medium sm:table-cell">{t("versions.source")}</th>
						<th className="px-4 py-2 font-medium">{t("versions.changes")}</th>
						<th className="px-4 py-2 text-right font-medium">{t("versions.tasks")}</th>
					</tr>
				</thead>
				<tbody>
					{versions.map((v) => (
						<tr key={v.version} className="border-b last:border-0" data-testid={`suite-data-version-${v.version}`}>
							<td className="px-4 py-2 font-mono text-xs" title={v.sha256}>
								v{v.version}
							</td>
							<td className="text-muted-foreground px-4 py-2 text-xs">{new Date(v.at).toLocaleString()}</td>
							<td className="text-muted-foreground hidden px-4 py-2 text-xs sm:table-cell" title={v.actor || undefined}>
								{v.actor_name || v.actor || "-"}
							</td>
							<td className="text-muted-foreground hidden max-w-48 truncate px-4 py-2 text-xs sm:table-cell" title={v.source}>
								{v.source || "-"}
							</td>
							<td className="px-4 py-2 text-xs tabular-nums">
								{t("versions.change", { added: v.added?.length ?? 0, updated: v.updated?.length ?? 0, removed: v.removed?.length ?? 0 })}
							</td>
							<td className="px-4 py-2 text-right tabular-nums">{v.tasks}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

// Platform import

/** The secret each role of a platform reads: the one chosen, else the platform's default name. */
export function missingSecrets(platform: ImportPlatform, chosen: Record<string, string>, have: string[]): string[] {
	return (platform.secrets ?? []).map((s) => chosen[s.role] || s.default).filter((name) => !have.includes(name));
}

function ImportSection({ suite, t }: { suite: string; t: T }) {
	const platforms = useGetImportPlatformsQuery();
	if (platforms.isLoading) return <BlockSkeleton rows={3} testId="suite-data-import-loading" />;
	if (platforms.error || !platforms.data)
		return (
			<LoadError
				title={t("import.loadError")}
				error={platforms.error}
				onRetry={() => void platforms.refetch()}
				testId="suite-data-import-error"
			/>
		);
	const list = platforms.data.platforms ?? [];
	if (list.length === 0) return null;
	return (
		<ImportForm suite={suite} platforms={list} secrets={platforms.data.secrets ?? []} secretsLink={platforms.data.secrets_link} t={t} />
	);
}

export function ImportForm({
	suite,
	platforms,
	secrets,
	secretsLink,
	t,
}: {
	suite: string;
	platforms: ImportPlatform[];
	secrets: string[];
	secretsLink: string;
	t: T;
}) {
	const [run, result] = useImportFromPlatformMutation();
	const { t: tf } = useTranslation("uf6a");
	const [platformId, setPlatformId] = useState(platforms[0]!.id);
	const [kind, setKind] = useState<"dataset" | "traces">("dataset");
	const [datasetText, setDataset] = useState("");
	const [projectText, setProject] = useState("");
	const [query, setQuery] = useState("");
	const [limit, setLimit] = useState(100);
	const [baseUrl, setBaseUrl] = useState("");
	const [chosen, setChosen] = useState<Record<string, string>>({});
	const [preview, setPreview] = useState<PlatformImportResult | null>(null);
	const platform = platforms.find((p) => p.id === platformId) ?? platforms[0]!;
	const missing = missingSecrets(platform, chosen, secrets);
	const request = (dryRun: boolean): PlatformImportRequest => {
		const dataset = kind === "dataset" ? datasetText : "";
		const project = kind === "traces" ? projectText : "";
		const body: PlatformImportRequest = {
			platform: platform.id,
			kind,
			target: "benchmark",
			benchmark: suite,
			limit: Math.max(1, Math.trunc(limit) || 100),
			dry_run: dryRun,
		};
		if (dataset.trim()) body.dataset = dataset.trim();
		if (project.trim()) body.project = project.trim();
		if (query.trim()) body.query = query.trim();
		if (baseUrl.trim()) body.base_url = baseUrl.trim();
		const roles = Object.fromEntries(Object.entries(chosen).filter(([, v]) => v));
		if (Object.keys(roles).length) body.secrets = roles;
		return body;
	};
	const reset = () => setPreview(null);
	const doPreview = () =>
		void run(request(true))
			.unwrap()
			.then(setPreview)
			.catch(() => setPreview(null));
	const doImport = () =>
		void run(request(false))
			.unwrap()
			.then(() => setPreview(null))
			.catch(() => {});
	const written = result.data && !result.data.benchmark?.dry_run ? result.data.benchmark : null;
	return (
		<Section title={t("import.title")} description={t("import.description")} testId="suite-data-import">
			<div className="bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5">
				<div className="grid min-w-0 grid-cols-1 gap-4 md:grid-cols-3">
					<Field label={t("import.platform")}>
						<select
							className={selectClass}
							value={platformId}
							onChange={(e) => {
								setPlatformId(e.target.value);
								setChosen({});
								reset();
							}}
							data-testid="suite-data-import-platform"
						>
							{platforms.map((p) => (
								<option key={p.id} value={p.id}>
									{p.name}
								</option>
							))}
						</select>
					</Field>
					<Field label={t("import.kind")}>
						<select
							className={selectClass}
							value={kind}
							onChange={(e) => {
								setKind(e.target.value as "dataset" | "traces");
								reset();
							}}
							data-testid="suite-data-import-kind"
						>
							<option value="dataset">{t("import.kindDataset")}</option>
							{(platform.kinds ?? []).includes("traces") && <option value="traces">{t("import.kindTraces")}</option>}
						</select>
					</Field>
					<Field label={t("import.limit")}>
						<Input
							type="number"
							min={1}
							value={limit}
							onChange={(e) => setLimit(Number(e.target.value))}
							data-testid="suite-data-import-limit"
						/>
					</Field>
					{kind === "dataset" && (
						<Field label={t("import.dataset")} hint={platform.dataset_hint}>
							<Input value={datasetText} onChange={(e) => (setDataset(e.target.value), reset())} data-testid="suite-data-import-dataset" />
						</Field>
					)}
					{/* M6-33: Dataset for a dataset import, Project for traces; never both. */}
					{kind === "traces" && (
						<Field label={t("import.project")} hint={platform.project_hint}>
							<Input value={projectText} onChange={(e) => (setProject(e.target.value), reset())} data-testid="suite-data-import-project" />
						</Field>
					)}
					{kind === "traces" && (
						<Field label={t("import.query")}>
							<Input value={query} onChange={(e) => (setQuery(e.target.value), reset())} data-testid="suite-data-import-query" />
						</Field>
					)}
					<Field label={t("import.baseUrl")}>
						<Input
							value={baseUrl}
							placeholder={platform.base_url}
							onChange={(e) => (setBaseUrl(e.target.value), reset())}
							data-testid="suite-data-import-base-url"
						/>
					</Field>
					{(platform.secrets ?? []).map((s) => (
						<Field key={s.role} label={t("import.secret", { label: s.label })}>
							<select
								className={selectClass}
								value={chosen[s.role] || s.default}
								onChange={(e) => setChosen((c) => ({ ...c, [s.role]: e.target.value === s.default ? "" : e.target.value }))}
								data-testid={`suite-data-import-secret-${s.role}`}
							>
								{Array.from(new Set([s.default, ...secrets])).map((name) => (
									<option key={name} value={name}>
										{secrets.includes(name) ? name : tf("data.noSecret", { name })}
									</option>
								))}
							</select>
						</Field>
					))}
				</div>
				{missing.length > 0 && (
					<p className="text-muted-foreground text-xs" role="note" data-testid="suite-data-import-missing">
						{missing.map((name) => t("import.missingSecret", { name })).join(" ")}{" "}
						{secretsLink && (
							<a href={secretsLink} className="text-primary underline-offset-4 hover:underline">
								{t("import.secretsLink")}
							</a>
						)}
					</p>
				)}
				<div className="flex flex-wrap items-center gap-2">
					<Button
						variant="outline"
						onClick={doPreview}
						disabled={result.isLoading || missing.length > 0}
						data-testid="suite-data-import-preview"
					>
						{t("import.preview")}
					</Button>
					{preview && (
						<Button onClick={doImport} disabled={result.isLoading || preview.read === 0} data-testid="suite-data-import-run">
							{t("import.import", { count: preview.read })}
						</Button>
					)}
				</div>
				<ErrorLine error={result.error} testId="suite-data-import-run-error" />
				{preview && <ImportPreview result={preview} t={t} />}
				{written && !preview && <ImportWritten result={result.data!} t={t} />}
			</div>
		</Section>
	);
}

export function ImportPreview({ result, t }: { result: PlatformImportResult; t: T }) {
	const pii = result.benchmark?.pii;
	return (
		<div className="flex min-w-0 flex-col gap-2 text-sm" data-testid="suite-data-import-result">
			<p className="text-muted-foreground text-xs">
				{t("import.read", { count: result.read })} {result.truncated ? t("import.truncated") : ""}
			</p>
			{(result.warnings ?? []).map((w) => (
				<p key={w} className="text-muted-foreground text-xs">
					{w}
				</p>
			))}
			{pii && pii.summary && (
				<p className="text-xs" data-testid="suite-data-import-pii">
					{t("import.pii", { summary: pii.summary })}
				</p>
			)}
			{(result.benchmark?.problems?.length ?? 0) > 0 && (
				<p className="text-chart-error-ink text-xs">{t("import.problems", { count: result.benchmark!.problems!.length })}</p>
			)}
			{(result.preview ?? []).length > 0 && (
				<ul className="flex max-h-64 flex-col gap-1 overflow-auto rounded-md border p-2 text-xs">
					{(result.preview ?? []).map((p) => (
						<li key={p.source_id} className="flex min-w-0 gap-2">
							<span className="text-muted-foreground shrink-0 font-mono">{p.source_id}</span>
							<span className="truncate">{p.input}</span>
							{p.expected && <span className="text-muted-foreground truncate">{p.expected}</span>}
						</li>
					))}
				</ul>
			)}
		</div>
	);
}

function ImportWritten({ result, t }: { result: PlatformImportResult; t: T }) {
	const b = result.benchmark;
	if (!b) return null;
	return (
		<p className="text-sm" role="status" data-testid="suite-data-import-written">
			{b.version
				? t("import.wrote", {
						version: b.version.version,
						added: b.added?.length ?? 0,
						updated: b.updated?.length ?? 0,
						unchanged: b.unchanged?.length ?? 0,
					})
				: t("import.nothing")}
			{b.pii?.summary ? ` ${t("import.pii", { summary: b.pii.summary })}` : ""}
		</p>
	);
}