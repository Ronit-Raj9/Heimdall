import { formatMeterValue, formatMoney, meterPercent, meterTone, planCards } from "@/components/billing/billing.utils";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useGetBillingQuery,
	useGetInvoicesQuery,
	useOpenPortalMutation,
	useStartCheckoutMutation,
	useSyncSeatsMutation,
} from "@/lib/store/apis/billingApi";
import type { BillingView, MeterView, Plan, SeatSync } from "@/lib/types/billing";
import { cn } from "@/lib/utils";
import { Check, CreditCard, ExternalLink, Info, Minus, RefreshCw } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { SettingsSection, TableFrame } from "./settingsBits";

/** ?checkout= after Stripe sends the visitor back. */
function checkoutReturn(): "done" | "cancelled" | null {
	if (typeof window === "undefined") return null;
	const v = new URLSearchParams(window.location.search).get("checkout");
	return v === "done" || v === "cancelled" ? v : null;
}

/** Settings, Billing: the plan, this month's meters against its limits, plans, invoices. */
export function BillingTab() {
	const { t } = useTranslation("billing");
	const billing = useGetBillingQuery();
	if (billing.isLoading) return <BlockSkeleton rows={5} testId="billing-loading" />;
	if (billing.isError || !billing.data) {
		return <LoadError title={t("loadError")} error={billing.error} onRetry={() => void billing.refetch()} testId="billing-error" />;
	}
	return <BillingBody view={billing.data} />;
}

function Notice({ children, tone = "info", testId }: { children: React.ReactNode; tone?: "info" | "ok"; testId: string }) {
	return (
		<p
			className={cn(
				"flex items-start gap-2 rounded-lg border px-3.5 py-2.5 text-[13px] leading-relaxed",
				tone === "ok" ? "border-chart-success/30 bg-chart-success/5" : "bg-muted/40 border-border/80",
			)}
			data-testid={testId}
		>
			<Info className="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<span>{children}</span>
		</p>
	);
}

