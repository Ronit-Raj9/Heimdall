import { useConfirm } from "@/components/platform/confirmDialog";
import { policyErrorText } from "@/components/govern/policyError";
import { PolicyForm, checkSentence, policyNameProblem, starterPolicy } from "@/components/govern/policyForm";
import "@/components/canaries/uf7bI18n";
import { WhyDisabled } from "@/components/ui/whyDisabled";
import i18n from "@/lib/i18n";
import "@/app/schedules/views/w4m7bI18n";
import { RoleGate, RoleNote } from "@/components/platform/accessNotes";
import { LoadError } from "@/components/runs/screenStates";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { PageHeader } from "@/components/shell";
import { EmptyState } from "@/components/shell/emptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	isValidPolicyFile,
	normalizePolicyFile,
	useDeletePolicyMutation,
	useGetPolicyQuery,
	useListPoliciesQuery,
	useSavePolicyMutation,
	useValidatePolicyMutation,
} from "@/lib/store/apis/governApi";
import type { PolicySaveRequest, ReleasePolicy } from "@/lib/types/govern";
import { cn } from "@/lib/utils";
import { Code2, FilePlus2, ListChecks, ShieldAlert, ShieldCheck } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/** The error line of a rule set file, or the plain "not a rule set" text for schedules.yaml (Q-265). */
function errText(file: string, error: string | undefined): string | undefined {
	return policyErrorText(file, error, (k) => String(i18n.t(k as never, { ns: "govern" } as never)));
}

/** The release policy editor: every policies/*.yaml on the left, a form or YAML editor on the right. */
export default function PoliciesView() {
	const { t } = useTranslation("govern");
	const canWrite = canUse(useSession(), "member");
	const [file, setFile] = useQueryState("file", parseAsString);
	const [creating, setCreating] = useState(false);
	// N6: every press of New rule set mounts a fresh editor, even while a new set is already open.
	const [newKey, setNewKey] = useState(0);
	const list = useListPoliciesQuery();
	const policies = list.data?.policies ?? [];

	const startNew = () => {
		setCreating(true);
		setNewKey((n) => n + 1);
		void setFile(null);
	};

	return (
		<section className="flex w-full flex-col gap-8" data-testid="policies-screen">
			<PageHeader
				title={t("policies.title")}
				actions={
					<RoleGate min="member" fallback={null}>
						<Button onClick={startNew} data-testid="policies-new">
							<FilePlus2 className="size-4" aria-hidden="true" />
							{t("policies.new")}
						</Button>
					</RoleGate>
				}
			/>
			{list.isLoading ? (
				<Skeleton className="h-40 w-full" data-testid="policies-loading" />
			) : list.isError ? (
				<LoadError title={t("policies.loadError")} error={list.error} onRetry={list.refetch} testId="policies-error" />
			) : policies.length === 0 && !creating ? (
				<EmptyState
					icon={ShieldCheck}
					message={t("policies.empty")}
					action={
						canWrite
							? { label: t("policies.createDefault"), onClick: startNew }
							: { label: t("policies.readGuide"), to: "/docs", search: { doc: "releases" } }
					}
					testId="policies-empty"
				/>
			) : (
				<div className="grid grid-cols-[minmax(0,1fr)] gap-6 lg:grid-cols-[18rem_minmax(0,1fr)] lg:items-start">
					<ul className="flex flex-col gap-2" data-testid="policies-list">
						{policies.map((p) => {
							const active = file === p.file && !creating;
							return (
								<li key={p.file}>
									<button
										type="button"
										onClick={() => {
											setCreating(false);
											void setFile(p.file);
										}}
										className={cn(
											"bg-card hover:border-primary/40 flex w-full items-center gap-3 rounded-xl border p-4 text-left transition hover:shadow-md",
											active && "border-primary/60 ring-primary/20 ring-2",
										)}
										data-testid={`policies-item-${p.file}`}
									>
										<span
											className={cn(
												"flex size-9 shrink-0 items-center justify-center rounded-lg",
												p.valid ? "bg-primary/10 text-primary" : "bg-destructive/10 text-chart-error-ink",
											)}
										>
											{p.valid ? (
												<ShieldCheck className="size-4.5" aria-hidden="true" />
											) : (
												<ShieldAlert className="size-4.5" aria-hidden="true" />
											)}
										</span>
										<span className="flex min-w-0 flex-col gap-0.5">
											<span className="truncate text-sm font-semibold" title={p.file}>
												{p.file.replace(/\.ya?ml$/, "")}
											</span>
											{p.valid ? (
												<span className="text-muted-foreground text-xs">{t("policies.checks", { count: p.checks })}</span>
											) : (
												// M7-80: say what is wrong, not only that something is.
												<span className="flex flex-col gap-0.5">
													<Badge variant="destructive" className="w-fit">
														{t("policies.invalid")}
													</Badge>
													{errText(p.file, p.error) && (
														<span className="text-danger-ink line-clamp-2 text-xs break-words" title={errText(p.file, p.error)} data-testid={`policies-item-error-${p.file}`}>
															{errText(p.file, p.error)}
														</span>
													)}
												</span>
											)}
										</span>
									</button>
								</li>
							);
						})}
					</ul>
					<Card className="rounded-xl">
						<CardContent className="p-5 sm:p-6">
							{creating ? (
								<PolicyEditor
									key={`new-${newKey}`}
									isNew
									taken={policies.map((p) => p.file)}
									onSaved={(f) => {
										setCreating(false);
										void setFile(f);
									}}
								/>
							) : file ? (
								<ExistingPolicy key={file} file={file} onDeleted={() => void setFile(null)} />
							) : (
								<p className="text-muted-foreground text-sm" data-testid="policies-pick">
									{t("policies.pick")}
								</p>
							)}
						</CardContent>
					</Card>
				</div>
			)}
		</section>
	);
}

