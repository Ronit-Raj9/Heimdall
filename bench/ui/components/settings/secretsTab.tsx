import { ForbiddenNote, RoleGate, isRoleRefusal } from "@/components/platform/accessNotes";
import { ConfirmButton } from "@/components/platform/badges";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateSecretMutation, useDeleteSecretMutation, useListSecretsQuery, useRotateSecretMutation } from "@/lib/store/apis/secretsApi";
import { MAX_SECRET_BYTES, normalizeSecretName, secretNameProblem, type OrgSecret } from "@/lib/types/secrets";
import { LockKeyhole } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { RegistryLoginsSection } from "./registryLogins";
import { SettingsSection, TableFrame, When } from "./settingsBits";

function byteLength(v: string): number {
	return new TextEncoder().encode(v).length;
}

export interface SecretsTableProps {
	secrets: OrgSecret[];
	canWrite: boolean;
	onRotate?: (id: string, value: string) => Promise<boolean>;
	onDelete?: (id: string) => void;
	deletingId?: string;
	rotatingId?: string;
}

/** The secrets list: names, who added them and when. Never a value, not even a part of one. */
export function SecretsTable({ secrets, canWrite, onRotate, onDelete, deletingId, rotatingId }: SecretsTableProps) {
	const { t } = useTranslation("secrets");
	const [editing, setEditing] = useState<string | null>(null);
	const [value, setValue] = useState("");

	if (secrets.length === 0) {
		return <EmptyNote testId="secrets-empty">{canWrite ? t("empty") : t("emptyViewer")}</EmptyNote>;
	}
	return (
		<TableFrame testId="secrets-table">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>{t("name")}</TableHead>
						<TableHead>{t("value")}</TableHead>
						<TableHead>{t("createdBy")}</TableHead>
						<TableHead>{t("updatedAt")}</TableHead>
						{canWrite && <TableHead className="text-right" />}
					</TableRow>
				</TableHeader>
				<TableBody>
					{secrets.map((s) => (
						<TableRow key={s.id} data-testid={`secret-row-${s.id}`}>
							<TableCell className="text-foreground font-mono text-xs font-medium">{s.name}</TableCell>
							<TableCell>
								<Badge variant="outline" className="gap-1">
									<LockKeyhole className="size-3" aria-hidden="true" />
									{t("stored")}
								</Badge>
							</TableCell>
							<TableCell className="text-muted-foreground">{s.created_by ?? ""}</TableCell>
							<TableCell className="text-muted-foreground">
								<When value={s.updated_at} />
							</TableCell>
							{canWrite && (
								<TableCell className="text-right">
									{editing === s.id ? (
										<form
											className="flex items-center justify-end gap-2"
											onSubmit={(event) => {
												event.preventDefault();
												if (!value || !onRotate) return;
												void onRotate(s.id, value).then((ok) => {
													if (ok) {
														setEditing(null);
														setValue("");
													}
												});
											}}
										>
											<Input
												type="password"
												name="heimdall-secret-rotate"
												autoComplete="new-password"
												data-1p-ignore="true"
												data-lpignore="true"
												className="h-8 w-56"
												aria-label={t("rotateTitle", { name: s.name })}
												placeholder={t("rotateTitle", { name: s.name })}
												value={value}
												onChange={(event) => setValue(event.target.value)}
												data-testid={`secret-rotate-value-${s.id}`}
											/>
											<Button type="submit" size="sm" disabled={!value} isLoading={rotatingId === s.id} dataTestId={`secret-rotate-save-${s.id}`}>
												{t("rotateSave")}
											</Button>
											<Button
												type="button"
												size="sm"
												variant="ghost"
												onClick={() => {
													setEditing(null);
													setValue("");
												}}
											>
												{t("cancel")}
											</Button>
										</form>
									) : (
										<div className="flex items-center justify-end gap-2">
											<Button
												type="button"
												size="sm"
												variant="outline"
												onClick={() => {
													setEditing(s.id);
													setValue("");
												}}
												dataTestId={`secret-rotate-${s.id}`}
											>
												{t("rotate")}
											</Button>
											<ConfirmButton
												label={t("delete")}
												question={t("confirmDelete", { name: s.name })}
												confirmLabel={t("confirm")}
												cancelLabel={t("cancel")}
												onConfirm={() => onDelete?.(s.id)}
												isLoading={deletingId === s.id}
												variant="destructive"
												testId={`secret-delete-${s.id}`}
											/>
										</div>
									)}
								</TableCell>
							)}
						</TableRow>
					))}
				</TableBody>
			</Table>
		</TableFrame>
	);
}

