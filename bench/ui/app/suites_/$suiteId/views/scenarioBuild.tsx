import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useImportScenarioRowsMutation, useWriteSuiteMockMutation } from "@/lib/store/apis/scenarioBuildApi";
import type { SuiteSummary } from "@/lib/types/bench";
import type { ScenarioRowsRequest, ScenarioRowsResult, SuiteMockResult } from "@/lib/types/scenarioBuild";
import { useState, type ChangeEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		scenarioBuild: typeof import("@/locales/en/scenarioBuild.json");
	}
}

const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";
const areaClass = "border-input bg-background min-h-40 w-full rounded-md border p-2 font-mono text-xs";

/** A mock's service name, as the server accepts it. */
export const MOCK_ID = /^[a-z][a-z0-9_-]{0,31}$/;

/** CSV or JSONL from a file name; JSON Lines when the name says neither. */
export function scenarioFormatOf(name: string): "csv" | "jsonl" {
	const n = name.toLowerCase();
	return n.endsWith(".csv") || n.endsWith(".tsv") ? "csv" : "jsonl";
}

/** A service name from an OpenAPI file name: lowercase, letters and digits, starting with a letter. */
export function mockIdFrom(fileName: string): string {
	const base = fileName.replace(/\.(ya?ml|json)$/i, "").toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^[^a-z]+/, "");
	return base.slice(0, 32).replace(/-+$/, "");
}

/** The scenario rows body: optional parts only when set. */
export function scenarioBody(v: {
	format: "csv" | "jsonl";
	data: string;
	shape: string;
	switchGrader: boolean;
	provider: string;
	model: string;
	heldOut: boolean;
	source: string;
}): ScenarioRowsRequest {
	const provider = v.provider.trim();
	const model = v.model.trim();
	const judge = v.switchGrader && provider && model ? { judge: { provider, model } } : {};
	return {
		format: v.format,
		data: v.data,
		...(v.shape.trim() ? { shape_task: v.shape.trim() } : {}),
		...(v.switchGrader ? { switch_grader: true } : {}),
		...judge,
		...(v.heldOut ? { held_out: true } : {}),
		...(v.source ? { source: v.source } : {}),
	};
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
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
		<p role="alert" className="text-chart-error-ink text-xs break-words" data-testid={testId}>
			{getBenchErrorMessage(error as never)}
		</p>
	);
}

/**
 * Support conversations from a spreadsheet or JSON Lines: each row becomes a task with a simulated
 * customer, the customer's account in the starting data, and declarative checks of the tool calls
 * and the reply. Preview first; nothing is written until "Add".
 */
