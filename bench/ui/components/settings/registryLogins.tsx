import { ForbiddenNote, RoleGate, isRoleRefusal } from "@/components/platform/accessNotes";
import { ConfirmButton } from "@/components/platform/badges";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import "@/components/share/uf8bI18n";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useCreateRegistryCredentialMutation,
	useDeleteRegistryCredentialMutation,
	useListRegistryCredentialsQuery,
} from "@/lib/store/apis/registryCredentialsApi";
import { REGISTRY_KINDS, registryFormProblem, type RegistryCredential, type RegistryKind } from "@/lib/types/registryCredentials";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { SettingsSection, TableFrame } from "./settingsBits";

export interface RegistryLoginsTableProps {
	logins: RegistryCredential[];
	canWrite: boolean;
	onDelete?: (id: string) => void;
	deletingId?: string;
}

/** The org's registry logins: host, user name and the secret that holds the password. Never a password. */
export function RegistryLoginsTable({ logins, canWrite, onDelete, deletingId }: RegistryLoginsTableProps) {
	const { t } = useTranslation("uf8b");
	if (logins.length === 0) {
		return (
			<EmptyNote testId="registry-logins-empty">
				{canWrite ? "No registry logins yet. Add one to run agent images from a private registry." : "No registry logins yet. Ask an org admin to add one."}
			</EmptyNote>
		);
	}
	return (
		<TableFrame testId="registry-logins-table">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>Registry</TableHead>
						<TableHead>User name</TableHead>
						<TableHead>Password from secret</TableHead>
						{canWrite && <TableHead className="text-right" />}
					</TableRow>
				</TableHeader>
				<TableBody>
					{logins.map((l) => (
						<TableRow key={l.id} data-testid={`registry-login-${l.id}`}>
							<TableCell>
								<div className="font-mono text-xs font-medium">{l.host}</div>
								<div className="text-muted-foreground text-xs">{REGISTRY_KINDS[l.kind]?.label ?? l.kind}</div>
							</TableCell>
							<TableCell className="font-mono text-xs">{l.username}</TableCell>
							<TableCell>
								<span className="mr-2 font-mono text-xs">{l.secret_name}</span>
								{l.secret_found ? (
									<Badge variant="outline">Ready</Badge>
								) : (
									<Badge variant="warning" data-testid={`registry-secret-missing-${l.id}`}>
										Secret missing: add it under Secrets
									</Badge>
								)}
							</TableCell>
							{canWrite && (
								<TableCell className="text-right">
									<ConfirmButton
										label={t("registry.remove")}
										question={t("registry.removeQuestion", { host: l.host })}
										confirmLabel={t("registry.remove")}
										cancelLabel={t("registry.keep")}
										onConfirm={() => onDelete?.(l.id)}
										isLoading={deletingId === l.id}
										testId={`registry-delete-${l.id}`}
									/>
								</TableCell>
							)}
						</TableRow>
					))}
				</TableBody>
			</Table>
		</TableFrame>
	);
}

