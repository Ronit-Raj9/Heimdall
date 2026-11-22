import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateSecretMutation } from "@/lib/store/apis/secretsApi";
import type { ConnectQuestion } from "@/lib/types/connect";
import { CircleCheck } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * Inline secret capture: each secret the agent needs is stored on the spot as an org secret
 * (Settings > Secrets). The value pasted in a curl command is offered, never written to the config.
 * onStored reports the name and value so the detection can run again with it.
 */
export function SecretCapture({
	questions,
	onStored,
}: {
	questions: ConnectQuestion[];
	onStored: (name: string, value: string, q: ConnectQuestion) => void;
}) {
	const { t } = useTranslation("connect");
	if (questions.length === 0) return null;
	return (
		<div className="flex flex-col gap-3" data-testid="secret-capture">
			<div>
				<h3 className="text-sm font-medium">{t("secret.title")}</h3>
				<p className="text-muted-foreground text-xs">{t("secret.help")}</p>
			</div>
			{questions.map((q) => (
				<SecretRow key={q.secret ?? q.param} q={q} onStored={onStored} />
			))}
		</div>
	);
}

function SecretRow({ q, onStored }: { q: ConnectQuestion; onStored: (name: string, value: string, q: ConnectQuestion) => void }) {
	const { t } = useTranslation("connect");
	const [name, setName] = useState(q.secret ?? "");
	const [value, setValue] = useState(q.prefill ?? "");
	const [stored, setStored] = useState(false);
	const [create, { isLoading, error }] = useCreateSecretMutation();
	const save = async () => {
		const res = await create({ name, value });
		if (!("error" in res)) {
			setStored(true);
			onStored(name, value, q);
		}
	};
	return (
		<div className="flex flex-col gap-2 rounded-lg border p-3" data-testid={`secret-${q.secret}`}>
			<div className="text-sm font-medium">
				{q.label}
				{q.header ? <span className="text-muted-foreground font-normal"> ({q.header})</span> : null}
			</div>
			{q.help && <p className="text-muted-foreground text-xs">{q.help}</p>}
			<div className="grid gap-2 sm:grid-cols-[14rem_1fr_auto]">
				<Input
					aria-label={t("secret.name")}
					value={name}
					onChange={(e) => setName(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "_"))}
					disabled={stored}
				/>
				<Input
					aria-label={t("secret.value")}
					type="password"
					autoComplete="off"
					value={value}
					onChange={(e) => setValue(e.target.value)}
					disabled={stored}
				/>
				<Button onClick={() => void save()} disabled={stored || !name || !value} isLoading={isLoading}>
					{t("secret.save")}
				</Button>
			</div>
			{stored && (
				<p className="text-chart-success-ink flex items-center gap-1.5 text-xs">
					<CircleCheck className="size-3.5" aria-hidden />
					{t("secret.stored", { name })}
				</p>
			)}
			{error ? <p className="text-chart-error-ink text-xs">{getBenchErrorMessage(error)}</p> : null}
		</div>
	);
}