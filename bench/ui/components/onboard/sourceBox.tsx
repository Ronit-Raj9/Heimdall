import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { OnboardInput } from "@/lib/types/onboard";
import { cn } from "@/lib/utils";
import { Upload } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";

export interface SourceChoice {
	/** A link or Harbor id, pasted rows, or a file to upload first. */
	input: OnboardInput;
	file?: File;
}

/** Step 1 of Add benchmark: one box that takes a link, a dropped file or pasted rows. */
export function SourceBox({ busy, onSubmit, onClear }: { busy: boolean; onSubmit(c: SourceChoice): void; onClear?(): void }) {
	const { t } = useTranslation("onboard");
	const [link, setLink] = useState("");
	const [rows, setRows] = useState("");
	const [file, setFile] = useState<File | null>(null);
	const [connection, setConnection] = useState("");
	const [over, setOver] = useState(false);
	const [error, setError] = useState("");
	const pick = useRef<HTMLInputElement>(null);
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
		<div className="flex min-w-0 flex-col gap-3" data-testid="onboard-source">
			<label className="flex flex-col gap-1 text-sm">
				<span className="font-medium">{t("source.link")}</span>
				<Input
					value={link}
					onChange={(e) => setLink(e.target.value)}
					placeholder={t("source.linkPlaceholder")}
					data-testid="onboard-link"
					onKeyDown={(e) => e.key === "Enter" && submit()}
				/>
			</label>
			<div
				role="button"
				tabIndex={0}
				onClick={() => pick.current?.click()}
				onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && pick.current?.click()}
				onDragOver={(e) => {
					e.preventDefault();
					setOver(true);
				}}
				onDragLeave={() => setOver(false)}
				onDrop={(e) => {
					e.preventDefault();
					setOver(false);
					const f = e.dataTransfer.files?.[0];
					if (f) setFile(f);
				}}
				className={cn(
					"text-muted-foreground flex cursor-pointer items-center justify-center gap-2 rounded-lg border border-dashed px-4 py-6 text-sm",
					over && "border-primary bg-accent",
				)}
				data-testid="onboard-drop"
			>
				<Upload className="size-4" aria-hidden="true" />
				{file ? `${t("source.file")}: ${file.name}` : t("source.drop")}
				<input
					ref={pick}
					type="file"
					className="hidden"
					onChange={(e) => setFile(e.target.files?.[0] ?? null)}
					data-testid="onboard-file"
				/>
			</div>
			<label className="flex flex-col gap-1 text-sm">
				<span className="font-medium">{t("source.paste")}</span>
				<textarea
					value={rows}
					onChange={(e) => setRows(e.target.value)}
					placeholder={t("source.pastePlaceholder")}
					rows={4}
					className="border-input bg-background rounded-md border px-3 py-2 font-mono text-xs"
					data-testid="onboard-paste"
				/>
			</label>
			<label className="flex flex-col gap-1 text-sm">
				<span className="font-medium">{t("source.connection")}</span>
				<Input value={connection} onChange={(e) => setConnection(e.target.value)} data-testid="onboard-connection" />
				<span className="text-muted-foreground text-xs">{t("source.connectionHint")}</span>
			</label>
			{error && (
				<p role="alert" className="text-chart-error-ink text-xs">
					{error}
				</p>
			)}
			<div className="flex flex-wrap gap-2">
				<Button onClick={submit} isLoading={busy} disabled={busy} dataTestId="onboard-inspect">
					{t("source.inspect")}
				</Button>
				{onClear && (
					<Button
						variant="outline"
						onClick={() => {
							setLink("");
							setRows("");
							setFile(null);
							onClear();
						}}
						dataTestId="onboard-clear"
					>
						{t("source.clear")}
					</Button>
				)}
			</div>
		</div>
	);
}