function ExistingPolicy({ file, onDeleted }: { file: string; onDeleted(): void }) {
	const { t } = useTranslation("govern");
	const q = useGetPolicyQuery(file);
	const [editing, setEditing] = useState(false);
	if (q.isLoading) return <Skeleton className="h-64 w-full" />;
	if (q.isError || !q.data) return <LoadError title={t("policies.loadError")} error={q.error} onRetry={q.refetch} testId="policy-error" />;
	// M7-80: a valid rule set opens as plain sentences first; Edit opens the editor. A broken one
	// opens in the editor so it can be fixed, with its error text above it.
	if (!editing && q.data.valid && q.data.policy)
		return <PolicyReadView policy={q.data.policy} onEdit={() => setEditing(true)} />;
	return (
		<div className="flex flex-col gap-3">
			{!q.data.valid && errText(file, q.data.error) && (
				<p role="alert" className="text-danger-ink text-sm break-words" data-testid="policy-invalid-error">
					{errText(file, q.data.error)}
				</p>
			)}
			<PolicyEditor
				key={q.data.sha256}
				file={file}
				initialYAML={q.data.yaml}
				initialPolicy={q.data.policy}
				sha256={q.data.sha256}
				onReload={() => void q.refetch()}
				onDeleted={onDeleted}
			/>
		</div>
	);
}

/** A rule set as plain sentences, with Edit for members (M7-80). Exported for tests. */
export function PolicyReadView({ policy, onEdit }: { policy: ReleasePolicy; onEdit(): void }) {
	const { t: tu } = useTranslation("u14");
	const { t: m } = useTranslation("uf7b");
	return (
		<div className="flex flex-col gap-4" data-testid="policy-read">
			<div className="flex flex-wrap items-start justify-between gap-3">
				<div className="min-w-0">
					<h2 className="text-base font-semibold break-words">{policy.name}</h2>
					{policy.description && <p className="text-muted-foreground text-sm">{policy.description}</p>}
				</div>
				<RoleGate min="member" fallback={null}>
					<Button size="sm" variant="outline" onClick={onEdit} data-testid="policy-edit">
						{m("policies.edit")}
					</Button>
				</RoleGate>
			</div>
			{policy.checks.length === 0 ? (
				<p className="text-muted-foreground text-sm">{m("policies.noRules")}</p>
			) : (
				<ul className="flex list-disc flex-col gap-1 pl-5 text-sm" data-testid="policy-read-rules">
					{policy.checks.map((c) => (
						<li key={c.id}>{checkSentence(c, tu)}</li>
					))}
				</ul>
			)}
		</div>
	);
}

/** The first of my-release-policy.yaml, my-release-policy-2.yaml, ... that no rule set uses (N6). */
export function freePolicyFile(taken: readonly string[]): string {
	const used = new Set(taken);
	for (let n = 1; ; n++) {
		const name = n === 1 ? "my-release-policy.yaml" : `my-release-policy-${n}.yaml`;
		if (!used.has(name)) return name;
	}
}

