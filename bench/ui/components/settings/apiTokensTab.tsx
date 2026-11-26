import { ForbiddenNote, RoleGate, isRoleRefusal } from "@/components/platform/accessNotes";
import { ConfirmButton, RoleBadge } from "@/components/platform/badges";
import { CopyBlock, OneTimeSecret } from "@/components/platform/oneTimeSecret";
import { tokenState } from "@/components/platform/platform.utils";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateApiTokenMutation, useListApiTokensQuery, useRevokeApiTokenMutation } from "@/lib/store/apis/platformApi";
import type { ApiToken, ApiTokenScope, Role } from "@/lib/types/bench";
import { KeyRound } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import "@/app/agents/views/w4m1I18n";
import { RoleSelect, SettingsSection, TableFrame, When, grantableRoles } from "./settingsBits";

/** Where ingest tokens live: they are not access tokens and are listed on the Inbox page (p02-7). */
export function IngestTokensNote() {
	const { t } = useTranslation("w4m1");
	return (
		<p className="text-muted-foreground text-xs" data-testid="tokens-ingest-note">
			<a href="/inbox" className="text-primary underline-offset-4 hover:underline">
				{t("tokens.ingestAccess")}
			</a>
		</p>
	);
}

/** Expiry choices in days; 0 never expires. */
export const TOKEN_EXPIRY_DAYS = ["7", "30", "90", "365", "0"] as const;
type ExpiryChoice = (typeof TOKEN_EXPIRY_DAYS)[number];

/** Scope choices, narrowest first (auth.Scopes). */
export const TOKEN_SCOPES: readonly ApiTokenScope[] = ["read", "evaluate", "admin"];

/** The widest scope a token holds; tokens made before scopes act as admin, limited by role alone. */
export function tokenScope(token: Pick<ApiToken, "scopes">): ApiTokenScope {
	const held = token.scopes ?? [];
	if (held.length === 0) return "admin";
	for (const scope of [...TOKEN_SCOPES].reverse()) if (held.includes(scope)) return scope;
	return "read";
}

/** The example request shown under a new token: the public API v1 (docs/api-v1.md). */
export function tokenUsage(origin: string, secret: string): string {
	return `curl -H "Authorization: Bearer ${secret}" ${origin}${PUBLIC_API_BASE}/evaluations`;
}

/** The public, versioned API every token works with (handlers/apiv1.go). */
export const PUBLIC_API_BASE = "/api/v1";

const STATE_VARIANT = { active: "success", revoked: "outline", expired: "warning" } as const;

