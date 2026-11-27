import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render } from "@/lib/testing/render";
import type { WorkerTargetView } from "@/lib/store/apis/workerTargetApi";
import { describe, expect, it, vi } from "vitest";
import { createConfirmStore } from "@/components/platform/confirmDialog";
import i18n from "@/lib/i18n";
import { changeTarget, choiceLabel, confirmTarget, evalWorkerOptions, EvaluationWorkerPicker, WhereRunsGo } from "./whereRunsGo";

const q = vi.hoisted(() => ({ target: {} as Record<string, unknown> }));

vi.mock("@/lib/store/apis/workerTargetApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/workerTargetApi")>()),
	useGetWorkerTargetQuery: () => q.target,
	useSetWorkerTargetMutation: () => mutation(),
}));

const view: WorkerTargetView = {
	target: "platform:modal",
	choices: [
		{
			target: "shared",
			kind: "shared",
			name: "shared",
			title: "Shared worker",
			current: false,
			per_evaluation: false,
			note: "while a sandbox platform is where runs go, the shared worker runs every run there",
		},
		{ target: "platform:modal", kind: "platform", name: "modal", title: "Modal", status: "ok", current: true, per_evaluation: true },
		{ target: "platform:e2b", kind: "platform", name: "e2b", title: "E2B", status: "ok", current: false, per_evaluation: false },
		{
			target: "worker:gpu",
			kind: "worker",
			name: "gpu",
			title: "gpu (Fly Machines)",
			status: "ready",
			current: false,
			per_evaluation: true,
		},
	],
};

describe("WhereRunsGo", () => {
	it("lists every choice in one select with the current one chosen", () => {
		q.target = okQuery(view);
		const html = render(<WhereRunsGo />);
		expect(html).toContain('data-testid="where-runs-go-select"');
		// Q-239: the styled select shows the current choice and fits a phone (no fixed min width below sm).
		expect(html).toContain("Modal (a fresh sandbox per run)");
		expect(html).toContain('data-value="platform:modal"');
		expect(html).toMatch(/class="[^"]*w-full min-w-0 sm:w-auto sm:min-w-64[^"]*"/);
		expect(view.choices.map((c) => choiceLabel(c, "a fresh sandbox per run"))).toEqual([
			"Shared worker",
			"Modal (a fresh sandbox per run)",
			"E2B (a fresh sandbox per run)",
			"gpu (Fly Machines)",
		]);
		expect(html).not.toMatch(/arm|runner|lease/i);
	});

	it("renders nothing off the hosted platform", () => {
		q.target = { isError: true, error: { status: 503 } };
		expect(render(<WhereRunsGo />)).toBe("");
	});
});

