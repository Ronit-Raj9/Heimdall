import { pageWidthClass } from "@/components/shell/pageWidth";
import { RESULT_TONE } from "@/components/gateway/gatewayEffectCard";
import { type DestructiveConfirmOptions, useConfirm } from "@/components/platform/confirmDialog";
import { BlockSkeleton, InlineError, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { type StatusTone, StatusPill } from "@/components/ui/statusPill";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetGatewayReportCardQuery, useRemoveGatewaySourceMutation, useSetGatewaySourceMutation } from "@/lib/store/apis/gatewayApi";
import { useListSecretsQuery } from "@/lib/store/apis/secretsApi";
import type { GwCardLine, GwGatewayInfo, GwSourceInfo } from "@/lib/types/gateway";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { FeatureTestDialog } from "./featureTestDialog";
import { ModelWatchCard } from "./modelWatchCard";
import { RegisteredAgents } from "./registeredAgents";
import { SkillImportAction } from "./skillImport";
import "./uf7cI18n";
import "./w4p6I18n";

// The shared status pill: On green, Off and Not known neutral grey.
export const IN_USE_TONE: Record<GwCardLine["enabled"], StatusTone> = {
	on: "ok",
	off: "neutral",
	unknown: "neutral",
};

/** Features whose test reads your own Bifrost (its prompt repository or its skills); the others use the built-in gateway. */
export const NEEDS_SOURCE: ReadonlySet<string> = new Set(["prompt_version", "skill_version"]);

/** Lines Heimdall cannot test yet, shown under "Coming later". */
export const COMING_LATER: ReadonlySet<string> = new Set(["batch"]);

/** A version the gateway really reported: "unknown" and "vunknown" mean none. Pure. */
export function reportedVersion(version: string | undefined): string {
	const v = (version ?? "").trim();
	return v === "" || /^v?unknown$/i.test(v) ? "" : v;
}

/** The header line: the built-in gateway's host and version, or why it could not be reached. Pure. */
export function GatewayHeaderLine({ gateway }: { gateway: GwGatewayInfo }) {
	const { t } = useTranslation("w4p6");
	const { t: u } = useTranslation("uf7c");
	if (!gateway.reachable) {
		return (
			<p className="text-chart-warning-ink text-sm" role="alert" data-testid="gateway-unreachable">
				{t("page.unreachable", { error: gateway.error ?? "" })}
			</p>
		);
	}
	const version = reportedVersion(gateway.version);
	return (
		<p className="text-sm" data-testid="gateway-header">
			{version ? u("page.builtIn", { version, host: gateway.host ?? "" }) : u("page.builtInNoVersion", { host: gateway.host ?? "" })}
		</p>
	);
}

/** What a feature test is, and the three first steps. Pure. */
export function FeatureTestHow() {
	const { t: u } = useTranslation("uf7c");
	return (
		<div className="bg-muted/40 flex flex-col gap-2 rounded-xl p-4 text-sm" data-testid="gateway-how">
			<h2 className="text-base font-medium">{u("page.howTitle")}</h2>
			<p>{u("page.howDef")}</p>
			<ol className="list-decimal pl-5">
				<li>{u("page.how1")}</li>
				<li>{u("page.how2")}</li>
				<li>{u("page.how3")}</li>
			</ol>
		</div>
	);
}

