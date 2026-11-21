import { Button } from "@/components/ui/button";
import { useGetRunMaximQuery, usePushRunMaximMutation } from "@/lib/store/apis/traceToolsApi";
import type { RunMaxim } from "@/lib/types/traceTools";
import { ExternalLink, Send } from "lucide-react";
import { useTranslation } from "react-i18next";

export type MaximAction = { kind: "hidden" } | { kind: "open"; url: string } | { kind: "send"; error?: string };

/**
 * What the run header shows for Maxim: nothing when Maxim is not connected (or the lookup
 * failed), a link once the trace is in Maxim, and a send button for a finished run that is not.
 */
export function maximAction(data: RunMaxim | undefined, failed: boolean, sealed: boolean): MaximAction {
	if (failed || !data || !data.connected) return { kind: "hidden" };
	if (data.status === "pushed") return data.url ? { kind: "open", url: data.url } : { kind: "hidden" };
	if (!sealed) return { kind: "hidden" };
	return data.status === "failed" ? { kind: "send", error: data.error } : { kind: "send" };
}

export function MaximRunButtonView({ action, sending, onSend }: { action: MaximAction; sending?: boolean; onSend(): void }) {
	const { t } = useTranslation("traceTools");
	if (action.kind === "hidden") return null;
	if (action.kind === "open") {
		return (
			<Button variant="outline" size="sm" asChild>
				<a href={action.url} target="_blank" rel="noopener noreferrer" data-testid="run-maxim-open">
					<ExternalLink aria-hidden="true" />
					{t("maxim.open")}
				</a>
			</Button>
		);
	}
	return (
		<Button
			variant="ghost"
			size="sm"
			disabled={sending}
			onClick={onSend}
			title={action.error ? t("maxim.sendFailed", { error: action.error }) : undefined}
			data-testid="run-maxim-send"
			data-failed={action.error ? "true" : undefined}
		>
			<Send aria-hidden="true" />
			{sending ? t("maxim.sending") : t("maxim.send")}
		</Button>
	);
}

/** "Open in Maxim" / "Send to Maxim" in the run header. */
export function MaximRunButton({ runId, sealed }: { runId: string; sealed: boolean }) {
	const q = useGetRunMaximQuery(runId);
	const [push, pushing] = usePushRunMaximMutation();
	// Any error (404 before the backend has the route, 503 without a key store) hides the button.
	const failed = Boolean(q.error);
	return <MaximRunButtonView action={maximAction(q.data, failed, sealed)} sending={pushing.isLoading} onSend={() => void push(runId)} />;
}
