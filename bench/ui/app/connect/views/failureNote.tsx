import { Button } from "@/components/ui/button";
import type { ConnectFailure } from "@/lib/types/connect";
import { CircleAlert } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * One failure of the connect catalog: its title, the exact message and the one-click fix. A fix
 * that is a command is copied; a field pick calls onPick; the rest are handed to onFix.
 */
export function FailureNote({
	failure,
	onFix,
	onPick,
	testId,
}: {
	failure: ConnectFailure;
	onFix?: (failure: ConnectFailure) => void;
	onPick?: (path: string) => void;
	testId?: string;
}) {
	const { t } = useTranslation("connect");
	const [copied, setCopied] = useState(false);
	const fix = failure.fix;
	const copy = async (text: string) => {
		try {
			await navigator.clipboard.writeText(text);
			setCopied(true);
		} catch {
			setCopied(false);
		}
	};
	return (
		<div
			className="border-chart-warning/40 bg-chart-warning/5 flex flex-col gap-2 rounded-lg border p-3 text-sm"
			data-testid={testId ?? `failure-${failure.code}`}
			data-code={failure.code}
		>
			<div className="flex items-center gap-2 font-medium">
				<CircleAlert className="text-chart-warning-ink size-4 shrink-0" aria-hidden />
				{failure.title}
			</div>
			<p className="text-foreground/90">{failure.message}</p>
			{failure.options && failure.options.length > 0 && onPick && (
				<div className="flex flex-wrap gap-1.5" data-testid="failure-options">
					{failure.options.map((o) => (
						<Button key={o} size="sm" variant="outline" onClick={() => onPick(o)}>
							<code className="text-xs">{o}</code>
						</Button>
					))}
				</div>
			)}
			{fix && fix.action === "copy_command" && fix.value && (
				<div className="flex items-center gap-2">
					<code className="bg-muted rounded px-2 py-1 text-xs break-all">{fix.value}</code>
					<Button size="sm" variant="outline" onClick={() => void copy(fix.value ?? "")}>
						{copied ? t("result.copied") : t("result.copy")}
					</Button>
				</div>
			)}
			{fix && fix.action !== "copy_command" && fix.action !== "pick_field" && onFix && (
				<div>
					<Button size="sm" variant="outline" onClick={() => onFix(failure)} data-testid={`fix-${failure.code}`}>
						{fix.label}
					</Button>
				</div>
			)}
		</div>
	);
}