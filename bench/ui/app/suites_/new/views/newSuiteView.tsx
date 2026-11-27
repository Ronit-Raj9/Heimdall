import "@/components/agents/hw02I18n";
import { AGENT_ID_RE } from "@/components/agents/agentName";
import { PageHeader } from "@/components/shell";
import { isBuiltinSuite } from "@/components/suites/suiteDisplay";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { authoringError, useCreateSuiteMutation, useGetSuiteTemplatesQuery } from "@/lib/store/apis/suiteAuthoringApi";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import { Link, useNavigate } from "@tanstack/react-router";
import { parseAsStringLiteral, useQueryState } from "nuqs";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import "@/components/suites/uf6bI18n";

// One id rule for agents and benchmarks (M6-60): 1 to 63 characters.
const ID_RE = AGENT_ID_RE;
const FALLBACK_TEMPLATES = ["python-pytest", "node-test", "go-test", "generic-reward"];
/** Templates of a benchmark cut from repository commits, graded by tests. */
const CODE_TEMPLATES = new Set(["python-pytest", "node-test", "go-test", "generic-reward"]);

/** Where a new benchmark's tasks come from (X13): own questions and tickets, repository commits, or any template. */
export type StartingPoint = "questions" | "code" | "other";
const STARTS = ["questions", "code", "other"] as const satisfies readonly StartingPoint[];