/** One feature line: name, what it checks, in use state, result, headline, where it comes from, buttons. Pure. */
export function GatewayCardLineRow({
	line,
	onStart,
	connected = true,
}: {
	line: GwCardLine;
	onStart(line: GwCardLine): void;
	connected?: boolean;
}) {
	const { t } = useTranslation("w4p6");
	const { t: u } = useTranslation("uf7c");
	const what = u(`line.what.${line.feature}` as "line.what.batch", { defaultValue: "" });
	const blocked = !connected && NEEDS_SOURCE.has(line.feature);
	const notTested = line.state === "not_measured" && !line.result;
	const from = line.source
		? line.measured_at
			? t("line.from", { source: line.source.text, date: line.measured_at.slice(0, 10) })
			: t("line.fromNoDate", { source: line.source.text })
		: "";
	return (
		<li
			className="grid grid-cols-[1fr_auto] gap-x-3 gap-y-1 py-3 text-sm"
			data-testid={`gateway-line-${line.feature}`}
			data-state={line.state}
		>
			<div className="flex min-w-0 flex-col gap-1">
				<div className="flex flex-wrap items-center gap-2">
					<h3 className="font-medium">{line.name}</h3>
					{connected && (
						<StatusPill tone={IN_USE_TONE[line.enabled] ?? IN_USE_TONE.unknown} testId={`gateway-line-${line.feature}-inuse`}>
							{u(`line.state.${line.enabled}`)}
						</StatusPill>
					)}
					{notTested && (
						<span className="bg-muted text-muted-foreground rounded-full px-2 py-0.5 text-xs" data-testid={`gateway-line-${line.feature}-nottested`}>
							{u("line.notTested")}
						</span>
					)}
					{line.result && <span className={cn("font-medium", RESULT_TONE[line.result])}>{t(`line.result.${line.result}`)}</span>}
				</div>
				{what && <p className="text-muted-foreground">{what}</p>}
				{connected && line.enabled === "unknown" && line.enabled_source && (
					<p className="text-muted-foreground text-xs" data-testid={`gateway-line-${line.feature}-why`}>
						{t("line.unknown", { reason: line.enabled_source })}
					</p>
				)}
				{!notTested && <p>{line.state === "not_enough_data" && !line.headline ? t("line.notEnough") : line.headline}</p>}
				{from && <p className="text-muted-foreground text-xs">{from}</p>}
				{line.feature === "skill_version" && <SkillImportAction />}
			</div>
			<div className="flex flex-col items-end gap-1">
				{line.exp_id && (
					<Link to="/experiments/$expId" params={{ expId: line.exp_id }} className="text-primary text-sm hover:underline">
						{u("line.seeResult")}
					</Link>
				)}
				{line.start && (
					<Button
						type="button"
						size="sm"
						variant="outline"
						disabled={blocked}
						onClick={() => onStart(line)}
						data-testid={`gateway-line-${line.feature}-start`}
					>
						{u("line.start")}
					</Button>
				)}
				{line.start && blocked && (
					<span className="text-muted-foreground text-xs" data-testid={`gateway-line-${line.feature}-blocked`}>
						{u("page.connectFirst")}
					</span>
				)}
			</div>
		</li>
	);
}

/** The feature lines, with one line on top when none is measured yet and the lines Heimdall cannot test under "Coming later". Pure. */
export function GatewayLines({ lines, onStart, connected }: { lines: GwCardLine[]; onStart(line: GwCardLine): void; connected: boolean }) {
	const { t: u } = useTranslation("uf7c");
	const now = lines.filter((l) => !COMING_LATER.has(l.feature));
	const later = lines.filter((l) => COMING_LATER.has(l.feature));
	const noneMeasured = now.every((l) => l.state === "not_measured" && !l.result);
	return (
		<section className="flex flex-col gap-2" aria-labelledby="gateway-features-title">
			<h2 id="gateway-features-title" className="text-base font-medium">
				{u("page.featuresTitle")}
			</h2>
			{noneMeasured && (
				<p className="text-muted-foreground text-sm" data-testid="gateway-none-measured">
					{u("line.noneMeasured")}
				</p>
			)}
			<ul className="flex flex-col divide-y" data-testid="gateway-lines">
				{now.map((l) => (
					<GatewayCardLineRow key={l.feature} line={l} onStart={onStart} connected={connected} />
				))}
			</ul>
			{later.length > 0 && (
				<>
					<h3 className="text-muted-foreground mt-2 text-sm font-medium">{u("page.comingLater")}</h3>
					<ul className="flex flex-col divide-y" data-testid="gateway-lines-later">
						{later.map((l) => (
							<GatewayCardLineRow key={l.feature} line={{ ...l, start: undefined }} onStart={onStart} connected={connected} />
						))}
					</ul>
				</>
			)}
		</section>
	);
}

