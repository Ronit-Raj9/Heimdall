import { JudgeFields, RowsImportForm, type T as BuildT } from "@/app/suites_/$suiteId/views/rowMapper";
import { JobRunButton } from "@/components/jobs/jobRunButton";
import { ReadinessResults, readinessReport } from "@/components/suites/admissionResults";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useSession } from "@/lib/session/sessionContext";
import { benchApi } from "@/lib/store/apis/benchApi";
import { authoringError, useCreateSuiteMutation } from "@/lib/store/apis/suiteAuthoringApi";
import { useSetSuiteGraderMutation } from "@/lib/store/apis/suiteBuildApi";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { Job } from "@/lib/types/bench";
import { SUITE_OPS_KINDS } from "@/lib/types/suiteops";
import { cn } from "@/lib/utils";
import { type ReactNode, useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { fw05 } from "./fw05I18n";
import type { CatalogProvider } from "@/components/models/groupedModelPicker";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import "@/app/evaluations_/$evalId/views/w4m2I18n";

/**
 * True when the model catalog has loaded and no provider in it lists the typed model. A free-text model
 * stays allowed (the card only warns): false while the catalog is not loaded or the model is empty.
 */
export function unknownModel(catalog: readonly CatalogProvider[] | undefined, provider: string, model: string): boolean {
	const id = model.trim();
	if (!catalog || id === "") return false;
	void provider;
	return !catalog.some((p) => (p.models ?? []).some((m) => m.id === id));
}

// Lane Y13: the "Your own tasks" card of wizard step 3. Creates a private question-and-answer
// benchmark, imports the customer's rows through the Build tab's import form, runs the readiness
// checks and selects the benchmark for this evaluation.

/** Benchmark id from a name: lower case letters, digits and single hyphens, at most 64 characters. */
export function benchmarkSlug(name: string): string {
	return name
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, "-")
		.replace(/^-+|-+$/g, "")
		.slice(0, 64)
		.replace(/-+$/g, "");
}

/**
 * Why the name cannot become a new benchmark, checked before Create; "" when it can. `suites` is the
 * benchmark list (built-in and the organization's); while it loads pass [] and the server decides.
 */
export function nameProblem(slug: string, name: string, suites: readonly { id: string; builtin?: boolean }[]): string {
	if (!slug) return name.trim() ? fw05("own.noLetters") : "";
	const same = suites.find((s) => s.id === slug);
	if (!same) return "";
	if (same.builtin) return fw05("own.builtinTaken", { id: slug });
	const ids = new Set(suites.map((s) => s.id));
	let n = 2;
	while (ids.has(`${slug}-${n}`)) n++;
	return fw05("own.orgTaken", { id: slug, suggest: `${slug}-${n}` });
}

/**
 * Asks for the light benchmark list and hands the name's problem to children. It is mounted only
 * once a name is typed, so a closed or empty card sends no request. While the list loads the
 * existing-id check is skipped and the server's checks decide.
 */
function WithNameProblem({ slug, name, children }: { slug: string; name: string; children: (problem: string) => ReactNode }) {
	const list = useGetSuitesLightQuery();
	return <>{children(nameProblem(slug, name, list.data?.suites ?? []))}</>;
}

/** Whether a create error means the id is already used. */
export function isTaken(error: unknown): boolean {
	const e = error as { status?: unknown; data?: { error?: { message?: string } } } | undefined;
	return e?.status === 409 || /already|exists|taken/i.test(e?.data?.error?.message ?? "");
}

type Grader = "exact" | "judge";
type Stage = "setup" | "rows";

export interface OwnTasksCardProps {
	/** Adds the benchmark to the evaluation's selection. */
	onAdd: (id: string) => void;
	/** Start open (tests); the card opens on click otherwise. */
	initialOpen?: boolean;
	/** A benchmark already created by this card: the card resumes at the import step. */
	initialSuite?: { id: string; grader: "exact" | "judge" };
	/** The Name box's starting text (tests). */
	initialName?: string;
}