export function BillingBody({ view }: { view: BillingView }) {
	const { t } = useTranslation("billing");
	const session = useSession();
	const isAdmin = canUse(session, "admin");
	const [checkout, checkoutState] = useStartCheckoutMutation();
	const [portal, portalState] = useOpenPortalMutation();
	const [sync, syncState] = useSyncSeatsMutation();
	const [synced, setSynced] = useState<SeatSync | null>(null);
	const returned = checkoutReturn();
	const paid = view.plan.id === "team" && view.status !== "manual" && view.status !== "";

	const go = async (start: () => { unwrap(): Promise<{ url: string }> }) => {
		try {
			const { url } = await start().unwrap();
			if (url.startsWith("https://") || url.startsWith("http://")) window.location.assign(url);
		} catch {
			// The mutation state renders the failure.
		}
	};
	const onSync = async () => {
		try {
			setSynced(await sync().unwrap());
		} catch {
			// syncState.error renders it.
		}
	};
	const failure = checkoutState.error ?? portalState.error ?? syncState.error;

	return (
		<div className="flex flex-col gap-10" data-testid="billing-tab">
			<div className="flex flex-col gap-2">
				{!view.enabled && <Notice testId="billing-off">{t("off")}</Notice>}
				{view.enabled && view.stripe_mode === "test" && <Notice testId="billing-test-mode">{t("testMode")}</Notice>}
				{returned === "done" && (
					<Notice tone="ok" testId="billing-checkout-done">
						{t("checkoutDone")}
					</Notice>
				)}
				{returned === "cancelled" && <Notice testId="billing-checkout-cancelled">{t("checkoutCancelled")}</Notice>}
			</div>

			<SettingsSection
				title={t("plan.title")}
				description={t("plan.ownKey")}
				testId="billing-plan"
				actions={
					<div className="flex flex-wrap items-center gap-2">
						{/* With billing off there is nothing to buy, so no Upgrade (a disabled one only puzzled). */}
						{view.enabled && view.plan.id === "free" && (
							<Button
								size="sm"
								onClick={() => void go(checkout)}
								disabled={!isAdmin || !view.checkout_ready}
								isLoading={checkoutState.isLoading}
								title={!isAdmin ? t("actions.adminOnly") : view.checkout_blocker}
								data-testid="billing-upgrade"
							>
								<CreditCard className="size-4" aria-hidden="true" />
								{t("actions.upgrade")}
							</Button>
						)}
						{view.portal_ready && isAdmin && (
							<Button
								size="sm"
								variant="outline"
								onClick={() => void go(portal)}
								isLoading={portalState.isLoading}
								data-testid="billing-portal"
							>
								<ExternalLink className="size-4" aria-hidden="true" />
								{t("actions.manage")}
							</Button>
						)}
						{paid && isAdmin && view.checkout_ready && (
							<Button
								size="sm"
								variant="outline"
								onClick={() => void onSync()}
								isLoading={syncState.isLoading}
								data-testid="billing-sync-seats"
							>
								<RefreshCw className="size-4" aria-hidden="true" />
								{t("actions.syncSeats")}
							</Button>
						)}
					</div>
				}
			>
				<div className="bg-card grid gap-4 rounded-xl border p-5 sm:grid-cols-[minmax(0,1fr)_auto]" data-testid="billing-current">
					<div className="flex min-w-0 flex-col gap-1">
						<div className="flex items-baseline gap-3">
							<span className="text-foreground text-xl font-semibold tracking-tight" data-testid="billing-plan-name">
								{view.plan.name}
							</span>
							<span className="text-muted-foreground text-sm">{priceLabel(view.plan, t)}</span>
						</div>
						<p className="text-muted-foreground text-[13px]">{view.plan.summary}</p>
					</div>
					<dl className="grid grid-cols-[auto_auto] gap-x-6 gap-y-1 text-[13px]">
						<dt className="text-muted-foreground">{t("plan.status")}</dt>
						<dd data-testid="billing-status">{t(`status.${view.status || "none"}`, { defaultValue: view.status })}</dd>
						{view.paid_seats > 0 && (
							<>
								<dt className="text-muted-foreground">{t("plan.seats")}</dt>
								<dd className="tabular-nums">{view.paid_seats}</dd>
							</>
						)}
						{view.period_end && (
							<>
								<dt className="text-muted-foreground">{t("plan.renews")}</dt>
								<dd className="tabular-nums">{view.period_end.slice(0, 10)}</dd>
							</>
						)}
					</dl>
				</div>
				{failure && (
					<p role="alert" className="text-chart-error-ink text-xs" data-testid="billing-action-error">
						{getBenchErrorMessage(failure)}
					</p>
				)}
				{synced && (
					<p className="text-muted-foreground text-xs" data-testid="billing-seats-synced">
						{synced.changed ? t("actions.seatsChanged", { count: synced.seats }) : t("actions.seatsSynced", { count: synced.members })}
					</p>
				)}
			</SettingsSection>

			<SettingsSection
				title={t("meters.title")}
				description={t("meters.description", { from: view.period_from, to: view.period_to })}
				testId="billing-meters"
			>
				<ul className="grid gap-3 sm:grid-cols-2">
					{view.meters.map((m) => (
						<MeterCard key={m.id} meter={m} enabled={view.enabled} />
					))}
				</ul>
			</SettingsSection>

			<SettingsSection title={t("features.title")} testId="billing-features">
				<ul className="grid gap-2 sm:grid-cols-2">
					{view.features.map((f) => (
						<li key={f.id} className="flex items-center gap-2 text-sm" data-testid={`billing-feature-${f.id}`} data-included={f.included}>
							{f.included ? (
								<Check className="text-chart-success-ink size-4" aria-label={t("features.included")} />
							) : (
								<Minus className="text-muted-foreground size-4" aria-label={t("features.notIncluded")} />
							)}
							<span className={f.included ? "" : "text-muted-foreground"}>{t(`features.${f.id}`)}</span>
						</li>
					))}
				</ul>
			</SettingsSection>

			<SettingsSection title={t("plans.title")} testId="billing-plans">
				<ul className="grid gap-3 md:grid-cols-3">
					{planCards(view.plans, view.plan.id).map(({ plan, current }) => (
						<PlanCard
							key={plan.id}
							plan={plan}
							current={current}
							contact={view.contact}
							canUpgrade={isAdmin && view.checkout_ready && view.plan.id === "free" && plan.id === "team"}
							onUpgrade={() => void go(checkout)}
						/>
					))}
				</ul>
			</SettingsSection>

			{isAdmin && view.checkout_ready && view.portal_ready && <Invoices />}
		</div>
	);
}