export function ScenarioRowsImport({ suite, writable }: { suite: SuiteSummary; writable: boolean }) {
	const { t } = useTranslation("scenarioBuild");
	const [data, setData] = useState("");
	const [format, setFormat] = useState<"csv" | "jsonl">("jsonl");
	const [source, setSource] = useState("");
	const [shape, setShape] = useState("");
	const [switchGrader, setSwitchGrader] = useState(suite.grader !== "checks");
	const [provider, setProvider] = useState("");
	const [model, setModel] = useState("");
	const [heldOut, setHeldOut] = useState(false);
	const [preview, setPreview] = useState<ScenarioRowsResult | null>(null);
	const [done, setDone] = useState<ScenarioRowsResult | null>(null);
	const [run, state] = useImportScenarioRowsMutation();
	const tasks = suite.tasks ?? [];
	const body = scenarioBody({ format, data, shape, switchGrader, provider, model, heldOut, source });
	const reset = () => {
		setPreview(null);
		setDone(null);
	};
	const onFile = (e: ChangeEvent<HTMLInputElement>) => {
		const file = e.target.files?.[0];
		if (!file) return;
		setFormat(scenarioFormatOf(file.name));
		setSource(file.name);
		reset();
		void file.text().then(setData);
	};
	const send = async (dryRun: boolean) => {
		const res = await run({ suite: suite.id, ...body, dry_run: dryRun });
		if (!("data" in res) || !res.data) return;
		if (dryRun) {
			setDone(null);
			setPreview(res.data);
		} else {
			setDone(res.data);
			setPreview(null);
			setData("");
		}
	};
	if (!writable) return null;
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="suite-build-scenarios">
			<p className="text-muted-foreground max-w-3xl text-xs">{t("scenarios.help")}</p>
			<div className="flex flex-wrap items-end gap-3">
				<Field label={t("scenarios.file")}>
					<input type="file" accept=".csv,.tsv,.jsonl,.ndjson,.json" onChange={onFile} className="text-sm" data-testid="suite-build-scenarios-file" />
				</Field>
				<Field label={t("scenarios.format")}>
					<select className={selectClass} value={format} onChange={(e) => (setFormat(e.target.value as "csv" | "jsonl"), reset())}>
						<option value="jsonl">JSON Lines</option>
						<option value="csv">CSV</option>
					</select>
				</Field>
				<Field label={t("scenarios.shape")}>
					<select className={selectClass} value={shape} onChange={(e) => (setShape(e.target.value), reset())} data-testid="suite-build-scenarios-shape">
						<option value="">{t("scenarios.shapeAuto")}</option>
						{tasks.map((task) => (
							<option key={task.id} value={task.id}>
								{task.id}
							</option>
						))}
					</select>
				</Field>
			</div>
			<textarea
				className={areaClass}
				value={data}
				placeholder={t("scenarios.placeholder")}
				aria-label={t("scenarios.file")}
				onChange={(e) => (setData(e.target.value), reset())}
				data-testid="suite-build-scenarios-data"
			/>
			{suite.grader !== "checks" && (
				<label className="flex flex-col gap-0.5 text-sm">
					<span className="flex items-center gap-2">
						<input type="checkbox" checked={switchGrader} onChange={(e) => (setSwitchGrader(e.target.checked), reset())} data-testid="suite-build-scenarios-switch" />
						{t("scenarios.switchGrader")}
					</span>
					<span className="text-muted-foreground pl-6 text-xs">{t("scenarios.switchGraderHint")}</span>
				</label>
			)}
			{switchGrader && (
				<div className="flex flex-wrap items-end gap-3">
					<Field label={t("scenarios.judgeProvider")}>
						<Input className="h-9 w-40" value={provider} onChange={(e) => (setProvider(e.target.value), reset())} data-testid="suite-build-scenarios-provider" />
					</Field>
					<Field label={t("scenarios.judgeModel")}>
						<Input className="h-9 w-56" value={model} onChange={(e) => (setModel(e.target.value), reset())} data-testid="suite-build-scenarios-model" />
					</Field>
				</div>
			)}
			<label className="flex items-center gap-2 text-sm">
				<input type="checkbox" checked={heldOut} onChange={(e) => (setHeldOut(e.target.checked), reset())} />
				{t("scenarios.heldOut")}
			</label>
			<div className="flex flex-wrap gap-2">
				<Button variant="outline" disabled={!data.trim() || state.isLoading} onClick={() => void send(true)} dataTestId="suite-build-scenarios-preview">
					{t("scenarios.preview")}
				</Button>
				<Button
					disabled={!preview || preview.tasks.length === 0 || state.isLoading}
					isLoading={state.isLoading}
					onClick={() => void send(false)}
					dataTestId="suite-build-scenarios-import"
				>
					{preview ? t("scenarios.import", { count: preview.tasks.length }) : t("scenarios.importIdle")}
				</Button>
			</div>
			<ErrorLine error={state.error} testId="suite-build-scenarios-error" />
			{preview && <ScenarioPreview result={preview} />}
			{done && (
				<div role="status" className="border-primary/40 bg-primary/5 flex flex-col gap-1 rounded-md border px-3 py-2 text-sm" data-testid="suite-build-scenarios-done">
					<p className="font-medium">{t("scenarios.done", { count: done.tasks.length })}</p>
					{done.grader_changed && <p className="text-xs">{t("scenarios.graderChanged")}</p>}
					<p className="text-muted-foreground text-xs">{done.next}</p>
				</div>
			)}
		</div>
	);
}

