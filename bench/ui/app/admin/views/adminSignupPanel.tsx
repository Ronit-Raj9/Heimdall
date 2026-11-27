import { useConfirm } from "@/components/platform/confirmDialog";
import { formatRelative } from "@/components/platform/platform.utils";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { getLocale } from "@/lib/i18n";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetSignupSettingsQuery, useSetSignupSettingsMutation } from "@/lib/store/apis/identityApi";
import type { SignupPolicy } from "@/lib/types/bench";
import type { SignupSettings } from "@/lib/types/identity";
import { cn } from "@/lib/utils";
import { Mail, ScrollText } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

export const SIGNUP_MODES: SignupPolicy[] = ["invite", "allowlist", "open", "closed"];

/** Splits the domains box (lines or commas) into trimmed, lowercase, de-duplicated entries. */
export function parseDomains(text: string): string[] {
	const out: string[] = [];
	for (const raw of text.split(/[\s,]+/)) {
		const d = raw.trim().toLowerCase().replace(/^@/, "");
		if (d && !out.includes(d)) out.push(d);
	}
	return out;
}

/** Server admin: who may sign up (auth/signupmode.go), and how verification links are delivered. */
export function AdminSignupPanel() {
	const { t } = useTranslation("identity");
	const q = useGetSignupSettingsQuery();
	if (q.isLoading) {
		return (
			<div className="flex flex-col gap-2" data-testid="admin-signup-loading">
				{[0, 1, 2].map((i) => (
					<div key={i} className="bg-muted h-12 animate-pulse rounded-lg" />
				))}
			</div>
		);
	}
	if (q.error || !q.data) {
		return (
			<EmptyState
				compact
				message={t("admin.loadFailed", { message: getBenchErrorMessage(q.error) })}
				action={{ label: t("admin.retry"), onClick: () => void q.refetch() }}
				testId="admin-signup-error"
			/>
		);
	}
	// Remount the form when the server's answer changes, so it starts from the saved state.
	return <SignupForm key={`${q.data.mode}|${q.data.domains.join(",")}|${q.data.source}`} settings={q.data} />;
}

/** The signup settings with the names the server adds for the admin who changed them (fix wave 3, L3). */
type NamedSignupSettings = SignupSettings & { updated_by_name?: string; updated_by_email?: string };

/** Who changed the signup settings: the name, never a raw user id. */
export function updaterName(settings: NamedSignupSettings): string {
	if (settings.updated_by_name) return settings.updated_by_name;
	const by = settings.updated_by ?? "";
	return by.startsWith("usr_") ? "" : by;
}

export function SignupForm({ settings }: { settings: SignupSettings }) {
	const { t } = useTranslation("identity");
	const named = settings as NamedSignupSettings;
	const confirm = useConfirm();
	const [save, state] = useSetSignupSettingsMutation();
	const [mode, setMode] = useState<SignupPolicy>(settings.mode);
	const [domainsText, setDomainsText] = useState(settings.domains.join("\n"));
	const [saved, setSaved] = useState(false);
	const domains = parseDomains(domainsText);
	const needDomains = mode === "allowlist" && domains.length === 0;
	const dirty = mode !== settings.mode || domains.join(",") !== settings.domains.join(",");

	const submit = async (body: { mode: SignupPolicy | ""; domains: string[] }) => {
		setSaved(false);
		if (body.mode === "open" && settings.mode !== "open" && !(await confirm(t("admin.confirmOpen")))) return;
		try {
			await save(body).unwrap();
			setSaved(true);
		} catch {
			// state.error renders the failure.
		}
	};
	const when = settings.updated_at ? (formatRelative(settings.updated_at, getLocale()) ?? "") : "";

	return (
		<div className="flex flex-col gap-6" data-testid="admin-signup">
			<Card>
				<CardHeader>
					<CardTitle>{t("admin.title")}</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-5">
					<div role="radiogroup" aria-label={t("admin.title")} className="grid grid-cols-1 gap-3 sm:grid-cols-2">
						{SIGNUP_MODES.map((m) => (
							<button
								key={m}
								type="button"
								role="radio"
								aria-checked={mode === m}
								onClick={() => setMode(m)}
								className={cn(
									"flex flex-col items-start gap-1 rounded-xl border p-4 text-left transition-colors",
									mode === m ? "border-primary bg-primary/5" : "hover:bg-muted/50",
								)}
								data-testid={`admin-signup-mode-${m}`}
							>
								<span className="text-foreground text-sm font-medium">{t(`admin.modes.${m}`)}</span>
								<span className="text-muted-foreground text-xs">{t(`admin.modeHelp.${m}`)}</span>
							</button>
						))}
					</div>
					{mode === "allowlist" && (
						<label className="flex flex-col gap-1.5">
							<span className="text-foreground text-sm font-medium">{t("admin.domains")}</span>
							<textarea
								value={domainsText}
								onChange={(e) => setDomainsText(e.target.value)}
								rows={3}
								spellCheck={false}
								placeholder="acme.com"
								className="border-input bg-background focus-visible:ring-ring rounded-md border px-3 py-2 font-mono text-sm focus-visible:ring-2 focus-visible:outline-none"
								data-testid="admin-signup-domains"
							/>
							<span className={cn("text-xs", needDomains ? "text-destructive" : "text-muted-foreground")}>
								{needDomains ? t("admin.domainsNeeded") : t("admin.domainsHint")}
							</span>
						</label>
					)}
					<p className="text-muted-foreground text-xs" title={named.updated_by_email || undefined} data-testid="admin-signup-source">
						{settings.source === "admin"
							? t("admin.sourceAdmin", { who: updaterName(named), when, mode: t(`admin.modes.${settings.config_mode}`) })
							: t("admin.sourceConfig")}
					</p>
					{state.error && <p className="text-destructive text-sm">{getBenchErrorMessage(state.error)}</p>}
					{saved && !state.error && (
						<p className="text-chart-success-ink text-sm" role="status" data-testid="admin-signup-saved">
							{t("admin.saved")}
						</p>
					)}
					<div className="flex flex-wrap gap-2">
						<Button
							disabled={!dirty || needDomains}
							isLoading={state.isLoading}
							onClick={() => void submit({ mode, domains: mode === "allowlist" ? domains : [] })}
							dataTestId="admin-signup-save"
						>
							{t("admin.save")}
						</Button>
						{settings.source === "admin" && (
							<Button
								variant="outline"
								disabled={state.isLoading}
								onClick={() => void submit({ mode: "", domains: [] })}
								dataTestId="admin-signup-reset"
							>
								{t("admin.reset")}
							</Button>
						)}
					</div>
				</CardContent>
			</Card>
			<Card data-testid="admin-signup-delivery" data-delivery={settings.email_delivery}>
				<CardContent className="flex items-start gap-3 pt-6">
					{settings.email_delivery === "email" ? (
						<Mail className="text-chart-success-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					) : (
						<ScrollText className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					)}
					<div className="flex min-w-0 flex-col gap-1 text-sm">
						<span className="text-foreground font-medium">{t("admin.delivery")}</span>
						<p className="text-muted-foreground">
							{settings.email_delivery === "email" ? t("admin.deliveryEmail") : t("admin.deliveryLog")}
						</p>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}