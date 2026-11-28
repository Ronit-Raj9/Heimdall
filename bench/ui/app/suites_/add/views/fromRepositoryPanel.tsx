import { JobRunButton } from "@/components/jobs/jobRunButton";
import { useGetSuitesQuery } from "@/lib/store/apis/benchApi";
import type { Job, JsonValue } from "@/lib/types/bench";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "./uft4I18n";

export const REPO_TASK_KIND = "repotask.build";
export const REPO_CANDIDATES_KIND = "repotask.candidates";
export const REPO_TEMPLATES = ["python-pytest", "node-test", "go-test"] as const;

export interface RepoTaskForm {
	repo_url: string;
	base: string;
	reference: string;
	suite: string;
	template: string;
}

/** The repotask.build params of the form, or null while a field is missing. Pure. */
export function repoTaskBody(f: RepoTaskForm): Record<string, string> | null {
	const body: Record<string, string> = {
		repo_url: f.repo_url.trim(),
		base: f.base.trim(),
		reference: f.reference.trim(),
		suite: f.suite.trim(),
	};
	if (!body.repo_url || !body.base || !body.reference || !body.suite) return null;
	if (f.template) body.template = f.template;
	return body;
}

interface Control {
	name: string;
	want: string;
	got: string;
	passed: boolean;
}

export interface RepoCandidate {
	base: string;
	reference: string;
	subject: string;
	lines: number;
	files: number;
}

/** The candidates of a repotask.candidates result. Pure. */
export function repoCandidates(result: Record<string, JsonValue> | undefined): RepoCandidate[] {
	const list = result?.candidates;
	return Array.isArray(list) ? (list as unknown as RepoCandidate[]) : [];
}

/** The job's result: admitted or the failing check, and every control. */
export function RepoTaskResult({ result }: { result: Record<string, JsonValue> }) {
	const { t } = useTranslation("uft4");
	const controls = (Array.isArray(result.controls) ? result.controls : []) as unknown as Control[];
	const suite = String(result.suite ?? "");
	const task = String(result.task ?? "");
	return (
		<div className="flex flex-col gap-2" data-testid="repotask-result">
			{result.admitted === true ? (
				<p className="text-sm" data-testid="repotask-admitted">
					{t("admitted", { task, suite })}
				</p>
			) : (
				<p className="text-chart-error-ink text-sm" role="alert" data-testid="repotask-refused">
					{t("refused", { reason: String(result.reason ?? "") })}
				</p>
			)}
			{controls.length > 0 && (
				<table className="text-xs" data-testid="repotask-controls">
					<thead>
						<tr className="text-muted-foreground text-left">
							<th className="pr-4 font-normal">{t("control")}</th>
							<th className="pr-4 font-normal">{t("want")}</th>
							<th className="font-normal">{t("got")}</th>
						</tr>
					</thead>
					<tbody>
						{controls.map((c) => (
							<tr key={c.name} data-testid={`repotask-control-${c.name}`} className={c.passed ? "" : "text-chart-error-ink"}>
								<td className="pr-4 font-mono">{c.name}</td>
								<td className="pr-4">{c.want}</td>
								<td>{c.got}</td>
							</tr>
						))}
					</tbody>
				</table>
			)}
		</div>
	);
}

/** Add benchmark, "From a repository": one commit pair of a public repository becomes a task, admission decides. */
export function FromRepositoryPanel() {
	const { t } = useTranslation("uft4");
	const { data } = useGetSuitesQuery();
	const suites = (data?.suites ?? []).filter((s) => s.source !== "library");
	const [form, setForm] = useState<RepoTaskForm>({ repo_url: "", base: "", reference: "", suite: "", template: "" });
	const [done, setDone] = useState<Job | null>(null);
	const [found, setFound] = useState<RepoCandidate[] | null>(null);
	const set = (k: keyof RepoTaskForm) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
	const body = repoTaskBody(form);
	const input = "border-input bg-background rounded-md border px-2 py-1 text-sm";
	return (
		<section className="flex flex-col gap-3 rounded-md border p-4" data-testid="repotask-panel">
			<h2 className="text-sm font-medium">{t("title")}</h2>
			<p className="text-muted-foreground text-xs">{t("help")}</p>
			<label className="flex flex-col gap-1 text-xs">
				{t("url")}
				<input
					className={input}
					value={form.repo_url}
					placeholder={t("urlPlaceholder")}
					onChange={set("repo_url")}
					data-testid="repotask-url"
				/>
			</label>
			<div className="grid gap-3 sm:grid-cols-2">
				<label className="flex flex-col gap-1 text-xs">
					{t("base")}
					<input className={`${input} font-mono`} value={form.base} onChange={set("base")} data-testid="repotask-base" />
				</label>
				<label className="flex flex-col gap-1 text-xs">
					{t("reference")}
					<input className={`${input} font-mono`} value={form.reference} onChange={set("reference")} data-testid="repotask-reference" />
				</label>
				<label className="flex flex-col gap-1 text-xs">
					{t("benchmark")}
					<select className={input} value={form.suite} onChange={set("suite")} data-testid="repotask-suite">
						<option value="">{t("pickBenchmark")}</option>
						{suites.map((s) => (
							<option key={s.id} value={s.id}>
								{s.id}
							</option>
						))}
					</select>
				</label>
				<label className="flex flex-col gap-1 text-xs">
					{t("template")}
					<select className={input} value={form.template} onChange={set("template")} data-testid="repotask-template">
						<option value="">{t("anyTemplate")}</option>
						{REPO_TEMPLATES.map((id) => (
							<option key={id} value={id}>
								{id}
							</option>
						))}
					</select>
				</label>
			</div>
			{form.repo_url.trim() && form.suite && (
				<JobRunButton
					kind={REPO_CANDIDATES_KIND}
					params={{ repo_url: form.repo_url.trim(), suite: form.suite }}
					label={t("find")}
					variant="outline"
					onDone={(j) => setFound(repoCandidates(j.result))}
				/>
			)}
			{found && found.length === 0 && <p className="text-muted-foreground text-xs">{t("noCandidates")}</p>}
			{found && found.length > 0 && (
				<ul className="flex flex-col gap-1 text-xs" data-testid="repotask-candidates">
					{found.map((c) => (
						<li key={c.reference}>
							<button
								type="button"
								className="hover:bg-muted w-full rounded px-2 py-1 text-left"
								onClick={() => setForm({ ...form, base: c.base, reference: c.reference })}
								data-testid={`repotask-candidate-${c.reference.slice(0, 10)}`}
							>
								<span className="font-mono">{c.reference.slice(0, 10)}</span> {c.subject}{" "}
								<span className="text-muted-foreground">{t("size", { lines: c.lines, files: c.files })}</span>
							</button>
						</li>
					))}
				</ul>
			)}
			{suites.length === 0 && <p className="text-muted-foreground text-xs">{t("noBenchmarks")}</p>}
			<p className="text-xs" data-testid="repotask-free">
				{t("free")}
			</p>
			{body ? (
				<JobRunButton
					kind={REPO_TASK_KIND}
					params={body}
					label={t("run")}
					confirm={t("confirm", { suite: body.suite })}
					onDone={(j) => setDone(j)}
				/>
			) : (
				<p className="text-muted-foreground text-xs" data-testid="repotask-missing">
					{t("missing")}
				</p>
			)}
			{done?.result && <RepoTaskResult result={done.result} />}
			{done && !done.result && done.error && (
				<p className="text-chart-error-ink text-xs" role="alert" data-testid="repotask-error">
					{done.error}
				</p>
			)}
		</section>
	);
}

export default FromRepositoryPanel;