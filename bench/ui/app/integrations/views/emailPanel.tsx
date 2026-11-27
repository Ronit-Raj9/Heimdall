import { LoadError } from "@/components/runs/screenStates";
import "@/components/settings/w4m8I18n";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
	apiErrorMessage,
	useGetEmailSettingsQuery,
	usePutEmailSettingsMutation,
	useTestEmailMutation,
} from "@/lib/store/apis/integrationsApi";
import type { EmailEventType, EmailSettings } from "@/lib/types/integrations";
import { Mail } from "lucide-react";
import { useEffect, useState } from "react";
import { canRetry } from "@/lib/requestErrors";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import { useTranslation } from "react-i18next";

/** The caller's own email notifications: weekly digest and per-event email (handlers/notifications.go). */
export function EmailPanel() {
	const { t } = useTranslation("integrations");
	const { t: tm } = useTranslation("w4m8");
	const { data, error, isLoading, refetch } = useGetEmailSettingsQuery();
	const [save, saving] = usePutEmailSettingsMutation();
	const [sendTest, testing] = useTestEmailMutation();
	const [events, setEvents] = useState<EmailEventType[]>([]);
	const [digest, setDigest] = useState(true);
	const [note, setNote] = useState<{ ok: boolean; text: string } | null>(null);

	useEffect(() => {
		if (!data) return;
		setEvents(data.prefs.events);
		setDigest(data.prefs.digest);
	}, [data]);
	// Leaving with an unsaved choice asks first (M8-44).
	useLeaveGuard(data ? !sameChoice(data, events, digest) : false);

	if (isLoading) return <Skeleton className="h-40 w-full" />;
	if (error || !data) {
		return (
			<LoadError
				title={tm("loadErrors.email")}
				error={error}
				onRetry={canRetry(error) ? () => void refetch() : undefined}
				testId="email-load-error"
			/>
		);
	}
	return (
		<EmailForm
			settings={data}
			events={events}
			digest={digest}
			dirty={!sameChoice(data, events, digest)}
			saving={saving.isLoading}
			testing={testing.isLoading}
			note={note}
			onEvents={setEvents}
			onDigest={setDigest}
			onSave={async () => {
				setNote(null);
				const res = await save({ events, digest });
				setNote(
					"error" in res ? { ok: false, text: apiErrorMessage(res.error, t("errors.generic")) } : { ok: true, text: t("email.saved") },
				);
			}}
			onTest={async () => {
				setNote(null);
				const res = await sendTest();
				if ("error" in res) setNote({ ok: false, text: apiErrorMessage(res.error, t("errors.generic")) });
				else if (res.data)
					setNote({
						ok: true,
						text: res.data.delivers ? t("email.testSent", { to: res.data.to }) : t("email.testLogged", { to: res.data.to }),
					});
			}}
		/>
	);
}

/** Whether the form still shows the saved choice. */
export function sameChoice(s: EmailSettings, events: readonly EmailEventType[], digest: boolean): boolean {
	return s.prefs.digest === digest && s.prefs.events.length === events.length && s.prefs.events.every((e) => events.includes(e));
}

export interface EmailFormProps {
	settings: EmailSettings;
	events: EmailEventType[];
	digest: boolean;
	dirty: boolean;
	saving?: boolean;
	testing?: boolean;
	note?: { ok: boolean; text: string } | null;
	onEvents(next: EmailEventType[]): void;
	onDigest(next: boolean): void;
	onSave(): void;
	onTest(): void;
}

/** The email choice form (presentational, for tests). */
export function EmailForm({ settings, events, digest, dirty, saving, testing, note, onEvents, onDigest, onSave, onTest }: EmailFormProps) {
	const { t } = useTranslation("integrations");
	return (
		<section className="border-border bg-card flex flex-col gap-4 rounded-xl border p-5" data-testid="email-settings">
			<div className="flex items-start gap-3">
				<span className="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg">
					<Mail className="size-4.5" aria-hidden="true" />
				</span>
				<div className="flex flex-col gap-0.5">
					<h2 className="text-base font-semibold">{t("email.heading")}</h2>
					{settings.available && <p className="text-muted-foreground text-sm">{t("email.intro", { email: settings.email })}</p>}
				</div>
			</div>
			{!settings.available ? (
				<p className="text-muted-foreground text-sm" data-testid="email-unavailable">
					{t("email.unavailable", { reason: settings.reason ?? "" })}
				</p>
			) : (
				<>
					{!settings.delivers && (
						<p className="border-chart-warning/60 bg-chart-warning/10 rounded-md border p-3 text-sm" data-testid="email-not-delivering">
							{t("email.notDelivering")}
						</p>
					)}
					<label className="flex items-center gap-2 text-sm">
						<input type="checkbox" checked={digest} onChange={(e) => onDigest(e.target.checked)} data-testid="email-digest" />
						{t("email.digest")}
					</label>
					<fieldset className="flex flex-col gap-2 text-sm">
						<legend className="text-muted-foreground mb-1 text-xs font-medium">{t("email.events")}</legend>
						{settings.events.map((ev) => (
							<label key={ev} className="flex items-center gap-2">
								<input
									type="checkbox"
									checked={events.includes(ev)}
									onChange={(e) => onEvents(e.target.checked ? [...events, ev] : events.filter((x) => x !== ev))}
									data-testid={`email-event-${ev}`}
								/>
								{t(`webhooks.eventNames.${ev}`)}
							</label>
						))}
					</fieldset>
					<div className="flex flex-wrap items-center gap-2">
						<Button size="sm" onClick={onSave} disabled={!dirty || saving} data-testid="email-save">
							{saving ? t("email.saving") : t("email.save")}
						</Button>
						<Button size="sm" variant="outline" onClick={onTest} disabled={testing} data-testid="email-test">
							{t("email.test")}
						</Button>
						{note && (
							<span
								className={note.ok ? "text-chart-success-ink text-xs" : "text-chart-error-ink text-xs"}
								role={note.ok ? "status" : "alert"}
								data-testid="email-note"
							>
								{note.text}
							</span>
						)}
					</div>
				</>
			)}
		</section>
	);
}