function priceLabel(plan: Plan, t: TFunction<"billing">): string {
	if (plan.contact) return t("plan.contact");
	if (plan.price_usd <= 0) return t("plan.free");
	return t("plan.perSeat", { price: plan.price_usd });
}

function MeterCard({ meter, enabled }: { meter: MeterView; enabled: boolean }) {
	const { t } = useTranslation("billing");
	const tone = meterTone(meter);
	const hint =
		meter.id === "seats"
			? t("meters.seatsHint")
			: meter.id === "shared_worker_minutes"
				? t("meters.minutesHint")
				: meter.id === "operator_judge_calls"
					? t("meters.judgeHint")
					: null;
	return (
		<li
			className="bg-card flex flex-col gap-2 rounded-xl border p-4"
			data-testid={`billing-meter-${meter.id}`}
			data-reached={meter.reached}
		>
			<div className="flex items-baseline justify-between gap-2">
				<span className="text-muted-foreground text-[13px]">{t(`meters.${meter.id}`)}</span>
				<span className="text-foreground text-sm font-medium tabular-nums">
					{!meter.available
						? t("meters.notAvailable")
						: meter.limit > 0
							? t("meters.of", { used: formatMeterValue(meter.unit, meter.used), limit: formatMeterValue(meter.unit, meter.limit) })
							: formatMeterValue(meter.unit, meter.used)}
				</span>
			</div>
			{meter.limit > 0 ? (
				<div
					className="bg-muted h-1.5 overflow-hidden rounded-full"
					role="progressbar"
					aria-label={t(`meters.${meter.id}`)}
					aria-valuemin={0}
					aria-valuemax={meter.limit}
					aria-valuenow={Math.min(meter.used, meter.limit)}
				>
					<div
						className={cn(
							"h-full rounded-full transition-[width] duration-500",
							!enabled
								? "bg-muted-foreground/40"
								: tone === "full"
									? "bg-chart-error"
									: tone === "warn"
										? "bg-chart-warning"
										: "bg-primary",
						)}
						style={{ width: `${meterPercent(meter)}%` }}
					/>
				</div>
			) : (
				<span className="text-muted-foreground text-xs">{t("meters.noLimit")}</span>
			)}
			{meter.reached && (
				<span
					className={cn("text-xs font-medium", enabled ? "text-chart-error-ink" : "text-muted-foreground")}
					data-testid={`billing-meter-${meter.id}-reached`}
				>
					{enabled ? t("meters.reached") : t("meters.reachedOff")}
				</span>
			)}
			{hint && <span className="text-muted-foreground text-xs leading-relaxed">{hint}</span>}
		</li>
	);
}

