import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useImportInboxSessionMutation } from "@/lib/store/apis/traceDraftApi";
import { FileUp } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import "./w445I18n";

/** The largest session file the server keeps (handlers/inboxsession_w445.go). */
export const SESSION_FILE_MAX_BYTES = 32 << 20;

/**
 * "Import a session file": a small panel says what is kept and the file limits, then a file pick
 * and Import. A Claude Code or Codex session file becomes an inbox item, cleaned before it is kept;
 * onImported gets the new item's id so the screen can open it in New (M7-11).
 */
export function SessionImport({ onImported, defaultOpen = false }: { onImported?: (itemId: string) => void; defaultOpen?: boolean } = {}) {
	const { t } = useTranslation("w445");
	const input = useRef<HTMLInputElement>(null);
	const [importSession, { isLoading }] = useImportInboxSessionMutation();
	const [open, setOpen] = useState(defaultOpen);
	const [file, setFile] = useState<File | null>(null);
	const [message, setMessage] = useState<{
		ok: boolean;
		text: string;
		itemId?: string;
	} | null>(null);

	const pick = (next: File | undefined) => {
		setMessage(null);
		if (!next) return setFile(null);
		if (next.size > SESSION_FILE_MAX_BYTES) {
			setFile(null);
			setMessage({ ok: false, text: t("import.tooLarge") });
			if (input.current) input.current.value = "";
			return;
		}
		setFile(next);
	};

	const run = async () => {
		if (!file) return;
		setMessage(null);
		try {
			const res = await importSession(file).unwrap();
			setMessage({ ok: true, text: t("import.done"), itemId: res?.item_id });
			setFile(null);
			setOpen(false);
			if (res?.item_id) onImported?.(res.item_id);
		} catch (error) {
			setMessage({ ok: false, text: getBenchErrorMessage(error) });
		} finally {
			if (input.current) input.current.value = "";
		}
	};

	return (
		<div className="flex flex-col gap-1.5" data-testid="inbox-session-import">
			<div>
				<Button
					type="button"
					variant="outline"
					size="sm"
					aria-expanded={open}
					onClick={() => setOpen((o) => !o)}
					dataTestId="inbox-session-import-button"
				>
					<FileUp className="size-3.5" aria-hidden="true" />
					{t("import.button")}
				</Button>
			</div>
			{open && (
				<div
					className="bg-card flex max-w-md flex-col gap-2 rounded-md border p-3 text-sm"
					role="group"
					aria-label={t("import.button")}
					data-testid="inbox-session-import-panel"
				>
					<p>{t("import.help")}</p>
					<p className="text-muted-foreground">{t("import.limits")}</p>
					<input
						ref={input}
						type="file"
						accept=".jsonl"
						className="text-sm"
						aria-label={t("import.pick")}
						onChange={(e) => pick(e.target.files?.[0])}
						data-testid="inbox-session-import-input"
					/>
					<div className="flex flex-wrap gap-2">
						<Button type="button" size="sm" disabled={!file || isLoading} onClick={() => void run()} dataTestId="inbox-session-import-run">
							{isLoading ? t("import.busy") : t("import.run")}
						</Button>
						<Button type="button" size="sm" variant="ghost" onClick={() => setOpen(false)}>
							{t("import.cancel")}
						</Button>
					</div>
				</div>
			)}
			{message && (
				<p
					className={message.ok ? "text-sm" : "text-chart-error-ink text-sm"}
					role={message.ok ? "status" : "alert"}
					data-testid="inbox-session-import-message"
				>
					{message.text}
				</p>
			)}
		</div>
	);
}