/** "It will be saved as bad-name.yaml." when the typed file name is changed to a valid one, else null (p14-1). */
export function fileNameNote(input: string): string | null {
	const target = normalizePolicyFile(input);
	if (target === input.trim() || !isValidPolicyFile(target)) return null;
	return i18n.t("policies.savedAs", { ns: "w4m7b", file: target });
}

interface PolicyEditorProps {
	isNew?: boolean;
	/** File names of the rule sets that exist: a new set may not take one (N6). */
	taken?: readonly string[];
	file?: string;
	initialYAML?: string;
	initialPolicy?: ReleasePolicy;
	sha256?: string;
	onSaved?(file: string): void;
	onReload?(): void;
	onDeleted?(): void;
}

/** Form or YAML editing of one policy. A file that does not parse opens on the YAML tab. */
export function PolicyEditor({
	isNew,
	taken = [],
	file,
	initialYAML,
	initialPolicy,
	sha256,
	onSaved,
	onReload,
	onDeleted,
}: PolicyEditorProps) {
	const { t } = useTranslation("govern");
	const confirm = useConfirm();
	const [mode, setMode] = useState<"form" | "yaml">(isNew || initialPolicy ? "form" : "yaml");
	const [fileName, setFileName] = useState(file ?? freePolicyFile(taken));
	// N6: a new set is named after the free file it will be saved as.
	const [policy, setPolicy] = useState<ReleasePolicy>(initialPolicy ?? starterPolicy(fileName.replace(/\.ya?ml$/, "")));
	const [yaml, setYaml] = useState(initialYAML ?? "");
	const [savedPath, setSavedPath] = useState<string | null>(null);
	const [save, saveState] = useSavePolicyMutation();
	const [validate, validation] = useValidatePolicyMutation();
	const [del, delState] = useDeletePolicyMutation();

	useEffect(() => setSavedPath(null), [policy, yaml]);

	const body = (): PolicySaveRequest => (mode === "form" ? { policy } : { yaml });

	// Switching tabs carries the edit across: the form renders to YAML on the server, YAML parses back into the form.
	const switchMode = async (next: "form" | "yaml") => {
		if (next === mode) return;
		const res = await validate(body());
		if ("data" in res && res.data) {
			if (next === "yaml") setYaml(res.data.yaml);
			else if (res.data.policy) setPolicy(res.data.policy);
			else return; // invalid YAML stays on the YAML tab with its error shown
		}
		setMode(next);
	};

	const target = normalizePolicyFile(fileName);
	const nameTaken = Boolean(isNew) && taken.includes(target);
	const nameOk = isValidPolicyFile(target) && !nameTaken;
	const savedAs = isNew ? fileNameNote(fileName) : null;
	// p14-2: in the form, Save waits for a valid rule set name; the YAML tab is checked by the server.
	const why = mode === "yaml" ? null : policyNameProblem(policy.name);
	const doSave = async () => {
		// A new set is saved with create_only, so the server refuses a name taken since the list loaded (409).
		const res = await save({ file: target, body: { ...body(), ...(isNew ? { create_only: true } : { if_match: sha256 }) } });
		if ("data" in res && res.data) {
			setSavedPath(res.data.path);
			onSaved?.(res.data.file);
		}
	};
	// A 409 on a new set is the server's "already exists" text, shown as is; only an edit has the reload conflict.
	const conflict = !isNew && saveState.error && "status" in saveState.error && saveState.error.status === 409;
	const v = validation.data;

	return (
		<div className="flex flex-col gap-6" data-testid="policy-editor">
			<div className="flex flex-wrap items-end justify-between gap-3">
				{isNew ? (
					<label className="flex min-w-60 flex-col gap-1 text-sm" title={t("policies.fileNameHint")}>
						{t("policies.fileName")}
						<Input value={fileName} onChange={(e) => setFileName(e.target.value)} data-testid="policy-file" aria-invalid={!nameOk} />
						{nameTaken ? (
							<span className="text-destructive text-xs" data-testid="policy-file-taken">
								{t("policies.fileNameTaken")}
							</span>
						) : (
							!nameOk && <span className="text-destructive text-xs">{t("policies.fileNameHint")}</span>
						)}
						{nameOk && savedAs && (
							<span className="text-muted-foreground text-xs" data-testid="policy-file-normalized">
								{savedAs}
							</span>
						)}
					</label>
				) : (
					<span className="flex min-w-0 flex-col">
						<span className="truncate text-lg font-semibold tracking-tight">{file?.replace(/\.ya?ml$/, "")}</span>
						<span className="text-muted-foreground font-mono text-xs">policies/{file}</span>
					</span>
				)}
				<div className="bg-muted inline-flex rounded-lg p-1" role="tablist">
					<button
						type="button"
						role="tab"
						aria-selected={mode === "form"}
						className={cn(
							"inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition",
							mode === "form" ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground",
						)}
						onClick={() => void switchMode("form")}
						data-testid="policy-mode-form"
					>
						<ListChecks className="size-3.5" aria-hidden="true" />
						{t("policies.form")}
					</button>
					<button
						type="button"
						role="tab"
						aria-selected={mode === "yaml"}
						className={cn(
							"inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition",
							mode === "yaml" ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground",
						)}
						onClick={() => void switchMode("yaml")}
						data-testid="policy-mode-yaml"
					>
						<Code2 className="size-3.5" aria-hidden="true" />
						{t("policies.yaml")}
					</button>
				</div>
			</div>

			{mode === "form" ? (
				<div className="flex flex-col gap-3">
					{isNew && policy.checks.length > 0 && (
						<Button
							type="button"
							size="sm"
							variant="outline"
							className="w-fit"
							onClick={() => setPolicy({ ...policy, checks: [] })}
							data-testid="policy-start-empty"
						>
							{i18n.t("policies.startEmpty", { ns: "w4m7b" })}
						</Button>
					)}
					<PolicyForm value={policy} onChange={setPolicy} />
				</div>
			) : (
				<div className="flex flex-col gap-1">
					<p className="text-muted-foreground text-xs">{t("policies.yamlHint")}</p>
					<textarea
						className="border-input bg-muted/30 min-h-96 w-full rounded-lg border p-4 font-mono text-xs leading-relaxed"
						spellCheck={false}
						value={yaml}
						onChange={(e) => setYaml(e.target.value)}
						data-testid="policy-yaml"
					/>
				</div>
			)}

			{v && (
				<p className={cn("text-xs", v.valid ? "text-chart-success-ink" : "text-destructive")} role="status" data-testid="policy-validation">
					{v.valid ? t("policies.valid") : v.error}
				</p>
			)}
			{saveState.error && !conflict && (
				<p className="text-destructive text-xs" role="alert" data-testid="policy-save-error">
					{getBenchErrorMessage(saveState.error)}
				</p>
			)}
			{conflict && (
				<p className="text-destructive flex items-center gap-2 text-xs" role="alert" data-testid="policy-conflict">
					{t("policies.conflict")}
					{onReload && (
						<Button size="sm" variant="outline" onClick={onReload}>
							{t("policies.reload")}
						</Button>
					)}
				</p>
			)}
			{delState.error && (
				<p className="text-destructive text-xs" role="alert">
					{getBenchErrorMessage(delState.error)}
				</p>
			)}
			{savedPath && (
				<p className="text-chart-success-ink text-xs" role="status" data-testid="policy-saved">
					{t("policies.saved", { path: savedPath })}
				</p>
			)}

			<RoleNote min="member" />
			<RoleGate min="member" fallback={null}>
				<div className="flex flex-wrap justify-between gap-2 border-t pt-5">
					<div>
						{!isNew && file && (
							<Button
								variant="ghost"
								disabled={delState.isLoading}
								onClick={async () => {
									if (!(await confirm(t("policies.deleteConfirm", { file })))) return;
									const res = await del(file);
									if (!("error" in res)) onDeleted?.();
								}}
								data-testid="policy-delete"
							>
								{t("policies.delete")}
							</Button>
						)}
					</div>
					<div className="flex flex-col items-end gap-1">
						<div className="flex gap-2">
							<Button variant="outline" disabled={validation.isLoading} onClick={() => void validate(body())} data-testid="policy-validate">
								{t("policies.validate")}
							</Button>
							<Button
								disabled={saveState.isLoading || !nameOk || why !== null || (mode === "form" && policy.checks.length === 0)}
								aria-describedby={why ? "policy-save-why" : undefined}
								onClick={() => void doSave()}
								data-testid="policy-save"
							>
								{saveState.isLoading ? t("policies.saving") : t("policies.save")}
							</Button>
						</div>
						<WhyDisabled reason={saveState.isLoading ? null : why} testId="policy-save-why" />
					</div>
				</div>
			</RoleGate>
		</div>
	);
}