export function ScenarioPreview({ result }: { result: ScenarioRowsResult }) {
	const { t } = useTranslation("scenarioBuild");
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid="suite-build-scenarios-result">
			<p className="text-sm">{t("scenarios.previewSummary", { ok: result.tasks.length, bad: result.problems.length })}</p>
			{result.shape && <p className="text-muted-foreground text-xs">{t("scenarios.copies", { shape: result.shape })}</p>}
			{result.tasks.length > 0 && (
				<ul className="flex flex-col gap-1.5 text-xs">
					{result.tasks.map((task) => (
						<li key={task.id} className="border-border rounded-sm border px-2 py-1.5">
							<p>
								<span className="font-mono">{task.id}</span>
								<span className="text-muted-foreground ml-2">{t("scenarios.checks", { count: task.checks })}</span>
							</p>
							<p className="text-muted-foreground break-words">{task.opening}</p>
							{(task.warnings ?? []).map((w, i) => (
								<p key={i} className="text-chart-warning-ink break-words">
									{w}
								</p>
							))}
						</li>
					))}
				</ul>
			)}
			{result.problems.length > 0 && (
				<ul className="text-chart-error-ink flex flex-col gap-0.5 text-xs" data-testid="suite-build-scenarios-problems">
					{result.problems.map((p, i) => (
						<li key={`${p.line}-${i}`} className="break-words">
							{t("scenarios.line", { line: p.line })}: {p.message}
						</li>
					))}
				</ul>
			)}
		</div>
	);
}

/**
 * A stand-in for the customer's own API from its OpenAPI file: the same routes over HTTP and one
 * tool per operation, over starting data each task seeds, exported for the checks.
 */
export function MockFromOpenAPI({ suite, writable }: { suite: SuiteSummary; writable: boolean }) {
	const { t } = useTranslation("scenarioBuild");
	const [id, setId] = useState("");
	const [spec, setSpec] = useState("");
	const [fileName, setFileName] = useState("");
	const [replace, setReplace] = useState(false);
	const [preview, setPreview] = useState<SuiteMockResult | null>(null);
	const [done, setDone] = useState<SuiteMockResult | null>(null);
	const [run, state] = useWriteSuiteMockMutation();
	const validId = MOCK_ID.test(id);
	const reset = () => {
		setPreview(null);
		setDone(null);
	};
	const onFile = (e: ChangeEvent<HTMLInputElement>) => {
		const file = e.target.files?.[0];
		if (!file) return;
		setFileName(file.name);
		if (!id) setId(mockIdFrom(file.name));
		reset();
		void file.text().then(setSpec);
	};
	const send = async (dryRun: boolean) => {
		const res = await run({ suite: suite.id, id, spec, ...(fileName ? { file_name: fileName } : {}), ...(replace ? { replace: true } : {}), dry_run: dryRun });
		if (!("data" in res) || !res.data) return;
		if (dryRun) {
			setDone(null);
			setPreview(res.data);
		} else {
			setDone(res.data);
			setPreview(null);
		}
	};
	if (!writable) return null;
	const shown = done ?? preview;
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="suite-build-mock">
			<p className="text-muted-foreground max-w-3xl text-xs">{t("mock.help")}</p>
			<div className="flex flex-wrap items-end gap-3">
				<Field label={t("mock.file")}>
					<input type="file" accept=".yaml,.yml,.json" onChange={onFile} className="text-sm" data-testid="suite-build-mock-file" />
				</Field>
				<Field label={t("mock.id")} hint={t("mock.idHint")}>
					<Input
						className="h-9 w-56 font-mono"
						value={id}
						onChange={(e) => (setId(e.target.value), reset())}
						aria-invalid={(id !== "" && !validId) || undefined}
						data-testid="suite-build-mock-id"
					/>
				</Field>
			</div>
			<textarea className={areaClass} value={spec} aria-label={t("mock.paste")} placeholder={t("mock.paste")} onChange={(e) => (setSpec(e.target.value), reset())} data-testid="suite-build-mock-spec" />
			<label className="flex items-center gap-2 text-sm">
				<input type="checkbox" checked={replace} onChange={(e) => (setReplace(e.target.checked), reset())} data-testid="suite-build-mock-replace" />
				{t("mock.replace")}
			</label>
			<div className="flex flex-wrap gap-2">
				<Button variant="outline" disabled={!spec.trim() || !validId || state.isLoading} onClick={() => void send(true)} dataTestId="suite-build-mock-preview">
					{t("mock.preview")}
				</Button>
				<Button disabled={!preview || state.isLoading} isLoading={state.isLoading} onClick={() => void send(false)} dataTestId="suite-build-mock-create">
					{t("mock.create")}
				</Button>
			</div>
			<ErrorLine error={state.error} testId="suite-build-mock-error" />
			{shown && <MockResult result={shown} />}
		</div>
	);
}

