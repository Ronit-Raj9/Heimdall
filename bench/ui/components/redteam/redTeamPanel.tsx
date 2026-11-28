import { JobRunButton } from "@/components/jobs/jobRunButton";
import { HarvestForm } from "./harvestForm";
import { ModelPicker, type ModelRef } from "@/components/models/modelPicker";
import { Button } from "@/components/ui/button";
import {
	type RedTeamCatalog,
	type RedTeamOption,
	type RedTeamPlan,
	type RedTeamReviewItem,
	useCreateRedTeamBenchmarkMutation,
	useGetRedTeamCatalogQuery,
	useGetRedTeamRegressionsQuery,
	useReviewRedTeamRegressionMutation,
} from "@/lib/store/apis/redteamApi";
import { SUITE_OPS_KINDS } from "@/lib/types/suiteops";
import { cn } from "@/lib/utils";
import { ShieldAlert } from "lucide-react";
import { useState } from "react";
import "@/components/platform/ufintI18n";
import i18n from "@/lib/i18n";

// Z04: "Red-team my agent" in the evaluate wizard (step 3). Writes the org's red-team benchmark
// (adaptive attacks run on the org's own model through Bifrost), starts its readiness check, adds
// it to the evaluation, and lists the regressions successful attacks became for review.

/** Plan from the choices; null with the reason when it cannot be sent yet. */
export function planFrom(
	choice: { categories: string[]; strategies: string[]; channels: string[]; model: ModelRef | null; attempts: number },
	catalog: RedTeamCatalog,
): { plan: RedTeamPlan | null; problem: string } {
	if (!choice.model?.provider || !choice.model.model) return { plan: null, problem: "Pick the model that writes the attacks." };
	if (choice.categories.length === 0 || choice.strategies.length === 0 || choice.channels.length === 0)
		return { plan: null, problem: "Pick at least one attack, one strategy and one channel." };
	const fits = catalog.categories.some(
		(c) => choice.categories.includes(c.id) && (c.channels ?? []).some((ch) => choice.channels.includes(ch)),
	);
	if (!fits) return { plan: null, problem: "None of the attacks you picked is offered on the channels you picked." };
	return {
		plan: {
			suite: catalog.suite,
			categories: choice.categories,
			strategies: choice.strategies,
			channels: choice.channels,
			max_attempts: choice.attempts,
			attacker_provider: choice.model.provider,
			attacker_model: choice.model.model,
		},
		problem: "",
	};
}

/** Number of tasks a plan writes: every attack crossed with its offered channels, times strategies. */
export function taskCount(catalog: RedTeamCatalog, categories: string[], strategies: string[], channels: string[]): number {
	let n = 0;
	for (const c of catalog.categories) {
		if (!categories.includes(c.id)) continue;
		n += (c.channels ?? []).filter((ch) => channels.includes(ch)).length;
	}
	return n * strategies.length;
}

function toggle(xs: string[], id: string): string[] {
	return xs.includes(id) ? xs.filter((x) => x !== id) : [...xs, id];
}

function Choices({
	title,
	options,
	value,
	onChange,
	testId,
}: {
	title: string;
	options: RedTeamOption[];
	value: string[];
	onChange(v: string[]): void;
	testId: string;
}) {
	return (
		<fieldset className="flex flex-col gap-1.5" data-testid={testId}>
			<legend className="mb-1 text-sm font-medium">{title}</legend>
			{options.map((o) => (
				<label key={o.id} className="flex items-start gap-2 text-sm">
					<input
						type="checkbox"
						className="mt-1"
						checked={value.includes(o.id)}
						onChange={() => onChange(toggle(value, o.id))}
						data-testid={`${testId}-${o.id}`}
					/>
					<span className="flex flex-col">
						<span className="font-medium">{o.label}</span>
						<span className="text-muted-foreground text-xs">{o.description}</span>
					</span>
				</label>
			))}
		</fieldset>
	);
}

export interface RedTeamPanelProps {
	/** Benchmarks already in the evaluation. */
	selected: string[];
	onAdd(suite: string): void;
}