/** /suites/new: create benchmarks/<id>/ from a template, then open its Edit tab. */
export default function NewSuiteView({ initialId = "" }: { initialId?: string } = {}) {
	const { t } = useTranslation("suiteAuthoring");
	const { t: th } = useTranslation("hw02");
	const { t: tb } = useTranslation("bench");
	const { t: tu } = useTranslation("uf6b");
	// ?start=code opens on "From code changes" (the add chooser's code row).
	const [startParam] = useQueryState("start", parseAsStringLiteral(STARTS));
	const session = useSession();
	const writable = canUse(session, "member");
	const templatesQuery = useGetSuiteTemplatesQuery();
	const [createSuite, { isLoading, error }] = useCreateSuiteMutation();
	const navigate = useNavigate();
	const [id, setId] = useState(initialId);
	const [start, setStart] = useState<StartingPoint>(startParam ?? "questions");
	const [template, setTemplate] = useState("");
	const [image, setImage] = useState("");
	const [notVisible, setNotVisible] = useState(false);
	const allTemplates = templatesQuery.data?.templates ?? FALLBACK_TEMPLATES.map((tid) => ({ id: tid, scenario: false }));
	const templates = start === "code" ? allTemplates.filter((tpl) => CODE_TEMPLATES.has(tpl.id)) : allTemplates;
	const fallback = templatesQuery.data?.default || "python-pytest";
	const chosen = start === "questions" ? "qa" : templates.some((tpl) => tpl.id === template) ? template : fallback;
	// Fix wave 3, N265: an id another benchmark (of the organization or built in) already has is taken.
	const light = useGetSuitesLightQuery();
	const existing = light.data?.suites?.find((s) => s.id === id);
	const taken = existing ? (isBuiltinSuite(existing) ? "builtin" : "org") : null;
	const idOk = ID_RE.test(id) && taken === null;

	const submit = async (e: FormEvent) => {
		e.preventDefault();
		if (!idOk || !writable) return;
		const blank = start === "questions";
		const res = await createSuite({ id, template: chosen, image: image.trim() || undefined, ...(blank ? { blank: true } : {}) });
		if ("data" in res && res.data) {
			if (!res.data.visible) {
				setNotVisible(true);
				return;
			}
			void navigate({ to: "/suites/$suiteId", params: { suiteId: res.data.id }, search: { tab: blank ? "build" : "edit" } as never });
		}
	};

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="suites-new-screen">
			<PageHeader
				title={t("new.title")}
				subtitle={t("new.subtitle")}
				breadcrumbs={[{ label: tb("suites.title"), to: "/suites" }, { label: t("new.title") }]}
			/>
			{!writable && (
				<p role="note" className="text-muted-foreground text-sm" data-testid="suites-new-readonly">
					{t("edit.readOnly")}
				</p>
			)}
			<form className="flex max-w-2xl flex-col gap-5" onSubmit={(e) => void submit(e)} data-testid="suites-new-form">
				<label className="flex flex-col gap-1.5 text-sm font-medium">
					{t("new.id")}
					<Input
						value={id}
						onChange={(e) => setId(e.target.value.toLowerCase())}
						placeholder="my-benchmark"
						aria-invalid={id !== "" && !idOk}
						data-testid="suites-new-id"
					/>
					{taken === "builtin" ? (
						<span className="text-destructive text-xs font-normal" data-testid="suites-new-id-taken">
							{th("newSuite.builtin", { id })}
						</span>
					) : taken === "org" ? (
						<span className="text-destructive text-xs font-normal" data-testid="suites-new-id-taken">
							{th("newSuite.taken", { id })}{" "}
							<Link to="/suites/$suiteId" params={{ suiteId: id }} className="underline">
								{th("newSuite.open", { id })}
							</Link>
							.
						</span>
					) : (
						<span className="text-muted-foreground text-xs font-normal">{id !== "" && !idOk ? t("new.invalidId") : t("new.idHint")}</span>
					)}
				</label>
				<fieldset className="flex flex-col gap-2">
					<legend className="mb-1.5 text-sm font-medium">{t("new.start.title")}</legend>
					<div className="grid grid-cols-1 gap-2 sm:grid-cols-3" data-testid="suites-new-start">
						{STARTS.map((sp) => (
							<label
								key={sp}
								className={`flex cursor-pointer flex-col gap-0.5 rounded-md border px-3 py-2 text-sm ${start === sp ? "border-primary bg-primary/5" : ""}`}
								data-testid={`suites-new-start-${sp}`}
							>
								<span className="flex items-center gap-2 font-medium">
									<input type="radio" name="start" value={sp} checked={start === sp} onChange={() => setStart(sp)} />
									{t(`new.start.${sp}`)}
								</span>
								<span className="text-muted-foreground text-xs">{t(`new.start.${sp}Hint`)}</span>
							</label>
						))}
					</div>
				</fieldset>
				{start !== "questions" && (
					<fieldset className="flex flex-col gap-2">
						<legend className="mb-1.5 text-sm font-medium">{t("new.template")}</legend>
						<div className="grid grid-cols-1 gap-2 sm:grid-cols-2" data-testid="suites-new-templates">
							{templates.map((tpl) => (
								<label
									key={tpl.id}
									className={`flex cursor-pointer flex-col gap-0.5 rounded-md border px-3 py-2 text-sm ${chosen === tpl.id ? "border-primary bg-primary/5" : ""}`}
								>
									<span className="flex items-center gap-2">
										<input type="radio" name="template" value={tpl.id} checked={chosen === tpl.id} onChange={() => setTemplate(tpl.id)} />
										<span className="font-mono">{tpl.id}</span>
									</span>
									<span className="text-muted-foreground text-xs">
										{t(`new.templates.${tpl.id}`, { defaultValue: "" })}
										{tpl.scenario ? ` (${t("new.scenario")})` : ""}
									</span>
								</label>
							))}
						</div>
					</fieldset>
				)}
				<details className="group" open={image !== ""} data-testid="suites-new-advanced">
					<summary className="text-muted-foreground hover:text-foreground cursor-pointer text-sm font-medium">{tu("newSuite.advanced")}</summary>
					<label className="mt-3 flex flex-col gap-1.5 text-sm font-medium">
						{t("new.image")}
						<Input value={image} onChange={(e) => setImage(e.target.value)} placeholder="python:3.12-slim" data-testid="suites-new-image" />
						<span className="text-muted-foreground text-xs font-normal">{t("new.imageHint")}</span>
					</label>
				</details>
				{error && (
					<p role="alert" className="text-destructive text-sm" data-testid="suites-new-error">
						{authoringError(error, t("new.failed"))}
					</p>
				)}
				{notVisible && (
					<p role="alert" className="text-destructive text-sm" data-testid="suites-new-not-visible">
						{t("new.notVisible")}
					</p>
				)}
				<div>
					<Button type="submit" disabled={!idOk || isLoading || !writable} data-testid="suites-new-create">
						{isLoading ? t("new.creating") : t("new.create")}
					</Button>
				</div>
			</form>
		</section>
	);
}