/** API tokens for CI and scripts: create with a name, role and expiry (shown once), list, revoke. */
export function ApiTokensTab() {
	const { t } = useTranslation("platform");
	const session = useSession();
	const tokens = useListApiTokensQuery();
	const [revoke, revokeState] = useRevokeApiTokenMutation();
	const isAdmin = canUse(session, "admin");
	const list = tokens.data?.tokens ?? [];

	return (
		<div className="flex flex-col gap-10" data-testid="settings-tokens">
			<RoleGate min="admin">
				<CreateTokenSection />
			</RoleGate>
			<SettingsSection title={t("settings.tokens.title")} description={t("settings.tokens.description")} testId="settings-tokens-list">
				{tokens.isLoading ? (
					<BlockSkeleton rows={3} testId="tokens-loading" />
				) : tokens.isError || !tokens.data ? (
					<LoadError
						title={t("settings.tokens.loadError")}
						error={tokens.error}
						onRetry={() => void tokens.refetch()}
						testId="tokens-error"
					/>
				) : list.length === 0 ? (
					<EmptyNote testId="tokens-empty">{t("settings.tokens.empty")}</EmptyNote>
				) : (
					<TableFrame testId="tokens-table">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>{t("settings.tokens.name")}</TableHead>
									<TableHead>{t("settings.tokens.prefix")}</TableHead>
									<TableHead>{t("settings.tokens.role")}</TableHead>
									<TableHead>{t("settings.tokens.scope")}</TableHead>
									<TableHead>{t("settings.tokens.createdAt")}</TableHead>
									<TableHead>{t("settings.tokens.lastUsed")}</TableHead>
									<TableHead>{t("settings.tokens.expires")}</TableHead>
									<TableHead>{t("settings.tokens.status")}</TableHead>
									{isAdmin && <TableHead className="text-right" />}
								</TableRow>
							</TableHeader>
							<TableBody>
								{list.map((token: ApiToken) => {
									const state = tokenState(token);
									return (
										<TableRow key={token.id} data-testid={`token-row-${token.id}`} data-state={state}>
											<TableCell className="text-foreground font-medium">{token.name}</TableCell>
											<TableCell className="text-muted-foreground font-mono text-xs">hm_{token.prefix}…</TableCell>
											<TableCell>
												<RoleBadge role={token.role} />
											</TableCell>
											<TableCell>
												<Badge variant="outline" data-testid={`token-scope-${token.id}`}>
													{t(`settings.tokens.scopeOptions.${tokenScope(token)}`)}
												</Badge>
											</TableCell>
											<TableCell className="text-muted-foreground">
												<When value={token.created_at} />
											</TableCell>
											<TableCell className="text-muted-foreground">
												<When value={token.last_used_at} />
											</TableCell>
											<TableCell className="text-muted-foreground">
												<When value={token.expires_at} empty={t("settings.tokens.expiryOptions.0")} />
											</TableCell>
											<TableCell>
												<Badge variant={STATE_VARIANT[state]} className={state === "revoked" ? "text-muted-foreground" : undefined}>
													{t(`settings.tokens.${state}`)}
												</Badge>
											</TableCell>
											{isAdmin && (
												<TableCell className="text-right">
													{state === "active" && (
														<ConfirmButton
															label={t("settings.tokens.revoke")}
															question={t("settings.tokens.confirmRevoke", { name: token.name })}
															confirmLabel={t("settings.tokens.confirm")}
															cancelLabel={t("settings.tokens.cancel")}
															onConfirm={() => void revoke(token.id)}
															isLoading={revokeState.isLoading && revokeState.originalArgs === token.id}
															testId={`token-revoke-${token.id}`}
														/>
													)}
												</TableCell>
											)}
										</TableRow>
									);
								})}
							</TableBody>
						</Table>
					</TableFrame>
				)}
				<IngestTokensNote />
				{revokeState.error &&
					(isRoleRefusal(session, revokeState.error) ? (
						<ForbiddenNote min="admin" error={revokeState.error} testId="token-revoke-error" />
					) : (
						<p role="alert" className="text-chart-error-ink text-xs" data-testid="token-revoke-error">
							{t("settings.tokens.revokeError")}: {getBenchErrorMessage(revokeState.error)}
						</p>
					))}
			</SettingsSection>
		</div>
	);
}

