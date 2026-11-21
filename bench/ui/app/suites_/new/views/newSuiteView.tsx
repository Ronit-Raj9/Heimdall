import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { authoringError, useCreateSuiteMutation, useGetSuiteTemplatesQuery } from "@/lib/store/apis/suiteAuthoringApi";
import { useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

const ID_RE = /^[a-z0-9][a-z0-9._-]{0,63}$/;
const FALLBACK_TEMPLATES = ["python-pytest", "node-test", "go-test", "generic-reward"];

/** /suites/new: create benchmarks/<id>/ from a template, then open its Edit tab. */
export default function NewSuiteView() {
	const { t } = useTranslation("suiteAuthoring");
	const session = useSession();
	const writable = canUse(session, "member");
	const templatesQuery = useGetSuiteTemplatesQuery();
	const [createSuite, { isLoading, error }] = useCreateSuiteMutation();
	const navigate = useNavigate();
	const [id, setId] = useState("");
	const [template, setTemplate] = useState("");
	const [image, setImage] = useState("");
	const [notVisible, setNotVisible] = useState(false);
	const templates = templatesQuery.data?.templates ?? FALLBACK_TEMPLATES.map((tid) => ({ id: tid, scenario: false }));
	const chosen = template || templatesQuery.data?.default || "python-pytest";
	const idOk = ID_RE.test(id);

	const submit = async (e: FormEvent) => {
		e.preventDefault();
		if (!idOk || !writable) return;
		const res = await createSuite({ id, template: chosen, image: image.trim() || undefined });
		if ("data" in res && res.data) {
			if (!res.data.visible) {
				setNotVisible(true);
				return;
			}
			void navigate({ to: "/suites/$suiteId", params: { suiteId: res.data.id }, search: { tab: "edit" } as never });
		}
	};

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="suites-new-screen">
			<PageHeader title={t("new.title")} subtitle={t("new.subtitle")} breadcrumbs={[{ label: "Suites", to: "/suites" }, { label: t("new.title") }]} />
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
						placeholder="my-suite"
						aria-invalid={id !== "" && !idOk}
						data-testid="suites-new-id"
					/>
					<span className="text-muted-foreground text-xs font-normal">{id !== "" && !idOk ? t("new.invalidId") : t("new.idHint")}</span>
				</label>
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
				<label className="flex flex-col gap-1.5 text-sm font-medium">
					{t("new.image")}
					<Input value={image} onChange={(e) => setImage(e.target.value)} placeholder="python:3.12-slim" data-testid="suites-new-image" />
					<span className="text-muted-foreground text-xs font-normal">{t("new.imageHint")}</span>
				</label>
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