describe("EvaluationWorkerPicker", () => {
	it("says where runs go with one choice, its runs at once and how to add a worker (N93)", () => {
		q.target = okQuery({
			target: "shared",
			choices: [{ ...view.choices[0], current: true, per_evaluation: true, note: undefined, slots: 2 }],
		});
		const member = render(<EvaluationWorkerPicker value="" onChange={() => undefined} />, hostedSession("member"));
		expect(member).toContain('data-testid="eval-worker-only"');
		expect(member).toContain("Runs on: Shared worker. It runs 2 at a time.");
		expect(member).toContain("An admin can add a worker on the Workers page.");
		expect(member).not.toContain("<select");
		const admin = render(<EvaluationWorkerPicker value="" onChange={() => undefined} />, hostedSession("admin"));
		expect(admin).toContain('href="/settings?tab=runners"');
		expect(admin).toContain("Change on the Workers page");
		expect(admin).not.toContain("An admin can add a worker");
		q.target = okQuery({ target: "shared", choices: [{ ...view.choices[0], current: true, per_evaluation: true }] });
		expect(render(<EvaluationWorkerPicker value="" onChange={() => undefined} />, hostedSession("member"))).toContain(
			"Runs on: Shared worker.</",
		);
	});

	it("names a single platform as 'Runs on: Modal, a fresh sandbox per run' (Q-250)", () => {
		q.target = okQuery({ target: view.choices.find((c) => c.kind === "platform")!.target, choices: [{ ...view.choices.find((c) => c.kind === "platform")!, current: true, per_evaluation: true, slots: 0 }] });
		const html = render(<EvaluationWorkerPicker value="" onChange={() => undefined} />, hostedSession("member"));
		expect(html).toContain(`Runs on: ${view.choices.find((c) => c.kind === "platform")!.title}, a fresh sandbox per run.`);
	});

	it("lists every choice when there are several", () => {
		q.target = okQuery(view);
		const html = render(<EvaluationWorkerPicker value="" onChange={() => undefined} />);
		expect(html).toContain("Modal (a fresh sandbox per run), where runs go");
		const t = i18n.getFixedT("en", "byoworker") as unknown as (k: string, o?: Record<string, unknown>) => string;
		const opts = evalWorkerOptions(view, t);
		expect(opts.find((o) => o.value === "worker:gpu")).toMatchObject({ label: "gpu (Fly Machines), ready", disabled: false });
		expect(opts.find((o) => o.value === "platform:e2b")).toMatchObject({
			label: "E2B (a fresh sandbox per run) (set it as where runs go on the Workers page)",
			disabled: true,
		});
		expect(opts.find((o) => o.value === "shared")?.disabled).toBe(true);
	});
});
describe("where runs go asks first (N217)", () => {
	const t = i18n.getFixedT("en", "hw05");
	// As the server answers with the shared worker as the target: a platform takes the org's queue only as a whole.
	const shared: WorkerTargetView = {
		...view,
		target: "shared",
		choices: view.choices.map((c) => ({ ...c, current: c.target === "shared", per_evaluation: c.kind !== "platform" })),
	};

	it("opens the dialog; Cancel sends nothing and Confirm sends one change", async () => {
		const pending: { message: string; opts: { title?: string; confirmLabel?: string; cancelLabel?: string } }[] = [];
		const store = createConfirmStore((p) => {
			if (p) pending.push({ message: p.message, opts: p.opts });
		});
		const save = vi.fn(() => Promise.resolve());
		const ask = (target: string) => confirmTarget((m, o) => store.ask(m, o), shared, target, t);

		const cancelled = changeTarget(ask, save, "platform:modal");
		expect(pending).toHaveLength(1);
		expect(pending[0].opts.title).toBe("Change where runs go?");
		expect(pending[0].message).toBe(
			"New evaluations of this organization will run on Modal. Runs not started yet also move. Each run starts a Modal sandbox billed to your Modal account.",
		);
		expect(pending[0].opts.confirmLabel).toBe("Send runs to Modal");
		expect(pending[0].opts.cancelLabel).toBe("Keep Shared worker");
		store.settle(false);
		expect(await cancelled).toBe(false);
		expect(save).not.toHaveBeenCalled();

		const confirmed = changeTarget(ask, save, "worker:gpu");
		expect(pending).toHaveLength(2);
		expect(pending[1].message).toBe(
			"New evaluations of this organization will run on gpu (Fly Machines). Evaluations already started keep running where they are.",
		);
		store.settle(true);
		expect(await confirmed).toBe(true);
		expect(save).toHaveBeenCalledTimes(1);
		expect(save).toHaveBeenCalledWith("worker:gpu");
	});

	it("says queued runs move when leaving a platform that takes the shared queue", async () => {
		const asked: string[] = [];
		await confirmTarget((m) => (asked.push(m), Promise.resolve(true)), view, "shared", t);
		expect(asked[0]).toBe("New evaluations of this organization will run on Shared worker. Runs not started yet also move.");
		const none: string[] = [];
		expect(await confirmTarget((m) => (none.push(m), Promise.resolve(false)), view, "platform:modal", t)).toBe(true);
		expect(none).toHaveLength(0);
	});
});