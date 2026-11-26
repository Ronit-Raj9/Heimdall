import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useImportInboxSessionMutation } from "@/lib/store/apis/traceDraftApi";
import { FileUp } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import "./w445I18n";

/** The largest session file the server keeps (handlers/inboxsession_w445.go). */
export const SESSION_FILE_MAX_BYTES = 32 << 20;

/** "Import a session file": a Claude Code or Codex session file becomes an inbox item, cleaned before it is kept. */
export function SessionImport() {
	const { t } = useTranslation("w445");
	const input = useRef<HTMLInputElement>(null);
	const [importSession, { isLoading }] = useImportInboxSessionMutation();
	const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null);

	const onFile = async (file: File | undefined) => {
		if (!file) return;
		if (file.size > SESSION_FILE_MAX_BYTES) {
			setMessage({ ok: false, text: t("import.tooLarge") });
			return;
		}
		setMessage(null);
		try {
			await importSession(file).unwrap();
			setMessage({ ok: true, text: t("import.done") });
		} catch (error) {
			setMessage({ ok: false, text: getBenchErrorMessage(error) });
		} finally {
			if (input.current) input.current.value = "";
		}
	};

	return (
		<div className="flex flex-col gap-1.5" data-testid="inbox-session-import">
			<div className="flex flex-wrap items-center gap-2">
				<Button
					type="button"
					variant="outline"
					size="sm"
					disabled={isLoading}
					onClick={() => input.current?.click()}
					dataTestId="inbox-session-import-button"
				>
					<FileUp className="size-3.5" aria-hidden="true" />
					{isLoading ? t("import.busy") : t("import.button")}
				</Button>
				<input
					ref={input}
					type="file"
					accept=".jsonl"
					className="hidden"
					aria-label={t("import.button")}
					onChange={(e) => void onFile(e.target.files?.[0])}
					data-testid="inbox-session-import-input"
				/>
			</div>
			<p className="text-muted-foreground text-xs">{t("import.help")}</p>
			{message && (
				<p
					className={message.ok ? "text-xs" : "text-chart-error-ink text-xs"}
					role={message.ok ? "status" : "alert"}
					data-testid="inbox-session-import-message"
				>
					{message.text}
				</p>
			)}
		</div>
	);
}