export function OwnTasksCard({ onAdd, initialOpen = false, initialSuite, initialName = "" }: OwnTasksCardProps) {
	const { t } = useTranslation("evaluate");
	const { t: tb } = useTranslation("suiteBuild");
	const session = useSession();
	const dispatch = useAppDispatch();
	const [open, setOpen] = useState(initialOpen || Boolean(initialSuite));
	const [name, setName] = useState(initialName);
	const [grader, setGrader] = useState<Grader>(initialSuite?.grader ?? "exact");
	const [judge, setJudge] = useState({ provider: "", model: "", votes: 1 });
	const [stage, setStage] = useState<Stage>(initialSuite ? "rows" : "setup");
	const [suiteId, setSuiteId] = useState(initialSuite?.id ?? "");
	const [format, setFormat] = useState<"rows" | "tickets">("rows");
	const [imported, setImported] = useState(0);
	const [job, setJob] = useState<Job | null>(null);
	const [createSuite, created] = useCreateSuiteMutation();
	const [setSuiteGrader, graded] = useSetSuiteGraderMutation();
	const slug = benchmarkSlug(name);
	// The name is checked against built-in and organization benchmarks before Create (the server checks again).
	const checkName = (show: (problem: string) => ReactNode) =>
		name.trim() !== "" && !suiteId ? (
			<WithNameProblem slug={slug} name={name}>
				{show}
			</WithNameProblem>
		) : (
			show("")
		);
	const judgeReady = grader === "exact" || (judge.provider.trim() !== "" && judge.model.trim() !== "");
	const report = useMemo(() => (job ? readinessReport(job.result) : null), [job]);
	const ready = report ? report.tasks.filter((x) => x.ready).length : 0;

	const create = async () => {
		// A benchmark created earlier in this card (its grader failed to save) is not created twice.
		let id = suiteId;
		if (!id) {
			const res = await createSuite({ id: slug, template: "qa", blank: true });
			if (!("data" in res) || !res.data) return;
			id = res.data.id || slug;
		}
		if (grader === "judge") {
			const g = await setSuiteGrader({
				suite: id,
				grader: "judge",
				judge: { provider: judge.provider.trim(), model: judge.model.trim(), votes: judge.votes },
			});
			if (!("data" in g)) {
				setSuiteId(id);
				return;
			}
		}
		setSuiteId(id);
		setStage("rows");
	};
	const onImported = useCallback((n: number) => {
		setImported((x) => x + n);
		setJob(null);
	}, []);
	const onDone = useCallback(
		(j: Job) => {
			setJob(j);
			dispatch(
				benchApi.util.invalidateTags([
					{ type: "BenchSuites", id: "LIST" },
					{ type: "BenchSuites", id: `${suiteId}/health` },
				]),
			);
			const r = readinessReport(j.result);
			if (r && r.tasks.some((x) => x.ready)) onAdd(suiteId);
		},
		[dispatch, suiteId, onAdd],
	);

	const createError = created.error
		? isTaken(created.error)
			? t("benchmarks.own.taken", { id: slug, suggest: `${slug}-2` })
			: authoringError(created.error, t("benchmarks.own.createError"))
		: "";

	return (
		<section className="rounded-md border px-3 py-3" data-testid="step3-own">
			<div className="flex flex-wrap items-start justify-between gap-2">
				<div className="min-w-0">
					<h3 className="text-sm font-medium">{t("benchmarks.own.title")}</h3>
					<p className="text-muted-foreground text-xs">{t("benchmarks.own.help")}</p>
				</div>
				{!open && (
					<Button size="sm" variant="outline" onClick={() => setOpen(true)} dataTestId="step3-own-open">
						{t("benchmarks.own.open")}
					</Button>
				)}
			</div>
			{open && stage === "setup" && (
				<div className="mt-3 flex min-w-0 flex-col gap-3" data-testid="step3-own-setup">
					<label className="flex min-w-0 flex-col gap-1 text-sm">
						<span className="text-muted-foreground">{t("benchmarks.own.name")}</span>
						<Input
							value={name}
							disabled={Boolean(suiteId)}
							placeholder={t("benchmarks.own.namePlaceholder")}
							onChange={(e) => setName(e.target.value)}
							data-testid="step3-own-name"
						/>
						{slug && (
							<span className="text-muted-foreground font-mono text-xs" data-testid="step3-own-slug">
								{slug === name.trim().toLowerCase() ? t("benchmarks.own.id", { id: slug }) : fw05("own.madeFrom", { id: slug })}
							</span>
						)}
						{checkName((problem) =>
							problem ? (
								<span className="text-chart-error-ink text-xs" role="alert" data-testid="step3-own-name-problem">
									{problem}
								</span>
							) : null,
						)}
					</label>
					<fieldset className="flex min-w-0 flex-col gap-2">
						<legend className="text-muted-foreground mb-1 text-sm">{t("benchmarks.own.grader")}</legend>
						<div className="grid grid-cols-1 gap-2 sm:grid-cols-2" role="radiogroup">
							{(["exact", "judge"] as const).map((g) => (
								<label
									key={g}
									className={cn(
										"flex cursor-pointer flex-col gap-0.5 rounded-md border px-3 py-2 text-sm",
										grader === g && "border-primary bg-primary/5",
									)}
									data-testid={`step3-own-grader-${g}`}
								>
									<span className="flex items-center gap-2 font-medium">
										<input type="radio" name="own-grader" checked={grader === g} onChange={() => setGrader(g)} />
										{t(`benchmarks.own.${g}`)}
									</span>
									<span className="text-muted-foreground text-xs">{t(`benchmarks.own.${g}Hint`)}</span>
								</label>
							))}
						</div>
						{grader === "judge" && <JudgeFields {...judge} onChange={(p) => setJudge((j) => ({ ...j, ...p }))} t={tb as BuildT} />}
						{grader === "judge" && judge.model.trim() !== "" && <JudgeUnknownWarning provider={judge.provider} model={judge.model} />}
					</fieldset>
					<div>
						{checkName((problem) => (
							<Button
								disabled={!slug || problem !== "" || !judgeReady || created.isLoading || graded.isLoading}
								isLoading={created.isLoading || graded.isLoading}
								onClick={() => void create()}
								dataTestId="step3-own-create"
							>
								{t("benchmarks.own.create")}
							</Button>
						))}
					</div>
					{createError && (
						<p role="alert" className="text-chart-error-ink text-xs" data-testid="step3-own-error">
							{createError}
						</p>
					)}
					{graded.error ? (
						<p role="alert" className="text-chart-error-ink text-xs" data-testid="step3-own-grader-error">
							{authoringError(graded.error, t("benchmarks.own.createError"))}
						</p>
					) : null}
				</div>
			)}
			{open && stage === "rows" && (
				<div className="mt-3 flex min-w-0 flex-col gap-4" data-testid="step3-own-rows">
					<p role="status" className="text-sm">
						{t("benchmarks.own.created", { id: suiteId })}
					</p>
					<div className="flex flex-wrap items-center gap-2" role="tablist" aria-label={t("benchmarks.own.format")}>
						{(["rows", "tickets"] as const).map((f) => (
							<Button
								key={f}
								role="tab"
								aria-selected={format === f}
								size="sm"
								variant={format === f ? "default" : "outline"}
								onClick={() => setFormat(f)}
								dataTestId={`step3-own-format-${f}`}
							>
								{t(`benchmarks.own.${f}`)}
							</Button>
						))}
					</div>
					<RowsImportForm
						key={format}
						suiteId={suiteId}
						exact={grader === "exact"}
						format={format}
						onImported={onImported}
						t={tb as BuildT}
					/>
					<div className="flex min-w-0 flex-col gap-2 border-t pt-3" data-testid="step3-own-readiness">
						<h4 className="text-sm font-medium">{t("benchmarks.own.readinessTitle")}</h4>
						<p className="text-muted-foreground text-xs">{t("benchmarks.own.readinessHelp")}</p>
						<p className="text-muted-foreground text-xs">{t("benchmarks.own.launchWhen")}</p>
						{imported > 0 && (
							<JobRunButton
								key={imported}
								kind={SUITE_OPS_KINDS.admit}
								params={{ suite: suiteId }}
								label={t("benchmarks.own.readinessRun")}
								confirm={
									session.authMode === "hosted"
										? tb("readiness.confirmHosted", { count: imported })
										: tb("readiness.confirm", { count: imported })
								}
								onDone={onDone}
							/>
						)}
						{job && !report && job.error && (
							<p role="alert" className="text-chart-error-ink text-sm">
								{job.error}
							</p>
						)}
						{report && <ReadinessResults report={report} />}
						{report && (
							<p role="status" className="text-sm" data-testid="step3-own-result">
								{ready > 0 ? t("benchmarks.own.added", { id: suiteId, count: ready }) : t("benchmarks.own.noneReady")}
							</p>
						)}
					</div>
				</div>
			)}
		</section>
	);
}

/** The warning under the judge picker when the model catalog does not list the typed model (it never blocks Create). */
function JudgeUnknownWarning({ provider, model }: { provider: string; model: string }) {
	const { t } = useTranslation("w4m2");
	const models = useGetModelsQuery();
	if (!unknownModel(models.data?.providers as unknown as CatalogProvider[] | undefined, provider, model)) return null;
	return (
		<p className="text-chart-warning-ink text-xs" role="status" data-testid="own-tasks-judge-unknown">
			{t("judgeUnknown")}
		</p>
	);
}