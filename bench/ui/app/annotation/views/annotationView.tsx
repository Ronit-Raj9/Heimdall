import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { StatusPill } from "@/components/ui/statusPill";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useCreateAnnotationQueueMutation, useGetAnnotationPeopleQuery, useGetAnnotationQueuesQuery } from "@/lib/store/apis/annotationApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { WhyDisabled } from "@/components/ui/whyDisabled";
import i18n from "@/lib/i18n";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import type { AnnotationAssignment, AnnotationKind, AnnotationQueue, AnnotationQueueRequest } from "@/lib/types/annotation";
import { Link } from "@tanstack/react-router";
import { Plus, Tags } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		annotation: typeof import("@/locales/en/annotation.json");
	}
}

export const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";

export function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
	return (
		<label className="flex min-w-0 flex-col gap-1 text-sm">
			<span className="text-muted-foreground">{label}</span>
			{children}
			{hint && <span className="text-muted-foreground text-xs">{hint}</span>}
		</label>
	);
}

/** Labellers typed as "a, b c" become a clean list of user ids. */
export function parseLabellers(text: string): string[] {
	return Array.from(
		new Set(
			text
				.split(/[\s,]+/)
				.map((s) => s.trim())
				.filter(Boolean),
		),
	);
}

/** True when people per item is not a whole number from 1 to 5 (N167): the form says so and disables saving; the value
 * is never clamped. */
export function perItemProblem(n: number): boolean {
	return !Number.isInteger(n) || n < 1 || n > 5;
}

/** Why a fixed queue cannot be created yet: fewer people named under Only these people than people per item (wave 4,
 * p12-7). Null when nothing is wrong. */
export function fixedProblem(mode: AnnotationAssignment, named: number, perItem: number): string | null {
	if (mode !== "fixed" || named >= perItem) return null;
	return i18n.t("labelling.fixedProblem", { ns: "w4m7", count: perItem });
}

/**
 * Who may label a queue (N168): every member, or only the people ticked in the list of members by name (user id in the
 * tooltip). An id on the queue that is not a member shows as an unknown person with Remove. A local server has no
 * members list, so it keeps the free-text box of user ids. `value` is the queue's labeller ids; empty means every member.
 */
export function LabellerPicker({
	value,
	onChange,
	disabled,
	testId,
}: {
	value: string[];
	onChange: (ids: string[]) => void;
	disabled?: boolean;
	testId: string;
}) {
	const { t } = useTranslation("annotation");
	const people = useGetAnnotationPeopleQuery();
	const [only, setOnly] = useState(value.length > 0);
	const [text, setText] = useState(value.join(", "));
	if (people.data?.local) {
		return (
			<Field label={t("create.labellersIds")} hint={`${t("create.labellersLocal")} ${t("create.labellersHint")}`}>
				<Input
					value={text}
					onChange={(e) => {
						setText(e.target.value);
						onChange(parseLabellers(e.target.value));
					}}
					disabled={disabled}
					data-testid={`${testId}-text`}
				/>
			</Field>
		);
	}
	const list = people.data?.people ?? [];
	const known = new Set(list.map((p) => p.user_id));
	const unknown = value.filter((id) => !known.has(id));
	const toggle = (id: string, on: boolean) => onChange(on ? [...value.filter((v) => v !== id), id] : value.filter((v) => v !== id));
	return (
		<fieldset className="flex min-w-0 flex-col gap-2 text-sm" disabled={disabled} data-testid={testId}>
			<legend className="text-muted-foreground mb-1">{t("create.labellers")}</legend>
			<div className="flex flex-wrap gap-4">
				<label className="flex items-center gap-2">
					<input
						type="radio"
						name={`${testId}-mode`}
						checked={!only}
						onChange={() => {
							setOnly(false);
							onChange([]);
						}}
						data-testid={`${testId}-every`}
					/>
					{t("create.labellersEvery")}
				</label>
				<label className="flex items-center gap-2">
					<input type="radio" name={`${testId}-mode`} checked={only} onChange={() => setOnly(true)} data-testid={`${testId}-only`} />
					{t("create.labellersOnly")}
				</label>
			</div>
			{only && (
				<div className="flex flex-col gap-1">
					{people.isLoading ? (
						<BlockSkeleton rows={2} testId={`${testId}-loading`} />
					) : people.error ? (
						<span role="alert" className="text-chart-error-ink text-xs">
							{t("create.labellersLoadError")}
						</span>
					) : (
						<>
							<span className="text-muted-foreground text-xs">{t("create.labellersPick")}</span>
							<ul className="flex max-h-48 flex-col gap-1 overflow-auto">
								{list.map((p) => (
									<li key={p.user_id}>
										<label className="flex items-center gap-2" title={p.user_id}>
											<input
												type="checkbox"
												checked={value.includes(p.user_id)}
												onChange={(e) => toggle(p.user_id, e.target.checked)}
												data-testid={`${testId}-person-${p.user_id}`}
											/>
											{p.name}
										</label>
									</li>
								))}
							</ul>
						</>
					)}
					{unknown.map((id) => (
						<div key={id} className="flex items-center gap-2 text-xs">
							<span className="text-chart-warning-ink">{t("create.labellersUnknown", { id })}</span>
							<Button type="button" size="sm" variant="ghost" onClick={() => toggle(id, false)} data-testid={`${testId}-remove-${id}`}>
								{t("create.labellersRemove")}
							</Button>
						</div>
					))}
				</div>
			)}
		</fieldset>
	);
}

