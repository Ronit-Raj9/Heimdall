import { PolicyForm, starterPolicy } from "@/components/govern/policyForm";
import { LoadError } from "@/components/runs/screenStates";
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
import { FilePlus2, ShieldCheck } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/** The release policy editor: every policies/*.yaml on the left, a form or YAML editor on the right. */
export default function PoliciesView() {
	const { t } = useTranslation("govern");
	const [file, setFile] = useQueryState("file", parseAsString);
	const [creating, setCreating] = useState(false);
	const list = useListPoliciesQuery();
	const policies = list.data?.policies ?? [];

	const startNew = () => {
		setCreating(true);
		void setFile(null);
	};

	return (
		<section className="flex flex-col gap-6" data-testid="policies-screen">
			<PageHeader
				title={t("policies.title")}
				subtitle={t("policies.subtitle")}
				actions={
					<Button onClick={startNew} data-testid="policies-new">
						<FilePlus2 className="size-4" aria-hidden="true" />
						{t("policies.new")}
					</Button>
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
					action={{ label: t("policies.createDefault"), onClick: startNew }}
					testId="policies-empty"
				/>
			) : (
				<div className="grid grid-cols-[minmax(0,1fr)] gap-6 lg:grid-cols-[16rem_minmax(0,1fr)] lg:items-start">
					<ul className="flex flex-col gap-1" data-testid="policies-list">
						{policies.map((p) => (
							<li key={p.file}>
								<button
									type="button"
									onClick={() => {
										setCreating(false);
										void setFile(p.file);
									}}
									className={cn(
										"hover:bg-muted w-full rounded-sm border px-3 py-2 text-left text-sm",
										file === p.file && !creating && "bg-muted border-primary/40",
									)}
									data-testid={`policies-item-${p.file}`}
								>
									<div className="font-mono text-xs break-all">{p.file}</div>
									<div className="text-muted-foreground mt-1 flex items-center gap-2 text-xs">
										{p.valid ? (
											<span>{t("policies.checks", { count: p.checks })}</span>
										) : (
											<Badge variant="destructive">{t("policies.invalid")}</Badge>
										)}
									</div>
								</button>
							</li>
						))}
					</ul>
					<Card>
						<CardContent>
							{creating ? (
								<PolicyEditor
									key="new"
									isNew
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
	if (q.isLoading) return <Skeleton className="h-64 w-full" />;
	if (q.isError || !q.data) return <LoadError title={t("policies.loadError")} error={q.error} onRetry={q.refetch} testId="policy-error" />;
	return (
		<PolicyEditor
			key={q.data.sha256}
			file={file}
			initialYAML={q.data.yaml}
			initialPolicy={q.data.policy}
			sha256={q.data.sha256}
			onReload={() => void q.refetch()}
			onDeleted={onDeleted}
		/>
	);
}

interface PolicyEditorProps {
	isNew?: boolean;
	file?: string;
	initialYAML?: string;
	initialPolicy?: ReleasePolicy;
	sha256?: string;
	onSaved?(file: string): void;
	onReload?(): void;
	onDeleted?(): void;
}

/** Form or YAML editing of one policy. A file that does not parse opens on the YAML tab. */
export function PolicyEditor({ isNew, file, initialYAML, initialPolicy, sha256, onSaved, onReload, onDeleted }: PolicyEditorProps) {
	const { t } = useTranslation("govern");
	const [mode, setMode] = useState<"form" | "yaml">(isNew || initialPolicy ? "form" : "yaml");
	const [policy, setPolicy] = useState<ReleasePolicy>(initialPolicy ?? starterPolicy());
	const [yaml, setYaml] = useState(initialYAML ?? "");
	const [fileName, setFileName] = useState(file ?? "my-release-policy.yaml");
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
	const nameOk = isValidPolicyFile(target);
	const doSave = async () => {
		const res = await save({ file: target, body: { ...body(), if_match: isNew ? undefined : sha256 } });
		if ("data" in res && res.data) {
			setSavedPath(res.data.path);
			onSaved?.(res.data.file);
		}
	};
	const conflict = saveState.error && "status" in saveState.error && saveState.error.status === 409;
	const v = validation.data;

	return (
		<div className="flex flex-col gap-4" data-testid="policy-editor">
			<div className="flex flex-wrap items-end justify-between gap-3">
				{isNew ? (
					<label className="flex min-w-60 flex-col gap-1 text-sm" title={t("policies.fileNameHint")}>
						{t("policies.fileName")}
						<Input value={fileName} onChange={(e) => setFileName(e.target.value)} data-testid="policy-file" aria-invalid={!nameOk} />
						{!nameOk && <span className="text-destructive text-xs">{t("policies.fileNameHint")}</span>}
					</label>
				) : (
					<span className="font-mono text-sm">policies/{file}</span>
				)}
				<div className="flex gap-1" role="tablist">
					<Button size="sm" variant={mode === "form" ? "default" : "outline"} onClick={() => void switchMode("form")} data-testid="policy-mode-form">
						{t("policies.form")}
					</Button>
					<Button size="sm" variant={mode === "yaml" ? "default" : "outline"} onClick={() => void switchMode("yaml")} data-testid="policy-mode-yaml">
						{t("policies.yaml")}
					</Button>
				</div>
			</div>

			{mode === "form" ? (
				<PolicyForm value={policy} onChange={setPolicy} />
			) : (
				<div className="flex flex-col gap-1">
					<p className="text-muted-foreground text-xs">{t("policies.yamlHint")}</p>
					<textarea
						className="border-input bg-background min-h-96 w-full rounded-md border p-3 font-mono text-xs"
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

			<div className="flex flex-wrap justify-between gap-2">
				<div>
					{!isNew && file && (
						<Button
							variant="ghost"
							disabled={delState.isLoading}
							onClick={async () => {
								if (!window.confirm(t("policies.deleteConfirm", { file }))) return;
								const res = await del(file);
								if (!("error" in res)) onDeleted?.();
							}}
							data-testid="policy-delete"
						>
							{t("policies.delete")}
						</Button>
					)}
				</div>
				<div className="flex gap-2">
					<Button variant="outline" disabled={validation.isLoading} onClick={() => void validate(body())} data-testid="policy-validate">
						{t("policies.validate")}
					</Button>
					<Button
						disabled={saveState.isLoading || !nameOk || (mode === "form" && policy.checks.length === 0)}
						onClick={() => void doSave()}
						data-testid="policy-save"
					>
						{saveState.isLoading ? t("policies.saving") : t("policies.save")}
					</Button>
				</div>
			</div>
		</div>
	);
}