function PlanCard({
	plan,
	current,
	contact,
	canUpgrade,
	onUpgrade,
}: {
	plan: Plan;
	current: boolean;
	contact?: string;
	canUpgrade: boolean;
	onUpgrade(): void;
}) {
	const { t } = useTranslation("billing");
	const contactHref = contact ? (contact.includes("@") && !contact.startsWith("mailto:") ? `mailto:${contact}` : contact) : undefined;
	return (
		<li
			className={cn("bg-card flex flex-col gap-3 rounded-xl border p-5", current && "border-primary/60 ring-primary/20 ring-2")}
			data-testid={`billing-plan-card-${plan.id}`}
			data-current={current}
		>
			<div className="flex items-center justify-between gap-2">
				<span className="text-foreground font-semibold">{plan.name}</span>
				{current && (
					<span className="bg-primary/10 text-primary rounded-full px-2 py-0.5 text-[11px] font-medium">{t("plans.current")}</span>
				)}
			</div>
			<span className="text-foreground text-sm">{priceLabel(plan, t)}</span>
			<p className="text-muted-foreground text-[13px] leading-relaxed">{plan.summary}</p>
			<ul className="flex flex-col gap-1.5 text-[13px]">
				{[
					plan.max_seats > 0 ? t("plans.seats", { count: plan.max_seats }) : t("plans.anySeats"),
					plan.shared_worker_minutes > 0 ? t("plans.minutes", { count: plan.shared_worker_minutes }) : t("plans.anyMinutes"),
					t("plans.ownKey"),
					...plan.features.map((f) => t(`features.${f}`)),
				].map((line) => (
					<li key={line} className="flex items-start gap-2">
						<Check className="text-chart-success-ink mt-0.5 size-3.5 shrink-0" aria-label={t("features.included")} />
						<span>{line}</span>
					</li>
				))}
			</ul>
			<div className="mt-auto pt-1">
				{canUpgrade && (
					<Button size="sm" onClick={onUpgrade} data-testid={`billing-plan-upgrade-${plan.id}`}>
						{t("actions.upgrade")}
					</Button>
				)}
				{plan.contact && !current && contactHref && (
					<Button size="sm" variant="outline" asChild>
						<a href={contactHref} data-testid="billing-plan-contact">
							{t("actions.contact")}
						</a>
					</Button>
				)}
			</div>
		</li>
	);
}

function Invoices() {
	const { t } = useTranslation("billing");
	const list = useGetInvoicesQuery();
	return (
		<SettingsSection title={t("invoices.title")} testId="billing-invoices">
			{list.isLoading ? (
				<BlockSkeleton rows={2} testId="billing-invoices-loading" />
			) : list.isError || !list.data ? (
				<LoadError title={t("invoices.error")} error={list.error} onRetry={() => void list.refetch()} testId="billing-invoices-error" />
			) : list.data.invoices.length === 0 ? (
				<EmptyNote testId="billing-invoices-empty">{t("invoices.empty")}</EmptyNote>
			) : (
				<TableFrame>
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>{t("invoices.number")}</TableHead>
								<TableHead>{t("invoices.date")}</TableHead>
								<TableHead className="text-right">{t("invoices.amount")}</TableHead>
								<TableHead>{t("invoices.status")}</TableHead>
								<TableHead />
							</TableRow>
						</TableHeader>
						<TableBody>
							{list.data.invoices.map((inv) => (
								<TableRow key={inv.id} data-testid="billing-invoice-row">
									<TableCell className="font-mono text-xs">{inv.number || inv.id}</TableCell>
									<TableCell className="tabular-nums">{inv.created.slice(0, 10)}</TableCell>
									<TableCell className="text-right tabular-nums">{formatMoney(inv.amount_due, inv.currency)}</TableCell>
									<TableCell>{inv.status}</TableCell>
									<TableCell className="text-right">
										<span className="inline-flex gap-3 text-xs">
											{inv.hosted_url && (
												<a
													href={inv.hosted_url}
													target="_blank"
													rel="noreferrer"
													className="text-primary underline-offset-2 hover:underline"
												>
													{t("invoices.open")}
												</a>
											)}
											{inv.pdf_url && (
												<a href={inv.pdf_url} target="_blank" rel="noreferrer" className="text-primary underline-offset-2 hover:underline">
													{t("invoices.pdf")}
												</a>
											)}
										</span>
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				</TableFrame>
			)}
		</SettingsSection>
	);
}