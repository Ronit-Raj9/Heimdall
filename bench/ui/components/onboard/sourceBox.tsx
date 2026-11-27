import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { OnboardInput } from "@/lib/types/onboard";
import { cn } from "@/lib/utils";
import { Upload } from "lucide-react";
import { useFormDraft } from "@/lib/hooks/useFormDraft";
import { useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import "@/components/suites/uf6aI18n";

export interface SourceChoice {
	/** A link or Harbor id, pasted rows, or a file to upload first. */
	input: OnboardInput;
	file?: File;
}

/** True when the box holds something to look inside: a link, a file or pasted rows (blank text is nothing). */
export function sourceGiven({ link, rows, file }: { link: string; rows: string; file: File | null }): boolean {
	return Boolean(file) || link.trim() !== "" || rows.trim() !== "";
}

/** The hint under the box shows only while nothing is given (wave 4, p10-9c). */
export function sourceHintShown(box: { link: string; rows: string; file: File | null }): boolean {
	return !sourceGiven(box);
}

/**
 * Step 1 of Add benchmark: one box that takes a link, a dropped file or pasted rows. Look inside
 * stays disabled until one is given, and the hint under it always says what to give.
 */
export function SourceBox({ busy, onSubmit, onClear }: { busy: boolean; onSubmit(c: SourceChoice): void; onClear?(): void }) {
	const { t } = useTranslation("onboard");
	const { t: tf } = useTranslation("uf6a");
	// X-83: a typed link or pasted rows survive a reload (sessionStorage); the file and the secret do not.
	const [draft, updateDraft, clearDraft] = useFormDraft("onboardSource", { link: "", rows: "" });
	const { link, rows } = draft;
	const setLink = (v: string) => updateDraft({ link: v });
	const setRows = (v: string) => updateDraft({ rows: v });
	const [file, setFile] = useState<File | null>(null);
	const [connection, setConnection] = useState("");
	const [error, setError] = useState("");
	const pick = useRef<HTMLInputElement>(null);
	const given = sourceGiven({ link, rows, file });
	const submit = () => {
		setError("");
		const conn = connection.trim() || undefined;
		if (file) return onSubmit({ input: { name: file.name, connection: conn }, file });
		if (link.trim()) return onSubmit({ input: { url: link.trim(), connection: conn } });
		if (rows.trim()) {
			const first = rows.trim()[0];
			return onSubmit({ input: { data: rows, name: first === "{" || first === "[" ? "pasted.jsonl" : "pasted.csv" } });
		}
		setError(t("source.nothing"));
	};
	return (
		<div className="flex w-full max-w-2xl min-w-0 flex-col gap-3" data-testid="onboard-source">
			<label className="flex flex-col gap-1 text-sm">
				<span className="font-medium">{t("source.link")}</span>
				<Input
					value={link}
					onChange={(e) => setLink(e.target.value)}
					placeholder={t("source.linkPlaceholder")}
					data-testid="onboard-link"
					onKeyDown={(e) => e.key === "Enter" && given && submit()}
				/>
			</label>
			<FileDrop
				file={file}
				onFile={setFile}
				inputRef={pick}
				label={file ? `${t("source.file")}: ${file.name}` : t("source.drop")}
				testId="onboard-drop"
				inputTestId="onboard-file"
			/>
			<label className="flex flex-col gap-1 text-sm">
				<span className="font-medium">{t("source.paste")}</span>
				<textarea
					value={rows}
					onChange={(e) => setRows(e.target.value)}
					placeholder={t("source.pastePlaceholder")}
					rows={4}
					className="border-input bg-card rounded-md border px-3 py-2 font-mono text-xs"
					data-testid="onboard-paste"
				/>
			</label>
			{/* X-45: the optional secret sits behind a disclosure, after the source and before the action. */}
			<details className="text-sm" data-testid="onboard-connection-details">
				<summary className="text-muted-foreground hover:text-foreground cursor-pointer text-sm">{tf("onboard.privateSource")}</summary>
				<label className="mt-2 flex flex-col gap-1 text-sm">
					<span className="font-medium">{t("source.connection")}</span>
					<Input value={connection} onChange={(e) => setConnection(e.target.value)} data-testid="onboard-connection" />
					<span className="text-muted-foreground text-xs">{t("source.connectionHint")}</span>
				</label>
			</details>
			{error && (
				<p role="alert" className="text-danger-ink text-xs">
					{error}
				</p>
			)}
			<div className="flex flex-wrap items-center gap-2">
				<Button
					size="lg"
					onClick={submit}
					isLoading={busy}
					disabled={busy || !given}
					dataTestId="onboard-inspect"
					aria-describedby={given ? undefined : "onboard-source-hint"}
				>
					{t("source.inspect")}
				</Button>
				{onClear && (
					<Button
						variant="outline"
						onClick={() => {
							clearDraft();
							setFile(null);
							setConnection("");
							setError("");
							if (pick.current) pick.current.value = "";
							onClear();
						}}
						dataTestId="onboard-clear"
					>
						{t("source.clear")}
					</Button>
				)}
			</div>
			{sourceHintShown({ link, rows, file }) && (
				<p id="onboard-source-hint" className="text-muted-foreground text-xs" data-testid="onboard-source-hint">
					{t("source.nothing")}
				</p>
			)}
		</div>
	);
}

/**
 * A drop zone for one file: click, Enter or Space opens the file picker, a dropped file is taken.
 * Shared by Add benchmark step 1 and the benchmark Build and Policy uploads (M6-21), in place of the
 * native "Choose File / No file chosen" input.
 */
export function FileDrop({
	file,
	onFile,
	label,
	accept,
	disabled = false,
	hint,
	testId,
	inputTestId,
	inputRef,
	multiple = false,
	onFiles,
}: {
	file: File | null;
	onFile: (file: File | null) => void;
	/** Take several files at once (attachments); onFiles gets them all. */
	multiple?: boolean;
	onFiles?: (files: File[]) => void;
	/** The text in the zone; say the file name once one is picked. */
	label: ReactNode;
	accept?: string;
	disabled?: boolean;
	hint?: ReactNode;
	testId: string;
	inputTestId?: string;
	inputRef?: React.RefObject<HTMLInputElement | null>;
}) {
	const own = useRef<HTMLInputElement>(null);
	const pick = inputRef ?? own;
	const [over, setOver] = useState(false);
	const open = () => {
		if (!disabled) pick.current?.click();
	};
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<div
				role="button"
				tabIndex={disabled ? -1 : 0}
				aria-disabled={disabled || undefined}
				onClick={open}
				onKeyDown={(e) => {
					if (e.key === "Enter" || e.key === " ") {
						e.preventDefault();
						open();
					}
				}}
				onDragOver={(e) => {
					e.preventDefault();
					if (!disabled) setOver(true);
				}}
				onDragLeave={() => setOver(false)}
				onDrop={(e) => {
					e.preventDefault();
					setOver(false);
					if (disabled) return;
					const all = Array.from(e.dataTransfer.files ?? []);
					if (multiple && onFiles) onFiles(all);
					else if (all[0]) onFile(all[0]);
				}}
				className={cn(
					"text-muted-foreground focus-visible:ring-(--focus) flex min-w-0 cursor-pointer items-center justify-center gap-2 rounded-lg border border-dashed px-4 py-6 text-center text-sm outline-none focus-visible:ring-2",
					over && "border-primary bg-accent",
					file && "text-foreground",
					disabled && "cursor-not-allowed opacity-50",
				)}
				data-testid={testId}
			>
				<Upload className="size-4 shrink-0" aria-hidden="true" />
				<span className="min-w-0 break-words">{label}</span>
				<input
					ref={pick}
					type="file"
					accept={accept}
					className="hidden"
					disabled={disabled}
					multiple={multiple}
					onChange={(e) => {
						const all = Array.from(e.target.files ?? []);
						if (multiple && onFiles) {
							e.target.value = "";
							onFiles(all);
						} else onFile(all[0] ?? null);
					}}
					data-testid={inputTestId}
				/>
			</div>
			{hint && <span className="text-muted-foreground text-xs">{hint}</span>}
		</div>
	);
}