/** Settings > Secrets: org-wide credentials for tools, write-only from the browser. */
export function SecretsTab(_props: Record<string, unknown> = {}) {
	const { t } = useTranslation("secrets");
	const session = useSession();
	const list = useListSecretsQuery();
	const [rotate, rotateState] = useRotateSecretMutation();
	const [remove, removeState] = useDeleteSecretMutation();
	const canWrite = canUse(session, "admin");
	const secrets = list.data?.secrets ?? [];
	const writeError = rotateState.error ?? removeState.error;

	return (
		<div className="flex flex-col gap-10" data-testid="settings-secrets">
			<RoleGate min="admin">
				<AddSecretSection taken={secrets.map((s) => s.name)} />
			</RoleGate>
			<SettingsSection title={t("title")} description={t("description")} testId="settings-secrets-list">
				<p className="text-muted-foreground text-xs" data-testid="secrets-usage">
					{t("usage")}
				</p>
				<p className="text-muted-foreground text-xs" data-testid="secrets-agent-usage">
					{t("agentUsage", { ref: "{{secret.TAVILY_API_KEY}}" })}
				</p>
				{list.isLoading ? (
					<BlockSkeleton rows={3} testId="secrets-loading" />
				) : list.isError || !list.data ? (
					<LoadError title={t("loadError")} error={list.error} onRetry={() => void list.refetch()} testId="secrets-error" />
				) : (
					<SecretsTable
						secrets={secrets}
						canWrite={canWrite}
						onRotate={async (id, value) => {
							try {
								await rotate({ id, value }).unwrap();
								return true;
							} catch {
								return false;
							}
						}}
						onDelete={(id) => void remove(id)}
						deletingId={removeState.isLoading ? removeState.originalArgs : undefined}
						rotatingId={rotateState.isLoading ? rotateState.originalArgs?.id : undefined}
					/>
				)}
				{writeError &&
					(isRoleRefusal(session, writeError) ? (
						<ForbiddenNote min="admin" error={writeError} testId="secret-write-error" />
					) : (
						<p role="alert" className="text-chart-error-ink text-xs" data-testid="secret-write-error">
							{rotateState.error ? t("rotateError") : t("deleteError")}: {getBenchErrorMessage(writeError)}
						</p>
					))}
			</SettingsSection>
			<RegistryLoginsSection secretNames={secrets.map((s) => s.name)} />
		</div>
	);
}

function AddSecretSection({ taken }: { taken: string[] }) {
	const { t } = useTranslation("secrets");
	const session = useSession();
	const formId = useId();
	const [name, setName] = useState("");
	const [value, setValue] = useState("");
	const [touched, setTouched] = useState(false);
	const [saved, setSaved] = useState<string | null>(null);
	const [create, createState] = useCreateSecretMutation();

	const problem = secretNameProblem(name);
	const nameError = !touched
		? undefined
		: problem === "required"
			? t("nameRequired")
			: problem === "format"
				? t("nameFormat")
				: problem === "reserved"
					? t("nameReserved")
					: taken.includes(name)
						? t("nameTaken")
						: undefined;
	const valueError = !touched ? undefined : !value ? t("valueRequired") : byteLength(value) > MAX_SECRET_BYTES ? t("valueTooLarge") : undefined;

	const onSubmit = async (event: FormEvent) => {
		event.preventDefault();
		setTouched(true);
		if (problem || taken.includes(name) || !value || byteLength(value) > MAX_SECRET_BYTES) return;
		try {
			const result = await create({ name, value }).unwrap();
			setSaved(result.name);
			setName("");
			setValue("");
			setTouched(false);
		} catch {
			// createState.error renders the failure.
		}
	};

	return (
		<SettingsSection title={t("addTitle")} description={t("addDescription")} testId="settings-secrets-create">
			<form
				className="bg-muted/30 grid gap-3 rounded-lg border p-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto] sm:items-start"
				onSubmit={(event) => void onSubmit(event)}
				noValidate
				autoComplete="off"
				data-testid="secret-form"
			>
				<div className="flex min-w-0 flex-col gap-1.5">
					<label htmlFor={`${formId}-name`} className="text-[13px] font-medium">
						{t("name")}
					</label>
					<Input
						id={`${formId}-name`}
						name="heimdall-secret-name"
						autoComplete="off"
						data-1p-ignore="true"
						data-lpignore="true"
						spellCheck={false}
						className="font-mono"
						placeholder={t("namePlaceholder")}
						value={name}
						onChange={(event) => {
							setName(normalizeSecretName(event.target.value));
							setSaved(null);
						}}
						aria-invalid={Boolean(nameError) || undefined}
						data-testid="secret-name"
					/>
					{nameError && <span className="text-chart-error-ink text-xs">{nameError}</span>}
				</div>
				<div className="flex min-w-0 flex-col gap-1.5">
					<label htmlFor={`${formId}-value`} className="text-[13px] font-medium">
						{t("value")}
					</label>
					<Input
						id={`${formId}-value`}
						type="password"
						name="heimdall-secret-value"
						autoComplete="new-password"
						data-1p-ignore="true"
						data-lpignore="true"
						placeholder={t("valuePlaceholder")}
						value={value}
						onChange={(event) => setValue(event.target.value)}
						aria-invalid={Boolean(valueError) || undefined}
						data-testid="secret-value"
					/>
					{valueError && <span className="text-chart-error-ink text-xs">{valueError}</span>}
				</div>
				<Button type="submit" className="h-9 gap-1.5 sm:mt-[22px]" isLoading={createState.isLoading} dataTestId="secret-submit">
					<LockKeyhole className="size-4" aria-hidden="true" />
					{t("save")}
				</Button>
			</form>
			{saved && (
				<p className="text-muted-foreground text-xs" role="status" data-testid="secret-saved">
					{t("saved", { name: saved })}
				</p>
			)}
			{createState.error &&
				(isRoleRefusal(session, createState.error) ? (
					<ForbiddenNote min="admin" error={createState.error} testId="secret-create-error" />
				) : (
					<p role="alert" className="text-chart-error-ink text-xs" data-testid="secret-create-error">
						{t("createError")}: {getBenchErrorMessage(createState.error)}
					</p>
				))}
		</SettingsSection>
	);
}

export default SecretsTab;