/** Labelling: /annotation. Every queue of the organization with its progress, and a form for a new one. */
export default function AnnotationView() {
	const { t } = useTranslation("annotation");
	const session = useSession();
	const writable = canUse(session, "member");
	const queues = useGetAnnotationQueuesQuery();
	const [creating, setCreating] = useState(false);
	const list = queues.data?.queues ?? [];
	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="annotation-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				icon={Tags}
				actions={
					writable && !creating ? (
						<Button onClick={() => setCreating(true)} data-testid="annotation-new">
							<Plus className="size-4" aria-hidden="true" />
							{t("create.open")}
						</Button>
					) : undefined
				}
			/>
			{!writable && (
				<p role="note" className="text-muted-foreground rounded-sm border px-4 py-2 text-sm" data-testid="annotation-viewer">
					{t("viewerNote")}
				</p>
			)}
			{creating && <CreateQueueForm onDone={() => setCreating(false)} />}
			{queues.isLoading ? (
				<BlockSkeleton rows={4} testId="annotation-loading" />
			) : queues.error ? (
				<LoadError title={t("loadError")} error={queues.error} onRetry={() => void queues.refetch()} testId="annotation-error" />
			) : list.length === 0 ? (
				<EmptyNote testId="annotation-empty">{t("empty")}</EmptyNote>
			) : (
				<QueueTable queues={list} />
			)}
		</section>
	);
}

