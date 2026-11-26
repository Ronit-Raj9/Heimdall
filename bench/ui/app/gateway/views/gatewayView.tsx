import { RESULT_TONE } from "@/components/gateway/gatewayEffectCard";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetGatewayReportCardQuery, useRemoveGatewaySourceMutation, useSetGatewaySourceMutation } from "@/lib/store/apis/gatewayApi";
import { errorMessage } from "@/lib/store/apis/schedulesApi";
import { useListSecretsQuery } from "@/lib/store/apis/secretsApi";
import type { GwCardLine, GwGatewayInfo, GwSourceInfo } from "@/lib/types/gateway";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { FeatureTestDialog } from "./featureTestDialog";
import { ModelWatchCard } from "./modelWatchCard";
import { RegisteredAgents } from "./registeredAgents";
import "./w4p6I18n";

const IN_USE_TONE: Record<GwCardLine["enabled"], string> = {
	on: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
	off: "bg-muted text-muted-foreground",
	unknown: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
};

/** The header line: version and host, or why the gateway could not be reached. Pure. */
export function GatewayHeaderLine({ gateway }: { gateway: GwGatewayInfo }) {
	const { t } = useTranslation("w4p6");
	if (!gateway.reachable) {
		return (
			<p className="text-sm text-amber-700 dark:text-amber-400" data-testid="gateway-unreachable">
				{t("page.unreachable", { error: gateway.error ?? "" })}
			</p>
		);
	}
	return (
		<p className="text-sm" data-testid="gateway-header">
			{gateway.version
				? t("page.gateway", { version: gateway.version, host: gateway.host ?? "" })
				: t("page.gatewayNoVersion", { host: gateway.host ?? "" })}
		</p>
	);
}

/** One feature line: name, in use badge, result word, headline, where it comes from, buttons. Pure. */
export function GatewayCardLineRow({ line, onStart }: { line: GwCardLine; onStart(line: GwCardLine): void }) {
	const { t } = useTranslation("w4p6");
	const from = line.source
		? line.measured_at
			? t("line.from", { source: line.source.text, date: line.measured_at.slice(0, 10) })
			: t("line.fromNoDate", { source: line.source.text })
		: line.state === "unknown"
			? t("line.unknown", { reason: line.enabled_source })
			: "";
	return (
		<li className="flex flex-col gap-1 p-3 text-sm" data-testid={`gateway-line-${line.feature}`} data-state={line.state}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="font-medium">{line.name}</span>
				<span
					className={cn("rounded-full px-2 py-0.5 text-xs", IN_USE_TONE[line.enabled] ?? IN_USE_TONE.unknown)}
					title={line.enabled_source}
					data-testid={`gateway-line-${line.feature}-inuse`}
				>
					{t("line.inUse")}: {t(`line.inUseState.${line.enabled}`)}
				</span>
				{line.result && <span className={cn("font-medium", RESULT_TONE[line.result])}>{t(`line.result.${line.result}`)}</span>}
				<span className="ml-auto flex gap-2">
					{line.exp_id && (
						<Link to="/experiments/$expId" params={{ expId: line.exp_id }} className="text-primary text-xs hover:underline">
							{t("line.open")}
						</Link>
					)}
					{line.start && (
						<Button
							type="button"
							size="sm"
							variant="outline"
							onClick={() => onStart(line)}
							data-testid={`gateway-line-${line.feature}-start`}
						>
							{t("line.start")}
						</Button>
					)}
				</span>
			</div>
			<p>{line.state === "not_enough_data" && !line.headline ? t("line.notEnough") : line.headline}</p>
			{from && <p className="text-muted-foreground text-xs">{from}</p>}
		</li>
	);
}

/** The source box: connected host with "Change" (admin), or the connect form. */
export function SourceBox({ source, admin }: { source: GwSourceInfo; admin: boolean }) {
	const { t } = useTranslation("w4p6");
	const [editing, setEditing] = useState(false);
	const [url, setUrl] = useState("");
	const [secret, setSecret] = useState("");
	const [error, setError] = useState("");
	const secrets = useListSecretsQuery(undefined, { skip: !admin || !(editing || !source.connected) });
	const [save, saving] = useSetGatewaySourceMutation();
	const [remove] = useRemoveGatewaySourceMutation();
	const showForm = admin && (editing || !source.connected);
	const onSave = async () => {
		setError("");
		try {
			await save({ gateway_url: url.trim(), credential_secret: secret }).unwrap();
			setEditing(false);
		} catch (e) {
			setError(errorMessage(e, String(e)));
		}
	};
	return (
		<div className="flex flex-col gap-2 rounded-xl border p-3 text-sm" data-testid="gateway-source">
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
			{source.error && <p className="text-amber-700 dark:text-amber-400">{source.error}</p>}
			{!admin && !source.connected && <p className="text-muted-foreground text-xs">{t("source.adminOnly")}</p>}
			{showForm && (
				<div className="flex flex-wrap items-end gap-2">
					<label className="flex flex-col gap-1">
						{t("source.address")}
						<Input value={url} onChange={(e) => setUrl(e.target.value)} data-testid="gateway-source-url" />
					</label>
					<label className="flex flex-col gap-1">
						{t("source.secret")}
						<select
							className="border-input bg-background h-9 rounded-md border px-2 text-sm"
							value={secret}
							onChange={(e) => setSecret(e.target.value)}
							data-testid="gateway-source-secret"
						>
							<option value="">{t("source.pickSecret")}</option>
							{(secrets.data?.secrets ?? []).map((s) => (
								<option key={s.id} value={s.name}>
									{s.name}
								</option>
							))}
						</select>
					</label>
					<Button type="button" size="sm" disabled={saving.isLoading || !url.trim() || !secret} onClick={() => void onSave()}>
						{t("source.save")}
					</Button>
					{source.connected && (
						<>
							<Button type="button" size="sm" variant="outline" onClick={() => void remove()}>
								{t("source.remove")}
							</Button>
							<Button type="button" size="sm" variant="ghost" onClick={() => setEditing(false)}>
								{t("source.cancel")}
							</Button>
						</>
					)}
				</div>
			)}
			{error && <p className="text-red-700 dark:text-red-400">{error}</p>}
		</div>
	);
}

/** /gateway: the Gateway report card. */
export default function GatewayView() {
	const { t } = useTranslation("w4p6");
	const session = useSession();
	const q = useGetGatewayReportCardQuery();
	const [starting, setStarting] = useState<GwCardLine | null>(null);
	return (
		<section className="mx-auto flex w-full max-w-[1200px] flex-col gap-6" data-testid="gateway-screen">
			<PageHeader title={t("page.title")} subtitle={t("page.subtitle")} />
			{q.isLoading ? (
				<BlockSkeleton rows={6} testId="gateway-loading" />
			) : q.error || !q.data ? (
				<LoadError title={t("page.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="gateway-error" />
			) : (
				<>
					<GatewayHeaderLine gateway={q.data.gateway} />
					<SourceBox source={q.data.source} admin={canUse(session, "admin")} />
					<ul className="flex flex-col divide-y rounded-xl border" data-testid="gateway-lines">
						{q.data.lines.map((l) => (
							<GatewayCardLineRow key={l.feature} line={l} onStart={setStarting} />
						))}
					</ul>
					<ModelWatchCard />
					<RegisteredAgents />
				</>
			)}
			{starting?.start && <FeatureTestDialog feature={starting.start} name={starting.name} open onClose={() => setStarting(null)} />}
		</section>
	);
}