/** The wizard's red-team card. */
export function RedTeamPanel({ selected, onAdd }: RedTeamPanelProps) {
	const [open, setOpen] = useState(false);
	const catalog = useGetRedTeamCatalogQuery(undefined, { skip: !open });
	return (
		<section className="bg-card flex flex-col gap-4 rounded-xl border p-4" data-testid="red-team-panel">
			<div className="flex items-start gap-3">
				<span className="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg">
					<ShieldAlert className="size-4" aria-hidden />
				</span>
				<div className="flex min-w-0 flex-1 flex-col gap-1">
					<h3 className="text-sm font-semibold">Red-team my agent</h3>
					<p className="text-muted-foreground text-sm">
						An attacker model adapts to your agent's answers (PAIR, tree of attacks, Crescendo) in the chat and in tool results, documents
						and emails. Every attack that gets through becomes a regression task for review.
					</p>
				</div>
				<Button variant="outline" size="sm" onClick={() => setOpen(!open)} dataTestId="red-team-toggle" aria-expanded={open}>
					{open ? "Close" : "Set up"}
				</Button>
			</div>
			{open && catalog.data && <RedTeamForm catalog={catalog.data} selected={selected} onAdd={onAdd} />}
			{open && catalog.isLoading && <p className="text-muted-foreground text-sm">Loading...</p>}
			{open && catalog.isError && <p className="text-destructive text-sm">Could not load the red-team options.</p>}
			{open && <RegressionsReview />}
			{open && <HarvestForm />}
		</section>
	);
}

/** The plan form and what happens after the benchmark is written. */
export function RedTeamForm({ catalog, selected, onAdd }: { catalog: RedTeamCatalog } & RedTeamPanelProps) {
	const [categories, setCategories] = useState(catalog.categories.map((c) => c.id));
	const [strategies, setStrategies] = useState(catalog.strategies.map((s) => s.id));
	const [channels, setChannels] = useState(catalog.channels.map((c) => c.id));
	const [model, setModel] = useState<ModelRef | null>(null);
	const [attempts, setAttempts] = useState(catalog.max_attempts);
	const [create, state] = useCreateRedTeamBenchmarkMutation();
	const [conflict, setConflict] = useState(false);
	const { plan, problem } = planFrom({ categories, strategies, channels, model, attempts }, catalog);
	const n = taskCount(catalog, categories, strategies, channels);
	const send = async (replace: boolean) => {
		if (!plan) return;
		setConflict(false);
		const res = await create({ ...plan, replace });
		if ("error" in res && res.error && (res.error as { status?: number }).status === 409) setConflict(true);
	};
	const done = state.data;
	const added = done ? selected.includes(done.suite) : false;
	return (
		<div className="flex flex-col gap-4" data-testid="red-team-form">
			<div className="grid gap-4 md:grid-cols-3">
				<Choices title="Attacks" options={catalog.categories} value={categories} onChange={setCategories} testId="red-team-categories" />
				<Choices title="Strategies" options={catalog.strategies} value={strategies} onChange={setStrategies} testId="red-team-strategies" />
				<Choices title="Channels" options={catalog.channels} value={channels} onChange={setChannels} testId="red-team-channels" />
			</div>
			<div className="flex flex-wrap items-end gap-4">
				<div className="flex flex-col gap-1.5">
					<span className="text-sm font-medium">Attacker model (runs on your own key)</span>
					<ModelPicker value={model} onChange={setModel} purpose="simuser" testId="red-team-model" />
				</div>
				<label className="flex flex-col gap-1.5 text-sm">
					<span className="font-medium">Attempts per attack</span>
					<input
						type="number"
						min={1}
						max={20}
						value={attempts}
						onChange={(e) => setAttempts(Math.max(1, Math.min(20, Number(e.target.value) || 1)))}
						className="border-input bg-background h-9 w-24 rounded-lg border px-2"
						data-testid="red-team-attempts"
					/>
				</label>
			</div>
			<p className="text-muted-foreground text-sm" data-testid="red-team-count">
				{n} {n === 1 ? "task" : "tasks"} in benchmark {catalog.suite}. Readiness checks play fixed attacks and call no model; the evaluation
				runs the adaptive attacker.
			</p>
			{problem && <p className="text-chart-warning-ink text-sm">{problem}</p>}
			<div className="flex flex-wrap items-center gap-2">
				<Button
					disabled={!plan || state.isLoading}
					isLoading={state.isLoading}
					onClick={() => void send(false)}
					dataTestId="red-team-create"
				>
					Create red-team benchmark
				</Button>
				{conflict && (
					<Button variant="outline" onClick={() => void send(true)} dataTestId="red-team-replace">
						Rewrite the existing one
					</Button>
				)}
			</div>
			{conflict && (
				<p className="text-muted-foreground text-sm">A red-team benchmark already exists. Rewriting it runs its readiness checks again.</p>
			)}
			{state.isError && !conflict && <p className="text-destructive text-sm">Could not write the red-team benchmark.</p>}
			{done && (
				<div className="flex flex-col gap-2 border-t pt-3" data-testid="red-team-created">
					<p className="text-sm">
						{done.replaced ? "Rewrote" : "Wrote"} <span className="font-medium">{done.suite}</span> with {done.tasks.length} tasks. Run its
						readiness check, then add it to this evaluation.
					</p>
					<div className="flex flex-wrap items-start gap-2">
						<JobRunButton
							kind={SUITE_OPS_KINDS.admit}
							params={{ suite: done.suite }}
							confirm={i18n.t("redTeam.admitConfirm", { ns: "ufint", suite: done.suite })}
							label="Run readiness check"
							variant="outline"
						/>
						<Button disabled={added} onClick={() => onAdd(done.suite)} dataTestId="red-team-add">
							{added ? "Added to this evaluation" : "Add to this evaluation"}
						</Button>
					</div>
				</div>
			)}
		</div>
	);
}

