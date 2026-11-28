import "./ufl8I18n";

import { BlockSkeleton, InlineError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useLazyGetModalAppsQuery, type ModalApp } from "@/lib/store/apis/modalAppsApi";
import { useTranslation } from "react-i18next";

/** The list of leftover apps, with the command that stops one (the server has no stop route for them). Exported for tests. */
export function ModalAppsList({ apps }: { apps: ModalApp[] }) {
	const { t } = useTranslation("ufl8");
	if (apps.length === 0)
		return (
			<p className="text-muted-foreground text-xs" data-testid="modal-apps-none">
				{t("modalApps.none")}
			</p>
		);
	return (
		<div className="flex min-w-0 flex-col gap-2">
			<ul className="flex min-w-0 flex-col gap-1 text-xs" data-testid="modal-apps-list">
				{apps.map((a) => (
					<li key={a.app_id} className="flex min-w-0 flex-wrap gap-x-2 break-all" data-testid="modal-app">
						<span className="font-mono">{a.name}</span>
						<span className="text-muted-foreground">
							{a.app_id} · {a.state}
							{a.tasks ? ` · ${t("modalApps.tasks", { tasks: a.tasks })}` : ""}
						</span>
					</li>
				))}
			</ul>
			<p className="text-muted-foreground text-xs">{t("modalApps.stopHint")}</p>
		</div>
	);
}

/** Org admins: serving apps left on the org's Modal account that no deployment owns; they keep billing until stopped. */
export function ModalAppsPanel() {
	const { t } = useTranslation("ufl8");
	const [load, state] = useLazyGetModalAppsQuery();
	return (
		<div className="flex min-w-0 flex-col gap-2 border-t pt-3" data-testid="modal-apps-panel">
			<div className="flex flex-wrap items-center justify-between gap-2">
				<span className="text-sm font-medium">{t("modalApps.title")}</span>
				<Button size="sm" variant="outline" onClick={() => void load()} disabled={state.isFetching} data-testid="modal-apps-load">
					{state.data ? t("modalApps.reload") : t("modalApps.load")}
				</Button>
			</div>
			{state.isFetching ? (
				<BlockSkeleton rows={2} testId="modal-apps-loading" />
			) : state.isError ? (
				<InlineError error={state.error} title={t("modalApps.error")} onRetry={() => void load()} testId="modal-apps-error" />
			) : state.data ? (
				<ModalAppsList apps={state.data.apps ?? []} />
			) : null}
		</div>
	);
}