function CreateTokenSection() {
	const { t } = useTranslation("platform");
	const session = useSession();
	const formId = useId();
	const roles = grantableRoles(session.role);
	const [name, setName] = useState("");
	const [role, setRole] = useState<Role>(roles.includes("member") ? "member" : (roles[0] ?? "viewer"));
	const [expiry, setExpiry] = useState<ExpiryChoice>("90");
	const [scope, setScope] = useState<ApiTokenScope>("evaluate");
	const [touched, setTouched] = useState(false);
	const [created, setCreated] = useState<{ name: string; secret: string } | null>(null);
	const [create, createState] = useCreateApiTokenMutation();

	const nameError = touched && !name.trim() ? t("settings.tokens.nameRequired") : undefined;
	const onSubmit = async (event: FormEvent) => {
		event.preventDefault();
		setTouched(true);
		if (!name.trim()) return;
		const days = Number(expiry);
		try {
			const result = await create({ name: name.trim(), role, scopes: [scope], ...(days > 0 ? { expires_in_days: days } : {}) }).unwrap();
			setCreated({ name: result.api_token?.name ?? name.trim(), secret: result.token });
			setName("");
			setTouched(false);
		} catch {
			// createState.error renders the failure.
		}
	};

	if (created) {
		const origin = typeof window === "undefined" ? "" : window.location.origin;
		return (
			<OneTimeSecret
				title={t("settings.tokens.created", { name: created.name })}
				secret={created.secret}
				secretLabel={t("settings.tokens.prefix")}
				onDone={() => setCreated(null)}
				testId="token-created"
			>
				<p className="text-muted-foreground text-xs">{t("settings.tokens.usage")}</p>
				<CopyBlock value={tokenUsage(origin, created.secret)} label={t("settings.tokens.usage")} testId="token-created-usage" />
			</OneTimeSecret>
		);
	}

	return (
		<SettingsSection
			title={t("settings.tokens.createTitle")}
			description={t("settings.tokens.createDescription", { org: session.org?.name ?? "" })}
			testId="settings-tokens-create"
		>
			<form
				className="bg-muted/30 grid gap-3 rounded-lg border p-4 sm:grid-cols-[minmax(0,1fr)_auto_auto_auto_auto] sm:items-end"
				onSubmit={(event) => void onSubmit(event)}
				noValidate
				data-testid="token-form"
			>
				<div className="flex min-w-0 flex-col gap-1.5">
					<label htmlFor={`${formId}-name`} className="text-[13px] font-medium">
						{t("settings.tokens.name")}
					</label>
					<Input
						id={`${formId}-name`}
						autoComplete="off"
						placeholder={t("settings.tokens.namePlaceholder")}
						value={name}
						onChange={(event) => setName(event.target.value)}
						aria-invalid={Boolean(nameError) || undefined}
						data-testid="token-name"
					/>
					{nameError && <span className="text-chart-error-ink text-xs">{nameError}</span>}
				</div>
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">{t("settings.tokens.role")}</span>
					<RoleSelect value={role} onChange={setRole} roles={roles} label={t("settings.tokens.role")} testId="token-role" size="default" />
				</div>
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">{t("settings.tokens.scope")}</span>
					<Select value={scope} onValueChange={(value) => setScope(value as ApiTokenScope)}>
						<SelectTrigger className="min-w-32" aria-label={t("settings.tokens.scope")} data-testid="token-scope">
							<SelectValue />
						</SelectTrigger>
						<SelectContent position="popper">
							{TOKEN_SCOPES.map((value) => (
								<SelectItem key={value} value={value}>
									{t(`settings.tokens.scopeOptions.${value}`)}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">{t("settings.tokens.expiry")}</span>
					<Select value={expiry} onValueChange={(value) => setExpiry(value as ExpiryChoice)}>
						<SelectTrigger className="min-w-32" aria-label={t("settings.tokens.expiry")} data-testid="token-expiry">
							<SelectValue />
						</SelectTrigger>
						<SelectContent position="popper">
							{TOKEN_EXPIRY_DAYS.map((days) => (
								<SelectItem key={days} value={days}>
									{t(`settings.tokens.expiryOptions.${days}`)}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
				<Button type="submit" className="h-9 gap-1.5" isLoading={createState.isLoading} dataTestId="token-submit">
					<KeyRound className="size-4" aria-hidden="true" />
					{t("settings.tokens.create")}
				</Button>
			</form>
			{createState.error &&
				(isRoleRefusal(session, createState.error) ? (
					<ForbiddenNote min="admin" error={createState.error} testId="token-create-error" />
				) : (
					<p role="alert" className="text-chart-error-ink text-xs" data-testid="token-create-error">
						{t("settings.tokens.createError")}: {getBenchErrorMessage(createState.error)}
					</p>
				))}
		</SettingsSection>
	);
}