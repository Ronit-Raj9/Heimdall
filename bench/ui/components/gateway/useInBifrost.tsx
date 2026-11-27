import { GovDialog } from "@/components/govern/govDialog";
import { Button } from "@/components/ui/button";
import type { GwSetting } from "@/lib/types/gateway";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Writes text to the clipboard; false when the browser refuses. */
export async function copyText(
	text: string,
	clipboard: Pick<Clipboard, "writeText"> | undefined = globalThis.navigator?.clipboard,
): Promise<boolean> {
	if (!clipboard) return false;
	try {
		await clipboard.writeText(text);
		return true;
	} catch {
		return false;
	}
}

/** The dialog's content: title, where it goes, the body to copy, the note and the fixed line. Pure. */
export function UseInBifrostBody({ setting, onCopy, copied }: { setting: GwSetting; onCopy(): void; copied: boolean }) {
	const { t } = useTranslation("w4p6");
	return (
		<div className="flex flex-col gap-3 text-sm" data-testid="use-in-bifrost-body">
			<p className="font-medium">{setting.title}</p>
			<p className="text-muted-foreground">{t("use.where", { where: setting.where })}</p>
			{setting.body && (
				<div className="flex flex-col gap-2">
					<pre className="bg-muted max-h-72 overflow-auto rounded-md p-3 text-xs" data-testid="use-in-bifrost-code">
						<code>{setting.body}</code>
					</pre>
					<Button type="button" variant="outline" size="sm" className="self-start" onClick={onCopy} data-testid="use-in-bifrost-copy">
						{copied ? t("use.copied") : t("use.copy")}
					</Button>
				</div>
			)}
			{setting.note && <p data-testid="use-in-bifrost-note">{setting.note}</p>}
			<p className="text-muted-foreground text-xs">{t("use.fixed")}</p>
		</div>
	);
}

/** "Use this in Bifrost": opens the setting to paste; disabled with the reason shown next to it when there is none. */
export function UseInBifrostButton({ setting, reason }: { setting: GwSetting | null | undefined; reason?: string }) {
	const { t } = useTranslation("w4p6");
	const [open, setOpen] = useState(false);
	const [copied, setCopied] = useState(false);
	const usable = Boolean(setting);
	return (
		<>
			<span title={usable ? undefined : reason} data-testid="use-in-bifrost-wrap">
				<Button type="button" size="sm" disabled={!usable} onClick={() => setOpen(true)} data-testid="use-in-bifrost">
					{t("use.button")}
				</Button>
			</span>
			{!usable && reason && (
				<span className="text-muted-foreground ml-2 text-xs" data-testid="use-in-bifrost-reason">
					{reason}
				</span>
			)}
			{setting && (
				<GovDialog
					open={open}
					onClose={() => setOpen(false)}
					title={t("use.button")}
					closeLabel={t("use.close")}
					testId="use-in-bifrost-dialog"
				>
					<UseInBifrostBody setting={setting} copied={copied} onCopy={() => void copyText(setting.body).then(setCopied)} />
				</GovDialog>
			)}
		</>
	);
}