export function MockResult({ result }: { result: SuiteMockResult }) {
	const { t } = useTranslation("scenarioBuild");
	const collections = result.collections.length > 0 ? result.collections.join(", ") : t("mock.noCollections");
	return (
		<div className="flex min-w-0 flex-col gap-2" role={result.dry_run ? undefined : "status"} data-testid="suite-build-mock-result">
			{!result.dry_run && <p className="text-sm font-medium">{t("mock.done", { folder: result.folder })}</p>}
			<p className="text-sm">{t("mock.operations", { count: result.operations.length, collections })}</p>
			<div className="min-w-0 overflow-x-auto rounded-sm border">
				<table className="w-full text-xs">
					<thead className="text-muted-foreground text-left">
						<tr>
							<th className="px-2 py-1 font-normal">{t("mock.method")}</th>
							<th className="px-2 py-1 font-normal">{t("mock.path")}</th>
							<th className="px-2 py-1 font-normal">{t("mock.tool")}</th>
							<th className="px-2 py-1 font-normal">{t("mock.kind")}</th>
						</tr>
					</thead>
					<tbody>
						{result.operations.map((op) => (
							<tr key={op.name} className="border-t">
								<td className="px-2 py-1 font-mono">{op.method}</td>
								<td className="px-2 py-1 font-mono break-all">{op.path}</td>
								<td className="px-2 py-1 font-mono">{op.name}</td>
								<td className="px-2 py-1">{t(`mock.kinds.${op.kind}`)}</td>
							</tr>
						))}
					</tbody>
				</table>
			</div>
			{result.warnings.length > 0 && (
				<div className="flex flex-col gap-0.5 text-xs">
					<p className="text-muted-foreground">{t("mock.warnings")}</p>
					<ul className="list-disc pl-5">
						{result.warnings.map((w, i) => (
							<li key={i} className="break-words">
								{w}
							</li>
						))}
					</ul>
				</div>
			)}
			<p className="text-muted-foreground text-xs">{t("mock.serviceYaml")}</p>
			<pre className="bg-muted overflow-x-auto rounded-sm px-2 py-1.5 text-xs" data-testid="suite-build-mock-yaml">
				{result.service_yaml}
			</pre>
			<p className="text-muted-foreground text-xs">{t("mock.seed")}</p>
			<pre className="bg-muted max-h-40 overflow-auto rounded-sm px-2 py-1.5 text-xs">{result.seed}</pre>
			{!result.dry_run && <p className="text-muted-foreground text-xs">{result.next}</p>}
		</div>
	);
}