/** Regressions made from successful attacks, with accept and reject. */
export function RegressionsReview() {
	const q = useGetRedTeamRegressionsQuery();
	const [review, state] = useReviewRedTeamRegressionMutation();
	const items = q.data?.items ?? [];
	if (!q.data?.exists || items.length === 0) return null;
	return <RegressionsList items={items} busy={state.isLoading} onDecide={(task, decision) => void review({ task, decision })} />;
}

export function RegressionsList({
	items,
	busy,
	onDecide,
}: {
	items: RedTeamReviewItem[];
	busy?: boolean;
	onDecide(task: string, d: "accepted" | "rejected"): void;
}) {
	const pending = items.filter((i) => i.state === "pending").length;
	return (
		<div className="flex flex-col gap-2 border-t pt-3" data-testid="red-team-regressions">
			<h4 className="text-sm font-semibold">Regressions from successful attacks</h4>
			<p className="text-muted-foreground text-xs">
				{pending} waiting for review. An accepted regression joins the regressions benchmark and replays the exact attack on every later
				version.
			</p>
			<ul className="flex flex-col gap-1.5">
				{items.map((i) => (
					<li key={i.task} className="flex flex-wrap items-center gap-2 text-sm" data-testid={`red-team-regression-${i.task}`}>
						<span className="font-mono text-xs">{i.task}</span>
						<span
							className={cn(
								"rounded-md px-1.5 py-0.5 text-xs",
								i.state === "pending" ? "bg-muted" : i.state === "accepted" ? "bg-primary/10 text-primary" : "text-muted-foreground",
							)}
						>
							{i.state === "pending" ? "Waiting for review" : i.state === "accepted" ? "Accepted" : "Rejected"}
						</span>
						{i.attempts_to_break ? <span className="text-muted-foreground text-xs">broke after {i.attempts_to_break} attempts</span> : null}
						{i.state !== "accepted" && (
							<Button
								size="sm"
								variant="outline"
								disabled={busy}
								onClick={() => onDecide(i.task, "accepted")}
								dataTestId={`red-team-accept-${i.task}`}
							>
								Accept
							</Button>
						)}
						{i.state !== "rejected" && (
							<Button
								size="sm"
								variant="ghost"
								disabled={busy}
								onClick={() => onDecide(i.task, "rejected")}
								dataTestId={`red-team-reject-${i.task}`}
							>
								Reject
							</Button>
						)}
					</li>
				))}
			</ul>
		</div>
	);
}