export function QueueTable({ queues }: { queues: AnnotationQueue[] }) {
	const { t } = useTranslation("annotation");
	return (
		<div className="bg-card min-w-0 overflow-x-auto rounded-xl border" data-testid="annotation-queues">
			<table className="w-full text-sm">
				<thead className="text-muted-foreground border-b text-left text-xs">
					<tr>
						<th className="px-4 py-2 font-medium">{t("table.name")}</th>
						<th className="px-4 py-2 font-medium">{t("table.benchmark")}</th>
						<th className="px-4 py-2 font-medium">{t("table.settings")}</th>
						<th className="px-4 py-2 text-right font-medium">{t("table.progress")}</th>
						<th className="px-4 py-2 text-right font-medium">{t("table.disputed")}</th>
						<th className="px-4 py-2 text-right font-medium">{t("table.mine")}</th>
					</tr>
				</thead>
				<tbody>
					{queues.map((q) => (
						<tr key={q.id} className="border-b last:border-0" data-testid={`annotation-queue-${q.id}`}>
							<td className="px-4 py-2.5">
								<Link to="/annotation/$queueId" params={{ queueId: q.id }} className="text-foreground font-medium hover:underline">
									{q.name}
								</Link>
								{q.archived && <span className="text-muted-foreground ml-2 text-xs">{t("table.archived")}</span>}
							</td>
							<td className="text-muted-foreground px-4 py-2.5 font-mono text-xs">{q.benchmark || "-"}</td>
							<td className="text-muted-foreground px-4 py-2.5 text-xs">
								{t("table.perItem", { count: q.reviewers_per_item })}
								{q.blind ? `, ${t("table.blind")}` : ""}
							</td>
							<td className="px-4 py-2.5 text-right tabular-nums">
								{q.counts.done} / {q.counts.total}
							</td>
							<td className="px-4 py-2.5 text-right tabular-nums">
								{q.counts.disputed > 0 ? <StatusPill tone="warn">{q.counts.disputed}</StatusPill> : 0}
							</td>
							<td className="px-4 py-2.5 text-right tabular-nums">{q.can_label ? q.mine : "-"}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

/** The body POST /annotation/queues gets from the form; empty optional fields are left out. */
export function queueRequest(form: {
	name: string;
	benchmark: string;
	kind: AnnotationKind;
	reviewers: number;
	blind: boolean;
	assignment: AnnotationAssignment;
	labellers: string[];
	instructions: string;
}): AnnotationQueueRequest {
	const body: AnnotationQueueRequest = {
		name: form.name.trim(),
		kind: form.kind,
		reviewers_per_item: form.reviewers,
		blind: form.blind,
		assignment: form.assignment,
	};
	if (form.benchmark) body.benchmark = form.benchmark;
	const labellers = Array.from(new Set(form.labellers));
	if (labellers.length) body.reviewers = labellers;
	if (form.instructions.trim()) body.instructions = form.instructions.trim();
	return body;
}

function CreateQueueForm({ onDone }: { onDone: () => void }) {
	const { t } = useTranslation("annotation");
	const suites = useGetSuitesLightQuery();
	const [create, created] = useCreateAnnotationQueueMutation();
	const [form, setForm] = useState({
		name: "",
		benchmark: "",
		kind: "runs" as AnnotationKind,
		reviewers: 2,
		blind: true,
		assignment: "open" as AnnotationAssignment,
		labellers: [] as string[],
		instructions: "",
	});
	const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) => setForm((f) => ({ ...f, [k]: v }));
	const benchmarks = (suites.data?.suites ?? []).map((s) => s.id);
	const fixedWhy = fixedProblem(form.assignment, new Set(form.labellers).size, form.reviewers);
	return (
		<Section title={t("create.title")} description={t("create.description")} testId="annotation-create">
			<form
				className="bg-card grid min-w-0 grid-cols-1 gap-4 rounded-xl border p-5 md:grid-cols-2"
				onSubmit={(e) => {
					e.preventDefault();
					if (!form.name.trim() || perItemProblem(form.reviewers) || fixedWhy) return;
					void create(queueRequest(form))
						.unwrap()
						.then(onDone)
						.catch(() => {});
				}}
			>
				<Field label={t("create.name")}>
					<Input
						value={form.name}
						onChange={(e) => set("name", e.target.value)}
						placeholder={t("create.namePlaceholder")}
						required
						data-testid="annotation-create-name"
					/>
				</Field>
				<Field label={t("create.benchmark")} hint={t("create.benchmarkHint")}>
					<select
						className={selectClass}
						value={form.benchmark}
						onChange={(e) => set("benchmark", e.target.value)}
						data-testid="annotation-create-benchmark"
					>
						<option value="">{t("create.benchmarkNone")}</option>
						{benchmarks.map((id) => (
							<option key={id} value={id}>
								{id}
							</option>
						))}
					</select>
				</Field>
				<Field label={t("create.kind")}>
					<select
						className={selectClass}
						value={form.kind}
						onChange={(e) => set("kind", e.target.value as AnnotationKind)}
						data-testid="annotation-create-kind"
					>
						<option value="runs">{t("create.kindRuns")}</option>
						<option value="judge">{t("create.kindJudge")}</option>
					</select>
				</Field>
				<Field label={t("create.reviewers")} hint={perItemProblem(form.reviewers) ? t("create.reviewersRange") : t("create.reviewersHint")}>
					<Input
						type="number"
						min={1}
						max={5}
						value={Number.isNaN(form.reviewers) ? "" : form.reviewers}
						onChange={(e) => set("reviewers", e.target.value === "" ? Number.NaN : Number(e.target.value))}
						aria-invalid={perItemProblem(form.reviewers) || undefined}
						data-testid="annotation-create-reviewers"
					/>
				</Field>
				<Field label={t("create.assignment")}>
					<select
						className={selectClass}
						value={form.assignment}
						onChange={(e) => set("assignment", e.target.value as AnnotationAssignment)}
						data-testid="annotation-create-assignment"
					>
						<option value="open">{t("create.assignmentOpen")}</option>
						<option value="fixed">{t("create.assignmentFixed")}</option>
					</select>
				</Field>
				<LabellerPicker value={form.labellers} onChange={(ids) => set("labellers", ids)} testId="annotation-create-labellers" />
				<label className="flex items-center gap-2 text-sm md:col-span-2">
					<input
						type="checkbox"
						checked={form.blind}
						onChange={(e) => set("blind", e.target.checked)}
						data-testid="annotation-create-blind"
					/>
					{t("create.blind")}
				</label>
				<Field label={t("create.instructions")}>
					<textarea
						className="border-input bg-background min-h-20 rounded-md border px-3 py-2 text-sm"
						value={form.instructions}
						onChange={(e) => set("instructions", e.target.value)}
						data-testid="annotation-create-instructions"
					/>
				</Field>
				<div className="flex items-end justify-end gap-2 md:col-span-2">
					{created.error ? (
						<p role="alert" className="text-chart-error-ink mr-auto text-xs" data-testid="annotation-create-error">
							{getBenchErrorMessage(created.error)}
						</p>
					) : null}
					<Button type="button" variant="ghost" onClick={onDone}>
						{t("create.cancel")}
					</Button>
					<Button
						type="submit"
						disabled={created.isLoading || !form.name.trim() || perItemProblem(form.reviewers) || fixedWhy !== null}
						aria-describedby={fixedWhy ? "annotation-create-why" : undefined}
						data-testid="annotation-create-submit"
					>
						{t("create.submit")}
					</Button>
				</div>
				<WhyDisabled reason={created.isLoading ? null : fixedWhy} testId="annotation-create-why" className="text-right md:col-span-2" />
			</form>
		</Section>
	);
}