function AddRegistryLogin({ secretNames }: { secretNames: string[] }) {
	const { t } = useTranslation("uf8b");
	const session = useSession();
	const formId = useId();
	const [kind, setKind] = useState<RegistryKind>("ghcr");
	const [host, setHost] = useState("");
	const [username, setUsername] = useState("");
	const [secretName, setSecretName] = useState("");
	const [problem, setProblem] = useState("");
	const [create, state] = useCreateRegistryCredentialMutation();
	const info = REGISTRY_KINDS[kind];

	const onSubmit = async (event: FormEvent) => {
		event.preventDefault();
		const p = registryFormProblem(kind, host, username, secretName);
		setProblem(p);
		if (p) return;
		try {
			await create({ kind, host: info.host ? undefined : host.trim(), username: info.fixedUser ? undefined : username.trim(), secret_name: secretName }).unwrap();
			setHost("");
			setUsername("");
			setSecretName("");
		} catch {
			// state.error renders the failure.
		}
	};

	const field = "flex min-w-0 flex-col gap-1.5";
	const label = "text-[13px] font-medium";
	return (
		<form className="bg-muted/30 grid gap-3 rounded-lg border p-4 sm:grid-cols-2" onSubmit={(e) => void onSubmit(e)} noValidate autoComplete="off" data-testid="registry-form">
			<div className={field}>
				<label htmlFor={`${formId}-kind`} className={label}>
					Registry
				</label>
				<Select value={kind} onValueChange={(v) => setKind(v as RegistryKind)}>
					<SelectTrigger id={`${formId}-kind`} className="w-full" data-testid="registry-kind">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						{(Object.keys(REGISTRY_KINDS) as RegistryKind[]).map((k) => (
							<SelectItem key={k} value={k}>
								{REGISTRY_KINDS[k].label}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</div>
			<div className={field}>
				<label htmlFor={`${formId}-host`} className={label}>
					Host
				</label>
				<Input
					id={`${formId}-host`}
					className="font-mono"
					spellCheck={false}
					value={info.host ?? host}
					disabled={Boolean(info.host)}
					placeholder={info.hostPlaceholder}
					onChange={(e) => setHost(e.target.value)}
					data-testid="registry-host"
				/>
			</div>
			<div className={field}>
				<label htmlFor={`${formId}-user`} className={label}>
					{info.userLabel}
				</label>
				<Input
					id={`${formId}-user`}
					className="font-mono"
					spellCheck={false}
					value={info.fixedUser ?? username}
					disabled={Boolean(info.fixedUser)}
					onChange={(e) => setUsername(e.target.value)}
					data-testid="registry-username"
				/>
			</div>
			<div className={field}>
				<label htmlFor={`${formId}-secret`} className={label}>
					Password from secret
				</label>
				{/* No item for "none": an empty value shows the placeholder. */}
				<Select value={secretName} onValueChange={setSecretName}>
					<SelectTrigger id={`${formId}-secret`} className="w-full font-mono" data-testid="registry-secret">
						<SelectValue placeholder={t("registry.pickSecret")} />
					</SelectTrigger>
					<SelectContent>
						{secretNames.map((n) => (
							<SelectItem key={n} value={n} className="font-mono">
								{n}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
				<span className="text-muted-foreground text-xs">Store {info.secretHint} as a secret above first.</span>
			</div>
			<div className="flex flex-wrap items-center gap-3 sm:col-span-2">
				<Button type="submit" className="h-9" isLoading={state.isLoading} dataTestId="registry-submit">
					Add login
				</Button>
				{problem && <span className="text-chart-error-ink text-xs">{problem}</span>}
				{state.error &&
					(isRoleRefusal(session, state.error) ? (
						<ForbiddenNote min="admin" error={state.error} testId="registry-create-error" />
					) : (
						<span role="alert" className="text-chart-error-ink text-xs" data-testid="registry-create-error">
							Could not add the login: {getBenchErrorMessage(state.error)}
						</span>
					))}
			</div>
		</form>
	);
}

/** Settings > Secrets: private registry logins the shared worker uses to pull agent images. */
export function RegistryLoginsSection({ secretNames }: { secretNames: string[] }) {
	const session = useSession();
	const list = useListRegistryCredentialsQuery();
	const [remove, removeState] = useDeleteRegistryCredentialMutation();
	return (
		<SettingsSection
			title="Private registries"
			description="Logins the worker uses to pull your agent images from Docker Hub, GitHub, Amazon ECR, Google, Azure or any other registry. Each login is used for one pull of one run and never kept; the password stays in a secret."
			testId="settings-registry-logins"
		>
			<RoleGate min="admin">
				<AddRegistryLogin secretNames={secretNames} />
			</RoleGate>
			{list.isLoading ? (
				<BlockSkeleton rows={2} testId="registry-logins-loading" />
			) : list.isError || !list.data ? (
				<LoadError title="Could not load registry logins" error={list.error} onRetry={() => void list.refetch()} testId="registry-logins-error" />
			) : (
				<RegistryLoginsTable
					logins={list.data.credentials}
					canWrite={canUse(session, "admin")}
					onDelete={(id) => void remove(id)}
					deletingId={removeState.isLoading ? removeState.originalArgs : undefined}
				/>
			)}
			{removeState.error && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="registry-delete-error">
					Could not remove the login: {getBenchErrorMessage(removeState.error)}
				</p>
			)}
		</SettingsSection>
	);
}