/** The source box: connected host with "Change" (admin), or the connect form. */
export function SourceBox({ source, admin }: { source: GwSourceInfo; admin: boolean }) {
	const { t } = useTranslation("w4p6");
	const { t: u } = useTranslation("uf7c");
	const [editing, setEditing] = useState(false);
	const [url, setUrl] = useState("");
	const [secret, setSecret] = useState("");
	const [error, setError] = useState<unknown>(null);
	const ask = useConfirm();
	const secrets = useListSecretsQuery(undefined, { skip: !admin || !(editing || !source.connected) });
	const [save, saving] = useSetGatewaySourceMutation();
	const [remove, removing] = useRemoveGatewaySourceMutation();
	const showForm = admin && (editing || !source.connected);
	const onSave = async () => {
		setError(null);
		try {
			await save({ gateway_url: url.trim(), credential_secret: secret }).unwrap();
			setEditing(false);
		} catch (e) {
			setError(e);
		}
	};
	const onRemove = async () => {
		const opts: DestructiveConfirmOptions = {
			destructive: true,
			title: u("page.removeTitle", { host: source.host ?? "" }),
			confirmLabel: u("page.removeYes"),
		};
		if (!(await ask(u("page.removeQuestion"), opts))) return;
		setError(null);
		try {
			await remove().unwrap();
			setEditing(false);
		} catch (e) {
			setError(e);
		}
	};
	return (
		<section className="flex flex-col gap-2 text-sm" aria-labelledby="gateway-connect-title" data-testid="gateway-source">
			<h2 id="gateway-connect-title" className="text-base font-medium">
				{u("page.connectTitle")}
			</h2>
			{source.connected ? (
				<div className="flex flex-wrap items-center gap-2">
					<span>{t("source.connected", { host: source.host })}</span>
					{admin && !editing && (
						<Button type="button" size="sm" variant="outline" onClick={() => setEditing(true)}>
							{t("source.change")}
						</Button>
					)}
				</div>
			) : (
				<p className="font-medium">{t("source.connect")}</p>
			)}
			{source.error && (
				<p className="text-chart-warning-ink" role="alert">
					{source.error}
				</p>
			)}
			{!admin && !source.connected && <p className="text-muted-foreground text-xs">{t("source.adminOnly")}</p>}
			{showForm && (
				<div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-start">
					<label className="flex w-full flex-col gap-1.5 text-sm leading-5 sm:w-80">
						{t("source.address")}
						<Input
							value={url}
							placeholder={u("page.addressPlaceholder")}
							onChange={(e) => setUrl(e.target.value)}
							data-testid="gateway-source-url"
						/>
					</label>
					<div className="flex w-full flex-col gap-1.5 text-sm leading-5 sm:w-auto">
						<label className="flex flex-col gap-1.5">
						{t("source.secret")}
						<Select value={secret || undefined} onValueChange={setSecret}>
							<SelectTrigger className="w-full sm:w-56" data-testid="gateway-source-secret">
								<SelectValue placeholder={t("source.pickSecret")} />
							</SelectTrigger>
							<SelectContent>
								{(secrets.data?.secrets ?? []).map((s) => (
									<SelectItem key={s.id} value={s.name}>
										{s.name}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
						</label>
						<Link to="/settings" search={{ tab: "secrets" } as never} className="text-primary w-fit text-xs hover:underline">
							{u("page.addSecret")}
						</Link>
					</div>
					<div className="flex gap-2 sm:pt-[26px]">
						<Button type="button" size="sm" disabled={saving.isLoading || !url.trim() || !secret} onClick={() => void onSave()}>
							{t("source.save")}
						</Button>
						{source.connected && (
							<Button type="button" size="sm" variant="ghost" onClick={() => setEditing(false)}>
								{t("source.cancel")}
							</Button>
						)}
					</div>
				</div>
			)}
			{admin && source.connected && editing && (
				<div className="border-t pt-2">
					<Button
						type="button"
						size="sm"
						variant="destructive"
						isLoading={removing.isLoading}
						onClick={() => void onRemove()}
						dataTestId="gateway-source-remove"
					>
						{t("source.remove")}
					</Button>
				</div>
			)}
			<InlineError error={error} testId="gateway-source-error" />
		</section>
	);
}

/** "Send production traces": where traces from Bifrost go in Heimdall. Pure. */
export function TraceIngestLine() {
	const { t: u } = useTranslation("uf7c");
	return (
		<p className="text-sm" data-testid="gateway-traces">
			<span className="font-medium">{u("page.tracesTitle")}: </span>
			<Link to="/agents/from-traces" className="text-primary hover:underline">
				{u("page.tracesAgents")}
			</Link>
			{", "}
			<Link to="/failures" className="text-primary hover:underline">
				{u("page.tracesInbox")}
			</Link>
		</p>
	);
}

/** /gateway: the Gateway report card. */
export default function GatewayView() {
	const { t } = useTranslation("w4p6");
	const session = useSession();
	const q = useGetGatewayReportCardQuery();
	const [starting, setStarting] = useState<GwCardLine | null>(null);
	return (
		<section className={cn(pageWidthClass("wide"), "flex flex-col gap-6")} data-testid="gateway-screen">
			<PageHeader title={t("page.title")} subtitle={t("page.subtitle")} />
			{q.isLoading ? (
				<BlockSkeleton rows={6} testId="gateway-loading" />
			) : q.error || !q.data ? (
				<LoadError title={t("page.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="gateway-error" />
			) : (
				<>
					<FeatureTestHow />
					<GatewayHeaderLine gateway={q.data.gateway} />
					<SourceBox source={q.data.source} admin={canUse(session, "admin")} />
					<GatewayLines lines={q.data.lines} onStart={setStarting} connected={q.data.source.connected} />
					<ModelWatchCard />
					<RegisteredAgents connected={q.data.source.connected} />
					<TraceIngestLine />
				</>
			)}
			{starting?.start && <FeatureTestDialog feature={starting.start} name={starting.name} open onClose={() => setStarting(null)} />}
		</section>
	);
}